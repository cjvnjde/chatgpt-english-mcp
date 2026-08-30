package dictionary

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"english-learning-mcp/internal/domain"
	"english-learning-mcp/internal/storage"
)

type fakeProvider struct {
	calls int
	fail  bool
}

func (provider *fakeProvider) Name() string {
	return "fake"
}

func (provider *fakeProvider) ParserVersion() int {
	return 1
}

func (provider *fakeProvider) DatasetVersion() string {
	return "test"
}

func (provider *fakeProvider) Lookup(context.Context, string) (domain.DictionarySnapshotData, error) {
	provider.calls++
	if provider.fail {
		return domain.DictionarySnapshotData{}, errors.New("provider unavailable")
	}
	return domain.DictionarySnapshotData{
		SourceURL: "https://example.test/bank",
		Status:    200,
		Entries: []domain.DictionaryEntry{{
			Headword: "bank",
			Definitions: []domain.DictionaryDefinition{{
				Definition: "land beside a river",
				Examples:   []string{},
				Phrases:    []string{},
				SeeAlso:    []string{},
				Images:     []domain.DictionaryImage{},
				Labels:     []string{"B1"},
			}},
		}},
		Suggestions: []string{},
		Images:      []domain.DictionaryImage{},
	}, nil
}

func TestServiceUsesImmutableSnapshotsAndStaleFallback(t *testing.T) {
	store, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})

	provider := &fakeProvider{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := NewService(store, provider, time.Hour, logger)
	now := time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }

	first, err := service.Lookup(context.Background(), "  Bank  ", false)
	if err != nil {
		t.Fatalf("first Lookup() error = %v", err)
	}
	if first.Cache.State != domain.CacheMiss || first.RequestedTerm != "Bank" || first.NormalizedTerm != "bank" {
		t.Fatalf("first lookup = %#v", first)
	}

	second, err := service.Lookup(context.Background(), "bank", false)
	if err != nil {
		t.Fatalf("second Lookup() error = %v", err)
	}
	if second.Cache.State != domain.CacheHit || second.LookupID != first.LookupID || provider.calls != 1 {
		t.Fatalf("second lookup = %#v, provider calls = %d", second, provider.calls)
	}

	refreshed, err := service.Lookup(context.Background(), "bank", true)
	if err != nil {
		t.Fatalf("refresh Lookup() error = %v", err)
	}
	if refreshed.Cache.State != domain.CacheRefreshed || refreshed.LookupID == first.LookupID || provider.calls != 2 {
		t.Fatalf("refreshed lookup = %#v, provider calls = %d", refreshed, provider.calls)
	}

	now = now.Add(2 * time.Hour)
	provider.fail = true
	fallback, err := service.Lookup(context.Background(), "bank", false)
	if err != nil {
		t.Fatalf("fallback Lookup() error = %v", err)
	}
	if fallback.Cache.State != domain.CacheStaleFallback || fallback.LookupID != refreshed.LookupID || provider.calls != 3 {
		t.Fatalf("fallback lookup = %#v, provider calls = %d", fallback, provider.calls)
	}
}
