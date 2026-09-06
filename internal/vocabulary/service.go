package vocabulary

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"sort"
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
	SaveVocabulary(ctx context.Context, input storage.VocabularyCreate) (created bool, item domain.VocabularyItem, err error)
	UpdateVocabulary(ctx context.Context, input storage.VocabularyUpdate) (domain.VocabularyItem, error)
	VocabularyByID(ctx context.Context, ownerKey, itemID string) (domain.VocabularyItem, error)
	VocabularyByTerm(ctx context.Context, ownerKey, normalizedTerm string) (domain.VocabularyItem, error)
	VocabularyBySense(ctx context.Context, ownerKey, normalizedTerm, senseKey string) (domain.VocabularyItem, error)
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
	Created bool `json:"created"`
	domain.VocabularyItem
	Item domain.VocabularyItem `json:"-"`
}

type InitialValues struct {
	Status            domain.LearningStatus
	Usefulness        domain.Usefulness
	Tags              []string
	CustomDescription *string
	DescriptionSource *domain.DescriptionSource
	Notes             []string
	Examples          []string
	Context           string
	Definition        string
}

type UpdateChanges struct {
	Status            *domain.LearningStatus
	Usefulness        *domain.Usefulness
	Tags              *[]string
	CustomDescription *string
	DescriptionSource *domain.DescriptionSource
	Notes             *[]string
	Examples          *[]string
}

type ListOptions struct {
	Query                string
	Statuses             []domain.LearningStatus
	Tags                 []string
	HasLookup            *bool
	HasCustomDescription *bool
	Sort                 string
	Limit                int
	Cursor               string
}

type ListResult struct {
	Items      []domain.VocabularyItem `json:"items"`
	NextCursor string                  `json:"nextCursor,omitempty"`
}

type listFilter struct {
	Query                string                  `json:"query"`
	Statuses             []domain.LearningStatus `json:"statuses"`
	Tags                 []string                `json:"tags"`
	HasLookup            *bool                   `json:"hasLookup,omitempty"`
	HasCustomDescription *bool                   `json:"hasCustomDescription,omitempty"`
}

func NewService(store Store, ownerKey string, currentSource storage.SourceVersion) *Service {
	return &Service{
		store:         store,
		ownerKey:      ownerKey,
		currentSource: currentSource,
		now:           time.Now,
	}
}

func (service *Service) Save(ctx context.Context, term string, initial InitialValues) (SaveResult, error) {
	displayTerm, normalizedTerm, err := validateTerm(term)
	if err != nil {
		return SaveResult{}, err
	}
	metadata, err := normalizeInitialValues(initial)
	if err != nil {
		return SaveResult{}, err
	}
	snapshot, err := service.store.ActiveDictionarySnapshot(
		ctx,
		service.currentSource.Provider,
		normalizedTerm,
		service.currentSource.DatasetVersion,
		service.currentSource.ParserVersion,
	)
	lookupID := ""
	var selectedEntryIndex *int
	var selectedDefinitionIndex *int
	var selectedDefinition *domain.DictionaryDefinition
	if err == nil && len(snapshot.Data.Entries) > 0 {
		lookupID = snapshot.ID
	} else if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return SaveResult{}, apperr.Wrap(apperr.InternalError, "failed to read the dictionary lookup", err)
	}
	contextValue := strings.TrimSpace(initial.Context)
	definitionValue := strings.TrimSpace(initial.Definition)
	if definitionValue != "" {
		if snapshot == nil || len(snapshot.Data.Entries) == 0 {
			return SaveResult{}, apperr.New(apperr.InvalidArgument, "definition requires a successful cached dictionary lookup")
		}
		entryIndex, definitionIndex, definition, found := findDefinition(snapshot.Data.Entries, definitionValue)
		if !found {
			return SaveResult{}, apperr.New(apperr.InvalidArgument, "definition must exactly match a definition returned by dictionary_lookup")
		}
		selectedEntryIndex = &entryIndex
		selectedDefinitionIndex = &definitionIndex
		selectedDefinition = &definition
	}
	senseKey := vocabularySenseKey(definitionValue, contextValue, metadata.CustomDescription)
	created, item, err := service.store.SaveVocabulary(ctx, storage.VocabularyCreate{
		OwnerKey:                service.ownerKey,
		Term:                    displayTerm,
		NormalizedTerm:          normalizedTerm,
		LookupID:                lookupID,
		Status:                  metadata.Status,
		Usefulness:              metadata.Usefulness,
		Tags:                    metadata.Tags,
		CustomDescription:       metadata.CustomDescription,
		DescriptionSource:       metadata.DescriptionSource,
		Notes:                   metadata.Notes,
		Examples:                metadata.Examples,
		SenseKey:                senseKey,
		Context:                 contextValue,
		SelectedEntryIndex:      selectedEntryIndex,
		SelectedDefinitionIndex: selectedDefinitionIndex,
		SelectedDefinition:      selectedDefinition,
		Now:                     service.now().UTC(),
	})
	if err != nil {
		return SaveResult{}, apperr.Wrap(apperr.InternalError, "failed to save the vocabulary item", err)
	}
	return SaveResult{Created: created, VocabularyItem: item, Item: item}, nil
}

func (service *Service) Update(
	ctx context.Context,
	itemID string,
	term string,
	changes UpdateChanges,
) (domain.VocabularyItem, error) {
	current, err := service.Get(ctx, itemID, term)
	if err != nil {
		return domain.VocabularyItem{}, err
	}
	update, err := normalizeUpdateChanges(changes, current)
	if err != nil {
		return domain.VocabularyItem{}, err
	}
	update.OwnerKey = service.ownerKey
	update.ItemID = current.ItemID
	update.Now = service.now().UTC()

	item, err := service.store.UpdateVocabulary(ctx, update)
	if errors.Is(err, storage.ErrNotFound) {
		return domain.VocabularyItem{}, apperr.New(apperr.NotFound, "the vocabulary item was not found")
	}
	if errors.Is(err, storage.ErrAmbiguous) {
		return domain.VocabularyItem{}, apperr.New(apperr.InvalidArgument, "term matches multiple saved senses; use itemId")
	}
	if err != nil {
		return domain.VocabularyItem{}, apperr.Wrap(apperr.InternalError, "failed to update the vocabulary item", err)
	}
	return item, nil
}

func findDefinition(entries []domain.DictionaryEntry, wanted string) (int, int, domain.DictionaryDefinition, bool) {
	for entryIndex, entry := range entries {
		for definitionIndex, definition := range entry.Definitions {
			if strings.TrimSpace(definition.Definition) == wanted {
				return entryIndex, definitionIndex, definition, true
			}
		}
	}
	return 0, 0, domain.DictionaryDefinition{}, false
}

func vocabularySenseKey(definition, contextValue, customDescription string) string {
	identity := definition
	if identity == "" {
		identity = contextValue
	}
	if identity == "" {
		return "legacy"
	}
	digest := sha256.Sum256([]byte(domain.NormalizeTerm(identity)))
	return hex.EncodeToString(digest[:])
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
	if errors.Is(err, storage.ErrAmbiguous) {
		return domain.VocabularyItem{}, apperr.New(apperr.InvalidArgument, "term matches multiple saved senses; use itemId")
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
		Query:                domain.NormalizeTerm(options.Query),
		HasLookup:            options.HasLookup,
		HasCustomDescription: options.HasCustomDescription,
	}
	statuses, err := normalizeStatuses(options.Statuses)
	if err != nil {
		return ListResult{}, err
	}
	filter.Statuses = statuses
	tags, err := normalizeTags(options.Tags)
	if err != nil {
		return ListResult{}, err
	}
	filter.Tags = tags
	filterDigest, digestErr := storage.FilterDigest(filter)
	if digestErr != nil {
		return ListResult{}, apperr.Wrap(apperr.InternalError, "failed to prepare vocabulary pagination", digestErr)
	}
	cursorPrimary, cursorID, cursorErr := storage.DecodeCursor(options.Cursor, "vocabulary", sortOrder, filterDigest)
	if cursorErr != nil {
		return ListResult{}, apperr.New(apperr.InvalidArgument, "cursor is invalid or does not match these filters")
	}

	items, err := service.store.ListVocabulary(ctx, storage.VocabularyListQuery{
		OwnerKey:             service.ownerKey,
		Query:                filter.Query,
		Statuses:             filter.Statuses,
		Tags:                 filter.Tags,
		HasLookup:            filter.HasLookup,
		HasCustomDescription: filter.HasCustomDescription,
		Sort:                 sortOrder,
		Limit:                limit + 1,
		CursorPrimary:        cursorPrimary,
		CursorID:             cursorID,
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

type normalizedMetadata struct {
	Status            domain.LearningStatus
	Usefulness        domain.Usefulness
	Tags              []string
	CustomDescription string
	DescriptionSource *domain.DescriptionSource
	Notes             []string
	Examples          []string
}

func normalizeInitialValues(input InitialValues) (normalizedMetadata, error) {
	status := input.Status
	if status == "" {
		status = domain.LearningStatusNew
	}
	if !status.Valid() {
		return normalizedMetadata{}, apperr.New(apperr.InvalidArgument, "status is unsupported")
	}
	usefulness := input.Usefulness
	if usefulness == "" {
		usefulness = domain.UsefulnessNormal
	}
	if !usefulness.Valid() {
		return normalizedMetadata{}, apperr.New(apperr.InvalidArgument, "usefulness is unsupported")
	}
	description, err := normalizeDescription(input.CustomDescription)
	if err != nil {
		return normalizedMetadata{}, err
	}
	source, err := normalizeDescriptionSource(input.DescriptionSource)
	if err != nil {
		return normalizedMetadata{}, err
	}
	if source != nil && description == "" {
		return normalizedMetadata{}, apperr.New(
			apperr.InvalidArgument,
			"descriptionSource requires a non-empty customDescription",
		)
	}
	tags, err := normalizeTags(input.Tags)
	if err != nil {
		return normalizedMetadata{}, err
	}
	notes, err := normalizeTextList("notes", input.Notes, 100, 1000)
	if err != nil {
		return normalizedMetadata{}, err
	}
	examples, err := normalizeTextList("examples", input.Examples, 100, 1000)
	if err != nil {
		return normalizedMetadata{}, err
	}
	return normalizedMetadata{
		Status:            status,
		Usefulness:        usefulness,
		Tags:              tags,
		CustomDescription: description,
		DescriptionSource: source,
		Notes:             notes,
		Examples:          examples,
	}, nil
}

func normalizeUpdateChanges(input UpdateChanges, current domain.VocabularyItem) (storage.VocabularyUpdate, error) {
	if input.Status == nil && input.Usefulness == nil && input.Tags == nil &&
		input.CustomDescription == nil && input.DescriptionSource == nil && input.Notes == nil && input.Examples == nil {
		return storage.VocabularyUpdate{}, apperr.New(apperr.InvalidArgument, "changes must contain at least one field")
	}

	var update storage.VocabularyUpdate
	if input.Status != nil {
		if !input.Status.Valid() {
			return storage.VocabularyUpdate{}, apperr.New(apperr.InvalidArgument, "status is unsupported")
		}
		status := *input.Status
		update.Status = &status
	}
	if input.Usefulness != nil {
		if !input.Usefulness.Valid() {
			return storage.VocabularyUpdate{}, apperr.New(apperr.InvalidArgument, "usefulness is unsupported")
		}
		usefulness := *input.Usefulness
		update.Usefulness = &usefulness
	}
	if input.Tags != nil {
		tags, err := normalizeTags(*input.Tags)
		if err != nil {
			return storage.VocabularyUpdate{}, err
		}
		update.Tags = &tags
	}
	if input.Notes != nil {
		notes, err := normalizeTextList("notes", *input.Notes, 100, 1000)
		if err != nil {
			return storage.VocabularyUpdate{}, err
		}
		update.Notes = &notes
	}
	if input.Examples != nil {
		examples, err := normalizeTextList("examples", *input.Examples, 100, 1000)
		if err != nil {
			return storage.VocabularyUpdate{}, err
		}
		update.Examples = &examples
	}

	effectiveDescription := current.CustomDescription
	if input.CustomDescription != nil {
		description, err := normalizeDescription(input.CustomDescription)
		if err != nil {
			return storage.VocabularyUpdate{}, err
		}
		update.CustomDescription = &description
		effectiveDescription = description
		if description == "" && input.DescriptionSource == nil {
			update.SetDescriptionSource = true
		}
	}
	if input.DescriptionSource != nil {
		source, err := normalizeDescriptionSource(input.DescriptionSource)
		if err != nil {
			return storage.VocabularyUpdate{}, err
		}
		if source != nil && effectiveDescription == "" {
			return storage.VocabularyUpdate{}, apperr.New(
				apperr.InvalidArgument,
				"descriptionSource requires a non-empty customDescription",
			)
		}
		update.SetDescriptionSource = true
		update.DescriptionSource = source
	}
	return update, nil
}

func normalizeDescription(value *string) (string, error) {
	if value == nil {
		return "", nil
	}
	description := strings.TrimSpace(*value)
	if utf8.RuneCountInString(description) > 5000 {
		return "", apperr.New(
			apperr.InvalidArgument,
			"customDescription must contain at most 5000 Unicode characters",
		)
	}
	return description, nil
}

func normalizeDescriptionSource(value *domain.DescriptionSource) (*domain.DescriptionSource, error) {
	if value == nil {
		return nil, nil
	}
	title := strings.TrimSpace(value.Title)
	sourceURL := strings.TrimSpace(value.URL)
	if title == "" && sourceURL == "" {
		return nil, nil
	}
	if utf8.RuneCountInString(title) > 200 {
		return nil, apperr.New(apperr.InvalidArgument, "descriptionSource.title must contain at most 200 Unicode characters")
	}
	if len(sourceURL) > 2000 {
		return nil, apperr.New(apperr.InvalidArgument, "descriptionSource.url must contain at most 2000 characters")
	}
	if sourceURL != "" {
		parsed, err := url.Parse(sourceURL)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return nil, apperr.New(apperr.InvalidArgument, "descriptionSource.url must be an absolute HTTP or HTTPS URL")
		}
	}
	return &domain.DescriptionSource{Title: title, URL: sourceURL}, nil
}

func normalizeTags(values []string) ([]string, error) {
	if len(values) > 50 {
		return nil, apperr.New(apperr.InvalidArgument, "tags must contain at most 50 values")
	}
	unique := make(map[string]struct{}, len(values))
	for _, value := range values {
		tag := strings.ToLower(domain.DisplayTerm(value))
		if tag == "" || utf8.RuneCountInString(tag) > 50 {
			return nil, apperr.New(apperr.InvalidArgument, "each tag must contain 1 to 50 Unicode characters")
		}
		unique[tag] = struct{}{}
	}
	tags := make([]string, 0, len(unique))
	for tag := range unique {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	return tags, nil
}

func normalizeStatuses(values []domain.LearningStatus) ([]domain.LearningStatus, error) {
	requested := make(map[domain.LearningStatus]struct{}, len(values))
	for _, status := range values {
		if !status.Valid() {
			return nil, apperr.New(apperr.InvalidArgument, "statuses contains an unsupported value")
		}
		requested[status] = struct{}{}
	}
	ordered := []domain.LearningStatus{
		domain.LearningStatusNew,
		domain.LearningStatusLearning,
		domain.LearningStatusLearned,
		domain.LearningStatusArchived,
	}
	statuses := make([]domain.LearningStatus, 0, len(requested))
	for _, status := range ordered {
		if _, ok := requested[status]; ok {
			statuses = append(statuses, status)
		}
	}
	return statuses, nil
}

func normalizeTextList(name string, values []string, maximumItems, maximumLength int) ([]string, error) {
	if len(values) > maximumItems {
		return nil, apperr.New(apperr.InvalidArgument, name+" contains too many values")
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		text := strings.TrimSpace(value)
		if text == "" || utf8.RuneCountInString(text) > maximumLength {
			return nil, apperr.New(
				apperr.InvalidArgument,
				"each "+name+" value must contain 1 to 1000 Unicode characters",
			)
		}
		result = append(result, text)
	}
	return result, nil
}
