package dictionary

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"english-learning-mcp/internal/apperr"
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
	service := NewService(store, provider, logger)
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

	now = now.Add(365 * 24 * time.Hour)
	provider.fail = true
	permanentHit, err := service.Lookup(context.Background(), "bank", false)
	if err != nil {
		t.Fatalf("permanent Lookup() error = %v", err)
	}
	if permanentHit.Cache.State != domain.CacheHit || permanentHit.LookupID != refreshed.LookupID || provider.calls != 2 {
		t.Fatalf("permanent lookup = %#v, provider calls = %d", permanentHit, provider.calls)
	}

	fallback, err := service.Lookup(context.Background(), "bank", true)
	if err != nil {
		t.Fatalf("refresh fallback Lookup() error = %v", err)
	}
	if fallback.Cache.State != domain.CacheStaleFallback || fallback.LookupID != refreshed.LookupID || provider.calls != 3 {
		t.Fatalf("refresh fallback = %#v, provider calls = %d", fallback, provider.calls)
	}
}

func TestUnrecognizedProviderResponsePreservesSuccessfulCachedLookup(t *testing.T) {
	var malformed atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/dictionary/english/bank" {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		if malformed.Load() {
			_, _ = io.WriteString(writer, "<html><body>Verify you are human</body></html>")
			return
		}
		_, _ = io.WriteString(writer, `<div class="entry-body__el"><span class="hw dhw">bank</span><div class="def ddef_d">an institution</div></div>`)
	}))
	t.Cleanup(server.Close)
	baseURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := NewService(store, NewCambridgeProvider(baseURL, time.Second, logger), logger)
	ctx := context.Background()
	first, err := service.Lookup(ctx, "bank", false)
	if err != nil {
		t.Fatal(err)
	}
	malformed.Store(true)
	fallback, err := service.Lookup(ctx, "bank", true)
	if err != nil || fallback.Cache.State != domain.CacheStaleFallback || fallback.LookupID != first.LookupID ||
		len(fallback.Entries) != 1 || fallback.Entries[0].Definitions[0].Definition != "an institution" {
		t.Fatalf("malformed refresh replaced valid lookup: %#v, error %v", fallback, err)
	}
	cached, err := service.Lookup(ctx, "bank", false)
	if err != nil || cached.Cache.State != domain.CacheHit || cached.LookupID != first.LookupID {
		t.Fatalf("malformed refresh changed active snapshot: %#v, error %v", cached, err)
	}
	missing, err := service.Lookup(ctx, "absent", false)
	if err != nil || missing.Status != http.StatusNotFound || len(missing.Entries) != 0 {
		t.Fatalf("genuine missing term = %#v, error %v", missing, err)
	}
}

func TestDictionaryRejectsMalformedUTF8BeforeFetching(t *testing.T) {
	store, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	provider := &fakeProvider{}
	service := NewService(store, provider, nil)
	_, err = service.Lookup(context.Background(), string([]byte{0xff}), false)
	if err == nil || apperr.From(err).Code != apperr.InvalidArgument || provider.calls != 0 {
		t.Fatalf("invalid UTF-8 lookup error = %v, provider calls = %d", err, provider.calls)
	}
}
