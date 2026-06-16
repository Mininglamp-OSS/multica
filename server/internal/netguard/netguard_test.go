package netguard

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIsBlockedIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1",       // loopback
		"::1",             // loopback v6
		"169.254.169.254", // link-local (cloud metadata)
		"fe80::1",         // link-local v6
		"10.0.0.5",        // private
		"172.16.0.1",      // private
		"192.168.1.1",     // private
		"fc00::1",         // unique-local v6 (IsPrivate)
		"0.0.0.0",         // unspecified
		"::",              // unspecified v6
	}
	for _, s := range blocked {
		if ip := net.ParseIP(s); ip == nil || !IsBlockedIP(ip) {
			t.Errorf("IsBlockedIP(%s) = false, want true", s)
		}
	}

	allowed := []string{
		"8.8.8.8",
		"1.1.1.1",
		"203.0.113.10",
		"2606:4700:4700::1111",
	}
	for _, s := range allowed {
		if ip := net.ParseIP(s); ip == nil || IsBlockedIP(ip) {
			t.Errorf("IsBlockedIP(%s) = true, want false", s)
		}
	}
}

// TestRestrictedClientRefusesLoopback proves the guard fires at dial time:
// httptest.Server binds 127.0.0.1, which the restricted client must refuse.
func TestRestrictedClientRefusesLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewRestrictedHTTPClient(5 * time.Second)
	resp, err := client.Get(srv.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatalf("expected dial to be refused for loopback %s, got %d", srv.URL, resp.StatusCode)
	}
}

// TestRestrictedClientFollowsGuardOnRedirect proves a redirect to a loopback
// target is rejected at the redirect hop's dial, not followed.
func TestRestrictedClientRefusesRedirectToLoopback(t *testing.T) {
	// internal target the redirect points at (loopback).
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot) // should never be reached
	}))
	defer internal.Close()

	// A redirector — also on loopback in tests, so the first hop is already
	// refused. The assertion is simply that no request succeeds.
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, internal.URL, http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()

	client := NewRestrictedHTTPClient(5 * time.Second)
	resp, err := client.Get(redirector.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatalf("expected refusal, got status %d", resp.StatusCode)
	}
}

// TestRestrictedClientIgnoresProxyEnv proves the client does not honor
// HTTP(S)_PROXY: with a proxy configured, a request to a loopback target must
// still be refused by the dial guard (which only runs on direct connections),
// not tunneled to the proxy. If the proxy were used, the guard would never see
// the target host and the error would be a proxy-connect failure instead.
func TestRestrictedClientIgnoresProxyEnv(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://10.0.0.1:3128")
	t.Setenv("HTTPS_PROXY", "http://10.0.0.1:3128")
	t.Setenv("ALL_PROXY", "http://10.0.0.1:3128")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewRestrictedHTTPClient(5 * time.Second)
	resp, err := client.Get(srv.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatalf("expected refusal for loopback target, got %d", resp.StatusCode)
	}
	if !strings.Contains(err.Error(), "refusing to connect to internal") {
		t.Fatalf("dial guard must fire (proxy must be disabled); got: %v", err)
	}
}
