package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
	"qslate/services/auth/internal/domain"
)

type Issuer interface {
	IssueSession(int64, string) (string, error)
}
type MailSealer interface {
	Seal(email, kind, token string) ([]byte, error)
}
type Config struct {
	AccessTTL, RefreshTTL time.Duration
	BcryptCost            int
}
type Auth struct {
	repo   domain.Store
	issuer Issuer
	mail   MailSealer
	cfg    Config
	dummy  []byte
}
type Session struct {
	User             domain.User `json:"user"`
	AccessToken      string      `json:"access_token"`
	TokenType        string      `json:"token_type"`
	ExpiresIn        int64       `json:"expires_in"`
	RefreshToken     string      `json:"-"`
	RefreshExpiresAt time.Time   `json:"-"`
}

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{2,31}$`)

func New(repo domain.Store, issuer Issuer, mail MailSealer, cfg Config) (*Auth, error) {
	if repo == nil || issuer == nil || mail == nil || cfg.BcryptCost < bcrypt.MinCost || cfg.BcryptCost > 14 || cfg.AccessTTL <= 0 || cfg.RefreshTTL <= cfg.AccessTTL {
		return nil, errors.New("invalid auth configuration")
	}
	secret, err := opaque()
	if err != nil {
		return nil, err
	}
	dummy, err := bcrypt.GenerateFromPassword([]byte(secret), cfg.BcryptCost)
	if err != nil {
		return nil, err
	}
	return &Auth{repo: repo, issuer: issuer, mail: mail, cfg: cfg, dummy: dummy}, nil
}

func validEmail(email string) bool {
	parsed, err := mail.ParseAddress(email)
	at := strings.LastIndexByte(email, '@')
	return err == nil && parsed.Address == email && len(email) <= 254 && at > 0 && strings.Contains(email[at+1:], ".")
}

func validPassword(password string) bool {
	return utf8.ValidString(password) && utf8.RuneCountInString(password) >= 12 && len(password) <= 72
}
func invalid(message string) error { return fmt.Errorf("%w: %s", domain.ErrInvalidInput, message) }
func opaque() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}
func digest(raw string) string { sum := sha256.Sum256([]byte(raw)); return hex.EncodeToString(sum[:]) }

func (s *Auth) Register(ctx context.Context, email, username, password string) (Session, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	username = strings.ToLower(strings.TrimSpace(username))
	if !validEmail(email) {
		return Session{}, invalid("valid email required")
	}
	if !usernamePattern.MatchString(username) {
		return Session{}, invalid("username must be 3-32 letters, digits, underscores or hyphens")
	}
	if !validPassword(password) {
		return Session{}, invalid("password must contain at least 12 characters and at most 72 bytes")
	}
	if err := ctx.Err(); err != nil {
		return Session{}, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), s.cfg.BcryptCost)
	if err != nil {
		return Session{}, err
	}
	action, err := s.action(email, "verify_email")
	if err != nil {
		return Session{}, err
	}
	user, err := s.repo.CreateAccount(ctx, email, username, string(hash), action)
	if err != nil {
		return Session{}, err
	}
	return s.session(ctx, user)
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
	return s.session(ctx, user)
}

func (s *Auth) session(ctx context.Context, user domain.User) (Session, error) {
	id, err := opaque()
	if err != nil {
		return Session{}, err
	}
	raw, err := opaque()
	if err != nil {
		return Session{}, err
	}
	session := domain.Session{ID: id, UserID: user.ID, ExpiresAt: time.Now().Add(s.cfg.RefreshTTL), PasswordHash: user.PasswordHash}
	access, err := s.issuer.IssueSession(user.ID, id)
	if err != nil {
		return Session{}, err
	}
	if err = s.repo.CreateSession(ctx, session, digest(raw)); err != nil {
		return Session{}, err
	}
	return Session{User: user, AccessToken: access, TokenType: "Bearer", ExpiresIn: int64(s.cfg.AccessTTL.Seconds()), RefreshToken: raw, RefreshExpiresAt: session.ExpiresAt}, nil
}

func (s *Auth) Refresh(ctx context.Context, previous string) (Session, error) {
	if len(previous) != 43 {
		return Session{}, domain.ErrCredentials
	}
	raw, err := opaque()
	if err != nil {
		return Session{}, err
	}
	session, user, err := s.repo.RotateSession(ctx, digest(previous), digest(raw))
	if err != nil {
		return Session{}, err
	}
	access, err := s.issuer.IssueSession(user.ID, session.ID)
	if err != nil {
		return Session{}, err
	}
	return Session{User: user, AccessToken: access, TokenType: "Bearer", ExpiresIn: int64(s.cfg.AccessTTL.Seconds()), RefreshToken: raw, RefreshExpiresAt: session.ExpiresAt}, nil
}

func (s *Auth) Logout(ctx context.Context, raw string) error {
	if len(raw) != 43 {
		return nil
	}
	return s.repo.RevokeSession(ctx, digest(raw))
}

func (s *Auth) ValidateSession(ctx context.Context, id int64, sid string) error {
	if id <= 0 || sid == "" {
		return domain.ErrCredentials
	}
	return s.repo.ActiveSession(ctx, id, sid)
}

func (s *Auth) Me(ctx context.Context, id int64) (domain.User, error) {
	return s.repo.FindByID(ctx, id)
}

// ExternalLogin shares local session issuance with password-based authentication.
func (s *Auth) ExternalLogin(ctx context.Context, identity domain.ExternalIdentity) (Session, error) {
	identity.Email = strings.ToLower(strings.TrimSpace(identity.Email))
	if !validEmail(identity.Email) || identity.Provider == "" || identity.Subject == "" || len(identity.Subject) > 255 {
		return Session{}, invalid("invalid external identity")
	}
	if !utf8.ValidString(identity.DisplayName) || utf8.RuneCountInString(identity.DisplayName) > 100 || strings.ContainsFunc(identity.DisplayName, unicode.IsControl) {
		identity.DisplayName = ""
	}
	user, err := s.repo.ExternalAccount(ctx, identity)
	if err != nil {
		return Session{}, err
	}
	return s.session(ctx, user)
}

func (s *Auth) UpdateProfile(ctx context.Context, id int64, patch domain.ProfilePatch) (domain.User, error) {
	if patch.Username == nil && patch.DisplayName == nil {
		return domain.User{}, invalid("provide username or display_name")
	}
	if patch.Username != nil {
		normalized := strings.ToLower(strings.TrimSpace(*patch.Username))
		patch.Username = &normalized
		if !usernamePattern.MatchString(normalized) {
			return domain.User{}, invalid("invalid username")
		}
	}
	if patch.DisplayName != nil {
		name := strings.TrimSpace(*patch.DisplayName)
		patch.DisplayName = &name
		if !utf8.ValidString(name) || utf8.RuneCountInString(name) > 100 || strings.ContainsFunc(name, unicode.IsControl) {
			return domain.User{}, invalid("display_name must contain at most 100 printable characters")
		}
	}
	return s.repo.UpdateProfile(ctx, id, patch)
}

func (s *Auth) Preferences(ctx context.Context, id int64) (domain.Preferences, error) {
	return s.repo.Preferences(ctx, id)
}
func (s *Auth) UpdatePreferences(ctx context.Context, id int64, patch domain.PreferencesPatch) (domain.Preferences, error) {
	if patch.Theme == nil && patch.ColorblindMode == nil {
		return domain.Preferences{}, invalid("provide theme or colorblind_mode")
	}
	if patch.Theme != nil && !slices.Contains([]string{"dark", "light", "system"}, *patch.Theme) {
		return domain.Preferences{}, invalid("theme must be dark, light or system")
	}
	return s.repo.UpdatePreferences(ctx, id, patch)
}

func (s *Auth) action(email, kind string) (domain.Action, error) {
	raw, err := opaque()
	if err != nil {
		return domain.Action{}, err
	}
	sealed, err := s.mail.Seal(email, kind, raw)
	if err != nil {
		return domain.Action{}, err
	}
	ttl := time.Hour
	if kind == "verify_email" {
		ttl = 24 * time.Hour
	}
	return domain.Action{Kind: kind, Hash: digest(raw), ExpiresAt: time.Now().Add(ttl), EncryptedMail: sealed}, nil
}

// Requests for unknown accounts deliberately have the same HTTP result.
func (s *Auth) RequestAction(ctx context.Context, email, kind string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if !validEmail(email) {
		return invalid("valid email required")
	}
	if kind != "verify_email" && kind != "reset_password" {
		return invalid("unsupported action")
	}
	user, err := s.repo.FindByIdentifier(ctx, email)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if kind == "verify_email" && user.EmailVerified {
		return nil
	}
	action, err := s.action(user.Email, kind)
	if err != nil {
		return err
	}
	action.UserID = user.ID
	return s.repo.SaveAction(ctx, action)
}

func (s *Auth) VerifyEmail(ctx context.Context, raw string) error {
	if len(raw) != 43 {
		return domain.ErrActionToken
	}
	return s.repo.ConsumeAction(ctx, "verify_email", digest(raw), "")
}

func (s *Auth) ResetPassword(ctx context.Context, raw, password string) error {
	if len(raw) != 43 {
		return domain.ErrActionToken
	}
	if !validPassword(password) {
		return invalid("password must contain at least 12 characters and at most 72 bytes")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), s.cfg.BcryptCost)
	if err != nil {
		return err
	}
	return s.repo.ConsumeAction(ctx, "reset_password", digest(raw), string(hash))
}

type APIKeyInput struct {
	Name          string   `json:"name"`
	Scopes        []string `json:"scopes"`
	ExpiresInDays *int     `json:"expires_in_days"`
}
type NewAPIKey struct {
	Key    domain.APIKey `json:"key"`
	Secret string        `json:"secret"`
}

var runnerScopes = []string{"runner:heartbeat", "runner:configs:read", "runner:results:write"}

func (s *Auth) CreateAPIKey(ctx context.Context, id int64, input APIKeyInput) (NewAPIKey, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || !utf8.ValidString(input.Name) || utf8.RuneCountInString(input.Name) > 100 || strings.ContainsFunc(input.Name, unicode.IsControl) {
		return NewAPIKey{}, invalid("key name must contain 1-100 printable characters")
	}
	days := 90
	if input.ExpiresInDays != nil {
		days = *input.ExpiresInDays
	}
	if days < 1 || days > 365 {
		return NewAPIKey{}, invalid("expires_in_days must be between 1 and 365")
	}
	if len(input.Scopes) == 0 {
		input.Scopes = slices.Clone(runnerScopes)
	}
	for _, scope := range input.Scopes {
		if !slices.Contains(runnerScopes, scope) {
			return NewAPIKey{}, invalid("unsupported runner scope")
		}
	}
	slices.Sort(input.Scopes)
	input.Scopes = slices.Compact(input.Scopes)
	keyID, err := opaque()
	if err != nil {
		return NewAPIKey{}, err
	}
	raw, err := opaque()
	if err != nil {
		return NewAPIKey{}, err
	}
	secret := "qsl_" + keyID + "." + raw
	key, err := s.repo.CreateAPIKey(ctx, id, domain.APIKey{ID: keyID, Name: input.Name, Hash: digest(secret), Scopes: input.Scopes, ExpiresAt: time.Now().Add(time.Duration(days) * 24 * time.Hour)})
	return NewAPIKey{Key: key, Secret: secret}, err
}
func (s *Auth) APIKeys(ctx context.Context, id int64) ([]domain.APIKey, error) {
	return s.repo.ListAPIKeys(ctx, id)
}
func (s *Auth) RevokeAPIKey(ctx context.Context, id int64, keyID string) error {
	if len(keyID) != 43 {
		return domain.ErrNotFound
	}
	return s.repo.RevokeAPIKey(ctx, id, keyID)
}
