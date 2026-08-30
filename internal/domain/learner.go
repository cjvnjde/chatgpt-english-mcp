package domain

type VocabularyItem struct {
	ItemID            string                  `json:"itemId"`
	Term              string                  `json:"term"`
	NormalizedTerm    string                  `json:"normalizedTerm"`
	CustomDescription string                  `json:"customDescription,omitempty"`
	Lookup            *DictionaryLookupResult `json:"lookup,omitempty"`
	CreatedAt         string                  `json:"createdAt"`
	UpdatedAt         string                  `json:"updatedAt"`
}
