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

type Claims struct {
	jwt.RegisteredClaims
	SessionID string `json:"sid,omitempty"`
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
	return m.IssueSession(id, "")
}

func (m *Manager) IssueSession(id int64, sessionID string) (string, error) {
	if id <= 0 || len(m.private) != ed25519.PrivateKeySize || m.ttl <= 0 {
		return "", errors.New("invalid signing configuration")
	}
	now := time.Now()
	claims := Claims{RegisteredClaims: jwt.RegisteredClaims{Issuer: m.issuer, Subject: strconv.FormatInt(id, 10), Audience: jwt.ClaimStrings{m.audience},
		ExpiresAt: jwt.NewNumericDate(now.Add(m.ttl)), IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now)}, SessionID: sessionID}
	return jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims).SignedString(m.private)
}

func (m *Manager) Verify(raw string) (int64, error) {
	id, _, err := m.VerifySession(raw)
	return id, err
}

func (m *Manager) VerifySession(raw string) (int64, string, error) {
	claims := &Claims{}
	parsed, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) { return m.public, nil },
		jwt.WithValidMethods([]string{"EdDSA"}), jwt.WithIssuer(m.issuer), jwt.WithAudience(m.audience), jwt.WithExpirationRequired(), jwt.WithIssuedAt())
	if err != nil || parsed == nil || !parsed.Valid {
		return 0, "", errors.New("invalid access token")
	}
	id, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil || id <= 0 {
		return 0, "", errors.New("invalid token subject")
	}
	return id, claims.SessionID, nil
}

type Verifier interface{ Verify(string) (int64, error) }
type subjectKey struct{}
type sessionKey struct{}
type authorizationKey struct{}

func Subject(ctx context.Context) int64    { id, _ := ctx.Value(subjectKey{}).(int64); return id }
func SessionID(ctx context.Context) string { id, _ := ctx.Value(sessionKey{}).(string); return id }

// Authorization forwards only the Bearer token already checked by Protect.
func Authorization(ctx context.Context) string {
	value, _ := ctx.Value(authorizationKey{}).(string)
	return value
}

func Protect(verifier Verifier, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Fields(r.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			unauthorized(w)
			return
		}
		var id int64
		var sid string
		var err error
		if stateful, ok := verifier.(interface {
			VerifySession(string) (int64, string, error)
		}); ok {
			id, sid, err = stateful.VerifySession(parts[1])
		} else {
			id, err = verifier.Verify(parts[1])
		}
		if err != nil {
			unauthorized(w)
			return
		}
		ctx := context.WithValue(r.Context(), subjectKey{}, id)
		ctx = context.WithValue(ctx, sessionKey{}, sid)
		ctx = context.WithValue(ctx, authorizationKey{}, "Bearer "+parts[1])
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	httpx.Error(w, 401, "unauthorized", "valid access token required")
}
