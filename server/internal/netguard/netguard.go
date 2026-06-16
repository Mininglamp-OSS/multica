// Package netguard provides SSRF protection for outbound HTTP requests to
// user-configured URLs. The core guarantee is enforced at dial time, not just
// at validation time: NewRestrictedHTTPClient resolves the destination host and
// refuses to connect to internal addresses on every dial — including each
// redirect hop and across DNS rebinding — which a one-shot URL string check
// cannot cover.
package netguard

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// blockedCIDRs are IANA special-purpose ranges that Go's net.IP predicates
// (IsLoopback/IsLinkLocal*/IsPrivate/IsUnspecified/IsMulticast) do NOT cover but
// which an outbound webhook must never reach — most importantly shared address
// space, which hosts cloud metadata endpoints in some environments
// (e.g. 100.100.100.200 on Alibaba Cloud).
var blockedCIDRs = mustParseCIDRs(
	"100.64.0.0/10", // shared address space / CGNAT (RFC 6598); incl. 100.100.100.200 metadata
	"192.0.0.0/24",  // IETF protocol assignments (RFC 6890)
	"198.18.0.0/15", // benchmarking (RFC 2544)
	"240.0.0.0/4",   // reserved for future use + 255.255.255.255 broadcast (RFC 1112)
	"2001:db8::/32", // documentation (RFC 3849)
)

func mustParseCIDRs(cidrs ...string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic("netguard: invalid CIDR " + c + ": " + err.Error())
		}
		out = append(out, n)
	}
	return out
}

// IsBlockedIP reports whether ip is an address an outbound request to a
// user-supplied URL must never reach: loopback, link-local (covers the
// 169.254.169.254 cloud-metadata endpoint), private, unspecified, multicast,
// and the IANA special-purpose ranges in blockedCIDRs (incl. 100.64.0.0/10
// shared space, which carries metadata endpoints in some clouds).
func IsBlockedIP(ip net.IP) bool {
	if ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsPrivate() ||
		ip.IsUnspecified() ||
		ip.IsMulticast() {
		return true
	}
	for _, n := range blockedCIDRs {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// NewRestrictedHTTPClient returns an http.Client whose transport rejects any
// connection to a blocked internal address. The DialContext resolves the host,
// fails if ANY resolved IP is blocked (defends against split-horizon DNS), and
// then dials the exact IP it checked — closing the resolve→dial TOCTOU window
// that a check-then-redial approach (and DNS rebinding) would leave open.
//
// Redirects use Go's default policy; because every hop re-dials through this
// transport, a redirect to an internal target is rejected at dial time too.
//
// Proxy is explicitly disabled (Proxy: nil): with http.ProxyFromEnvironment, a
// configured HTTP_PROXY/HTTPS_PROXY would make Go dial the proxy instead of the
// target, so DialContext would only see the proxy address and the user-supplied
// host would never reach IsBlockedIP — a complete SSRF bypass. This client is
// used solely for webhook delivery, so forcing direct connections has no
// collateral impact.
func NewRestrictedHTTPClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           restrictedDialContext(dialer),
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &http.Client{Timeout: timeout, Transport: transport}
}

func restrictedDialContext(dialer *net.Dialer) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("netguard: no addresses for host %q", host)
		}
		for _, ipa := range ips {
			if IsBlockedIP(ipa.IP) {
				return nil, fmt.Errorf("netguard: refusing to connect to internal address %s (host %q)", ipa.IP, host)
			}
		}
		// Dial the exact IP we just verified so a concurrent rebind can't swap
		// in a blocked address between the check and the connect.
		return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}
}
