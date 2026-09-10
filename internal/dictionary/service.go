package dictionary

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"golang.org/x/sync/singleflight"

	"english-learning-mcp/internal/apperr"
	"english-learning-mcp/internal/domain"
	"english-learning-mcp/internal/storage"
)

type SnapshotStore interface {
	ActiveDictionarySnapshot(
		ctx context.Context,
		provider string,
		normalizedTerm string,
		datasetVersion string,
		parserVersion int,
	) (*storage.DictionarySnapshot, error)
	InsertDictionarySnapshot(ctx context.Context, input storage.DictionarySnapshotInsert) (*storage.DictionarySnapshot, error)
}

type Service struct {
	store    SnapshotStore
	provider Provider
	now      func() time.Time
	logger   *slog.Logger
	lookups  singleflight.Group
}

func NewService(store SnapshotStore, provider Provider, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		store:    store,
		provider: provider,
		now:      time.Now,
		logger:   logger,
	}
}

func (service *Service) Lookup(ctx context.Context, term string, refresh bool) (domain.DictionaryLookupResult, error) {
	displayTerm := domain.DisplayTerm(term)
	normalizedTerm := domain.NormalizeTerm(displayTerm)
	if !domain.ValidTerm(displayTerm) {
		return domain.DictionaryLookupResult{}, apperr.New(
			apperr.InvalidArgument,
			"term must contain 1 to 200 Unicode characters after whitespace normalization",
		)
	}

	cached, err := service.cachedSnapshot(ctx, normalizedTerm)
	if err != nil && !errors.Is(err, storage.ErrCorruptData) {
		return domain.DictionaryLookupResult{}, err
	}
	if !refresh && usableSnapshot(cached, service.now().UTC()) {
		return lookupResult(cached, displayTerm, domain.CacheHit), nil
	}

	key := service.provider.Name() + "\x00" + service.provider.DatasetVersion() + "\x00" +
		strconv.Itoa(service.provider.ParserVersion()) + "\x00" + normalizedTerm
	for {
		if err := ctx.Err(); err != nil {
			return domain.DictionaryLookupResult{}, err
		}
		result := service.lookups.DoChan(key, func() (any, error) {
			// A canceled caller must not cancel shared work. The provider's HTTP
			// timeouts still bound each upstream request.
			return service.fetchSnapshot(context.WithoutCancel(ctx), normalizedTerm, refresh)
		})
		select {
		case <-ctx.Done():
			return domain.DictionaryLookupResult{}, ctx.Err()
		case shared := <-result:
			if err := ctx.Err(); err != nil {
				return domain.DictionaryLookupResult{}, err
			}
			if shared.Err != nil {
				return domain.DictionaryLookupResult{}, shared.Err
			}
			fetched := shared.Val.(snapshotResult)
			if refresh && fetched.state == domain.CacheHit {
				// An ordinary lookup may have won the race and found a newly
				// cached snapshot. A refresh must join real upstream work.
				continue
			}
			state := fetched.state
			if state == domain.CacheMiss && (refresh || cached != nil) {
				state = domain.CacheRefreshed
			}
			return lookupResult(fetched.snapshot, displayTerm, state), nil
		}
	}
}

const negativeCacheTTL = 5 * time.Minute

type snapshotResult struct {
	snapshot *storage.DictionarySnapshot
	state    domain.CacheState
}

func usableSnapshot(snapshot *storage.DictionarySnapshot, now time.Time) bool {
	return snapshot != nil && (len(snapshot.Data.Entries) > 0 ||
		snapshot.Data.Status == http.StatusNotFound && now.Before(snapshot.ExpiresAt))
}

func (service *Service) cachedSnapshot(ctx context.Context, normalizedTerm string) (*storage.DictionarySnapshot, error) {
	cached, err := service.store.ActiveDictionarySnapshot(
		ctx,
		service.provider.Name(),
		normalizedTerm,
		service.provider.DatasetVersion(),
		service.provider.ParserVersion(),
	)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, nil
	}
	if errors.Is(err, storage.ErrCorruptData) {
		service.logger.Warn("dictionary cache entry is corrupt and will be refreshed")
		return nil, err
	}
	if err != nil {
		return nil, apperr.Wrap(apperr.InternalError, "failed to read the dictionary cache", err)
	}
	return cached, nil
}

func (service *Service) fetchSnapshot(ctx context.Context, normalizedTerm string, refresh bool) (snapshotResult, error) {
	// Recheck inside the flight: another flight may have completed after the
	// caller's initial cache read but before this one was registered.
	cached, cacheErr := service.cachedSnapshot(ctx, normalizedTerm)
	if cacheErr != nil && !errors.Is(cacheErr, storage.ErrCorruptData) {
		return snapshotResult{}, cacheErr
	}
	if !refresh && usableSnapshot(cached, service.now().UTC()) {
		return snapshotResult{cached, domain.CacheHit}, nil
	}

	data, providerErr := service.provider.Lookup(ctx, normalizedTerm)
	if providerErr != nil {
		if cached != nil {
			service.logger.Warn("dictionary provider failed; using cached snapshot")
			return snapshotResult{cached, domain.CacheStaleFallback}, nil
		}
		if errors.Is(cacheErr, storage.ErrCorruptData) {
			return snapshotResult{}, apperr.Wrap(
				apperr.InternalError,
				"the cached dictionary snapshot is corrupt and could not be refreshed",
				cacheErr,
			)
		}
		return snapshotResult{}, apperr.Wrap(
			apperr.UpstreamError,
			"the dictionary provider failed and no cached snapshot is available",
			providerErr,
		)
	}

	now := service.now().UTC()
	expiresAt := now
	if data.Status == http.StatusNotFound && len(data.Entries) == 0 {
		// Only a genuine missing-term response receives a negative cache TTL.
		expiresAt = now.Add(negativeCacheTTL)
	}
	snapshot, err := service.store.InsertDictionarySnapshot(ctx, storage.DictionarySnapshotInsert{
		Provider:       service.provider.Name(),
		NormalizedTerm: normalizedTerm,
		ParserVersion:  service.provider.ParserVersion(),
		DatasetVersion: service.provider.DatasetVersion(),
		Data:           data,
		FetchedAt:      now,
		ExpiresAt:      expiresAt,
	})
	if err != nil {
		return snapshotResult{}, apperr.Wrap(
			apperr.InternalError,
			"failed to store the dictionary snapshot",
			err,
		)
	}
	return snapshotResult{snapshot, domain.CacheMiss}, nil
}

func lookupResult(snapshot *storage.DictionarySnapshot, requestedTerm string, state domain.CacheState) domain.DictionaryLookupResult {
	return domain.DictionaryLookupResult{
		LookupID:       snapshot.ID,
		RequestedTerm:  requestedTerm,
		NormalizedTerm: snapshot.NormalizedTerm,
		Cache: domain.DictionaryCache{
			State:     state,
			FetchedAt: storage.TimeString(snapshot.FetchedAt),
		},
		Source: domain.SourceRef{
			Provider:       snapshot.Provider,
			SourceURL:      snapshot.Data.SourceURL,
			DatasetVersion: snapshot.DatasetVersion,
			ParserVersion:  snapshot.ParserVersion,
		},
		Status:       snapshot.Data.Status,
		Entries:      snapshot.Data.Entries,
		Suggestions:  snapshot.Data.Suggestions,
		Images:       snapshot.Data.Images,
		Idioms:       snapshot.Data.Idioms,
		Collocations: snapshot.Data.Collocations,
	}
}
