package vocabulary

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"english-learning-mcp/internal/apperr"
	"english-learning-mcp/internal/domain"
	"english-learning-mcp/internal/storage"
)

type Store interface {
	ActiveDictionarySnapshot(
		ctx context.Context,
		provider string,
		normalizedTerm string,
		datasetVersion string,
		parserVersion int,
	) (*storage.DictionarySnapshot, error)
	SaveVocabulary(
		ctx context.Context,
		ownerKey string,
		term string,
		normalizedTerm string,
		lookupID string,
		customDescription *string,
		now time.Time,
	) (created bool, item domain.VocabularyItem, err error)
	VocabularyByID(ctx context.Context, ownerKey, itemID string) (domain.VocabularyItem, error)
	VocabularyByTerm(ctx context.Context, ownerKey, normalizedTerm string) (domain.VocabularyItem, error)
	ListVocabulary(ctx context.Context, input storage.VocabularyListQuery) ([]domain.VocabularyItem, error)
	DeleteVocabulary(ctx context.Context, ownerKey, itemID string) error
}

type Service struct {
	store         Store
	ownerKey      string
	currentSource storage.SourceVersion
	now           func() time.Time
}

type SaveResult struct {
	Created bool                  `json:"created"`
	Item    domain.VocabularyItem `json:"item"`
}

type ListOptions struct {
	Query  string
	Sort   string
	Limit  int
	Cursor string
}

type ListResult struct {
	Items      []domain.VocabularyItem `json:"items"`
	NextCursor string                  `json:"nextCursor,omitempty"`
}

type listFilter struct {
	Query string `json:"query"`
}

func NewService(store Store, ownerKey string, currentSource storage.SourceVersion) *Service {
	return &Service{
		store:         store,
		ownerKey:      ownerKey,
		currentSource: currentSource,
		now:           time.Now,
	}
}

func (service *Service) Save(ctx context.Context, term string, customDescription *string) (SaveResult, error) {
	displayTerm, normalizedTerm, err := validateTerm(term)
	if err != nil {
		return SaveResult{}, err
	}
	if customDescription != nil {
		description := strings.TrimSpace(*customDescription)
		if utf8.RuneCountInString(description) > 5000 {
			return SaveResult{}, apperr.New(
				apperr.InvalidArgument,
				"customDescription must contain at most 5000 Unicode characters",
			)
		}
		customDescription = &description
	}
	snapshot, err := service.store.ActiveDictionarySnapshot(
		ctx,
		service.currentSource.Provider,
		normalizedTerm,
		service.currentSource.DatasetVersion,
		service.currentSource.ParserVersion,
	)
	lookupID := ""
	if err == nil && len(snapshot.Data.Entries) > 0 {
		lookupID = snapshot.ID
	} else if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return SaveResult{}, apperr.Wrap(apperr.InternalError, "failed to read the dictionary lookup", err)
	}
	created, item, err := service.store.SaveVocabulary(
		ctx,
		service.ownerKey,
		displayTerm,
		normalizedTerm,
		lookupID,
		customDescription,
		service.now().UTC(),
	)
	if err != nil {
		return SaveResult{}, apperr.Wrap(apperr.InternalError, "failed to save the vocabulary item", err)
	}
	return SaveResult{Created: created, Item: item}, nil
}

func (service *Service) Get(ctx context.Context, itemID, term string) (domain.VocabularyItem, error) {
	itemID = strings.TrimSpace(itemID)
	hasID := itemID != ""
	hasTerm := domain.DisplayTerm(term) != ""
	if hasID == hasTerm {
		return domain.VocabularyItem{}, apperr.New(
			apperr.InvalidArgument,
			"exactly one of itemId or term is required",
		)
	}

	var item domain.VocabularyItem
	var err error
	if hasID {
		item, err = service.store.VocabularyByID(ctx, service.ownerKey, itemID)
	} else {
		_, normalizedTerm, validationErr := validateTerm(term)
		if validationErr != nil {
			return domain.VocabularyItem{}, validationErr
		}
		item, err = service.store.VocabularyByTerm(ctx, service.ownerKey, normalizedTerm)
	}
	if errors.Is(err, storage.ErrNotFound) {
		return domain.VocabularyItem{}, apperr.New(apperr.NotFound, "the vocabulary item was not found")
	}
	if err != nil {
		return domain.VocabularyItem{}, apperr.Wrap(apperr.InternalError, "failed to read the vocabulary item", err)
	}
	return item, nil
}

func (service *Service) List(ctx context.Context, options ListOptions) (ListResult, error) {
	sortOrder := options.Sort
	if sortOrder == "" {
		sortOrder = "recent"
	}
	if sortOrder != "recent" && sortOrder != "oldest" && sortOrder != "alphabetical" {
		return ListResult{}, apperr.New(apperr.InvalidArgument, "sort must be recent, oldest, or alphabetical")
	}
	limit := options.Limit
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 100 {
		return ListResult{}, apperr.New(apperr.InvalidArgument, "limit must be between 1 and 100")
	}

	filter := listFilter{
		Query: domain.NormalizeTerm(options.Query),
	}
	filterDigest, digestErr := storage.FilterDigest(filter)
	if digestErr != nil {
		return ListResult{}, apperr.Wrap(apperr.InternalError, "failed to prepare vocabulary pagination", digestErr)
	}
	cursorPrimary, cursorID, cursorErr := storage.DecodeCursor(options.Cursor, "vocabulary", sortOrder, filterDigest)
	if cursorErr != nil {
		return ListResult{}, apperr.New(apperr.InvalidArgument, "cursor is invalid or does not match these filters")
	}

	items, err := service.store.ListVocabulary(ctx, storage.VocabularyListQuery{
		OwnerKey:      service.ownerKey,
		Query:         filter.Query,
		Sort:          sortOrder,
		Limit:         limit + 1,
		CursorPrimary: cursorPrimary,
		CursorID:      cursorID,
	})
	if err != nil {
		return ListResult{}, apperr.Wrap(apperr.InternalError, "failed to list vocabulary items", err)
	}

	result := ListResult{Items: items}
	if len(items) <= limit {
		return result, nil
	}
	result.Items = items[:limit]
	last := result.Items[len(result.Items)-1]
	primary := last.UpdatedAt
	if sortOrder == "alphabetical" {
		primary = last.NormalizedTerm
	}
	result.NextCursor, err = storage.EncodeCursor("vocabulary", sortOrder, filterDigest, primary, last.ItemID)
	if err != nil {
		return ListResult{}, apperr.Wrap(apperr.InternalError, "failed to encode the vocabulary cursor", err)
	}
	return result, nil
}

func (service *Service) Delete(ctx context.Context, itemID string) error {
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return apperr.New(apperr.InvalidArgument, "itemId is required")
	}
	if err := service.store.DeleteVocabulary(ctx, service.ownerKey, itemID); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return apperr.New(apperr.NotFound, "the vocabulary item was not found")
		}
		return apperr.Wrap(apperr.InternalError, "failed to delete the vocabulary item", err)
	}
	return nil
}

func validateTerm(term string) (displayTerm, normalizedTerm string, err error) {
	displayTerm = domain.DisplayTerm(term)
	if !domain.ValidTerm(displayTerm) {
		return "", "", apperr.New(
			apperr.InvalidArgument,
			"term must contain 1 to 200 Unicode characters after whitespace normalization",
		)
	}
	return displayTerm, domain.NormalizeTerm(displayTerm), nil
}
