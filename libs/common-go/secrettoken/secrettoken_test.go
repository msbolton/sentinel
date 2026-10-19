package secrettoken

import (
	"crypto/sha256"
	"strings"
	"testing"
)

func TestGenerateShapes(t *testing.T) {
	for _, prefix := range []string{"sk_", "si_"} {
		secret, display, hash, err := Generate(prefix)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(secret, prefix) || len(secret) != len(prefix)+43 || !Valid(secret, prefix) {
			t.Fatalf("%s secret %q", prefix, secret)
		}
		if display != secret[:len(prefix)+8] || Display(secret, prefix) != display {
			t.Fatalf("%s display %q of %q", prefix, display, secret)
		}
		want := sha256.Sum256([]byte(secret))
		if !Equal(hash, want[:]) || !Equal(Hash(secret), hash) {
			t.Fatalf("%s hash", prefix)
		}
		if strings.ContainsAny(secret[len(prefix):], "+/=") {
			t.Fatalf("%s secret is not base64url without padding: %q", prefix, secret)
		}
	}
	a, _, _, _ := Generate("si_")
	b, _, _, _ := Generate("si_")
	if a == b {
		t.Fatal("two secrets are equal")
	}
}

func TestValidRefusesOtherShapes(t *testing.T) {
	good, _, _, _ := Generate("si_")
	for _, bad := range []string{"", "si_short", "sk_" + good[3:], good + "x", strings.Repeat("x", len(good))} {
		if Valid(bad, "si_") {
			t.Errorf("accepted %q", bad)
		}
	}
	if Display("si_abc", "si_") != "si_abc" {
		t.Error("a short secret displays as itself")
	}
	if Equal([]byte{1}, []byte{1, 2}) {
		t.Error("different lengths compare equal")
	}
}
