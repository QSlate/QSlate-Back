package service

import (
	"context"
	"errors"
	"qslate/services/marketplace/internal/domain"
	"strings"
	"testing"
)

type repositoryStub struct {
	domain.Repository
	calls  int
	filter domain.Filter
}

func (r *repositoryStub) Publish(_ context.Context, uid int64, key string, in domain.PublishInput, _ domain.Snapshot) (domain.Item, error) {
	r.calls++
	return domain.Item{ID: key, AuthorID: uid, Name: in.Name, Visibility: in.Visibility}, nil
}
func (r *repositoryStub) List(_ context.Context, f domain.Filter) (domain.Page, error) {
	r.filter = f
	return domain.Page{}, nil
}

type artifactStub struct {
	calls int
	err   error
}

func (a *artifactStub) Snapshot(context.Context, string, string, string) (domain.Snapshot, error) {
	a.calls++
	return domain.Snapshot{}, a.err
}
func TestPublicationValidationAndDependencyFailures(t *testing.T) {
	cfg, e := LoadConfig("../../config/marketplace.json")
	if e != nil {
		t.Fatal(e)
	}
	repo, art := &repositoryStub{}, &artifactStub{}
	svc, e := New(repo, art, cfg)
	if e != nil {
		t.Fatal(e)
	}
	in := domain.PublishInput{Kind: "widget", ResourceID: strings.Repeat("a", 43), VersionID: strings.Repeat("b", 43), Name: "  Chart  ", Category: "visualization"}
	if _, e = svc.Publish(context.Background(), 0, in); !errors.Is(e, domain.ErrUnauthorized) {
		t.Fatal(e)
	}
	for _, mutate := range []func(*domain.PublishInput){func(v *domain.PublishInput) { v.Kind = "bot" }, func(v *domain.PublishInput) { v.ResourceID = "forged" }, func(v *domain.PublishInput) { v.VersionID = "bad" }, func(v *domain.PublishInput) { v.Name = "\x00" }, func(v *domain.PublishInput) { v.Name = " " }, func(v *domain.PublishInput) { v.Category = "unknown" }, func(v *domain.PublishInput) { v.Visibility = "private" }, func(v *domain.PublishInput) { v.Description = strings.Repeat("x", 10001) }} {
		bad := in
		mutate(&bad)
		if _, e = svc.Publish(context.Background(), 42, bad); !errors.Is(e, domain.ErrInvalid) {
			t.Fatal("invalid input accepted", e)
		}
	}
	if art.calls != 0 || repo.calls != 0 {
		t.Fatal("invalid request reached dependency")
	}
	art.err = domain.ErrNotFound
	if _, e = svc.Publish(context.Background(), 42, in); !errors.Is(e, domain.ErrNotFound) || repo.calls != 0 {
		t.Fatal("unowned source published", e)
	}
	art.err = domain.ErrUnavailable
	if _, e = svc.Publish(context.Background(), 42, in); !errors.Is(e, domain.ErrUnavailable) || repo.calls != 0 {
		t.Fatal("fabricated snapshot", e)
	}
	art.err = nil
	v, e := svc.Publish(context.Background(), 42, in)
	if e != nil || v.Name != "Chart" || v.Visibility != "public" || !IDPattern.MatchString(v.ID) {
		t.Fatal("normalization", e)
	}
	for _, limit := range []int{-1, 1001} {
		if _, e = svc.List(context.Background(), domain.Filter{Limit: limit}); !errors.Is(e, domain.ErrInvalid) {
			t.Fatal("invalid limit accepted")
		}
	}
	if _, e = svc.List(context.Background(), domain.Filter{Query: " needle "}); e != nil || repo.filter.Query != "needle" || repo.filter.Limit != 100 {
		t.Fatal("search defaults", e)
	}
	if _, e = svc.Patch(context.Background(), 42, v.ID, domain.Patch{BaseRevision: 1}); !errors.Is(e, domain.ErrInvalid) {
		t.Fatal("empty patch accepted")
	}
	if _, e = svc.Review(context.Background(), 42, v.ID, domain.ReviewInput{Rating: 6}); !errors.Is(e, domain.ErrInvalid) {
		t.Fatal("invalid rating")
	}
}
