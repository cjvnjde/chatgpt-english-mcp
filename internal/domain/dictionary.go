package domain

type CacheState string

const (
	CacheHit           CacheState = "hit"
	CacheMiss          CacheState = "miss"
	CacheRefreshed     CacheState = "refreshed"
	CacheStaleFallback CacheState = "stale_fallback"
)

type SourceRef struct {
	Provider       string `json:"provider"`
	SourceURL      string `json:"sourceUrl,omitempty"`
	DatasetVersion string `json:"datasetVersion,omitempty"`
	ParserVersion  int    `json:"parserVersion"`
}

type DictionaryAudio struct {
	AudioURL    string `json:"audioUrl"`
	ContentType string `json:"contentType"`
}

type DictionaryImage struct {
	Title        string `json:"title,omitempty"`
	Alt          string `json:"alt,omitempty"`
	ImageURL     string `json:"imageUrl"`
	ThumbnailURL string `json:"thumbnailUrl,omitempty"`
	Credit       string `json:"credit,omitempty"`
}

type DictionaryWordGroup struct {
	Topic string   `json:"topic,omitempty"`
	Words []string `json:"words"`
}

type DictionaryUsage struct {
	Phrase  string `json:"phrase,omitempty"`
	Example string `json:"example,omitempty"`
}

type DictionaryDefinition struct {
	Definition  string                `json:"definition"`
	Examples    []string              `json:"examples"`
	Phrases     []string              `json:"phrases"`
	SeeAlso     []string              `json:"seeAlso"`
	Images      []DictionaryImage     `json:"images"`
	Guideword   string                `json:"guideword,omitempty"`
	PhraseTitle string                `json:"phraseTitle,omitempty"`
	Labels      []string              `json:"labels"`
	Usages      []DictionaryUsage     `json:"usages,omitempty"`
	Related     []DictionaryWordGroup `json:"related,omitempty"`
	Synonyms    []DictionaryWordGroup `json:"synonyms,omitempty"`
	Antonyms    []DictionaryWordGroup `json:"antonyms,omitempty"`
}

type DictionaryPronunciations struct {
	UK string `json:"uk,omitempty"`
	US string `json:"us,omitempty"`
}

type DictionaryAudioRegions struct {
	US *DictionaryAudio `json:"us,omitempty"`
	UK *DictionaryAudio `json:"uk,omitempty"`
}

type DictionaryEntry struct {
	Headword       string                   `json:"headword"`
	PartOfSpeech   string                   `json:"partOfSpeech,omitempty"`
	Pronunciations DictionaryPronunciations `json:"pronunciations"`
	Audio          *DictionaryAudioRegions  `json:"audio,omitempty"`
	Inflections    []string                 `json:"inflections,omitempty"`
	Definitions    []DictionaryDefinition   `json:"definitions"`
	Idioms         []string                 `json:"idioms,omitempty"`
}

type DictionaryCollocation struct {
	Phrase  string `json:"phrase"`
	Example string `json:"example,omitempty"`
}

type DictionaryCache struct {
	State     CacheState `json:"state"`
	FetchedAt string     `json:"fetchedAt"`
}

type DictionaryLookupResult struct {
	LookupID       string                  `json:"lookupId"`
	RequestedTerm  string                  `json:"requestedTerm"`
	NormalizedTerm string                  `json:"normalizedTerm"`
	Cache          DictionaryCache         `json:"cache"`
	Source         SourceRef               `json:"source"`
	Status         int                     `json:"status"`
	Entries        []DictionaryEntry       `json:"entries"`
	Suggestions    []string                `json:"suggestions"`
	Images         []DictionaryImage       `json:"images"`
	Idioms         []string                `json:"idioms,omitempty"`
	Collocations   []DictionaryCollocation `json:"collocations,omitempty"`
}

// DictionarySnapshotData is the immutable provider payload persisted for a lookup.
type DictionarySnapshotData struct {
	SourceURL    string                  `json:"sourceUrl,omitempty"`
	Status       int                     `json:"status"`
	Entries      []DictionaryEntry       `json:"entries"`
	Suggestions  []string                `json:"suggestions"`
	Images       []DictionaryImage       `json:"images"`
	Idioms       []string                `json:"idioms,omitempty"`
	Collocations []DictionaryCollocation `json:"collocations,omitempty"`
}
