package mcpserver

import (
	"context"
	"testing"

	"english-learning-mcp/internal/domain"
	"english-learning-mcp/internal/learning"
	"english-learning-mcp/internal/storage"
	"english-learning-mcp/internal/vocabulary"
)

func TestLearningFocusMCPEarlyRefillCompletionAndResume(t *testing.T) {
	ctx := context.Background()
	session, _ := newTestSession(t, ctx)
	status := callTool[storage.LearningFocus](t, ctx, session, "learning_focus", map[string]any{})
	if status.LearningMode != "mixed" || status.FocusBatchSize != 10 || status.BatchID != "" || status.Items == nil {
		t.Fatalf("initial: %#v", status)
	}
	a := callTool[vocabulary.SaveResult](t, ctx, session, "vocabulary_save", VocabularySaveInput{Term: "alpha"})
	b := callTool[vocabulary.SaveResult](t, ctx, session, "vocabulary_save", VocabularySaveInput{Term: "beta"})
	started := callTool[storage.LearningFocus](t, ctx, session, "learning_focus", LearningFocusInput{Action: "start", ItemIDs: []string{a.ItemID}})
	if started.LearningMode != "focused" || started.BatchID == "" || started.Remaining != 1 {
		t.Fatalf("start: %#v", started)
	}
	next := callTool[learning.NextResult](t, ctx, session, "learning_next", LearningNextInput{})
	if next.ItemID != a.ItemID || next.Focus == nil || next.Focus.BatchID != started.BatchID {
		t.Fatalf("issued: %#v", next)
	}
	callTool[learning.RecordResult](t, ctx, session, "learning_review", LearningReviewInput{ReviewToken: next.ReviewToken, Rating: domain.ReviewRatingEasy})
	early := callTool[learning.NextResult](t, ctx, session, "learning_next", LearningNextInput{IncludeComments: true})
	if early.Reason != "early" || early.ItemID != a.ItemID || early.ReviewToken == "" || early.Focus == nil || early.PresentationID == next.PresentationID {
		t.Fatalf("early: %#v", early)
	}
	stopped := callTool[storage.LearningFocus](t, ctx, session, "learning_focus", LearningFocusInput{Action: "stop"})
	if stopped.LearningMode != "mixed" || stopped.BatchID != started.BatchID {
		t.Fatalf("stop: %#v", stopped)
	}
	resumed := callTool[storage.LearningFocus](t, ctx, session, "learning_focus", LearningFocusInput{Action: "start"})
	if resumed.BatchID != started.BatchID {
		t.Fatalf("resume: %#v", resumed)
	}
	learned := domain.LearningStatusLearned
	callTool[domain.VocabularyItem](t, ctx, session, "vocabulary_update", VocabularyUpdateInput{ItemID: a.ItemID, Changes: VocabularyUpdateChanges{Status: &learned}})
	refill := callTool[learning.NextResult](t, ctx, session, "learning_next", LearningNextInput{})
	if refill.ItemID != b.ItemID || refill.Focus.BatchID != started.BatchID || refill.Focus.Total != 1 {
		t.Fatalf("refill: %#v", refill)
	}
	callTool[domain.VocabularyItem](t, ctx, session, "vocabulary_update", VocabularyUpdateInput{ItemID: b.ItemID, Changes: VocabularyUpdateChanges{Status: &learned}})
	complete := callTool[map[string]any](t, ctx, session, "learning_next", LearningNextInput{})
	if complete["reason"] != "complete" || complete["reviewToken"] != nil || complete["nextDueAt"] != nil {
		t.Fatalf("complete: %#v", complete)
	}
}

func TestLearningFocusMCPRejectsInvalidSelection(t *testing.T) {
	ctx := context.Background()
	session, _ := newTestSession(t, ctx)
	saved := callTool[vocabulary.SaveResult](t, ctx, session, "vocabulary_save", VocabularySaveInput{Term: "alpha"})
	for _, input := range []map[string]any{
		{"action": "random"}, {"action": "start", "itemIds": []string{}},
		{"action": "start", "itemIds": []string{saved.ItemID, saved.ItemID}},
		{"action": "start", "itemIds": []string{"missing"}},
		{"action": "status", "itemIds": []string{saved.ItemID}},
		{"action": "stop", "itemIds": []string{saved.ItemID}},
		{"action": "start", "unexpected": true},
	} {
		assertToolInvalidArgument(t, ctx, session, "learning_focus", input)
	}
	status := callTool[storage.LearningFocus](t, ctx, session, "learning_focus", LearningFocusInput{})
	if status.LearningMode != "mixed" || status.BatchID != "" {
		t.Fatalf("invalid inputs mutated state: %#v", status)
	}
}
