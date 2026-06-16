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

// IsBlockedIP reports whether ip is an address an outbound request to a
// user-supplied URL must never reach: loopback, link-local (covers the
// 169.254.169.254 cloud-metadata endpoint), private, and unspecified ranges.
func IsBlockedIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsPrivate() ||
		ip.IsUnspecified()
}

// NewRestrictedHTTPClient returns an http.Client whose transport rejects any
// connection to a blocked internal address. The DialContext resolves the host,
// fails if ANY resolved IP is blocked (defends against split-horizon DNS), and
// then dials the exact IP it checked — closing the resolve→dial TOCTOU window
// that a check-then-redial approach (and DNS rebinding) would leave open.
//
// Redirects use Go's default policy; because every hop re-dials through this
// transport, a redirect to an internal target is rejected at dial time too.
func NewRestrictedHTTPClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
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
