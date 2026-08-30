package domain

type CEFRLevel string

const (
	CEFRLevelA1 CEFRLevel = "A1"
	CEFRLevelA2 CEFRLevel = "A2"
	CEFRLevelB1 CEFRLevel = "B1"
	CEFRLevelB2 CEFRLevel = "B2"
	CEFRLevelC1 CEFRLevel = "C1"
	CEFRLevelC2 CEFRLevel = "C2"
)

var CEFRLevels = []CEFRLevel{
	CEFRLevelA1,
	CEFRLevelA2,
	CEFRLevelB1,
	CEFRLevelB2,
	CEFRLevelC1,
	CEFRLevelC2,
}

func (level CEFRLevel) Valid() bool {
	switch level {
	case CEFRLevelA1, CEFRLevelA2, CEFRLevelB1, CEFRLevelB2, CEFRLevelC1, CEFRLevelC2:
		return true
	default:
		return false
	}
}

type CEFR struct {
	Level      CEFRLevel `json:"level"`
	Source     string    `json:"source"`
	Confidence *float64  `json:"confidence,omitempty"`
	Reason     string    `json:"reason,omitempty"`
}

type CEFRSummary struct {
	Level  CEFRLevel `json:"level"`
	Source string    `json:"source"`
}

type SelectedMeaning struct {
	EntryIndex      int      `json:"entryIndex"`
	DefinitionIndex int      `json:"definitionIndex"`
	Headword        string   `json:"headword"`
	PartOfSpeech    string   `json:"partOfSpeech,omitempty"`
	Definition      string   `json:"definition"`
	Examples        []string `json:"examples"`
	Labels          []string `json:"labels"`
}

type LearnerAlternative struct {
	PartOfSpeech string   `json:"partOfSpeech,omitempty"`
	Explanation  string   `json:"explanation"`
	Reason       string   `json:"reason,omitempty"`
	Confidence   *float64 `json:"confidence,omitempty"`
}

type LearnerContent struct {
	Description        string               `json:"description"`
	WhyThisMeaningFits string               `json:"whyThisMeaningFits,omitempty"`
	Notes              []string             `json:"notes"`
	Examples           []string             `json:"examples"`
	Alternatives       []LearnerAlternative `json:"alternatives"`
}

type LexicalRelations struct {
	Synonyms []string `json:"synonyms"`
	Antonyms []string `json:"antonyms"`
	Source   string   `json:"source"`
}

type Generator struct {
	Name    string `json:"name"`
	Model   string `json:"model,omitempty"`
	Version string `json:"version"`
}

type Explanation struct {
	ExplanationID    string            `json:"explanationId"`
	Term             string            `json:"term"`
	NormalizedTerm   string            `json:"normalizedTerm"`
	Context          string            `json:"context"`
	LookupID         string            `json:"lookupId"`
	SelectedMeaning  *SelectedMeaning  `json:"selectedMeaning"`
	Learner          LearnerContent    `json:"learner"`
	CEFR             *CEFR             `json:"cefr,omitempty"`
	LexicalRelations *LexicalRelations `json:"lexicalRelations,omitempty"`
	Generator        Generator         `json:"generator"`
	Stale            bool              `json:"stale"`
	CreatedAt        string            `json:"createdAt"`
	UpdatedAt        string            `json:"updatedAt"`
}

type ExplanationSummary struct {
	ExplanationID      string       `json:"explanationId"`
	Term               string       `json:"term"`
	NormalizedTerm     string       `json:"normalizedTerm"`
	Context            string       `json:"context"`
	PartOfSpeech       string       `json:"partOfSpeech,omitempty"`
	CEFR               *CEFRSummary `json:"cefr,omitempty"`
	DescriptionPreview string       `json:"descriptionPreview"`
	Stale              bool         `json:"stale"`
	CreatedAt          string       `json:"createdAt"`
	UpdatedAt          string       `json:"updatedAt"`
}

type VocabularyItem struct {
	ItemID           string      `json:"itemId"`
	Term             string      `json:"term"`
	NormalizedTerm   string      `json:"normalizedTerm"`
	ExplanationCount int         `json:"explanationCount"`
	CEFRLevels       []CEFRLevel `json:"cefrLevels"`
	CreatedAt        string      `json:"createdAt"`
	UpdatedAt        string      `json:"updatedAt"`
}
