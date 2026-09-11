package mcpserver

import (
	"context"
	"testing"

	"english-learning-mcp/internal/domain"
	"english-learning-mcp/internal/vocabulary"
)

func TestLearningNextIdentifiesOverlappingSavedMeanings(t *testing.T) {
	ctx := context.Background()
	session, _ := newTestSession(t, ctx)
	callTool[domain.DictionaryLookupResult](t, ctx, session, "dictionary_lookup", map[string]any{"term": "bank"})
	legacy := callTool[vocabulary.SaveResult](t, ctx, session, "vocabulary_save", map[string]any{"term": "bank"})
	selected := callTool[vocabulary.SaveResult](t, ctx, session, "vocabulary_save", map[string]any{"term": "bank", "definition": "land beside a river"})
	assertToolInvalidArgument(t, ctx, session, "vocabulary_get", map[string]any{"term": "bank"})
	seen := map[string]bool{}
	for range 2 {
		next := callTool[map[string]any](t, ctx, session, "learning_next", map[string]any{})
		id, ok := next["itemId"].(string)
		if !ok || (id != legacy.ItemID && id != selected.ItemID) || seen[id] {
			t.Fatalf("selection cannot identify its exact saved meaning: %#v", next)
		}
		seen[id] = true
		item := callTool[domain.VocabularyItem](t, ctx, session, "vocabulary_get", map[string]any{"itemId": id})
		if item.ItemID != id || next["definition"] != "land beside a river" {
			t.Fatalf("wrong selected item: %#v, %#v", next, item)
		}
		callTool[domain.VocabularyItem](t, ctx, session, "vocabulary_update", map[string]any{"itemId": id, "changes": map[string]any{"notes": []string{id}}})
	}
	for _, id := range []string{legacy.ItemID, selected.ItemID} {
		item := callTool[domain.VocabularyItem](t, ctx, session, "vocabulary_get", map[string]any{"itemId": id})
		if len(item.Notes) != 1 || item.Notes[0] != id {
			t.Fatalf("follow-up changed the wrong meaning: %#v", item)
		}
	}
}

func TestMCPRejectsNULVocabularyWithoutMutation(t *testing.T) {
	ctx := context.Background()
	session, _ := newTestSession(t, ctx)
	for _, fields := range []map[string]any{
		{"term": "before\x00after"}, {"term": "bank", "context": "\x00"},
		{"term": "bank", "notes": []string{"before\x00after"}},
		{"term": "bank", "customDescription": "before\x00after"},
	} {
		assertToolInvalidArgument(t, ctx, session, "vocabulary_save", fields)
	}
	items := callTool[VocabularyListOutput](t, ctx, session, "vocabulary_list", map[string]any{})
	if len(items.Items) != 0 {
		t.Fatalf("invalid text was saved: %#v", items)
	}
}
