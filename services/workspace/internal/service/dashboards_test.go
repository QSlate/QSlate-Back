package service

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"qslate/services/workspace/internal/domain"
)

type recordingRepository struct {
	domain.Repository
	created domain.Dashboard
	owner   int64
	cursor  string
	limit   int
}

func (r *recordingRepository) Create(_ context.Context, item domain.Dashboard) (domain.Dashboard, error) {
	r.created = item
	return item, nil
}
func (r *recordingRepository) List(_ context.Context, owner int64, cursor string, limit int) (domain.Page, error) {
	r.owner = owner
	r.cursor = cursor
	r.limit = limit
	return domain.Page{Items: []domain.Summary{}}, nil
}

func TestCreateNormalizesMetadataAndGeneratesOpaqueIDs(t *testing.T) {
	repo := &recordingRepository{}
	svc, err := New(repo, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	input := domain.Metadata{Title: " My Lab ", Description: " Research\nnotes ", Tags: []string{" Forex ", "forex", "Rust"}}
	first, err := svc.Create(context.Background(), 42, input)
	if err != nil {
		t.Fatal(err)
	}
	if first.OwnerID != 42 || !idPattern.MatchString(first.ID) || first.Title != "My Lab" || first.Description != "Research\nnotes" || !reflect.DeepEqual(first.Tags, []string{"forex", "rust"}) {
		t.Fatal("normalization, owner or opaque ID invalid")
	}
	second, err := svc.Create(context.Background(), 42, domain.Metadata{Title: "Other"})
	if err != nil || first.ID == second.ID || second.Tags == nil {
		t.Fatal("IDs must be unique and empty tags must encode as an array")
	}
	if input.Tags[0] != " Forex " {
		t.Fatal("caller input mutated")
	}
}

func TestCreateRejectsInvalidMetadataBeforeRepository(t *testing.T) {
	svc, _ := New(&recordingRepository{}, testConfig())
	for _, input := range []domain.Metadata{
		{}, {Title: " "}, {Title: strings.Repeat("é", 151)}, {Title: "bad\x00name"}, {Title: "bad\nname"},
		{Title: "Lab", Description: strings.Repeat("a", 2001)}, {Title: "Lab", Description: "bad\x00description"},
		{Title: "Lab", Tags: []string{""}}, {Title: "Lab", Tags: []string{strings.Repeat("x", 41)}},
		{Title: "Lab", Tags: make([]string, 21)}, {Title: string([]byte{0xff})},
	} {
		if _, err := svc.Create(context.Background(), 1, input); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("invalid input accepted: %v", err)
		}
	}
	if _, err := svc.Create(context.Background(), 0, domain.Metadata{Title: "Lab"}); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatal("unauthenticated owner accepted")
	}
	if _, err := New(nil, testConfig()); err == nil {
		t.Fatal("nil repository accepted")
	}
}

func TestListValidationAndDefaultPagination(t *testing.T) {
	repo := &recordingRepository{}
	svc, _ := New(repo, testConfig())
	if _, err := svc.List(context.Background(), 42, "", 0); err != nil || repo.limit != 100 || repo.owner != 42 {
		t.Fatal("pagination defaults incorrect")
	}
	for _, limit := range []int{-1, 1001} {
		if _, err := svc.List(context.Background(), 42, "", limit); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatal("invalid limit accepted")
		}
	}
	if _, err := svc.List(context.Background(), 42, "invalid", 1); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatal("invalid cursor accepted")
	}
	if _, err := svc.List(context.Background(), 0, "", 1); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatal("unscoped list accepted")
	}
}

func TestResourceValidationAndCancellation(t *testing.T) {
	svc, _ := New(&recordingRepository{}, testConfig())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.Create(ctx, 1, domain.Metadata{Title: "Lab"}); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled create accepted")
	}
	if _, err := svc.List(ctx, 1, "", 1); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled list accepted")
	}
	if _, err := svc.Get(ctx, 1, strings.Repeat("a", 43)); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled read accepted")
	}
	if _, err := svc.Get(context.Background(), 1, "../other"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("invalid ID accepted")
	}
	if _, err := svc.ReplaceMetadata(context.Background(), 0, strings.Repeat("a", 43), domain.Metadata{Title: "Lab"}); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatal("unscoped update accepted")
	}
	if err := svc.Delete(context.Background(), 0, strings.Repeat("a", 43)); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatal("unscoped delete accepted")
	}
}
