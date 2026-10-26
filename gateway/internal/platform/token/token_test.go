package token

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestAccessTokenValidation(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manager := &Manager{private: private, public: public, issuer: "auth", audience: "api", ttl: time.Minute}
	raw, err := manager.Issue(42)
	if err != nil {
		t.Fatal(err)
	}
	verifier := &Manager{public: public, issuer: "auth", audience: "api"}
	id, err := verifier.Verify(raw)
	if err != nil || id != 42 {
		t.Fatalf("valid token rejected: id=%d err=%v", id, err)
	}
	now := time.Now()
	for _, test := range []struct {
		name   string
		claims jwt.RegisteredClaims
	}{
		{"expired", jwt.RegisteredClaims{Issuer: "auth", Audience: jwt.ClaimStrings{"api"}, Subject: "42", ExpiresAt: jwt.NewNumericDate(now.Add(-time.Minute))}},
		{"missing_exp", jwt.RegisteredClaims{Issuer: "auth", Audience: jwt.ClaimStrings{"api"}, Subject: "42"}},
		{"wrong_issuer", jwt.RegisteredClaims{Issuer: "other", Audience: jwt.ClaimStrings{"api"}, Subject: "42", ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute))}},
		{"wrong_audience", jwt.RegisteredClaims{Issuer: "auth", Audience: jwt.ClaimStrings{"other"}, Subject: "42", ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute))}},
		{"invalid_subject", jwt.RegisteredClaims{Issuer: "auth", Audience: jwt.ClaimStrings{"api"}, Subject: "-1", ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute))}},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, test.claims).SignedString(private)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = verifier.Verify(raw); err == nil {
				t.Fatal("invalid token accepted")
			}
		})
	}
	_, wrongPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manager.private = wrongPrivate
	wrong, err := manager.Issue(42)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = verifier.Verify(wrong); err == nil {
		t.Fatal("wrong signature accepted")
	}
	hs, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": "42"}).SignedString([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = verifier.Verify(hs); err == nil {
		t.Fatal("wrong algorithm accepted")
	}
}
