package mcpserver

import (
	"english-learning-mcp/internal/domain"
	"english-learning-mcp/internal/explanation"
)

type SortOrder string

const (
	SortRecent       SortOrder = "recent"
	SortOldest       SortOrder = "oldest"
	SortAlphabetical SortOrder = "alphabetical"
)

type ExplanationOperation string

const (
	ExplanationUpsert ExplanationOperation = "upsert"
	ExplanationDelete ExplanationOperation = "delete"
)

type DictionaryLookupInput struct {
	Term    string `json:"term" jsonschema:"word, phrase, idiom, or expression to look up"`
	Refresh bool   `json:"refresh,omitempty" jsonschema:"bypass an active cache entry and fetch a new immutable snapshot"`
}

type VocabularySaveInput struct {
	Term string `json:"term" jsonschema:"word, phrase, idiom, or expression to save"`
}

type VocabularyGetInput struct {
	ItemID                   string `json:"itemId,omitempty" jsonschema:"opaque saved vocabulary item identifier"`
	Term                     string `json:"term,omitempty" jsonschema:"exact saved term to retrieve"`
	IncludeStaleExplanations bool   `json:"includeStaleExplanations,omitempty" jsonschema:"include summaries backed by inactive dictionary snapshots"`
}

type vocabularyGetByIDInput struct {
	ItemID                   string `json:"itemId"`
	IncludeStaleExplanations bool   `json:"includeStaleExplanations,omitempty"`
}

type vocabularyGetByTermInput struct {
	Term                     string `json:"term"`
	IncludeStaleExplanations bool   `json:"includeStaleExplanations,omitempty"`
}

type VocabularyGetOutput struct {
	Item         domain.VocabularyItem       `json:"item"`
	Explanations []domain.ExplanationSummary `json:"explanations"`
}

type VocabularyListInput struct {
	Query          string             `json:"query,omitempty" jsonschema:"case-insensitive term substring search"`
	CEFR           []domain.CEFRLevel `json:"cefr,omitempty" jsonschema:"match any supplied CEFR level"`
	HasExplanation *bool              `json:"hasExplanation,omitempty" jsonschema:"filter by presence of a current explanation"`
	Sort           SortOrder          `json:"sort,omitempty" jsonschema:"result ordering"`
	Limit          int                `json:"limit,omitempty" jsonschema:"page size from 1 to 100"`
	Cursor         string             `json:"cursor,omitempty" jsonschema:"opaque cursor returned by the previous page"`
}

type VocabularyListOutput struct {
	Items      []domain.VocabularyItem `json:"items"`
	NextCursor string                  `json:"nextCursor,omitempty"`
}

type VocabularyDeleteInput struct {
	ItemID string `json:"itemId" jsonschema:"opaque saved vocabulary item identifier"`
}

type VocabularyDeleteOutput struct {
	Deleted bool   `json:"deleted"`
	ItemID  string `json:"itemId"`
}

type ExplanationGetInput struct {
	ExplanationID string                    `json:"explanationId,omitempty" jsonschema:"opaque explanation identifier"`
	Term          string                    `json:"term,omitempty" jsonschema:"exact lookup term for natural-key cache lookup"`
	Context       string                    `json:"context,omitempty" jsonschema:"meaning-selection context; empty is a distinct cache key"`
	Generator     *explanation.GeneratorKey `json:"generator,omitempty" jsonschema:"generator cache partition"`
	IncludeStale  bool                      `json:"includeStale,omitempty" jsonschema:"allow explanations backed by inactive snapshots"`
}

type explanationGetByIDInput struct {
	ExplanationID string `json:"explanationId"`
	IncludeStale  bool   `json:"includeStale,omitempty"`
}

type explanationGetByKeyInput struct {
	Term         string                   `json:"term"`
	Context      string                   `json:"context,omitempty"`
	Generator    explanation.GeneratorKey `json:"generator"`
	IncludeStale bool                     `json:"includeStale,omitempty"`
}

type ExplanationGetOutput struct {
	Found             bool                `json:"found"`
	Explanation       *domain.Explanation `json:"explanation,omitempty"`
	NormalizedTerm    *string             `json:"normalizedTerm,omitempty"`
	NormalizedContext *string             `json:"normalizedContext,omitempty"`
	Reason            string              `json:"reason,omitempty"`
}

type explanationFoundOutput struct {
	Found       bool               `json:"found"`
	Explanation domain.Explanation `json:"explanation"`
}

type explanationMissOutput struct {
	Found             bool   `json:"found"`
	NormalizedTerm    string `json:"normalizedTerm"`
	NormalizedContext string `json:"normalizedContext"`
	Reason            string `json:"reason"`
}

type ExplanationsListInput struct {
	Term           string             `json:"term,omitempty" jsonschema:"exact normalized term filter"`
	ItemID         string             `json:"itemId,omitempty" jsonschema:"resolve the filter from a saved vocabulary item"`
	CEFR           []domain.CEFRLevel `json:"cefr,omitempty" jsonschema:"match any supplied CEFR level"`
	OnlySavedItems bool               `json:"onlySavedItems,omitempty" jsonschema:"include only explanations whose term is saved"`
	IncludeStale   bool               `json:"includeStale,omitempty" jsonschema:"include explanations backed by inactive snapshots"`
	Sort           SortOrder          `json:"sort,omitempty" jsonschema:"result ordering"`
	Limit          int                `json:"limit,omitempty" jsonschema:"page size from 1 to 100"`
	Cursor         string             `json:"cursor,omitempty" jsonschema:"opaque cursor returned by the previous page"`
}

type ExplanationsListOutput struct {
	Explanations []domain.ExplanationSummary `json:"explanations"`
	NextCursor   string                      `json:"nextCursor,omitempty"`
}

type ExplanationWriteInput struct {
	Op            ExplanationOperation     `json:"op" jsonschema:"explicit cache mutation"`
	Value         *explanation.UpsertValue `json:"value,omitempty"`
	ExplanationID string                   `json:"explanationId,omitempty"`
}

type explanationUpsertInput struct {
	Op    ExplanationOperation    `json:"op"`
	Value explanation.UpsertValue `json:"value"`
}

type explanationDeleteInput struct {
	Op            ExplanationOperation `json:"op"`
	ExplanationID string               `json:"explanationId"`
}

type ExplanationWriteOutput struct {
	Created       *bool               `json:"created,omitempty"`
	Explanation   *domain.Explanation `json:"explanation,omitempty"`
	Deleted       bool                `json:"deleted,omitempty"`
	ExplanationID string              `json:"explanationId,omitempty"`
}

type explanationUpsertOutput struct {
	Created     bool               `json:"created"`
	Explanation domain.Explanation `json:"explanation"`
}

type explanationDeleteOutput struct {
	Deleted       bool   `json:"deleted"`
	ExplanationID string `json:"explanationId"`
}
