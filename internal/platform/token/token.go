// Package token signs and verifies access tokens. Only auth receives the private key.
package token

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"qslate/internal/platform/httpx"
)

type Manager struct {
	private          ed25519.PrivateKey
	public           ed25519.PublicKey
	issuer, audience string
	ttl              time.Duration
}

func NewSigner(path, issuer, audience string, ttl time.Duration) (*Manager, error) {
	if issuer == "" || audience == "" || ttl <= 0 {
		return nil, errors.New("issuer, audience and positive TTL required")
	}
	key, err := readPEM(path)
	if err != nil {
		return nil, err
	}
	parsed, err := x509.ParsePKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}
	private, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("expected Ed25519 private key")
	}
	return &Manager{private: private, public: private.Public().(ed25519.PublicKey), issuer: issuer, audience: audience, ttl: ttl}, nil
}

func NewVerifier(path, issuer, audience string) (*Manager, error) {
	if issuer == "" || audience == "" {
		return nil, errors.New("issuer and audience required")
	}
	key, err := readPEM(path)
	if err != nil {
		return nil, err
	}
	parsed, err := x509.ParsePKIXPublicKey(key)
	if err != nil {
		return nil, fmt.Errorf("invalid public key: %w", err)
	}
	public, ok := parsed.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("expected Ed25519 public key")
	}
	return &Manager{public: public, issuer: issuer, audience: audience}, nil
}

func readPEM(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read key: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("invalid PEM key")
	}
	return block.Bytes, nil
}

func (m *Manager) Issue(id int64) (string, error) {
	if id <= 0 || len(m.private) != ed25519.PrivateKeySize || m.ttl <= 0 {
		return "", errors.New("invalid signing configuration")
	}
	now := time.Now()
	claims := jwt.RegisteredClaims{Issuer: m.issuer, Subject: strconv.FormatInt(id, 10), Audience: jwt.ClaimStrings{m.audience},
		ExpiresAt: jwt.NewNumericDate(now.Add(m.ttl)), IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now)}
	return jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims).SignedString(m.private)
}

func (m *Manager) Verify(raw string) (int64, error) {
	claims := &jwt.RegisteredClaims{}
	parsed, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) { return m.public, nil },
		jwt.WithValidMethods([]string{"EdDSA"}), jwt.WithIssuer(m.issuer), jwt.WithAudience(m.audience), jwt.WithExpirationRequired(), jwt.WithIssuedAt())
	if err != nil || parsed == nil || !parsed.Valid {
		return 0, errors.New("invalid access token")
	}
	id, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid token subject")
	}
	return id, nil
}

type Verifier interface{ Verify(string) (int64, error) }
type subjectKey struct{}

func Subject(ctx context.Context) int64 { id, _ := ctx.Value(subjectKey{}).(int64); return id }

func Protect(verifier Verifier, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Fields(r.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			unauthorized(w)
			return
		}
		id, err := verifier.Verify(parts[1])
		if err != nil {
			unauthorized(w)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), subjectKey{}, id)))
	})
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	httpx.Error(w, 401, "unauthorized", "valid access token required")
}
