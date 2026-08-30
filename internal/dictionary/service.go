package dictionary

import (
	"context"
	"errors"
	"log/slog"
	"time"

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
	ttl      time.Duration
	now      func() time.Time
	logger   *slog.Logger
}

func NewService(store SnapshotStore, provider Provider, ttl time.Duration, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		store:    store,
		provider: provider,
		ttl:      ttl,
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

	cached, cacheErr := service.store.ActiveDictionarySnapshot(
		ctx,
		service.provider.Name(),
		normalizedTerm,
		service.provider.DatasetVersion(),
		service.provider.ParserVersion(),
	)
	if cacheErr != nil && !errors.Is(cacheErr, storage.ErrNotFound) && !errors.Is(cacheErr, storage.ErrCorruptData) {
		return domain.DictionaryLookupResult{}, apperr.Wrap(
			apperr.InternalError,
			"failed to read the dictionary cache",
			cacheErr,
		)
	}
	if errors.Is(cacheErr, storage.ErrCorruptData) {
		service.logger.Warn("dictionary cache entry is corrupt and will be refreshed")
		cached = nil
	}

	now := service.now().UTC()
	if cached != nil && !refresh && now.Before(cached.ExpiresAt) {
		return lookupResult(cached, displayTerm, domain.CacheHit), nil
	}

	data, providerErr := service.provider.Lookup(ctx, normalizedTerm)
	if providerErr != nil {
		if cached != nil {
			service.logger.Warn("dictionary provider failed; using cached snapshot")
			return lookupResult(cached, displayTerm, domain.CacheStaleFallback), nil
		}
		if errors.Is(cacheErr, storage.ErrCorruptData) {
			return domain.DictionaryLookupResult{}, apperr.Wrap(
				apperr.InternalError,
				"the cached dictionary snapshot is corrupt and could not be refreshed",
				cacheErr,
			)
		}
		return domain.DictionaryLookupResult{}, apperr.Wrap(
			apperr.UpstreamError,
			"the dictionary provider failed and no cached snapshot is available",
			providerErr,
		)
	}

	snapshot, err := service.store.InsertDictionarySnapshot(ctx, storage.DictionarySnapshotInsert{
		Provider:       service.provider.Name(),
		NormalizedTerm: normalizedTerm,
		ParserVersion:  service.provider.ParserVersion(),
		DatasetVersion: service.provider.DatasetVersion(),
		Data:           data,
		FetchedAt:      now,
		ExpiresAt:      now.Add(service.ttl),
	})
	if err != nil {
		return domain.DictionaryLookupResult{}, apperr.Wrap(
			apperr.InternalError,
			"failed to store the dictionary snapshot",
			err,
		)
	}

	state := domain.CacheMiss
	if refresh || cached != nil {
		state = domain.CacheRefreshed
	}
	return lookupResult(snapshot, displayTerm, state), nil
}

func lookupResult(snapshot *storage.DictionarySnapshot, requestedTerm string, state domain.CacheState) domain.DictionaryLookupResult {
	return domain.DictionaryLookupResult{
		LookupID:       snapshot.ID,
		RequestedTerm:  requestedTerm,
		NormalizedTerm: snapshot.NormalizedTerm,
		Cache: domain.DictionaryCache{
			State:     state,
			FetchedAt: storage.TimeString(snapshot.FetchedAt),
			ExpiresAt: storage.TimeString(snapshot.ExpiresAt),
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
