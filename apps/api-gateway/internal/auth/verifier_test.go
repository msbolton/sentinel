package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	testIssuer   = "http://keycloak:8080/realms/sentinel"
	testAudience = "sentinel-api"
)

func newTestKey(t *testing.T) (*rsa.PrivateKey, jwt.Keyfunc) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key, func(*jwt.Token) (interface{}, error) { return &key.PublicKey, nil }
}

func baseClaims() jwt.MapClaims {
	return jwt.MapClaims{
		"iss":                testIssuer,
		"aud":                testAudience,
		"sub":                "user-123",
		"exp":                time.Now().Add(time.Hour).Unix(),
		"iat":                time.Now().Unix(),
		"preferred_username": "alice",
		"email":              "alice@example.com",
		"name":               "Alice Analyst",
		"realm_access":       map[string]interface{}{"roles": []string{"sentinel-analyst", "offline_access"}},
		"resource_access": map[string]interface{}{
			"sentinel-api": map[string]interface{}{"roles": []string{"sentinel-analyst", "api-user"}},
			"other-client": map[string]interface{}{"roles": []string{"ignored"}},
		},
		"classification_level": "SECRET",
	}
}

func sign(t *testing.T, key *rsa.PrivateKey, method jwt.SigningMethod, claims jwt.MapClaims) string {
	t.Helper()
	var signed string
	var err error
	if method == jwt.SigningMethodHS256 {
		signed, err = jwt.NewWithClaims(method, claims).SignedString([]byte("secret"))
	} else {
		signed, err = jwt.NewWithClaims(method, claims).SignedString(key)
	}
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func newVerifier(kf jwt.Keyfunc, audience string) *KeycloakVerifier {
	return NewKeycloakVerifier(KeycloakOptions{
		Keyfunc:  kf,
		Issuers:  []string{testIssuer, "http://localhost:8080/realms/sentinel"},
		Audience: audience,
		ClientID: "sentinel-api",
	})
}

func TestVerify_ValidToken(t *testing.T) {
	key, kf := newTestKey(t)
	v := newVerifier(kf, testAudience)

	u, err := v.Verify(context.Background(), sign(t, key, jwt.SigningMethodRS256, baseClaims()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u.UserID != "user-123" || u.Username != "alice" || u.Email != "alice@example.com" || u.Name != "Alice Analyst" {
		t.Errorf("identity fields wrong: %+v", u)
	}
	if u.ClassificationLevel != "SECRET" {
		t.Errorf("classification = %q", u.ClassificationLevel)
	}
	want := []string{"sentinel-analyst", "offline_access", "api-user"}
	if len(u.Roles) != len(want) {
		t.Fatalf("roles = %v, want %v", u.Roles, want)
	}
	for i := range want {
		if u.Roles[i] != want[i] {
			t.Errorf("roles = %v, want %v", u.Roles, want)
		}
	}
	if len(u.ClientRoles) != 2 || u.ClientRoles[1] != "api-user" {
		t.Errorf("client roles = %v", u.ClientRoles)
	}
}

func TestVerify_DefaultsClassification(t *testing.T) {
	key, kf := newTestKey(t)
	c := baseClaims()
	delete(c, "classification_level")
	u, err := newVerifier(kf, testAudience).Verify(context.Background(), sign(t, key, jwt.SigningMethodRS256, c))
	if err != nil {
		t.Fatal(err)
	}
	if u.ClassificationLevel != "UNCLASSIFIED" {
		t.Errorf("classification = %q, want UNCLASSIFIED", u.ClassificationLevel)
	}
}

func TestVerify_SecondIssuerAccepted(t *testing.T) {
	key, kf := newTestKey(t)
	c := baseClaims()
	c["iss"] = "http://localhost:8080/realms/sentinel"
	if _, err := newVerifier(kf, testAudience).Verify(context.Background(), sign(t, key, jwt.SigningMethodRS256, c)); err != nil {
		t.Fatalf("second issuer rejected: %v", err)
	}
}

func TestVerify_Rejections(t *testing.T) {
	key, kf := newTestKey(t)
	otherKey, _ := newTestKey(t)

	cases := map[string]func() (string, string){
		"wrong issuer": func() (string, string) {
			c := baseClaims()
			c["iss"] = "http://evil/realms/sentinel"
			return sign(t, key, jwt.SigningMethodRS256, c), testAudience
		},
		"expired": func() (string, string) {
			c := baseClaims()
			c["exp"] = time.Now().Add(-2 * time.Minute).Unix()
			return sign(t, key, jwt.SigningMethodRS256, c), testAudience
		},
		"missing exp": func() (string, string) {
			c := baseClaims()
			delete(c, "exp")
			return sign(t, key, jwt.SigningMethodRS256, c), testAudience
		},
		"missing sub": func() (string, string) {
			c := baseClaims()
			delete(c, "sub")
			return sign(t, key, jwt.SigningMethodRS256, c), testAudience
		},
		"wrong audience": func() (string, string) {
			c := baseClaims()
			c["aud"] = "account"
			return sign(t, key, jwt.SigningMethodRS256, c), testAudience
		},
		"hs256 rejected": func() (string, string) {
			return sign(t, key, jwt.SigningMethodHS256, baseClaims()), testAudience
		},
		"wrong key": func() (string, string) {
			return sign(t, otherKey, jwt.SigningMethodRS256, baseClaims()), testAudience
		},
		"garbage": func() (string, string) { return "not.a.jwt", testAudience },
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			raw, aud := mk()
			_, err := newVerifier(kf, aud).Verify(context.Background(), raw)
			if !errors.Is(err, ErrInvalidToken) {
				t.Fatalf("expected ErrInvalidToken, got %v", err)
			}
		})
	}
}

func TestVerify_AudienceCheckDisabled(t *testing.T) {
	key, kf := newTestKey(t)
	c := baseClaims()
	c["aud"] = "account"
	if _, err := newVerifier(kf, "").Verify(context.Background(), sign(t, key, jwt.SigningMethodRS256, c)); err != nil {
		t.Fatalf("audience check should be disabled: %v", err)
	}
}

func TestClassificationRank(t *testing.T) {
	if ClassificationRank("TOP_SECRET") <= ClassificationRank("SECRET") {
		t.Error("TOP_SECRET should outrank SECRET")
	}
	if ClassificationRank("bogus") != 0 {
		t.Error("unknown level should rank as UNCLASSIFIED")
	}
	u := &User{ClassificationLevel: "CONFIDENTIAL"}
	if !u.MeetsClassification("UNCLASSIFIED") || u.MeetsClassification("SECRET") {
		t.Error("MeetsClassification wrong")
	}
}

func TestVerify_ClassificationFromRoles(t *testing.T) {
	key, kf := newTestKey(t)
	c := baseClaims()
	delete(c, "classification_level")
	c["realm_access"] = map[string]interface{}{"roles": []string{"sentinel-viewer", "classification-u", "classification-ts"}}
	u, err := newVerifier(kf, testAudience).Verify(context.Background(), sign(t, key, jwt.SigningMethodRS256, c))
	if err != nil {
		t.Fatal(err)
	}
	if u.ClassificationLevel != "TOP_SECRET" {
		t.Errorf("classification = %q, want TOP_SECRET (highest role wins)", u.ClassificationLevel)
	}
	if ClassificationFromRoles([]string{"sentinel-admin"}) != "" {
		t.Error("no classification role should yield empty")
	}
}
