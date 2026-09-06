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
	Term              string                    `json:"term" jsonschema:"word, phrase, idiom, or expression to save"`
	Status            domain.LearningStatus     `json:"status,omitempty" jsonschema:"initial learning status; defaults to new"`
	Usefulness        domain.Usefulness         `json:"usefulness,omitempty" jsonschema:"personal learning usefulness, not difficulty; defaults to normal"`
	Tags              []string                  `json:"tags,omitempty" jsonschema:"initial normalized learning tags"`
	CustomDescription *string                   `json:"customDescription,omitempty" jsonschema:"initial learner description from any source"`
	DescriptionSource *domain.DescriptionSource `json:"descriptionSource,omitempty" jsonschema:"source attribution for the custom description"`
	Notes             []string                  `json:"notes,omitempty" jsonschema:"initial personal learning notes"`
	Examples          []string                  `json:"examples,omitempty" jsonschema:"initial personal example sentences"`
	Context           string                    `json:"context,omitempty" jsonschema:"short context that distinguishes this meaning from other meanings of the same term"`
	Definition        string                    `json:"definition,omitempty" jsonschema:"exact dictionary definition selected from dictionary_lookup; identifies the separately learned sense"`
}

type VocabularyUpdateChanges struct {
	Status            *domain.LearningStatus    `json:"status,omitempty" jsonschema:"replacement learning status"`
	Usefulness        *domain.Usefulness        `json:"usefulness,omitempty" jsonschema:"replacement personal learning usefulness, not difficulty"`
	Tags              *[]string                 `json:"tags,omitempty" jsonschema:"replacement tags; an empty array clears them"`
	CustomDescription *string                   `json:"customDescription,omitempty" jsonschema:"replacement description; an empty string clears it"`
	DescriptionSource *domain.DescriptionSource `json:"descriptionSource,omitempty" jsonschema:"replacement source; an empty object clears it"`
	Notes             *[]string                 `json:"notes,omitempty" jsonschema:"replacement notes; an empty array clears them"`
	Examples          *[]string                 `json:"examples,omitempty" jsonschema:"replacement examples; an empty array clears them"`
}

type VocabularyUpdateInput struct {
	ItemID  string                  `json:"itemId,omitempty"`
	Term    string                  `json:"term,omitempty"`
	Changes VocabularyUpdateChanges `json:"changes"`
}

type vocabularyUpdateByIDInput struct {
	ItemID  string                  `json:"itemId"`
	Changes VocabularyUpdateChanges `json:"changes"`
}

type vocabularyUpdateByTermInput struct {
	Term    string                  `json:"term"`
	Changes VocabularyUpdateChanges `json:"changes"`
}

// Deprecated response shapes remain as Go aliases for source compatibility.
type VocabularyUpdateOutput struct {
	Item domain.VocabularyItem `json:"item"`
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
	Query                string                  `json:"query,omitempty" jsonschema:"case-insensitive term substring search"`
	Statuses             []domain.LearningStatus `json:"statuses,omitempty" jsonschema:"match any supplied learning status"`
	Tags                 []string                `json:"tags,omitempty" jsonschema:"require all supplied normalized tags"`
	HasLookup            *bool                   `json:"hasLookup,omitempty" jsonschema:"filter by presence of a linked dictionary lookup"`
	HasCustomDescription *bool                   `json:"hasCustomDescription,omitempty" jsonschema:"filter by presence of a custom description"`
	Sort                 SortOrder               `json:"sort,omitempty" jsonschema:"result ordering"`
	Limit                int                     `json:"limit,omitempty" jsonschema:"page size from 1 to 100"`
	Cursor               string                  `json:"cursor,omitempty" jsonschema:"opaque cursor returned by the previous page"`
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

type LearningNextInput struct {
	IncludeComments bool `json:"includeComments,omitempty" jsonschema:"include all saved review comments; latest comment is always returned"`
}

type LearningReviewInput struct {
	ReviewToken string              `json:"reviewToken" jsonschema:"opaque token returned by learning_next"`
	Rating      domain.ReviewRating `json:"rating" jsonschema:"again means failed; hard, good, and easy indicate increasing recall quality"`
	Comment     string              `json:"comment,omitempty" jsonschema:"optional note about what was difficult or confused"`
}
