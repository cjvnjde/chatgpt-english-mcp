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

type DescriptionSource struct {
	Title string `json:"title,omitempty"`
	URL   string `json:"url,omitempty"`
}

type VocabularyItem struct {
	ItemID            string                  `json:"itemId"`
	Term              string                  `json:"term"`
	NormalizedTerm    string                  `json:"normalizedTerm"`
	Status            LearningStatus          `json:"status"`
	Tags              []string                `json:"tags"`
	CustomDescription string                  `json:"customDescription,omitempty"`
	DescriptionSource *DescriptionSource      `json:"descriptionSource,omitempty"`
	Notes             []string                `json:"notes"`
	Examples          []string                `json:"examples"`
	Lookup            *DictionaryLookupResult `json:"lookup,omitempty"`
	CreatedAt         string                  `json:"createdAt"`
	UpdatedAt         string                  `json:"updatedAt"`
}
