package domain

type LearningStatus string

const (
	LearningStatusNew      LearningStatus = "new"
	LearningStatusLearning LearningStatus = "learning"
	LearningStatusLearned  LearningStatus = "learned"
	LearningStatusArchived LearningStatus = "archived"
)

func (status LearningStatus) Valid() bool {
	switch status {
	case LearningStatusNew, LearningStatusLearning, LearningStatusLearned, LearningStatusArchived:
		return true
	default:
		return false
	}
}

type Usefulness string

const (
	UsefulnessLow    Usefulness = "low"
	UsefulnessNormal Usefulness = "normal"
	UsefulnessHigh   Usefulness = "high"
)

func (usefulness Usefulness) Valid() bool {
	switch usefulness {
	case UsefulnessLow, UsefulnessNormal, UsefulnessHigh:
		return true
	default:
		return false
	}
}

type DescriptionSource struct {
	Title string `json:"title,omitempty"`
	URL   string `json:"url,omitempty"`
}

type VocabularySense struct {
	Context         string                   `json:"context,omitempty"`
	EntryIndex      int                      `json:"entryIndex"`
	DefinitionIndex int                      `json:"definitionIndex"`
	Headword        string                   `json:"headword"`
	PartOfSpeech    string                   `json:"partOfSpeech,omitempty"`
	Pronunciations  DictionaryPronunciations `json:"pronunciations,omitempty"`
	Definition      DictionaryDefinition     `json:"definition"`
}

type VocabularyItem struct {
	ItemID            string                  `json:"itemId"`
	Term              string                  `json:"term"`
	NormalizedTerm    string                  `json:"normalizedTerm"`
	Status            LearningStatus          `json:"status"`
	Usefulness        Usefulness              `json:"usefulness"`
	Tags              []string                `json:"tags"`
	CustomDescription string                  `json:"customDescription,omitempty"`
	DescriptionSource *DescriptionSource      `json:"descriptionSource,omitempty"`
	Notes             []string                `json:"notes"`
	Examples          []string                `json:"examples"`
	Sense             *VocabularySense        `json:"sense,omitempty"`
	Lookup            *DictionaryLookupResult `json:"lookup,omitempty"`
	CreatedAt         string                  `json:"createdAt"`
	UpdatedAt         string                  `json:"updatedAt"`
}
