package webhooksign

import "testing"

func TestSignVerifyRoundTrip(t *testing.T) {
	secret := "whsec_abc123"
	body := []byte(`{"event":"issue.status_changed","issue":{"id":"x"}}`)

	sig := Sign(secret, body)
	if sig == "" || sig[:7] != "sha256=" {
		t.Fatalf("unexpected signature format: %q", sig)
	}
	if !Verify(secret, sig, body) {
		t.Error("Verify should accept a signature produced by Sign")
	}
}

func TestVerifyRejectsTamperedBody(t *testing.T) {
	secret := "whsec_abc123"
	sig := Sign(secret, []byte("original"))
	if Verify(secret, sig, []byte("tampered")) {
		t.Error("Verify must reject a body that does not match the signature")
	}
}

func TestVerifyRejectsWrongSecret(t *testing.T) {
	body := []byte("payload")
	sig := Sign("secret-a", body)
	if Verify("secret-b", sig, body) {
		t.Error("Verify must reject a signature made with a different secret")
	}
}

func TestVerifyRejectsMalformedHeader(t *testing.T) {
	body := []byte("payload")
	for _, h := range []string{"", "deadbeef", "sha256=", "sha256=zzzz", "md5=abcd"} {
		if Verify("secret", h, body) {
			t.Errorf("Verify must reject malformed header %q", h)
		}
	}
}
