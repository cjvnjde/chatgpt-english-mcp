package mcpserver

import "english-learning-mcp/internal/domain"

type SortOrder string

const (
	SortRecent       SortOrder = "recent"
	SortOldest       SortOrder = "oldest"
	SortAlphabetical SortOrder = "alphabetical"
)

type DictionaryLookupInput struct {
	Term    string `json:"term" jsonschema:"word, phrase, idiom, or expression to look up"`
	Refresh bool   `json:"refresh,omitempty" jsonschema:"fetch a new dictionary snapshot even when an entry is cached"`
}

type VocabularySaveInput struct {
	Term              string  `json:"term" jsonschema:"word, phrase, idiom, or expression to save"`
	CustomDescription *string `json:"customDescription,omitempty" jsonschema:"optional learner description from any source; an empty string clears it"`
}

type VocabularyGetInput struct {
	ItemID string `json:"itemId,omitempty" jsonschema:"opaque saved vocabulary item identifier"`
	Term   string `json:"term,omitempty" jsonschema:"exact saved term to retrieve"`
}

type vocabularyGetByIDInput struct {
	ItemID string `json:"itemId"`
}

type vocabularyGetByTermInput struct {
	Term string `json:"term"`
}

type VocabularyGetOutput struct {
	Item domain.VocabularyItem `json:"item"`
}

type VocabularyListInput struct {
	Query  string    `json:"query,omitempty" jsonschema:"case-insensitive term substring search"`
	Sort   SortOrder `json:"sort,omitempty" jsonschema:"result ordering"`
	Limit  int       `json:"limit,omitempty" jsonschema:"page size from 1 to 100"`
	Cursor string    `json:"cursor,omitempty" jsonschema:"opaque cursor returned by the previous page"`
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
