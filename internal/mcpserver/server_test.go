package mcpserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"reflect"
	"sort"
	"strings"
	"testing"

	"english-learning-mcp/internal/apperr"
	"english-learning-mcp/internal/dictionary"
	"english-learning-mcp/internal/domain"
	"english-learning-mcp/internal/learning"
	"english-learning-mcp/internal/storage"
	"english-learning-mcp/internal/vocabulary"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fixtureProvider struct {
	calls   int
	entries []domain.DictionaryEntry
}

func (*fixtureProvider) Name() string           { return "cambridge" }
func (*fixtureProvider) ParserVersion() int     { return 12 }
func (*fixtureProvider) DatasetVersion() string { return "" }
func (provider *fixtureProvider) Lookup(context.Context, string) (domain.DictionarySnapshotData, error) {
	provider.calls++
	data := domain.DictionarySnapshotData{
		SourceURL: "https://dictionary.example/bank",
		Status:    200,
		Entries: []domain.DictionaryEntry{{
			Headword:     "bank",
			PartOfSpeech: "noun",
			Definitions: []domain.DictionaryDefinition{{
				Definition: "land beside a river",
				Examples:   []string{"We sat on the bank."},
				Phrases:    []string{},
				SeeAlso:    []string{},
				Images:     []domain.DictionaryImage{},
				Labels:     []string{"B1"},
			}},
		}},
		Suggestions: []string{},
		Images:      []domain.DictionaryImage{},
	}
	if provider.entries != nil {
		data.Entries = provider.entries
	}
	return data, nil
}

func TestMCPToolsExposeLookupAndLearningList(t *testing.T) {
	ctx := context.Background()
	clientSession, provider := newTestSession(t, ctx)

	listed, err := clientSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	names := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	wantNames := []string{
		"dictionary_lookup",
		"learning_next",
		"learning_review",
		"learning_review_update",
		"reinforcement_next",
		"reinforcement_review",
		"vocabulary_delete",
		"vocabulary_get",
		"vocabulary_list",
		"vocabulary_save",
		"vocabulary_update",
	}
	if !equalStrings(names, wantNames) {
		t.Fatalf("tool names = %#v, want %#v", names, wantNames)
	}

	invalidResult, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "vocabulary_save",
		Arguments: map[string]any{"term": "bank", "unexpected": true},
	})
	if err != nil {
		t.Fatalf("invalid CallTool() protocol error = %v", err)
	}
	if !invalidResult.IsError {
		t.Fatal("invalid CallTool() IsError = false")
	}
	var applicationError struct {
		Code apperr.Code `json:"code"`
	}
	decodeToolContent(t, invalidResult, &applicationError)
	if applicationError.Code != apperr.InvalidArgument {
		t.Fatalf("invalid call code = %s", applicationError.Code)
	}

	description := "A description imported from another source."
	saved := callTool[vocabulary.SaveResult](t, ctx, clientSession, "vocabulary_save", VocabularySaveInput{
		Term:              "bank",
		Status:            domain.LearningStatusLearning,
		Tags:              []string{"Finance", "common"},
		CustomDescription: &description,
		DescriptionSource: &domain.DescriptionSource{
			Title: "External dictionary",
			URL:   "https://example.test/bank",
		},
		Notes:    []string{"Personal note."},
		Examples: []string{"I visited the bank.", "The bank approved my loan."},
	})
	if saved.Lookup != nil || saved.CustomDescription != description || saved.Status != domain.LearningStatusLearning {
		t.Fatalf("bare saved item = %#v", saved.VocabularyItem)
	}

	next := callTool[learning.NextResult](t, ctx, clientSession, "learning_next", LearningNextInput{})
	if next.Term != "bank" || next.Reason != "new" || next.ReviewToken == "" {
		t.Fatalf("learning next = %#v", next)
	}
	if next.Definition != description || next.LatestComment != nil || next.Comments != nil {
		t.Fatalf("compact learning content = %#v", next)
	}
	if next.CustomDescription != saved.CustomDescription ||
		next.DescriptionSource == nil || *next.DescriptionSource != *saved.DescriptionSource ||
		!equalStrings(next.Notes, saved.Notes) || !equalStrings(next.Examples, saved.Examples) ||
		!equalStrings(next.Tags, saved.Tags) {
		t.Fatalf("learning next lost saved tutoring context: %#v, saved = %#v", next, saved)
	}
	repeated := callTool[learning.NextResult](t, ctx, clientSession, "learning_next", LearningNextInput{})
	if next.PresentationID <= 0 || repeated.PresentationID == next.PresentationID {
		t.Fatalf("repeated presentation reused its identity: first = %#v, repeated = %#v", next, repeated)
	}
	if repeated.Term != next.Term || repeated.ReviewToken != next.ReviewToken {
		t.Fatalf("repeated presentation changed the pending review: first = %#v, repeated = %#v", next, repeated)
	}

	review := callTool[learning.RecordResult](t, ctx, clientSession, "learning_review", LearningReviewInput{
		ReviewToken: next.ReviewToken,
		Rating:      domain.ReviewRatingGood,
		Comment:     "Needed a moment to separate the meanings.",
	})
	if !review.Recorded || review.Duplicate || review.NextReviewAt == "" {
		t.Fatalf("learning review = %#v", review)
	}
	duplicateReview := callTool[learning.RecordResult](t, ctx, clientSession, "learning_review", LearningReviewInput{
		ReviewToken: repeated.ReviewToken,
		Rating:      domain.ReviewRatingGood,
		Comment:     "Needed a moment to separate the meanings.",
	})
	if !duplicateReview.Duplicate || duplicateReview.NextReviewAt != review.NextReviewAt {
		t.Fatalf("duplicate learning review = %#v, first = %#v", duplicateReview, review)
	}
	withComment := callTool[learning.NextResult](t, ctx, clientSession, "learning_next", LearningNextInput{})
	if withComment.CustomDescription != saved.CustomDescription || !equalStrings(withComment.Notes, saved.Notes) {
		t.Fatalf("learning next lost tutoring context after review: %#v", withComment)
	}
	if withComment.LatestComment == nil || withComment.LatestComment.Text != "Needed a moment to separate the meanings." {
		t.Fatalf("learning next comment = %#v", withComment)
	}

	lookup := callTool[domain.DictionaryLookupResult](t, ctx, clientSession, "dictionary_lookup", DictionaryLookupInput{Term: "bank"})
	if lookup.LookupID == "" || lookup.Cache.State != domain.CacheMiss || provider.calls != 1 {
		t.Fatalf("dictionary lookup = %#v, calls = %d", lookup, provider.calls)
	}

	item := callTool[domain.VocabularyItem](t, ctx, clientSession, "vocabulary_get", vocabularyGetByTermInput{Term: "bank"})
	if item.Lookup == nil || item.Lookup.LookupID != lookup.LookupID {
		t.Fatalf("linked vocabulary get = %#v", item)
	}
	if len(item.Lookup.Entries) != 1 || item.CustomDescription != description {
		t.Fatalf("complete vocabulary get = %#v", item)
	}

	status := domain.LearningStatusLearned
	tags := []string{"Core", "Finance"}
	notes := []string{"Updated personal note."}
	contextValue := "money held by a financial institution"
	updatedMetadata := callTool[domain.VocabularyItem](t, ctx, clientSession, "vocabulary_update", vocabularyUpdateByTermInput{
		Term: "bank",
		Changes: VocabularyUpdateChanges{
			Status:  &status,
			Tags:    &tags,
			Notes:   &notes,
			Context: &contextValue,
		},
	})
	if updatedMetadata.Status != domain.LearningStatusLearned || len(updatedMetadata.Tags) != 2 ||
		updatedMetadata.Context != contextValue {
		t.Fatalf("vocabulary update = %#v", updatedMetadata)
	}
	if updatedMetadata.CustomDescription != description || !equalStrings(updatedMetadata.Examples, saved.Examples) {
		t.Fatalf("vocabulary update lost omitted fields = %#v", updatedMetadata)
	}

	cached := callTool[domain.DictionaryLookupResult](t, ctx, clientSession, "dictionary_lookup", DictionaryLookupInput{Term: "bank"})
	if cached.Cache.State != domain.CacheHit || cached.LookupID != lookup.LookupID || provider.calls != 1 {
		t.Fatalf("cached lookup = %#v, calls = %d", cached, provider.calls)
	}

	refreshed := callTool[domain.DictionaryLookupResult](t, ctx, clientSession, "dictionary_lookup", DictionaryLookupInput{
		Term:    "bank",
		Refresh: true,
	})
	if refreshed.Cache.State != domain.CacheRefreshed || refreshed.LookupID == lookup.LookupID || provider.calls != 2 {
		t.Fatalf("refreshed lookup = %#v, calls = %d", refreshed, provider.calls)
	}
	updatedLookup := callTool[domain.VocabularyItem](t, ctx, clientSession, "vocabulary_get", vocabularyGetByTermInput{Term: "bank"})
	if updatedLookup.Lookup == nil || updatedLookup.Lookup.LookupID != refreshed.LookupID {
		t.Fatalf("refreshed vocabulary item = %#v", updatedLookup)
	}

	hasLookup := true
	vocabularyItems := callTool[VocabularyListOutput](t, ctx, clientSession, "vocabulary_list", VocabularyListInput{
		Statuses:  []domain.LearningStatus{domain.LearningStatusLearned},
		Tags:      []string{"finance"},
		HasLookup: &hasLookup,
	})
	if len(vocabularyItems.Items) != 1 || vocabularyItems.Items[0].Lookup == nil {
		t.Fatalf("vocabulary list = %#v", vocabularyItems)
	}

	callTool[VocabularyDeleteOutput](t, ctx, clientSession, "vocabulary_delete", VocabularyDeleteInput{ItemID: item.ItemID})
	stillCached := callTool[domain.DictionaryLookupResult](t, ctx, clientSession, "dictionary_lookup", DictionaryLookupInput{Term: "bank"})
	if stillCached.LookupID != refreshed.LookupID || stillCached.Cache.State != domain.CacheHit {
		t.Fatalf("lookup after vocabulary delete = %#v", stillCached)
	}
}

func TestMCPLearningReviewUpdateCorrectsLatestWithOriginalToken(t *testing.T) {
	ctx := context.Background()
	session, _ := newTestSession(t, ctx)
	callTool[vocabulary.SaveResult](t, ctx, session, "vocabulary_save", VocabularySaveInput{Term: "bank"})
	first := callTool[learning.NextResult](t, ctx, session, "learning_next", LearningNextInput{})
	original := callTool[learning.RecordResult](t, ctx, session, "learning_review", LearningReviewInput{
		ReviewToken: first.ReviewToken,
		Rating:      domain.ReviewRatingGood,
		Comment:     "Confused the meanings.",
	})
	pending := callTool[learning.NextResult](t, ctx, session, "learning_next", LearningNextInput{})
	assertToolInvalidArgument(t, ctx, session, "learning_review", LearningReviewInput{
		ReviewToken: first.ReviewToken,
		Rating:      domain.ReviewRatingAgain,
	})
	assertToolInvalidArgument(t, ctx, session, "learning_review_update", map[string]any{
		"reviewToken": first.ReviewToken, "rating": "incorrect",
	})
	correction := LearningReviewUpdateInput{
		ReviewToken: first.ReviewToken,
		Rating:      domain.ReviewRatingAgain,
	}
	corrected := callTool[learning.RecordResult](t, ctx, session, "learning_review_update", correction)
	if !corrected.Recorded || corrected.Duplicate || corrected.EffectiveRating != domain.ReviewRatingAgain ||
		corrected.NextReviewAt == original.NextReviewAt {
		t.Fatalf("correction = %#v, original = %#v", corrected, original)
	}
	duplicate := callTool[learning.RecordResult](t, ctx, session, "learning_review_update", correction)
	if !duplicate.Duplicate || duplicate.NextReviewAt != corrected.NextReviewAt ||
		duplicate.EffectiveRating != domain.ReviewRatingAgain {
		t.Fatalf("duplicate correction = %#v, corrected = %#v", duplicate, corrected)
	}
	withComment := callTool[learning.NextResult](t, ctx, session, "learning_next", LearningNextInput{IncludeComments: true})
	if withComment.ReviewToken != pending.ReviewToken || len(withComment.Comments) != 1 ||
		withComment.LatestComment == nil || withComment.LatestComment.Text != "Confused the meanings." ||
		withComment.LatestComment.Rating != domain.ReviewRatingAgain {
		t.Fatalf("correction did not preserve pending token and update comment history: %#v", withComment)
	}
	empty := ""
	correction.Comment = &empty
	cleared := callTool[learning.RecordResult](t, ctx, session, "learning_review_update", correction)
	if cleared.Duplicate || cleared.NextReviewAt != corrected.NextReviewAt {
		t.Fatalf("comment-only correction changed the schedule: %#v", cleared)
	}
	withoutComment := callTool[learning.NextResult](t, ctx, session, "learning_next", LearningNextInput{IncludeComments: true})
	if withoutComment.LatestComment != nil || len(withoutComment.Comments) != 0 {
		t.Fatalf("cleared comment is still visible: %#v", withoutComment)
	}
	nextReview := callTool[learning.RecordResult](t, ctx, session, "learning_review", LearningReviewInput{
		ReviewToken: pending.ReviewToken,
		Rating:      domain.ReviewRatingGood,
	})
	if !nextReview.Recorded || nextReview.Duplicate {
		t.Fatalf("already presented next review was invalidated: %#v", nextReview)
	}
	assertToolInvalidArgument(t, ctx, session, "learning_review_update", correction)
}

func TestMCPUsefulnessMetadataAndValidation(t *testing.T) {
	ctx := context.Background()
	session, _ := newTestSession(t, ctx)
	saved := callTool[vocabulary.SaveResult](t, ctx, session, "vocabulary_save", map[string]any{
		"term": "count clouds before breakfast", "usefulness": "high",
	})
	if !saved.Created || saved.Usefulness != domain.UsefulnessHigh {
		t.Fatalf("saved usefulness = %#v", saved)
	}
	duplicate := callTool[vocabulary.SaveResult](t, ctx, session, "vocabulary_save", map[string]any{
		"term": "count clouds before breakfast", "usefulness": "low",
	})
	if duplicate.Created || duplicate.ItemID != saved.ItemID || duplicate.Usefulness != domain.UsefulnessHigh {
		t.Fatalf("duplicate save changed usefulness: %#v", duplicate)
	}

	for _, test := range []struct {
		name  string
		value any
	}{
		{name: "unknown", value: "urgent"},
		{name: "empty", value: ""},
		{name: "null", value: nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertToolInvalidArgument(t, ctx, session, "vocabulary_save", map[string]any{
				"term": "invalid", "usefulness": test.value,
			})
			assertToolInvalidArgument(t, ctx, session, "vocabulary_update", map[string]any{
				"itemId": saved.ItemID,
				"changes": map[string]any{
					"usefulness": test.value,
					"notes":      []string{"Must not be persisted."},
				},
			})
			items := callTool[VocabularyListOutput](t, ctx, session, "vocabulary_list", VocabularyListInput{})
			if len(items.Items) != 1 || items.Items[0].ItemID != saved.ItemID ||
				items.Items[0].Usefulness != domain.UsefulnessHigh || len(items.Items[0].Notes) != 0 {
				t.Fatalf("invalid input mutated vocabulary: %#v", items)
			}
		})
	}

	updated := callTool[domain.VocabularyItem](t, ctx, session, "vocabulary_update", map[string]any{
		"itemId": saved.ItemID, "changes": map[string]any{"usefulness": "low"},
	})
	if updated.Usefulness != domain.UsefulnessLow || updated.Status != saved.Status {
		t.Fatalf("usefulness-only update = %#v", updated)
	}
	preserved := callTool[domain.VocabularyItem](t, ctx, session, "vocabulary_update", map[string]any{
		"term": "count clouds before breakfast", "changes": map[string]any{"notes": []string{"A personal reminder."}},
	})
	if preserved.Usefulness != domain.UsefulnessLow || len(preserved.Notes) != 1 {
		t.Fatalf("partial update = %#v", preserved)
	}
	fetched := callTool[domain.VocabularyItem](t, ctx, session, "vocabulary_get", vocabularyGetByIDInput{ItemID: saved.ItemID})
	if fetched.Usefulness != domain.UsefulnessLow {
		t.Fatalf("get usefulness = %q", fetched.Usefulness)
	}
	listed := callTool[VocabularyListOutput](t, ctx, session, "vocabulary_list", VocabularyListInput{})
	if len(listed.Items) != 1 || listed.Items[0].Usefulness != domain.UsefulnessLow {
		t.Fatalf("list usefulness = %#v", listed)
	}
	next := callTool[learning.NextResult](t, ctx, session, "learning_next", LearningNextInput{})
	if next.Term != "count clouds before breakfast" || next.Usefulness != domain.UsefulnessLow {
		t.Fatalf("next usefulness = %#v", next)
	}
	normal := callTool[vocabulary.SaveResult](t, ctx, session, "vocabulary_save", map[string]any{"term": "compare seven invisible umbrellas"})
	if normal.Usefulness != domain.UsefulnessNormal {
		t.Fatalf("default usefulness = %q", normal.Usefulness)
	}
}

func TestMCPCombinesOfflineFrequencyWithUsefulnessHints(t *testing.T) {
	ctx := context.Background()
	session, _ := newTestSession(t, ctx)
	saved := callTool[vocabulary.SaveResult](t, ctx, session, "vocabulary_save", map[string]any{"term": "  THE  "})
	if saved.NormalizedTerm != "the" || saved.Usefulness != domain.UsefulnessHigh {
		t.Fatalf("automatic common-word usefulness = %#v", saved)
	}
	updated := callTool[domain.VocabularyItem](t, ctx, session, "vocabulary_update", map[string]any{
		"itemId": saved.ItemID, "changes": map[string]any{"usefulness": "low"},
	})
	if updated.Usefulness != domain.UsefulnessNormal {
		t.Fatalf("two high frequency votes combined with double-weighted low hint = %q", updated.Usefulness)
	}
	preserved := callTool[domain.VocabularyItem](t, ctx, session, "vocabulary_update", map[string]any{
		"itemId": saved.ItemID, "changes": map[string]any{"notes": []string{"Keep the original hint."}},
	})
	if preserved.Usefulness != domain.UsefulnessNormal {
		t.Fatalf("unrelated update recomputed from the effective result: %#v", preserved)
	}
	duplicate := callTool[vocabulary.SaveResult](t, ctx, session, "vocabulary_save", map[string]any{
		"term": "the", "usefulness": "high",
	})
	if duplicate.Created || duplicate.Usefulness != domain.UsefulnessNormal {
		t.Fatalf("duplicate save replaced the hint: %#v", duplicate)
	}
	hinted := callTool[vocabulary.SaveResult](t, ctx, session, "vocabulary_save", map[string]any{
		"term": "and", "usefulness": "normal",
	})
	if hinted.Usefulness != domain.UsefulnessHigh {
		t.Fatalf("unanimous frequency evidence should refine a normal hint: %#v", hinted)
	}
	singleSource := callTool[vocabulary.SaveResult](t, ctx, session, "vocabulary_save", map[string]any{"term": "so-called"})
	if singleSource.Usefulness != domain.UsefulnessLow {
		t.Fatalf("omitted hint should not become a normal vote: %#v", singleSource)
	}
	neutralHint := callTool[domain.VocabularyItem](t, ctx, session, "vocabulary_update", map[string]any{
		"itemId": singleSource.ItemID, "changes": map[string]any{"usefulness": "normal"},
	})
	if neutralHint.Usefulness != domain.UsefulnessNormal {
		t.Fatalf("explicit normal hint should contribute its own vote: %#v", neutralHint)
	}
}

func TestMCPExpressionInferencePreservesSavedSpellingAndIdentity(t *testing.T) {
	ctx := context.Background()
	session, _ := newTestSession(t, ctx)
	canonical := callTool[vocabulary.SaveResult](t, ctx, session, "vocabulary_save", map[string]any{
		"term": "spill the beans",
	})
	typo := callTool[vocabulary.SaveResult](t, ctx, session, "vocabulary_save", map[string]any{
		"term": "spill the beasn",
	})
	if canonical.Usefulness != domain.UsefulnessHigh || typo.Usefulness != domain.UsefulnessHigh {
		t.Fatalf("canonical and unique typo inference = %#v, %#v", canonical, typo)
	}
	if canonical.ItemID == typo.ItemID || typo.Term != "spill the beasn" || typo.NormalizedTerm != "spill the beasn" {
		t.Fatalf("usefulness matching rewrote or merged vocabulary: %#v, %#v", canonical, typo)
	}
	fetched := callTool[domain.VocabularyItem](t, ctx, session, "vocabulary_get", map[string]any{
		"term": "spill the beasn",
	})
	if fetched.ItemID != typo.ItemID || fetched.Term != "spill the beasn" {
		t.Fatalf("exact retrieval changed with usefulness matching: %#v", fetched)
	}
	updated := callTool[domain.VocabularyItem](t, ctx, session, "vocabulary_update", map[string]any{
		"itemId": typo.ItemID, "changes": map[string]any{"usefulness": "low"},
	})
	if updated.Usefulness != domain.UsefulnessNormal || updated.Term != typo.Term {
		t.Fatalf("expression evidence and low hint should combine without rewriting: %#v", updated)
	}
	duplicate := callTool[vocabulary.SaveResult](t, ctx, session, "vocabulary_save", map[string]any{
		"term": "spill the beasn", "usefulness": "high",
	})
	if duplicate.Created || duplicate.ItemID != typo.ItemID || duplicate.Usefulness != domain.UsefulnessNormal {
		t.Fatalf("duplicate replaced the original expression hint: %#v", duplicate)
	}
	protected := callTool[vocabulary.SaveResult](t, ctx, session, "vocabulary_save", map[string]any{
		"term": "spill the beads",
	})
	if protected.Usefulness != domain.UsefulnessNormal {
		t.Fatalf("real-word substitution was treated as a typo: %#v", protected)
	}
}

func TestMCPPersonalInterestRejectsInvalidUpdatesAtomically(t *testing.T) {
	ctx := context.Background()
	session, _ := newTestSession(t, ctx)
	saved := callTool[vocabulary.SaveResult](t, ctx, session, "vocabulary_save", map[string]any{
		"term": "interesting expression", "personalInterest": "high",
	})
	for _, invalid := range []any{nil, "", "interesting"} {
		assertToolInvalidArgument(t, ctx, session, "vocabulary_update", map[string]any{
			"itemId":  saved.ItemID,
			"changes": map[string]any{"personalInterest": invalid, "notes": []string{"must not commit"}},
		})
	}
	item := callTool[domain.VocabularyItem](t, ctx, session, "vocabulary_get", map[string]any{"itemId": saved.ItemID})
	if item.PersonalInterest != domain.PersonalInterestHigh || len(item.Notes) != 0 {
		t.Fatalf("invalid preference update changed saved content: %#v", item)
	}
	reset := callTool[domain.VocabularyItem](t, ctx, session, "vocabulary_update", map[string]any{
		"itemId": saved.ItemID, "changes": map[string]any{"personalInterest": "normal"},
	})
	if reset.PersonalInterest != domain.PersonalInterestNormal || reset.Usefulness != saved.Usefulness {
		t.Fatalf("interest reset changed general usefulness: %#v", reset)
	}
}

func TestMCPVocabularyExerciseFiltersAndRandomExclusions(t *testing.T) {
	ctx := context.Background()
	session, provider := newTestSession(t, ctx)
	provider.entries = []domain.DictionaryEntry{
		{
			Headword:     "clouds",
			PartOfSpeech: "noun",
			Definitions: []domain.DictionaryDefinition{
				{Definition: "an imagined collection of clouds"},
				{Definition: "another imagined collection of clouds"},
			},
		},
		{
			Headword:     "clouds",
			PartOfSpeech: "verb",
			Definitions: []domain.DictionaryDefinition{
				{Definition: "to imagine clouds"},
			},
		},
	}
	for entryIndex := range provider.entries {
		for definitionIndex := range provider.entries[entryIndex].Definitions {
			definition := &provider.entries[entryIndex].Definitions[definitionIndex]
			definition.Examples = []string{}
			definition.Phrases = []string{}
			definition.SeeAlso = []string{}
			definition.Images = []domain.DictionaryImage{}
			definition.Labels = []string{}
		}
	}
	nounDefinition := provider.entries[0].Definitions[0].Definition
	otherNounDefinition := provider.entries[0].Definitions[1].Definition
	verbDefinition := provider.entries[1].Definitions[0].Definition
	matching := make(map[string]domain.VocabularyItem)
	for _, seed := range []struct {
		term       string
		definition string
		status     domain.LearningStatus
		usefulness domain.Usefulness
		interest   domain.PersonalInterest
		tags       []string
		match      bool
	}{
		{"count clouds before breakfast", nounDefinition, domain.LearningStatusLearning, domain.UsefulnessHigh, domain.PersonalInterestHigh, []string{"exercise", "sky"}, true},
		{"count clouds before breakfast", otherNounDefinition, domain.LearningStatusLearned, domain.UsefulnessHigh, domain.PersonalInterestHigh, []string{"exercise", "sky"}, true},
		{"count clouds before breakfast", verbDefinition, domain.LearningStatusLearning, domain.UsefulnessHigh, domain.PersonalInterestHigh, []string{"exercise", "sky"}, false},
		{"clouds", nounDefinition, domain.LearningStatusLearning, domain.UsefulnessHigh, domain.PersonalInterestHigh, []string{"exercise", "sky"}, false},
		{"polish clouds before breakfast", nounDefinition, domain.LearningStatusLearning, domain.UsefulnessLow, domain.PersonalInterestHigh, []string{"exercise", "sky"}, false},
		{"embroider clouds before breakfast", nounDefinition, domain.LearningStatusLearning, domain.UsefulnessHigh, domain.PersonalInterestLow, []string{"exercise", "sky"}, false},
		{"pickle clouds before breakfast", nounDefinition, domain.LearningStatusArchived, domain.UsefulnessHigh, domain.PersonalInterestHigh, []string{"exercise", "sky"}, false},
		{"weigh clouds before breakfast", nounDefinition, domain.LearningStatusLearning, domain.UsefulnessHigh, domain.PersonalInterestHigh, []string{"exercise"}, false},
		{"count clouds after breakfast", "", domain.LearningStatusLearning, domain.UsefulnessHigh, domain.PersonalInterestHigh, []string{"exercise", "sky"}, false},
	} {
		callTool[domain.DictionaryLookupResult](t, ctx, session, "dictionary_lookup", DictionaryLookupInput{Term: seed.term})
		saved := callTool[vocabulary.SaveResult](t, ctx, session, "vocabulary_save", VocabularySaveInput{
			Term:             seed.term,
			Definition:       seed.definition,
			Status:           seed.status,
			Usefulness:       seed.usefulness,
			PersonalInterest: seed.interest,
			Tags:             seed.tags,
		})
		if seed.match {
			matching[saved.ItemID] = saved.VocabularyItem
		}
	}
	filters := VocabularyListInput{
		Query:            "clouds",
		Statuses:         []domain.LearningStatus{domain.LearningStatusLearning, domain.LearningStatusLearned},
		Tags:             []string{"Exercise", "Sky"},
		TermType:         TermTypeExpression,
		PartsOfSpeech:    []string{"  NOUN  ", "adjective"},
		Usefulness:       domain.UsefulnessHigh,
		PersonalInterest: domain.PersonalInterestHigh,
		Sort:             SortRandom,
		Limit:            100,
	}
	all := callTool[VocabularyListOutput](t, ctx, session, "vocabulary_list", filters)
	if len(all.Items) != len(matching) || all.NextCursor != "" {
		t.Fatalf("combined exercise filters = %#v", all)
	}
	seen := make(map[string]bool)
	for _, item := range all.Items {
		want, ok := matching[item.ItemID]
		if !ok || seen[item.ItemID] || !reflect.DeepEqual(item, want) {
			t.Fatalf("sample returned a duplicate, unmatched, or incomplete item: %#v", item)
		}
		seen[item.ItemID] = true
	}
	filters.Limit = 1
	first := callTool[VocabularyListOutput](t, ctx, session, "vocabulary_list", filters)
	if len(first.Items) != 1 || first.NextCursor != "" || !seen[first.Items[0].ItemID] {
		t.Fatalf("bounded random sample = %#v", first)
	}
	filters.ExcludeItemIDs = []string{"  " + first.Items[0].ItemID + "  "}
	second := callTool[VocabularyListOutput](t, ctx, session, "vocabulary_list", filters)
	if len(second.Items) != 1 || second.NextCursor != "" ||
		second.Items[0].ItemID == first.Items[0].ItemID || !seen[second.Items[0].ItemID] ||
		second.Items[0].NormalizedTerm != first.Items[0].NormalizedTerm {
		t.Fatalf("exact item exclusion did not retain the other saved sense: %#v", second)
	}
	filters.ExcludeItemIDs = append(filters.ExcludeItemIDs, second.Items[0].ItemID)
	empty := callTool[map[string]json.RawMessage](t, ctx, session, "vocabulary_list", filters)
	if string(empty["items"]) != "[]" || len(empty) != 1 {
		t.Fatalf("exhausted sample should return an empty array without a cursor: %#v", empty)
	}
}

func TestMCPVocabularyExerciseRejectsInvalidFilters(t *testing.T) {
	ctx := context.Background()
	session, _ := newTestSession(t, ctx)
	for _, test := range []struct {
		name      string
		arguments map[string]any
	}{
		{"term type", map[string]any{"termType": "idiom"}},
		{"empty term type", map[string]any{"termType": ""}},
		{"usefulness", map[string]any{"usefulness": "urgent"}},
		{"interest", map[string]any{"personalInterest": "urgent"}},
		{"sort", map[string]any{"sort": "shuffle"}},
		{"random cursor", map[string]any{"sort": "random", "cursor": "previous-page"}},
		{"empty part of speech", map[string]any{"partsOfSpeech": []string{""}}},
		{"long part of speech", map[string]any{"partsOfSpeech": []string{strings.Repeat("語", 51)}}},
		{"too many parts of speech", map[string]any{"partsOfSpeech": strings.Fields(strings.Repeat("noun ", 51))}},
		{"empty exclusion", map[string]any{"excludeItemIds": []string{""}}},
		{"long exclusion", map[string]any{"excludeItemIds": []string{strings.Repeat("x", 201)}}},
		{"too many exclusions", map[string]any{"excludeItemIds": strings.Fields(strings.Repeat("item ", 1001))}},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertToolInvalidArgument(t, ctx, session, "vocabulary_list", test.arguments)
		})
	}
}

func TestMCPVocabularyExercisePreservesPendingReview(t *testing.T) {
	ctx := context.Background()
	session, _ := newTestSession(t, ctx)
	saved := callTool[vocabulary.SaveResult](t, ctx, session, "vocabulary_save", VocabularySaveInput{
		Term: "count clouds before breakfast", Status: domain.LearningStatusLearning,
	})
	initial := callTool[learning.NextResult](t, ctx, session, "learning_next", LearningNextInput{})
	callTool[learning.RecordResult](t, ctx, session, "learning_review", LearningReviewInput{
		ReviewToken: initial.ReviewToken, Rating: domain.ReviewRatingGood,
	})
	pending := callTool[learning.NextResult](t, ctx, session, "learning_next", LearningNextInput{})
	before := callTool[domain.VocabularyItem](t, ctx, session, "vocabulary_get", VocabularyGetInput{ItemID: saved.ItemID})
	exercise := callTool[map[string]json.RawMessage](t, ctx, session, "vocabulary_list", VocabularyListInput{
		Statuses: []domain.LearningStatus{domain.LearningStatusLearning},
		Sort:     SortRandom,
		Limit:    1,
	})
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(exercise["items"], &items); err != nil {
		t.Fatalf("decode exercise items: %v", err)
	}
	if len(exercise) != 1 || len(items) != 1 {
		t.Fatalf("exercise should return only a vocabulary sample: %#v", exercise)
	}
	for _, key := range []string{"reviewToken", "presentationId", "nextReviewAt"} {
		if _, exists := items[0][key]; exists {
			t.Fatalf("exercise item unexpectedly exposes review lifecycle field %q", key)
		}
	}
	after := callTool[domain.VocabularyItem](t, ctx, session, "vocabulary_get", VocabularyGetInput{ItemID: saved.ItemID})
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("exercise changed saved vocabulary: before = %#v, after = %#v", before, after)
	}
	accepted := callTool[learning.RecordResult](t, ctx, session, "learning_review", LearningReviewInput{
		ReviewToken: pending.ReviewToken, Rating: domain.ReviewRatingGood,
	})
	if !accepted.Recorded || accepted.Duplicate {
		t.Fatalf("exercise consumed or invalidated the pending review: %#v", accepted)
	}
}

func newTestSession(t *testing.T, ctx context.Context) (*mcp.ClientSession, *fixtureProvider) {
	t.Helper()
	store, err := storage.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	provider := &fixtureProvider{}
	source := storage.SourceVersion{
		Provider:      provider.Name(),
		ParserVersion: provider.ParserVersion(),
	}
	server, err := New(Services{
		Dictionary: dictionary.NewService(store, provider, logger),
		Vocabulary: vocabulary.NewService(store, "owner-one", source),
		Learning:   learning.NewService(store, "owner-one"),
	}, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server.Connect() error = %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect() error = %v", err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })
	return clientSession, provider
}

func assertToolInvalidArgument(t *testing.T, ctx context.Context, session *mcp.ClientSession, name string, arguments any) {
	t.Helper()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatalf("CallTool(%s) protocol error = %v", name, err)
	}
	if !result.IsError {
		t.Fatalf("CallTool(%s) accepted invalid input", name)
	}
	var applicationError struct {
		Code apperr.Code `json:"code"`
	}
	decodeToolContent(t, result, &applicationError)
	if applicationError.Code != apperr.InvalidArgument {
		t.Fatalf("CallTool(%s) code = %s, want %s", name, applicationError.Code, apperr.InvalidArgument)
	}
}

func callTool[Output any](
	t *testing.T,
	ctx context.Context,
	session *mcp.ClientSession,
	name string,
	arguments any,
) Output {
	t.Helper()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatalf("CallTool(%s) protocol error = %v", name, err)
	}
	if result.IsError {
		t.Fatalf("CallTool(%s) tool error = %s", name, result.Content[0].(*mcp.TextContent).Text)
	}
	var output Output
	decodeToolContent(t, result, &output)
	return output
}

func decodeToolContent(t *testing.T, result *mcp.CallToolResult, target any) {
	t.Helper()
	if len(result.Content) != 1 {
		t.Fatalf("tool content length = %d, want 1", len(result.Content))
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("tool content type = %T, want TextContent", result.Content[0])
	}
	if err := json.Unmarshal([]byte(text.Text), target); err != nil {
		t.Fatalf("decode tool content %q: %v", text.Text, err)
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
