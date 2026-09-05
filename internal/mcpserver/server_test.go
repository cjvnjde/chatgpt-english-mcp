package mcpserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sort"
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
	calls int
}

func (*fixtureProvider) Name() string           { return "cambridge" }
func (*fixtureProvider) ParserVersion() int     { return 12 }
func (*fixtureProvider) DatasetVersion() string { return "" }
func (provider *fixtureProvider) Lookup(context.Context, string) (domain.DictionarySnapshotData, error) {
	provider.calls++
	return domain.DictionarySnapshotData{
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
	}, nil
}

func TestMCPToolsExposeLookupAndLearningList(t *testing.T) {
	ctx := context.Background()
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
		Examples: []string{"I visited the bank."},
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

	review := callTool[learning.RecordResult](t, ctx, clientSession, "learning_review", LearningReviewInput{
		ReviewToken: next.ReviewToken,
		Rating:      domain.ReviewRatingGood,
		Comment:     "Needed a moment to separate the meanings.",
	})
	if !review.Recorded || review.Duplicate || review.NextReviewAt == "" {
		t.Fatalf("learning review = %#v", review)
	}
	duplicateReview := callTool[learning.RecordResult](t, ctx, clientSession, "learning_review", LearningReviewInput{
		ReviewToken: next.ReviewToken,
		Rating:      domain.ReviewRatingGood,
		Comment:     "Needed a moment to separate the meanings.",
	})
	if !duplicateReview.Duplicate || duplicateReview.NextReviewAt != review.NextReviewAt {
		t.Fatalf("duplicate learning review = %#v, first = %#v", duplicateReview, review)
	}
	withComment := callTool[learning.NextResult](t, ctx, clientSession, "learning_next", LearningNextInput{})
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
	updatedMetadata := callTool[domain.VocabularyItem](t, ctx, clientSession, "vocabulary_update", vocabularyUpdateByTermInput{
		Term: "bank",
		Changes: VocabularyUpdateChanges{
			Status: &status,
			Tags:   &tags,
			Notes:  &notes,
		},
	})
	if updatedMetadata.Status != domain.LearningStatusLearned || len(updatedMetadata.Tags) != 2 {
		t.Fatalf("vocabulary update = %#v", updatedMetadata)
	}
	if updatedMetadata.CustomDescription != description || len(updatedMetadata.Examples) != 1 {
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
