package vocabulary

import (
	"context"
	"errors"
	"strings"
	"time"

	"english-learning-mcp/internal/apperr"
	"english-learning-mcp/internal/domain"
	"english-learning-mcp/internal/storage"
)

type Store interface {
	SaveVocabulary(
		ctx context.Context,
		ownerKey string,
		term string,
		normalizedTerm string,
		now time.Time,
		currentSource storage.SourceVersion,
	) (created bool, item domain.VocabularyItem, err error)
	VocabularyByID(ctx context.Context, ownerKey, itemID string, currentSource storage.SourceVersion) (domain.VocabularyItem, error)
	VocabularyByTerm(ctx context.Context, ownerKey, normalizedTerm string, currentSource storage.SourceVersion) (domain.VocabularyItem, error)
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
	Query          string
	CEFR           []domain.CEFRLevel
	HasExplanation *bool
	Sort           string
	Limit          int
	Cursor         string
}

type ListResult struct {
	Items      []domain.VocabularyItem `json:"items"`
	NextCursor string                  `json:"nextCursor,omitempty"`
}

type listFilter struct {
	Query          string             `json:"query"`
	CEFR           []domain.CEFRLevel `json:"cefr"`
	HasExplanation *bool              `json:"hasExplanation,omitempty"`
}

func NewService(store Store, ownerKey string, currentSource storage.SourceVersion) *Service {
	return &Service{
		store:         store,
		ownerKey:      ownerKey,
		currentSource: currentSource,
		now:           time.Now,
	}
}

func (service *Service) Save(ctx context.Context, term string) (SaveResult, error) {
	displayTerm, normalizedTerm, err := validateTerm(term)
	if err != nil {
		return SaveResult{}, err
	}
	created, item, err := service.store.SaveVocabulary(
		ctx,
		service.ownerKey,
		displayTerm,
		normalizedTerm,
		service.now().UTC(),
		service.currentSource,
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
		item, err = service.store.VocabularyByID(ctx, service.ownerKey, itemID, service.currentSource)
	} else {
		_, normalizedTerm, validationErr := validateTerm(term)
		if validationErr != nil {
			return domain.VocabularyItem{}, validationErr
		}
		item, err = service.store.VocabularyByTerm(ctx, service.ownerKey, normalizedTerm, service.currentSource)
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

	levels, err := normalizeCEFR(options.CEFR)
	if err != nil {
		return ListResult{}, err
	}
	filter := listFilter{
		Query:          domain.NormalizeTerm(options.Query),
		CEFR:           levels,
		HasExplanation: options.HasExplanation,
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
		OwnerKey:       service.ownerKey,
		Query:          filter.Query,
		CEFR:           levels,
		HasExplanation: options.HasExplanation,
		Sort:           sortOrder,
		Limit:          limit + 1,
		CursorPrimary:  cursorPrimary,
		CursorID:       cursorID,
		CurrentSource:  service.currentSource,
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

func normalizeCEFR(values []domain.CEFRLevel) ([]domain.CEFRLevel, error) {
	requested := make(map[domain.CEFRLevel]struct{}, len(values))
	for _, level := range values {
		if !level.Valid() {
			return nil, apperr.New(apperr.InvalidArgument, "cefr contains an unsupported level")
		}
		requested[level] = struct{}{}
	}
	levels := make([]domain.CEFRLevel, 0, len(requested))
	for _, level := range domain.CEFRLevels {
		if _, ok := requested[level]; ok {
			levels = append(levels, level)
		}
	}
	return levels, nil
}
