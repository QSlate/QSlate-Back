package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"qslate/services/auth/internal/domain"
)

type memoryRepository struct {
	users   []domain.User
	failure error
}

func (m *memoryRepository) Create(ctx context.Context, email, name, hash string) (domain.User, error) {
	if m.failure != nil {
		return domain.User{}, m.failure
	}
	if err := ctx.Err(); err != nil {
		return domain.User{}, err
	}
	for _, user := range m.users {
		if user.Email == email || user.Username == name {
			return domain.User{}, domain.ErrConflict
		}
	}
	user := domain.User{ID: int64(len(m.users) + 1), Email: email, Username: name, PasswordHash: hash, CreatedAt: time.Now()}
	m.users = append(m.users, user)
	return user, nil
}
func (m *memoryRepository) FindByIdentifier(ctx context.Context, id string) (domain.User, error) {
	if m.failure != nil {
		return domain.User{}, m.failure
	}
	for _, user := range m.users {
		if user.Email == id || user.Username == id {
			return user, nil
		}
	}
	return domain.User{}, domain.ErrNotFound
}
func (m *memoryRepository) FindByID(ctx context.Context, id int64) (domain.User, error) {
	for _, user := range m.users {
		if user.ID == id {
			return user, nil
		}
	}
	return domain.User{}, domain.ErrNotFound
}

type issuer struct{}

func (issuer) Issue(int64) (string, error) { return "signed-token", nil }

func TestRegisterLoginAndConflicts(t *testing.T) {
	repo := &memoryRepository{}
	svc, err := New(repo, issuer{}, bcrypt.MinCost, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	session, err := svc.Register(context.Background(), " Alice@Example.com ", " Alice_01 ", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if session.User.Email != "alice@example.com" || session.User.Username != "alice_01" || session.ExpiresIn != 900 {
		t.Fatal("normalization or token TTL failed")
	}
	if bcrypt.CompareHashAndPassword([]byte(repo.users[0].PasswordHash), []byte("correct horse battery")) != nil {
		t.Fatal("password not hashed")
	}
	for _, identifier := range []string{"alice@example.com", "ALICE_01"} {
		login, err := svc.Login(context.Background(), identifier, "correct horse battery")
		if err != nil || login.User.ID != session.User.ID {
			t.Fatalf("login failed: %v", err)
		}
	}
	for _, identifier := range []string{"alice_01", "missing"} {
		if _, err = svc.Login(context.Background(), identifier, "wrong-password"); !errors.Is(err, domain.ErrCredentials) {
			t.Fatalf("expected generic credentials error, got %v", err)
		}
	}
	if _, err = svc.Register(context.Background(), "ALICE@EXAMPLE.COM", "other", "correct horse battery"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("email conflict: %v", err)
	}
	if _, err = svc.Register(context.Background(), "other@example.com", "ALICE_01", "correct horse battery"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("username conflict: %v", err)
	}
	repo.failure = errors.New("database unavailable")
	if _, err = svc.Login(context.Background(), "alice_01", "correct horse battery"); !errors.Is(err, repo.failure) {
		t.Fatal("database failure masked as invalid credentials")
	}
}

func TestRegisterValidationAndCancellation(t *testing.T) {
	svc, err := New(&memoryRepository{}, issuer{}, bcrypt.MinCost, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ email, username, password string }{
		{"Alice <alice@example.com>", "alice", "correct horse battery"},
		{"alice@example.com", "a", "correct horse battery"},
		{"alice@example.com", "alice", "short"},
		{"alice@example.com", "alice", strings.Repeat("é", 37)},
	} {
		if _, err = svc.Register(context.Background(), test.email, test.username, test.password); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("invalid input accepted: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = svc.Register(ctx, "alice@example.com", "alice", "correct horse battery"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}
