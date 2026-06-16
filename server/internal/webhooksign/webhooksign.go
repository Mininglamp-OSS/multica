// Package webhooksign implements the GitHub-compatible HMAC-SHA256 webhook
// signature scheme shared by inbound verification (autopilot webhook ingress)
// and outbound signing (issue-event webhook delivery).
//
// Scheme: the signature header is `sha256=<hex(HMAC-SHA256(body, secret))>`,
// identical to GitHub's X-Hub-Signature-256. Keeping a single implementation
// guarantees that a payload Multica signs on the way out would verify with the
// same code path an inbound request is checked against.
package webhooksign

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// HeaderPrefix is the fixed prefix on the signature header value.
const HeaderPrefix = "sha256="

// Sign returns the signature header value for body under secret, in the form
// "sha256=<hex>". The caller sets it as X-Hub-Signature-256 (inbound convention)
// or X-Multica-Signature-256 (outbound convention) — the value format is the
// same.
func Sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return HeaderPrefix + hex.EncodeToString(mac.Sum(nil))
}

// Verify reports whether header is a valid "sha256=<hex>" signature of body
// under secret. The hmac.Equal comparison is constant-time so a partial-prefix
// match cannot leak timing.
func Verify(secret, header string, body []byte) bool {
	if !strings.HasPrefix(header, HeaderPrefix) {
		return false
	}
	want, err := hex.DecodeString(strings.TrimPrefix(header, HeaderPrefix))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), want)
}
