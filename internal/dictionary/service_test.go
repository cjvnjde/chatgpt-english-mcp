package dictionary

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
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

type memorySnapshotStore struct {
	mu       sync.Mutex
	snapshot *storage.DictionarySnapshot
	inserts  int
}

func (store *memorySnapshotStore) ActiveDictionarySnapshot(context.Context, string, string, string, int) (*storage.DictionarySnapshot, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.snapshot == nil {
		return nil, storage.ErrNotFound
	}
	return store.snapshot, nil
}

func (store *memorySnapshotStore) InsertDictionarySnapshot(_ context.Context, input storage.DictionarySnapshotInsert) (*storage.DictionarySnapshot, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.inserts++
	store.snapshot = &storage.DictionarySnapshot{
		ID:             strconv.Itoa(store.inserts),
		Provider:       input.Provider,
		NormalizedTerm: input.NormalizedTerm,
		ParserVersion:  input.ParserVersion,
		DatasetVersion: input.DatasetVersion,
		Data:           input.Data,
		FetchedAt:      input.FetchedAt,
		ExpiresAt:      input.ExpiresAt,
		Active:         true,
	}
	return store.snapshot, nil
}

type controlledProvider struct {
	fakeProvider
	lookup func(context.Context, string) (domain.DictionarySnapshotData, error)
}

func (provider *controlledProvider) Lookup(ctx context.Context, term string) (domain.DictionarySnapshotData, error) {
	return provider.lookup(ctx, term)
}

// Done is observed only when Lookup starts waiting for its shared result.
// The memory store does not inspect context, and upstream receives WithoutCancel.
type waitingContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (ctx *waitingContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.waiting) })
	return ctx.Context.Done()
}

type lookupOutcome struct {
	result domain.DictionaryLookupResult
	err    error
}

func waitForLookupSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("lookup did not reach synchronization point")
	}
}

func receiveLookup(t *testing.T, outcomes <-chan lookupOutcome) lookupOutcome {
	t.Helper()
	select {
	case outcome := <-outcomes:
		return outcome
	case <-time.After(5 * time.Second):
		t.Fatal("lookup did not finish")
		return lookupOutcome{}
	}
}

func TestServiceCoalescesConcurrentLookups(t *testing.T) {
	for _, mode := range []string{"miss", "refresh", "mixed"} {
		t.Run(mode, func(t *testing.T) {
			store := &memorySnapshotStore{}
			var calls atomic.Int32
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			provider := &controlledProvider{lookup: func(ctx context.Context, term string) (domain.DictionarySnapshotData, error) {
				calls.Add(1)
				select {
				case <-release:
					return domain.DictionarySnapshotData{Status: http.StatusNotFound}, nil
				case <-ctx.Done():
					return domain.DictionarySnapshotData{}, ctx.Err()
				}
			}}
			service := NewService(store, provider, nil)
			if mode == "refresh" {
				_, err := store.InsertDictionarySnapshot(context.Background(), storage.DictionarySnapshotInsert{
					Data: domain.DictionarySnapshotData{Status: http.StatusOK, Entries: []domain.DictionaryEntry{{Headword: "bank"}}},
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			terms := []string{"Bank", "bank", "BANK", " bank ", " Bank ", "BANK", "bank", "Bank"}
			outcomes := make([]chan lookupOutcome, len(terms))
			for index, term := range terms {
				ctx := &waitingContext{Context: context.Background(), waiting: make(chan struct{})}
				outcomes[index] = make(chan lookupOutcome, 1)
				refresh := mode == "refresh" || mode == "mixed" && index > 0
				go func() {
					result, err := service.Lookup(ctx, term, refresh)
					outcomes[index] <- lookupOutcome{result, err}
				}()
				waitForLookupSignal(t, ctx.waiting)
			}
			unblock()
			var lookupID string
			for index, outcomeChannel := range outcomes {
				outcome := receiveLookup(t, outcomeChannel)
				wantState := domain.CacheMiss
				if mode == "refresh" || mode == "mixed" && index > 0 {
					wantState = domain.CacheRefreshed
				}
				if outcome.err != nil || outcome.result.Cache.State != wantState ||
					outcome.result.RequestedTerm != domain.DisplayTerm(terms[index]) || outcome.result.NormalizedTerm != "bank" {
					t.Fatalf("caller %d = %#v, error %v", index, outcome.result, outcome.err)
				}
				if lookupID == "" {
					lookupID = outcome.result.LookupID
				} else if outcome.result.LookupID != lookupID {
					t.Fatalf("concurrent callers received different snapshots: %q and %q", lookupID, outcome.result.LookupID)
				}
			}
			wantInserts := 1
			if mode == "refresh" {
				wantInserts++
			}
			if calls.Load() != 1 || store.inserts != wantInserts {
				t.Fatalf("fetches = %d, snapshots = %d; want 1 fetch and %d snapshots", calls.Load(), store.inserts, wantInserts)
			}
		})
	}
}

func TestServiceCancellationDoesNotCancelSharedLookup(t *testing.T) {
	for _, canceledCaller := range []int{0, 1} {
		t.Run(strconv.Itoa(canceledCaller), func(t *testing.T) {
			store := &memorySnapshotStore{}
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			var calls atomic.Int32
			provider := &controlledProvider{lookup: func(ctx context.Context, term string) (domain.DictionarySnapshotData, error) {
				calls.Add(1)
				select {
				case <-release:
					return domain.DictionarySnapshotData{Status: http.StatusNotFound}, nil
				case <-ctx.Done():
					return domain.DictionarySnapshotData{}, ctx.Err()
				}
			}}
			service := NewService(store, provider, nil)
			var outcomes [2]chan lookupOutcome
			var cancel [2]context.CancelFunc
			for index := range outcomes {
				base, cancelCall := context.WithCancel(context.Background())
				cancel[index] = cancelCall
				t.Cleanup(cancelCall)
				ctx := &waitingContext{Context: base, waiting: make(chan struct{})}
				outcomes[index] = make(chan lookupOutcome, 1)
				go func() {
					result, err := service.Lookup(ctx, "bank", false)
					outcomes[index] <- lookupOutcome{result, err}
				}()
				waitForLookupSignal(t, ctx.waiting)
			}
			cancel[canceledCaller]()
			if outcome := receiveLookup(t, outcomes[canceledCaller]); !errors.Is(outcome.err, context.Canceled) {
				t.Fatalf("canceled caller error = %v", outcome.err)
			}
			unblock()
			survivor := receiveLookup(t, outcomes[1-canceledCaller])
			if survivor.err != nil || survivor.result.Status != http.StatusNotFound || calls.Load() != 1 || store.inserts != 1 {
				t.Fatalf("live caller = %#v, error %v; fetches %d, snapshots %d", survivor.result, survivor.err, calls.Load(), store.inserts)
			}
		})
	}
}

func TestServiceNegativeCacheExpiresAndRefreshes(t *testing.T) {
	var dictionaryCalls, spellcheckCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/dictionary/english/absent" {
			dictionaryCalls.Add(1)
		} else {
			spellcheckCalls.Add(1)
		}
		writer.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)
	baseURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	store := &memorySnapshotStore{}
	service := NewService(store, NewCambridgeProvider(baseURL, time.Second, nil), nil)
	now := time.Date(2026, time.September, 10, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	first, err := service.Lookup(context.Background(), "absent", false)
	if err != nil || first.Status != http.StatusNotFound || first.Cache.State != domain.CacheMiss {
		t.Fatalf("first missing lookup = %#v, error %v", first, err)
	}
	now = now.Add(5*time.Minute - time.Nanosecond)
	hit, err := service.Lookup(context.Background(), "ABSENT", false)
	if err != nil || hit.LookupID != first.LookupID || hit.Cache.State != domain.CacheHit ||
		hit.RequestedTerm != "ABSENT" || dictionaryCalls.Load() != 1 || spellcheckCalls.Load() != 1 || store.inserts != 1 {
		t.Fatalf("negative hit = %#v, error %v; dictionary %d, spellcheck %d, snapshots %d",
			hit, err, dictionaryCalls.Load(), spellcheckCalls.Load(), store.inserts)
	}
	now = now.Add(time.Nanosecond)
	expired, err := service.Lookup(context.Background(), "absent", false)
	if err != nil || expired.LookupID == first.LookupID || expired.Cache.State != domain.CacheRefreshed ||
		dictionaryCalls.Load() != 2 || spellcheckCalls.Load() != 2 || store.inserts != 2 {
		t.Fatalf("expired negative lookup = %#v, error %v; dictionary %d, spellcheck %d, snapshots %d",
			expired, err, dictionaryCalls.Load(), spellcheckCalls.Load(), store.inserts)
	}
	refreshed, err := service.Lookup(context.Background(), "absent", true)
	if err != nil || refreshed.LookupID == expired.LookupID || refreshed.Cache.State != domain.CacheRefreshed ||
		dictionaryCalls.Load() != 3 || spellcheckCalls.Load() != 3 || store.inserts != 3 {
		t.Fatalf("forced negative refresh = %#v, error %v; dictionary %d, spellcheck %d, snapshots %d",
			refreshed, err, dictionaryCalls.Load(), spellcheckCalls.Load(), store.inserts)
	}
}

func TestServiceProviderFailuresDoNotPopulateNegativeCache(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusServiceUnavailable} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				writer.WriteHeader(status)
				_, _ = io.WriteString(writer, "<html><body>Verify you are human</body></html>")
			}))
			t.Cleanup(server.Close)
			baseURL, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			store := &memorySnapshotStore{}
			service := NewService(store, NewCambridgeProvider(baseURL, time.Second, nil), nil)
			for attempt := range 2 {
				before := calls.Load()
				_, err := service.Lookup(context.Background(), "bank", false)
				if err == nil || apperr.From(err).Code != apperr.UpstreamError || calls.Load() <= before || store.inserts != 0 {
					t.Fatalf("attempt %d: error %v, upstream calls %d -> %d, snapshots %d", attempt, err, before, calls.Load(), store.inserts)
				}
			}
		})
	}
}

type racingSnapshotStore struct {
	memorySnapshotStore
	read func(context.Context) (*storage.DictionarySnapshot, error)
}

func (store *racingSnapshotStore) ActiveDictionarySnapshot(ctx context.Context, _ string, _ string, _ string, _ int) (*storage.DictionarySnapshot, error) {
	return store.read(ctx)
}

func TestServiceRefreshDoesNotJoinCacheOnlyResult(t *testing.T) {
	store := &racingSnapshotStore{}
	cached, err := store.InsertDictionarySnapshot(context.Background(), storage.DictionarySnapshotInsert{
		NormalizedTerm: "bank",
		Data:           domain.DictionarySnapshotData{Status: http.StatusOK, Entries: []domain.DictionaryEntry{{Headword: "bank"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	rechecking := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	var reads atomic.Int32
	store.read = func(ctx context.Context) (*storage.DictionarySnapshot, error) {
		switch reads.Add(1) {
		case 1:
			// Another request populated the cache after this stale initial read.
			return nil, storage.ErrNotFound
		case 2:
			close(rechecking)
			<-release
		}
		return store.memorySnapshotStore.ActiveDictionarySnapshot(ctx, "", "", "", 0)
	}
	provider := &fakeProvider{}
	service := NewService(store, provider, nil)
	ordinary := make(chan lookupOutcome, 1)
	go func() {
		result, err := service.Lookup(context.Background(), "Bank", false)
		ordinary <- lookupOutcome{result, err}
	}()
	waitForLookupSignal(t, rechecking)
	ctx := &waitingContext{Context: context.Background(), waiting: make(chan struct{})}
	refresh := make(chan lookupOutcome, 1)
	go func() {
		result, err := service.Lookup(ctx, "BANK", true)
		refresh <- lookupOutcome{result, err}
	}()
	waitForLookupSignal(t, ctx.waiting)
	unblock()
	hit := receiveLookup(t, ordinary)
	if hit.err != nil || hit.result.Cache.State != domain.CacheHit || hit.result.LookupID != cached.ID {
		t.Fatalf("ordinary lookup = %#v, error %v", hit.result, hit.err)
	}
	fetched := receiveLookup(t, refresh)
	if fetched.err != nil || fetched.result.Cache.State != domain.CacheRefreshed ||
		fetched.result.LookupID == cached.ID || provider.calls != 1 || store.inserts != 2 {
		t.Fatalf("explicit refresh = %#v, error %v; fetches %d, snapshots %d",
			fetched.result, fetched.err, provider.calls, store.inserts)
	}
}

func TestServiceRechecksCacheAfterDelayedMiss(t *testing.T) {
	store := &racingSnapshotStore{}
	reading := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	var reads atomic.Int32
	store.read = func(ctx context.Context) (*storage.DictionarySnapshot, error) {
		if reads.Add(1) == 1 {
			close(reading)
			<-release
			return nil, storage.ErrNotFound
		}
		return store.memorySnapshotStore.ActiveDictionarySnapshot(ctx, "", "", "", 0)
	}
	provider := &fakeProvider{}
	service := NewService(store, provider, nil)
	delayed := make(chan lookupOutcome, 1)
	go func() {
		result, err := service.Lookup(context.Background(), "Bank", false)
		delayed <- lookupOutcome{result, err}
	}()
	waitForLookupSignal(t, reading)
	first, err := service.Lookup(context.Background(), "bank", false)
	if err != nil {
		t.Fatal(err)
	}
	unblock()
	second := receiveLookup(t, delayed)
	if second.err != nil || second.result.Cache.State != domain.CacheHit || second.result.LookupID != first.LookupID ||
		provider.calls != 1 || store.inserts != 1 {
		t.Fatalf("delayed miss = %#v, error %v; fetches %d, snapshots %d",
			second.result, second.err, provider.calls, store.inserts)
	}
}
