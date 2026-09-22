package signing

import (
	"strings"
	"testing"
)

// testPrivateKey — a fixed 40-byte scalar used by the SDK tests. Not registered
// on any account; never use it for real trading.
const testPrivateKey string = "0x0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f202122232425262728"

func TestNewSigner(t *testing.T) {
	var s, err = NewSigner(testPrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Enabled() || len(s.PublicKeyHex()) != 80 {
		t.Fatalf("signer %v public %q", s.Enabled(), s.PublicKeyHex())
	}
	if strings.Contains(s.String(), testPrivateKey[2:10]) {
		t.Fatal("String leaks key material")
	}
	var upper, _ = NewSigner("0X" + strings.ToUpper(testPrivateKey[2:]))
	if upper.PublicKeyHex() != s.PublicKeyHex() {
		t.Fatal("case-insensitive hex must yield the same key")
	}
	var disabled, disabledErr = NewSigner("")
	if disabledErr != nil || disabled.Enabled() || disabled.PublicKeyHex() != "" {
		t.Fatal("empty key must give a disabled signer")
	}
	if _, err = NewSigner("0x1234"); err != ErrInvalidPrivateKey {
		t.Fatalf("short key: %v", err)
	}
	if _, err = NewSigner("0x" + strings.Repeat("zz", 40)); err != ErrInvalidPrivateKey {
		t.Fatalf("non-hex key: %v", err)
	}
	s.Close()
	if s.Enabled() {
		t.Fatal("Close must disable the signer")
	}
}

func TestSignAndVerify(t *testing.T) {
	var s, _ = NewSigner(testPrivateKey)
	var b HashBuilder
	b.Add(300)
	b.Add(14)
	b.Add(7)
	var hash Hash
	b.Finish(&hash)

	var sig Signature
	if err := s.Sign(&hash, &sig); err != nil {
		t.Fatal(err)
	}
	var pk = s.PublicKey()
	if !Verify(&pk, &hash, &sig) {
		t.Fatal("randomised signature does not verify")
	}
	var other Hash = hash
	other[0]++
	if Verify(&pk, &other, &sig) {
		t.Fatal("signature verifies a different hash")
	}

	var k = make([]byte, KeyLength)
	k[0] = 42
	var det1, det2 Signature
	if err := s.SignWithNonce(&hash, k, &det1); err != nil {
		t.Fatal(err)
	}
	if err := s.SignWithNonce(&hash, k, &det2); err != nil {
		t.Fatal(err)
	}
	if det1 != det2 {
		t.Fatal("SignWithNonce must be deterministic")
	}
	if !Verify(&pk, &hash, &det1) {
		t.Fatal("deterministic signature does not verify")
	}
	if err := s.SignWithNonce(&hash, k[:10], &det1); err != ErrInvalidPrivateKey {
		t.Fatalf("short k: %v", err)
	}
	var disabled, _ = NewSigner("")
	if err := disabled.Sign(&hash, &sig); err != ErrSignerDisabled {
		t.Fatalf("disabled: %v", err)
	}
	if out := sig.AppendBase64(nil); len(out) != 108 || out[107] != '=' {
		t.Fatalf("base64 = %s", out)
	}
}

func TestGenerateKey(t *testing.T) {
	var priv, pub = GenerateKey()
	if !strings.HasPrefix(priv, "0x") || len(priv) != 82 || !strings.HasPrefix(pub, "0x") || len(pub) != 82 {
		t.Fatalf("priv %d pub %d", len(priv), len(pub))
	}
	var s, err = NewSigner(priv)
	if err != nil || "0x"+s.PublicKeyHex() != pub {
		t.Fatalf("public key of the generated key does not match: %v", err)
	}
}

func TestAuthTokenShape(t *testing.T) {
	var s, _ = NewSigner(testPrivateKey)
	var token, err = AuthToken(s, 1790085499, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	var parts = strings.Split(token, ":")
	if len(parts) != 4 || parts[0] != "1790085499" || parts[1] != "1" || parts[2] != "2" || len(parts[3]) != 160 {
		t.Fatalf("token = %s", token)
	}
	var disabled, _ = NewSigner("")
	if _, err = AuthToken(disabled, 1, 1, 1); err != ErrSignerDisabled {
		t.Fatalf("disabled: %v", err)
	}
}

func BenchmarkSign(b *testing.B) {
	var s, _ = NewSigner(testPrivateKey)
	var hash Hash
	hash[0] = 1
	var sig Signature
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = s.Sign(&hash, &sig)
	}
}
