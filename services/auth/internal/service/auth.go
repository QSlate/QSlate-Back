package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
	"qslate/services/auth/internal/domain"
)

type Issuer interface{ Issue(int64) (string, error) }
type Auth struct {
	repo   domain.Repository
	issuer Issuer
	cost   int
	dummy  []byte
	ttl    time.Duration
}
type Session struct {
	User        domain.User `json:"user"`
	AccessToken string      `json:"access_token"`
	TokenType   string      `json:"token_type"`
	ExpiresIn   int64       `json:"expires_in"`
}

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{2,31}$`)

func New(repo domain.Repository, issuer Issuer, cost int, ttl time.Duration) (*Auth, error) {
	if cost < bcrypt.MinCost || cost > 14 || ttl <= 0 {
		return nil, errors.New("invalid auth configuration")
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	dummy, err := bcrypt.GenerateFromPassword([]byte(hex.EncodeToString(secret)), cost)
	if err != nil {
		return nil, err
	}
	return &Auth{repo: repo, issuer: issuer, cost: cost, dummy: dummy, ttl: ttl}, nil
}

func (s *Auth) Register(ctx context.Context, email, username, password string) (Session, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	username = strings.ToLower(strings.TrimSpace(username))
	parsed, err := mail.ParseAddress(email)
	at := strings.LastIndexByte(email, '@')
	if err != nil || parsed.Address != email || len(email) > 254 || at < 1 || !strings.Contains(email[at+1:], ".") {
		return Session{}, fmt.Errorf("%w: valid email required", domain.ErrInvalidInput)
	}
	if !usernamePattern.MatchString(username) {
		return Session{}, fmt.Errorf("%w: username must be 3–32 letters, digits, underscores or hyphens", domain.ErrInvalidInput)
	}
	if !utf8.ValidString(password) || utf8.RuneCountInString(password) < 12 || len(password) > 72 {
		return Session{}, fmt.Errorf("%w: password must contain at least 12 characters and at most 72 bytes", domain.ErrInvalidInput)
	}
	if err = ctx.Err(); err != nil {
		return Session{}, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), s.cost)
	if err != nil {
		return Session{}, err
	}
	user, err := s.repo.Create(ctx, email, username, string(hash))
	if err != nil {
		return Session{}, err
	}
	return s.session(user)
}

func (s *Auth) Login(ctx context.Context, identifier, password string) (Session, error) {
	identifier = strings.ToLower(strings.TrimSpace(identifier))
	if identifier == "" || len(identifier) > 254 || len(password) == 0 || len(password) > 72 {
		return Session{}, domain.ErrCredentials
	}
	user, err := s.repo.FindByIdentifier(ctx, identifier)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return Session{}, err
	}
	hash := []byte(user.PasswordHash)
	if errors.Is(err, domain.ErrNotFound) {
		hash = s.dummy
	}
	check := bcrypt.CompareHashAndPassword(hash, []byte(password))
	if err != nil || check != nil {
		return Session{}, domain.ErrCredentials
	}
	return s.session(user)
}

func (s *Auth) Me(ctx context.Context, id int64) (domain.User, error) {
	return s.repo.FindByID(ctx, id)
}

func (s *Auth) session(user domain.User) (Session, error) {
	access, err := s.issuer.Issue(user.ID)
	if err != nil {
		return Session{}, err
	}
	return Session{User: user, AccessToken: access, TokenType: "Bearer", ExpiresIn: int64(s.ttl.Seconds())}, nil
}
