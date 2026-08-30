package explanation

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"
	"unicode"

	"english-learning-mcp/internal/apperr"
	"english-learning-mcp/internal/domain"
	"english-learning-mcp/internal/storage"
)

const descriptionPreviewLength = 160

type Store interface {
	DictionarySnapshotByID(ctx context.Context, id string) (*storage.DictionarySnapshot, error)
	UpsertExplanation(ctx context.Context, input storage.ExplanationUpsert) (bool, *storage.ExplanationRecord, error)
	ExplanationByID(ctx context.Context, ownerKey, explanationID string, currentSource storage.SourceVersion) (*storage.ExplanationRecord, error)
	ExplanationByNaturalKey(
		ctx context.Context,
		ownerKey string,
		normalizedTerm string,
		normalizedContext string,
		generatorName string,
		generatorVersion string,
		currentOnly bool,
		currentSource storage.SourceVersion,
	) (*storage.ExplanationRecord, error)
	ListExplanations(ctx context.Context, input storage.ExplanationListQuery) ([]*storage.ExplanationRecord, error)
	DeleteExplanation(ctx context.Context, ownerKey, explanationID string, now time.Time) error
	VocabularyByID(ctx context.Context, ownerKey, itemID string, currentSource storage.SourceVersion) (domain.VocabularyItem, error)
}

type SelectedMeaningInput struct {
	EntryIndex      int `json:"entryIndex"`
	DefinitionIndex int `json:"definitionIndex"`
}

type LearnerAlternativeInput struct {
	PartOfSpeech string   `json:"partOfSpeech,omitempty"`
	Explanation  string   `json:"explanation"`
	Reason       string   `json:"reason,omitempty"`
	Confidence   *float64 `json:"confidence,omitempty"`
}

type LearnerInput struct {
	Description        string                    `json:"description"`
	WhyThisMeaningFits string                    `json:"whyThisMeaningFits,omitempty"`
	Notes              []string                  `json:"notes,omitempty"`
	Examples           []string                  `json:"examples,omitempty"`
	Alternatives       []LearnerAlternativeInput `json:"alternatives,omitempty"`
}

type LexicalRelationsInput struct {
	Synonyms []string `json:"synonyms,omitempty"`
	Antonyms []string `json:"antonyms,omitempty"`
	Source   string   `json:"source"`
}

type UpsertValue struct {
	Term             string                 `json:"term"`
	Context          string                 `json:"context,omitempty"`
	LookupID         string                 `json:"lookupId"`
	SelectedMeaning  *SelectedMeaningInput  `json:"selectedMeaning"`
	Learner          LearnerInput           `json:"learner"`
	CEFR             *domain.CEFR           `json:"cefr,omitempty"`
	LexicalRelations *LexicalRelationsInput `json:"lexicalRelations,omitempty"`
	Generator        domain.Generator       `json:"generator"`
}

type UpsertResult struct {
	Created     bool               `json:"created"`
	Explanation domain.Explanation `json:"explanation"`
}

type GeneratorKey struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type GetOptions struct {
	ExplanationID string
	Term          string
	Context       string
	Generator     *GeneratorKey
	IncludeStale  bool
}

type GetResult struct {
	Found             bool                `json:"found"`
	Explanation       *domain.Explanation `json:"explanation,omitempty"`
	NormalizedTerm    string              `json:"normalizedTerm,omitempty"`
	NormalizedContext string              `json:"normalizedContext,omitempty"`
	Reason            string              `json:"reason,omitempty"`
}

type ListOptions struct {
	Term           string
	ItemID         string
	CEFR           []domain.CEFRLevel
	OnlySavedItems bool
	IncludeStale   bool
	Sort           string
	Limit          int
	Cursor         string
}

type ListResult struct {
	Explanations []domain.ExplanationSummary `json:"explanations"`
	NextCursor   string                      `json:"nextCursor,omitempty"`
}

type listFilter struct {
	NormalizedTerm string             `json:"normalizedTerm,omitempty"`
	CEFR           []domain.CEFRLevel `json:"cefr"`
	OnlySavedItems bool               `json:"onlySavedItems"`
	IncludeStale   bool               `json:"includeStale"`
}

type Service struct {
	store         Store
	ownerKey      string
	currentSource storage.SourceVersion
	now           func() time.Time
}

func NewService(store Store, ownerKey string, currentSource storage.SourceVersion) *Service {
	return &Service{
		store:         store,
		ownerKey:      ownerKey,
		currentSource: currentSource,
		now:           time.Now,
	}
}

func (service *Service) Upsert(ctx context.Context, value UpsertValue) (UpsertResult, error) {
	term := domain.DisplayTerm(value.Term)
	if !domain.ValidTerm(term) {
		return UpsertResult{}, invalid("term must contain 1 to 200 Unicode characters after whitespace normalization")
	}
	normalizedTerm := domain.NormalizeTerm(term)
	contextText := domain.NormalizeContext(value.Context)
	lookupID := strings.TrimSpace(value.LookupID)
	if lookupID == "" {
		return UpsertResult{}, invalid("lookupId is required")
	}

	snapshot, err := service.store.DictionarySnapshotByID(ctx, lookupID)
	if errors.Is(err, storage.ErrNotFound) {
		return UpsertResult{}, apperr.New(apperr.NotFound, "the dictionary lookup snapshot was not found")
	}
	if err != nil {
		return UpsertResult{}, apperr.Wrap(apperr.InternalError, "failed to read the dictionary lookup snapshot", err)
	}
	if snapshot.NormalizedTerm != normalizedTerm {
		return UpsertResult{}, invalid("lookupId belongs to a different normalized term")
	}
	if !service.snapshotIsCurrent(snapshot) {
		return UpsertResult{}, apperr.New(apperr.StaleLookup, "lookupId is no longer active; run dictionary_lookup again")
	}

	selectedEntry, selectedDefinition, err := validateSelection(snapshot, value.SelectedMeaning)
	if err != nil {
		return UpsertResult{}, err
	}
	learner, err := normalizeLearner(value.Learner)
	if err != nil {
		return UpsertResult{}, err
	}
	cefr, err := validateCEFR(value.CEFR, selectedDefinition)
	if err != nil {
		return UpsertResult{}, err
	}
	lexicalRelations, err := normalizeLexicalRelations(value.LexicalRelations)
	if err != nil {
		return UpsertResult{}, err
	}
	generator, err := normalizeGenerator(value.Generator)
	if err != nil {
		return UpsertResult{}, err
	}

	created, record, err := service.store.UpsertExplanation(ctx, storage.ExplanationUpsert{
		OwnerKey:                service.ownerKey,
		Term:                    term,
		NormalizedTerm:          normalizedTerm,
		Context:                 contextText,
		NormalizedContext:       contextText,
		LookupID:                lookupID,
		SelectedEntryIndex:      selectedEntry,
		SelectedDefinitionIndex: selectedDefinitionIndex(value.SelectedMeaning),
		Learner:                 learner,
		CEFR:                    cefr,
		LexicalRelations:        lexicalRelations,
		Generator:               generator,
		Now:                     service.now().UTC(),
		CurrentSource:           service.currentSource,
	})
	if err != nil {
		return UpsertResult{}, apperr.Wrap(apperr.InternalError, "failed to store the explanation", err)
	}
	explanation, err := service.explanation(record)
	if err != nil {
		return UpsertResult{}, err
	}
	return UpsertResult{Created: created, Explanation: explanation}, nil
}

func (service *Service) Get(ctx context.Context, options GetOptions) (GetResult, error) {
	explanationID := strings.TrimSpace(options.ExplanationID)
	hasID := explanationID != ""
	hasNaturalFields := domain.DisplayTerm(options.Term) != "" || domain.NormalizeContext(options.Context) != "" || options.Generator != nil
	if hasID && hasNaturalFields || !hasID && options.Generator == nil {
		return GetResult{}, invalid("use either explanationId or term with generator")
	}

	if hasID {
		record, err := service.store.ExplanationByID(ctx, service.ownerKey, explanationID, service.currentSource)
		if errors.Is(err, storage.ErrNotFound) {
			return GetResult{}, apperr.New(apperr.NotFound, "the explanation was not found")
		}
		if err != nil {
			return GetResult{}, apperr.Wrap(apperr.InternalError, "failed to read the explanation", err)
		}
		if record.Stale && !options.IncludeStale {
			return staleMiss(record.NormalizedTerm, record.NormalizedContext), nil
		}
		return service.found(record)
	}

	term := domain.DisplayTerm(options.Term)
	if !domain.ValidTerm(term) {
		return GetResult{}, invalid("term must contain 1 to 200 Unicode characters after whitespace normalization")
	}
	normalizedTerm := domain.NormalizeTerm(term)
	normalizedContext := domain.NormalizeContext(options.Context)
	generator, err := normalizeGeneratorKey(*options.Generator)
	if err != nil {
		return GetResult{}, err
	}

	if options.IncludeStale {
		record, err := service.store.ExplanationByNaturalKey(
			ctx, service.ownerKey, normalizedTerm, normalizedContext, generator.Name, generator.Version, false, service.currentSource,
		)
		if errors.Is(err, storage.ErrNotFound) {
			return cacheMiss(normalizedTerm, normalizedContext), nil
		}
		if err != nil {
			return GetResult{}, apperr.Wrap(apperr.InternalError, "failed to read the explanation cache", err)
		}
		return service.found(record)
	}

	record, err := service.store.ExplanationByNaturalKey(
		ctx, service.ownerKey, normalizedTerm, normalizedContext, generator.Name, generator.Version, true, service.currentSource,
	)
	if err == nil {
		return service.found(record)
	}
	if !errors.Is(err, storage.ErrNotFound) {
		return GetResult{}, apperr.Wrap(apperr.InternalError, "failed to read the explanation cache", err)
	}
	_, staleErr := service.store.ExplanationByNaturalKey(
		ctx, service.ownerKey, normalizedTerm, normalizedContext, generator.Name, generator.Version, false, service.currentSource,
	)
	if staleErr == nil {
		return staleMiss(normalizedTerm, normalizedContext), nil
	}
	if !errors.Is(staleErr, storage.ErrNotFound) {
		return GetResult{}, apperr.Wrap(apperr.InternalError, "failed to check stale explanations", staleErr)
	}
	return cacheMiss(normalizedTerm, normalizedContext), nil
}

func (service *Service) List(ctx context.Context, options ListOptions) (ListResult, error) {
	if domain.DisplayTerm(options.Term) != "" && strings.TrimSpace(options.ItemID) != "" {
		return ListResult{}, invalid("term and itemId are mutually exclusive")
	}
	normalizedTerm := ""
	if domain.DisplayTerm(options.Term) != "" {
		term := domain.DisplayTerm(options.Term)
		if !domain.ValidTerm(term) {
			return ListResult{}, invalid("term must contain 1 to 200 Unicode characters after whitespace normalization")
		}
		normalizedTerm = domain.NormalizeTerm(term)
	}
	if itemID := strings.TrimSpace(options.ItemID); itemID != "" {
		item, err := service.store.VocabularyByID(ctx, service.ownerKey, itemID, service.currentSource)
		if errors.Is(err, storage.ErrNotFound) {
			return ListResult{}, apperr.New(apperr.NotFound, "the vocabulary item was not found")
		}
		if err != nil {
			return ListResult{}, apperr.Wrap(apperr.InternalError, "failed to resolve the vocabulary item", err)
		}
		normalizedTerm = item.NormalizedTerm
	}

	sortOrder := options.Sort
	if sortOrder == "" {
		sortOrder = "recent"
	}
	if sortOrder != "recent" && sortOrder != "oldest" && sortOrder != "alphabetical" {
		return ListResult{}, invalid("sort must be recent, oldest, or alphabetical")
	}
	limit := options.Limit
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 100 {
		return ListResult{}, invalid("limit must be between 1 and 100")
	}
	levels, err := normalizeLevels(options.CEFR)
	if err != nil {
		return ListResult{}, err
	}
	filter := listFilter{
		NormalizedTerm: normalizedTerm,
		CEFR:           levels,
		OnlySavedItems: options.OnlySavedItems,
		IncludeStale:   options.IncludeStale,
	}
	filterDigest, err := storage.FilterDigest(filter)
	if err != nil {
		return ListResult{}, apperr.Wrap(apperr.InternalError, "failed to prepare explanation pagination", err)
	}
	cursorPrimary, cursorID, err := storage.DecodeCursor(options.Cursor, "explanations", sortOrder, filterDigest)
	if err != nil {
		return ListResult{}, invalid("cursor is invalid or does not match these filters")
	}

	records, err := service.store.ListExplanations(ctx, storage.ExplanationListQuery{
		OwnerKey:       service.ownerKey,
		NormalizedTerm: normalizedTerm,
		CEFR:           levels,
		OnlySaved:      options.OnlySavedItems,
		IncludeStale:   options.IncludeStale,
		Sort:           sortOrder,
		Limit:          limit + 1,
		CursorPrimary:  cursorPrimary,
		CursorID:       cursorID,
		CurrentSource:  service.currentSource,
	})
	if err != nil {
		return ListResult{}, apperr.Wrap(apperr.InternalError, "failed to list explanations", err)
	}

	result := ListResult{Explanations: make([]domain.ExplanationSummary, 0, min(len(records), limit))}
	hasNextPage := len(records) > limit
	if hasNextPage {
		records = records[:limit]
	}
	for _, record := range records {
		summary, err := service.summary(record)
		if err != nil {
			return ListResult{}, err
		}
		result.Explanations = append(result.Explanations, summary)
	}
	if !hasNextPage {
		return result, nil
	}
	lastRecord := records[len(records)-1]
	primary := storage.TimeString(lastRecord.UpdatedAt)
	if sortOrder == "alphabetical" {
		primary = lastRecord.NormalizedTerm
	}
	result.NextCursor, err = storage.EncodeCursor("explanations", sortOrder, filterDigest, primary, lastRecord.ID)
	if err != nil {
		return ListResult{}, apperr.Wrap(apperr.InternalError, "failed to encode the explanation cursor", err)
	}
	return result, nil
}

func (service *Service) SummariesForTerm(ctx context.Context, normalizedTerm string, includeStale bool) ([]domain.ExplanationSummary, error) {
	records, err := service.store.ListExplanations(ctx, storage.ExplanationListQuery{
		OwnerKey:       service.ownerKey,
		NormalizedTerm: normalizedTerm,
		IncludeStale:   includeStale,
		Sort:           "recent",
		CurrentSource:  service.currentSource,
	})
	if err != nil {
		return nil, apperr.Wrap(apperr.InternalError, "failed to list vocabulary explanations", err)
	}
	summaries := make([]domain.ExplanationSummary, 0, len(records))
	for _, record := range records {
		summary, err := service.summary(record)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	return summaries, nil
}

func (service *Service) Delete(ctx context.Context, explanationID string) error {
	explanationID = strings.TrimSpace(explanationID)
	if explanationID == "" {
		return invalid("explanationId is required")
	}
	if err := service.store.DeleteExplanation(ctx, service.ownerKey, explanationID, service.now().UTC()); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return apperr.New(apperr.NotFound, "the explanation was not found")
		}
		return apperr.Wrap(apperr.InternalError, "failed to delete the explanation", err)
	}
	return nil
}

func (service *Service) found(record *storage.ExplanationRecord) (GetResult, error) {
	value, err := service.explanation(record)
	if err != nil {
		return GetResult{}, err
	}
	return GetResult{Found: true, Explanation: &value}, nil
}

func (service *Service) explanation(record *storage.ExplanationRecord) (domain.Explanation, error) {
	selectedMeaning, err := selectedMeaning(record)
	if err != nil {
		return domain.Explanation{}, apperr.Wrap(apperr.InternalError, "the stored explanation references an invalid source meaning", err)
	}
	return domain.Explanation{
		ExplanationID:    record.ID,
		Term:             record.Term,
		NormalizedTerm:   record.NormalizedTerm,
		Context:          record.Context,
		LookupID:         record.LookupID,
		SelectedMeaning:  selectedMeaning,
		Learner:          record.Learner,
		CEFR:             record.CEFR,
		LexicalRelations: record.LexicalRelations,
		Generator:        record.Generator,
		Stale:            record.Stale,
		CreatedAt:        storage.TimeString(record.CreatedAt),
		UpdatedAt:        storage.TimeString(record.UpdatedAt),
	}, nil
}

func (service *Service) summary(record *storage.ExplanationRecord) (domain.ExplanationSummary, error) {
	selected, err := selectedMeaning(record)
	if err != nil {
		return domain.ExplanationSummary{}, apperr.Wrap(apperr.InternalError, "the stored explanation references an invalid source meaning", err)
	}
	summary := domain.ExplanationSummary{
		ExplanationID:      record.ID,
		Term:               record.Term,
		NormalizedTerm:     record.NormalizedTerm,
		Context:            record.Context,
		DescriptionPreview: domain.Preview(record.Learner.Description, descriptionPreviewLength),
		Stale:              record.Stale,
		CreatedAt:          storage.TimeString(record.CreatedAt),
		UpdatedAt:          storage.TimeString(record.UpdatedAt),
	}
	if selected != nil {
		summary.PartOfSpeech = selected.PartOfSpeech
	}
	if record.CEFR != nil {
		summary.CEFR = &domain.CEFRSummary{Level: record.CEFR.Level, Source: record.CEFR.Source}
	}
	return summary, nil
}

func selectedMeaning(record *storage.ExplanationRecord) (*domain.SelectedMeaning, error) {
	if record.SelectedEntryIndex == nil && record.SelectedDefinitionIndex == nil {
		return nil, nil
	}
	if record.SelectedEntryIndex == nil || record.SelectedDefinitionIndex == nil {
		return nil, storage.ErrCorruptData
	}
	entryIndex := *record.SelectedEntryIndex
	definitionIndex := *record.SelectedDefinitionIndex
	if entryIndex < 0 || entryIndex >= len(record.Snapshot.Data.Entries) {
		return nil, storage.ErrCorruptData
	}
	entry := record.Snapshot.Data.Entries[entryIndex]
	if definitionIndex < 0 || definitionIndex >= len(entry.Definitions) {
		return nil, storage.ErrCorruptData
	}
	definition := entry.Definitions[definitionIndex]
	return &domain.SelectedMeaning{
		EntryIndex:      entryIndex,
		DefinitionIndex: definitionIndex,
		Headword:        entry.Headword,
		PartOfSpeech:    entry.PartOfSpeech,
		Definition:      definition.Definition,
		Examples:        definition.Examples,
		Labels:          definition.Labels,
	}, nil
}

func validateSelection(
	snapshot *storage.DictionarySnapshot,
	selection *SelectedMeaningInput,
) (entryIndex *int, definition *domain.DictionaryDefinition, err error) {
	hasUsableDefinition := false
	for _, entry := range snapshot.Data.Entries {
		for _, candidate := range entry.Definitions {
			if strings.TrimSpace(candidate.Definition) != "" {
				hasUsableDefinition = true
				break
			}
		}
	}
	if selection == nil {
		if hasUsableDefinition {
			return nil, nil, invalid("selectedMeaning is required when the lookup contains a usable definition")
		}
		return nil, nil, nil
	}
	if !hasUsableDefinition {
		return nil, nil, invalid("selectedMeaning must be null when the lookup contains no usable definition")
	}
	if selection.EntryIndex < 0 || selection.EntryIndex >= len(snapshot.Data.Entries) {
		return nil, nil, invalid("selectedMeaning.entryIndex is outside the lookup snapshot")
	}
	entry := &snapshot.Data.Entries[selection.EntryIndex]
	if selection.DefinitionIndex < 0 || selection.DefinitionIndex >= len(entry.Definitions) {
		return nil, nil, invalid("selectedMeaning.definitionIndex is outside the selected entry")
	}
	if strings.TrimSpace(entry.Definitions[selection.DefinitionIndex].Definition) == "" {
		return nil, nil, invalid("selectedMeaning references an empty definition")
	}
	index := selection.EntryIndex
	return &index, &entry.Definitions[selection.DefinitionIndex], nil
}

func selectedDefinitionIndex(selection *SelectedMeaningInput) *int {
	if selection == nil {
		return nil
	}
	index := selection.DefinitionIndex
	return &index
}

func normalizeLearner(input LearnerInput) (domain.LearnerContent, error) {
	description := strings.TrimSpace(input.Description)
	if description == "" {
		return domain.LearnerContent{}, invalid("learner.description is required")
	}
	notes, err := normalizeStrings(input.Notes, 10, "learner.notes")
	if err != nil {
		return domain.LearnerContent{}, err
	}
	examples, err := normalizeStrings(input.Examples, 10, "learner.examples")
	if err != nil {
		return domain.LearnerContent{}, err
	}
	alternatives, err := normalizeAlternatives(input.Alternatives)
	if err != nil {
		return domain.LearnerContent{}, err
	}
	return domain.LearnerContent{
		Description:        description,
		WhyThisMeaningFits: strings.TrimSpace(input.WhyThisMeaningFits),
		Notes:              notes,
		Examples:           examples,
		Alternatives:       alternatives,
	}, nil
}

func normalizeAlternatives(values []LearnerAlternativeInput) ([]domain.LearnerAlternative, error) {
	alternatives := make([]domain.LearnerAlternative, 0, len(values))
	seen := make(map[string]struct{})
	for _, value := range values {
		explanation := domain.NormalizeWhitespace(value.Explanation)
		if explanation == "" {
			return nil, invalid("learner.alternatives explanations must not be empty")
		}
		if err := validateConfidence(value.Confidence, "learner.alternatives confidence"); err != nil {
			return nil, err
		}
		partOfSpeech := domain.NormalizeWhitespace(value.PartOfSpeech)
		reason := domain.NormalizeWhitespace(value.Reason)
		key := strings.ToLower(partOfSpeech + "\n" + explanation + "\n" + reason)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		alternatives = append(alternatives, domain.LearnerAlternative{
			PartOfSpeech: partOfSpeech,
			Explanation:  explanation,
			Reason:       reason,
			Confidence:   value.Confidence,
		})
	}
	if len(alternatives) > 10 {
		return nil, invalid("learner.alternatives must contain at most 10 unique values")
	}
	return alternatives, nil
}

func validateCEFR(value *domain.CEFR, definition *domain.DictionaryDefinition) (*domain.CEFR, error) {
	if value == nil {
		return nil, nil
	}
	if !value.Level.Valid() {
		return nil, invalid("cefr.level is unsupported")
	}
	result := &domain.CEFR{
		Level:      value.Level,
		Source:     domain.NormalizeWhitespace(value.Source),
		Confidence: value.Confidence,
		Reason:     strings.TrimSpace(value.Reason),
	}
	switch result.Source {
	case "dictionary":
		if result.Confidence != nil {
			return nil, invalid("cefr.confidence is allowed only when source is ai")
		}
		if definition == nil || !definitionHasCEFR(*definition, result.Level) {
			return nil, invalid("cefr.source dictionary requires the selected source definition to contain that level")
		}
	case "ai":
		if err := validateConfidence(result.Confidence, "cefr.confidence"); err != nil {
			return nil, err
		}
	default:
		return nil, invalid("cefr.source must be dictionary or ai")
	}
	return result, nil
}

func definitionHasCEFR(definition domain.DictionaryDefinition, level domain.CEFRLevel) bool {
	for _, label := range definition.Labels {
		tokens := strings.FieldsFunc(strings.ToUpper(label), func(character rune) bool {
			return !unicode.IsLetter(character) && !unicode.IsDigit(character)
		})
		for _, token := range tokens {
			if token == string(level) {
				return true
			}
		}
	}
	return false
}

func normalizeLexicalRelations(input *LexicalRelationsInput) (*domain.LexicalRelations, error) {
	if input == nil {
		return nil, nil
	}
	if input.Source != "dictionary" && input.Source != "ai" && input.Source != "mixed" {
		return nil, invalid("lexicalRelations.source must be dictionary, ai, or mixed")
	}
	synonyms, err := normalizeStrings(input.Synonyms, 20, "lexicalRelations.synonyms")
	if err != nil {
		return nil, err
	}
	antonyms, err := normalizeStrings(input.Antonyms, 20, "lexicalRelations.antonyms")
	if err != nil {
		return nil, err
	}
	return &domain.LexicalRelations{Synonyms: synonyms, Antonyms: antonyms, Source: input.Source}, nil
}

func normalizeGenerator(generator domain.Generator) (domain.Generator, error) {
	generator.Name = domain.NormalizeWhitespace(generator.Name)
	generator.Model = domain.NormalizeWhitespace(generator.Model)
	generator.Version = domain.NormalizeWhitespace(generator.Version)
	if generator.Name == "" || generator.Version == "" {
		return domain.Generator{}, invalid("generator.name and generator.version are required")
	}
	return generator, nil
}

func normalizeGeneratorKey(generator GeneratorKey) (GeneratorKey, error) {
	generator.Name = domain.NormalizeWhitespace(generator.Name)
	generator.Version = domain.NormalizeWhitespace(generator.Version)
	if generator.Name == "" || generator.Version == "" {
		return GeneratorKey{}, invalid("generator.name and generator.version are required")
	}
	return generator, nil
}

func normalizeStrings(values []string, limit int, field string) ([]string, error) {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{})
	for _, value := range values {
		value = domain.NormalizeWhitespace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	if len(result) > limit {
		return nil, invalid(field + " must contain at most " + decimal(limit) + " unique values")
	}
	return result, nil
}

func normalizeLevels(values []domain.CEFRLevel) ([]domain.CEFRLevel, error) {
	requested := make(map[domain.CEFRLevel]struct{}, len(values))
	for _, level := range values {
		if !level.Valid() {
			return nil, invalid("cefr contains an unsupported level")
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

func validateConfidence(value *float64, field string) error {
	if value == nil {
		return nil
	}
	if math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0 || *value > 1 {
		return invalid(field + " must be a finite number between 0 and 1")
	}
	return nil
}

func (service *Service) snapshotIsCurrent(snapshot *storage.DictionarySnapshot) bool {
	return snapshot.Active &&
		snapshot.Provider == service.currentSource.Provider &&
		snapshot.ParserVersion == service.currentSource.ParserVersion &&
		snapshot.DatasetVersion == service.currentSource.DatasetVersion
}

func cacheMiss(normalizedTerm, normalizedContext string) GetResult {
	return GetResult{
		Found:             false,
		NormalizedTerm:    normalizedTerm,
		NormalizedContext: normalizedContext,
		Reason:            "not_cached",
	}
}

func staleMiss(normalizedTerm, normalizedContext string) GetResult {
	return GetResult{
		Found:             false,
		NormalizedTerm:    normalizedTerm,
		NormalizedContext: normalizedContext,
		Reason:            "stale_only",
	}
}

func invalid(message string) *apperr.Error {
	return apperr.New(apperr.InvalidArgument, message)
}

func decimal(value int) string {
	if value < 10 {
		return string(rune('0' + value))
	}
	return string(rune('0'+value/10)) + string(rune('0'+value%10))
}
