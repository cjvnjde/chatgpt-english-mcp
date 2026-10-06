package storage

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"english-learning-mcp/internal/apperr"
	"english-learning-mcp/internal/domain"
	"english-learning-mcp/internal/settings"
)

func focusSettings(t *testing.T, store *DB, size int) {
	t.Helper()
	snapshot, err := store.AlgorithmSettings(context.Background(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Values.LearningMode, snapshot.Values.FocusBatchSize = "focused", size
	if _, err := store.UpdateAlgorithmSettings(context.Background(), "owner", snapshot.Values, snapshot.Revision); err != nil {
		t.Fatal(err)
	}
}

func focusStatus(t *testing.T, store *DB, now time.Time) LearningFocus {
	t.Helper()
	focus, err := store.LearningFocus(context.Background(), "owner", "status", nil, clockAt(now))
	if err != nil {
		t.Fatal(err)
	}
	return focus
}

func TestFocusRollingPoolPersistsAndUsesEarlySelection(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "focus.sqlite")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	now := time.Now().UTC()
	a := savePresentationVocabulary(t, store, "owner", "alpha", now.Add(-3*time.Hour))
	b := savePresentationVocabulary(t, store, "owner", "beta", now.Add(-2*time.Hour))
	c := savePresentationVocabulary(t, store, "owner", "gamma", now.Add(-time.Hour))
	focusSettings(t, store, 2)
	focus, err := store.LearningFocus(ctx, "owner", "start", []string{a.ItemID, b.ItemID}, clockAt(now))
	if err != nil || focus.Total != 2 || focus.Due != 2 {
		t.Fatalf("start: %#v %v", focus, err)
	}
	for range 8 {
		selected, err := store.NextLearningItem(ctx, "owner", clockAt(now))
		if err != nil || (selected.Vocabulary.ItemID != a.ItemID && selected.Vocabulary.ItemID != b.ItemID) {
			t.Fatalf("escaped focus: %#v %v", selected, err)
		}
	}
	// A learned member is replaced immediately on the next selection, while
	// the unfinished member keeps its schedule and membership.
	if _, err := store.sql.Exec("UPDATE vocabulary_items SET learning_status = 'learned' WHERE id = ?", a.ItemID); err != nil {
		t.Fatal(err)
	}
	bReview := lifecycleReview(t, store, b.ItemID, now, domain.ReviewRatingGood)
	saveReinforcementVocabulary(t, store, "maintenance", now)
	focusSettings(t, store, 1) // the existing pool retains its capacity
	page, err := store.AdminSuggestions(ctx, "owner", 100, 0)
	if err != nil || page.Selectable != 1 || page.Rows[0].VocabularyItemID != c.ItemID || page.Focus.BatchID != focus.BatchID || page.Focus.Total != 2 {
		t.Fatalf("refill preview: %#v %v", page, err)
	}
	if got := focusStatus(t, store, now); got.Learned != 1 || got.Items[0].ItemID != a.ItemID {
		t.Fatalf("preview mutated membership: %#v", got)
	}
	next, err := store.NextLearningItem(ctx, "owner", clockAt(now))
	if err != nil || next.Vocabulary.ItemID != c.ItemID || next.Focus.BatchID != focus.BatchID || next.Focus.Remaining != 2 || next.Focus.Learned != 0 {
		t.Fatalf("refill: %#v %v", next, err)
	}
	cReview := lifecycleReview(t, store, c.ItemID, now, domain.ReviewRatingGood)
	// No due members: ordinary early selection keeps practice available.
	var before int
	if err := store.sql.QueryRow("SELECT count(*) FROM learning_presentations").Scan(&before); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		early, err := store.NextLearningItem(ctx, "owner", clockAt(now))
		if err != nil || early.IdleReason != "" || early.Card.ReviewToken == "" || early.Focus.Due != 0 || early.Focus.BatchID != focus.BatchID ||
			(early.Vocabulary.ItemID != b.ItemID && early.Vocabulary.ItemID != c.ItemID) {
			t.Fatalf("early: %#v %v", early, err)
		}
	}
	var after int
	if err := store.sql.QueryRow("SELECT count(*) FROM learning_presentations").Scan(&after); err != nil || after != before+3 {
		t.Fatalf("early presentations: %d %d %v", before, after, err)
	}
	if !reflect.DeepEqual(lifecycleCard(t, store, b.ItemID), bReview.After) || !reflect.DeepEqual(lifecycleCard(t, store, c.ItemID), cReview.After) {
		t.Fatal("selection changed scheduling or mastery")
	}
	page, err = store.AdminSuggestions(ctx, "owner", 100, 0)
	if err != nil || page.Selectable != 1 || page.Rows[0].Reason != "early" {
		t.Fatalf("early preview: %#v %v", page, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if got := focusStatus(t, store, now); got.BatchID != focus.BatchID || got.Remaining != 2 || got.targetSize != 2 || got.Items[0].ItemID != b.ItemID || got.Items[1].ItemID != c.ItemID {
		t.Fatalf("reopen: %#v", got)
	}
	for _, id := range []string{b.ItemID, c.ItemID} {
		if _, err := store.sql.Exec("UPDATE vocabulary_items SET learning_status = 'learned' WHERE id = ?", id); err != nil {
			t.Fatal(err)
		}
	}
	complete, err := store.NextLearningItem(ctx, "owner", clockAt(now))
	if err != nil || complete.IdleReason != "complete" || complete.Focus.Remaining != 0 || complete.Card.ReviewToken != "" {
		t.Fatalf("complete: %#v %v", complete, err)
	}
}

func TestFocusAutomaticSelectionPreviewAndConcurrentStart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "concurrent.sqlite")
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	now := time.Now().UTC()
	savePresentationVocabulary(t, first, "owner", "newest", now)
	want := savePresentationVocabulary(t, first, "owner", "started", now)
	if _, err := first.sql.Exec("UPDATE vocabulary_items SET learning_status = 'learning', personal_interest = 'low' WHERE id = ?", want.ItemID); err != nil {
		t.Fatal(err)
	}
	savePresentationVocabulary(t, first, "other", "other-owner", now)
	focusSettings(t, first, 1)
	page, err := first.AdminSuggestions(ctx, "owner", 100, 0)
	if err != nil || page.Selectable != 1 || page.Rows[0].VocabularyItemID != want.ItemID || page.Focus.BatchID != "" {
		t.Fatalf("automatic preview: %#v %v", page, err)
	}
	if focusStatus(t, first, now).BatchID != "" {
		t.Fatal("read-only preview saved a batch")
	}
	var group sync.WaitGroup
	results := make(chan LearningCandidate, 2)
	errors := make(chan error, 2)
	for _, store := range []*DB{first, second} {
		group.Add(1)
		go func(store *DB) {
			defer group.Done()
			candidate, err := store.NextLearningItem(ctx, "owner", clockAt(now))
			results <- candidate
			errors <- err
		}(store)
	}
	group.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	batch := ""
	for result := range results {
		if result.Vocabulary.ItemID != want.ItemID || result.Focus == nil {
			t.Fatalf("selected wrong member: %#v", result)
		}
		if batch != "" && batch != result.Focus.BatchID {
			t.Fatal("concurrent calls created different batches")
		}
		batch = result.Focus.BatchID
	}
	other, err := first.LearningFocus(ctx, "other", "status", nil, clockAt(now))
	if err != nil || other.BatchID != "" || other.LearningMode != "mixed" {
		t.Fatalf("owner leakage: %#v %v", other, err)
	}
}

func TestFocusControlsValidateAtomicallyAndPreserveSchedule(t *testing.T) {
	store, now := reinforcementTestStore(t)
	ctx := context.Background()
	a := savePresentationVocabulary(t, store, "owner", "alpha", now)
	b := savePresentationVocabulary(t, store, "owner", "beta", now)
	other := savePresentationVocabulary(t, store, "other", "other", now)
	learned := saveReinforcementVocabulary(t, store, "learned", now)
	before := lifecycleCard(t, store, a.ItemID)
	focus, err := store.LearningFocus(ctx, "owner", "start", []string{a.ItemID}, clockAt(now))
	if err != nil || focus.LearningMode != "focused" {
		t.Fatalf("start: %#v %v", focus, err)
	}
	snapshot, err := store.AlgorithmSettings(ctx, "owner")
	if err != nil || snapshot.Revision != 1 {
		t.Fatalf("settings: %#v %v", snapshot, err)
	}
	for _, input := range []struct {
		action string
		ids    []string
	}{
		{"unknown", nil}, {"status", []string{a.ItemID}}, {"stop", []string{a.ItemID}}, {"start", []string{}},
		{"start", []string{a.ItemID, a.ItemID}}, {"start", []string{other.ItemID}}, {"start", []string{learned.ItemID}}, {"start", []string{"missing"}},
	} {
		_, err := store.LearningFocus(ctx, "owner", input.action, input.ids, clockAt(now))
		if apperr.From(err) == nil || apperr.From(err).Code != apperr.InvalidArgument {
			t.Fatalf("accepted invalid input %#v: %v", input, err)
		}
		if got := focusStatus(t, store, now); got.BatchID != focus.BatchID {
			t.Fatal("invalid request changed batch")
		}
	}
	focusSettings(t, store, 1)
	if _, err := store.LearningFocus(ctx, "owner", "start", []string{a.ItemID, b.ItemID}, clockAt(now)); apperr.From(err) == nil || apperr.From(err).Code != apperr.InvalidArgument {
		t.Fatalf("oversized batch: %v", err)
	}
	stopped, err := store.LearningFocus(ctx, "owner", "stop", nil, clockAt(now))
	if err != nil || stopped.LearningMode != "mixed" || stopped.BatchID != focus.BatchID {
		t.Fatalf("stop: %#v %v", stopped, err)
	}
	resumed, err := store.LearningFocus(ctx, "owner", "start", nil, clockAt(now))
	if err != nil || resumed.BatchID != focus.BatchID || resumed.LearningMode != "focused" {
		t.Fatalf("resume: %#v %v", resumed, err)
	}
	retried, err := store.LearningFocus(ctx, "owner", "start", []string{a.ItemID}, clockAt(now))
	if err != nil || retried.BatchID != focus.BatchID {
		t.Fatalf("retry replaced batch: %#v %v", retried, err)
	}
	if after := lifecycleCard(t, store, a.ItemID); !reflect.DeepEqual(before, after) {
		t.Fatal("focus controls modified schedule")
	}
	replaced, err := store.LearningFocus(ctx, "owner", "start", []string{b.ItemID}, clockAt(now))
	if err != nil || replaced.BatchID == focus.BatchID || replaced.Items[0].ItemID != b.ItemID {
		t.Fatalf("replace: %#v %v", replaced, err)
	}
	if _, err := store.UpdateAlgorithmSettings(ctx, "owner", snapshot.Values, snapshot.Revision); apperr.From(err) == nil || apperr.From(err).Code != apperr.Conflict {
		t.Fatalf("stale settings accepted: %v", err)
	}
}

func TestFocusMasteryCorrectionsAndRemoval(t *testing.T) {
	store, now := reinforcementTestStore(t)
	ctx := context.Background()
	a := savePresentationVocabulary(t, store, "owner", "alpha", now)
	b := savePresentationVocabulary(t, store, "owner", "beta", now)
	c := savePresentationVocabulary(t, store, "owner", "gamma", now)
	focus, err := store.LearningFocus(ctx, "owner", "start", []string{a.ItemID, b.ItemID}, clockAt(now))
	if err != nil {
		t.Fatal(err)
	}
	var last ReviewAttempt
	for day := range 5 {
		last = lifecycleReview(t, store, a.ItemID, now.Add(time.Duration(day)*24*time.Hour), domain.ReviewRatingEasy)
	}
	if got := focusStatus(t, store, now); got.Learned != 1 || got.Remaining != 1 {
		t.Fatalf("mastery not reflected: %#v", got)
	}
	_, _, err = store.UpdateLatestReview(ctx, UpdateReviewInput{OwnerKey: "owner", ReviewToken: last.ReviewToken, Rating: domain.ReviewRatingHard, Now: clockAt(now)}, lifecycleSchedule)
	if err != nil {
		t.Fatal(err)
	}
	if got := focusStatus(t, store, now); got.BatchID != focus.BatchID || got.Learned != 0 || got.Remaining != 2 {
		t.Fatalf("correction lost membership: %#v", got)
	}
	archived := domain.LearningStatusArchived
	if _, err := store.UpdateVocabulary(ctx, VocabularyUpdate{OwnerKey: "owner", ItemID: a.ItemID, Status: &archived, Now: now}); err != nil {
		t.Fatal(err)
	}
	if got := focusStatus(t, store, now); got.Total != 1 || got.Items[0].ItemID != b.ItemID {
		t.Fatalf("archive did not remove member: %#v", got)
	}
	if err := store.DeleteVocabulary(ctx, "owner", b.ItemID); err != nil {
		t.Fatal(err)
	}
	next, err := store.NextLearningItem(ctx, "owner", clockAt(now))
	if err != nil || next.Vocabulary.ItemID != c.ItemID || next.Focus.BatchID != focus.BatchID {
		t.Fatalf("deleted pool not refilled: %#v %v", next, err)
	}
}

func TestFocusMigrationPreservesExistingSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.sqlite")
	legacy := openLegacyDatabase(t, path, 21)
	values := settings.Defaults()
	values.MasteryDays = 8
	encoded, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	var old map[string]any
	if err := json.Unmarshal(encoded, &old); err != nil {
		t.Fatal(err)
	}
	delete(old, "learningMode")
	delete(old, "focusBatchSize")
	encoded, err = json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec("INSERT INTO algorithm_settings VALUES ('owner', ?, 7)", string(encoded)); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snapshot, err := store.AlgorithmSettings(context.Background(), "owner")
	if err != nil || snapshot.Revision != 7 || !reflect.DeepEqual(snapshot.Values, values) {
		t.Fatalf("migrated settings: %#v %v", snapshot, err)
	}
}

func TestRollingFocusMigrationPreservesMembershipCapacityAndSchedule(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "batch.sqlite")
	legacy := openLegacyDatabase(t, path, 22)
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	values := settings.Defaults()
	values.LearningMode, values.FocusBatchSize = "focused", 7
	encoded, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	for _, owner := range []string{"owner", "empty"} {
		if _, err := legacy.Exec("INSERT INTO algorithm_settings VALUES (?, ?, 5)", owner, string(encoded)); err != nil {
			t.Fatal(err)
		}
	}
	for _, owner := range []string{"owner", "empty", "default"} {
		if _, err := legacy.Exec("INSERT INTO learning_focus_batches VALUES (?, ?, ?)", owner, owner+"-pool", TimeString(now)); err != nil {
			t.Fatal(err)
		}
	}
	for index, status := range []string{"learned", "learning"} {
		id := status + "-item"
		if _, err := legacy.Exec(`INSERT INTO vocabulary_items
			(id, owner_key, term, normalized_term, sense_key, learning_status, created_at, updated_at)
			VALUES (?, 'owner', ?, ?, 'custom', ?, ?, ?)`, id, status, status, status, TimeString(now), TimeString(now)); err != nil {
			t.Fatal(err)
		}
		if _, err := legacy.Exec("INSERT INTO learning_focus_items VALUES ('owner', ?, ?)", id, index); err != nil {
			t.Fatal(err)
		}
	}
	before := lifecycleCard(t, &DB{sql: legacy}, "learning-item")
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for owner, capacity := range map[string]int{"owner": 2, "empty": 7, "default": 10} {
		focus, err := store.LearningFocus(ctx, owner, "status", nil, clockAt(now))
		if err != nil || focus.targetSize != capacity || focus.BatchID != owner+"-pool" || focus.CreatedAt != TimeString(now) {
			t.Fatalf("migration for %s: %#v %v", owner, focus, err)
		}
	}
	if focus := focusStatus(t, store, now); focus.Total != 2 || focus.Learned != 1 || focus.Remaining != 1 {
		t.Fatalf("migration changed membership: %#v", focus)
	}
	if after := lifecycleCard(t, store, "learning-item"); !reflect.DeepEqual(before, after) {
		t.Fatal("migration changed schedule")
	}
	if snapshot, err := store.AlgorithmSettings(ctx, "owner"); err != nil || snapshot.Revision != 5 || !reflect.DeepEqual(snapshot.Values, values) {
		t.Fatalf("migration changed settings: %#v %v", snapshot, err)
	}
	added := savePresentationVocabulary(t, store, "owner", "replacement", now)
	resumed, err := store.LearningFocus(ctx, "owner", "start", nil, clockAt(now))
	if err != nil || resumed.BatchID != "owner-pool" || resumed.Total != 2 || resumed.Learned != 0 || resumed.Items[0].ItemID != "learning-item" || resumed.Items[1].ItemID != added.ItemID {
		t.Fatalf("migrated pool did not refill individually: %#v %v", resumed, err)
	}
}

func TestFocusSparsePoolCapacityAndExplicitSelection(t *testing.T) {
	store, now := reinforcementTestStore(t)
	ctx := context.Background()
	a := savePresentationVocabulary(t, store, "owner", "alpha", now)
	focusSettings(t, store, 3)
	initial, err := store.LearningFocus(ctx, "owner", "start", nil, clockAt(now))
	if err != nil || initial.Total != 1 || initial.targetSize != 3 {
		t.Fatalf("sparse automatic pool: %#v %v", initial, err)
	}
	// Choosing the same IDs explicitly still changes the intended capacity.
	explicit, err := store.LearningFocus(ctx, "owner", "start", []string{a.ItemID}, clockAt(now))
	if err != nil || explicit.targetSize != 1 {
		t.Fatalf("explicit capacity: %#v %v", explicit, err)
	}
	b := savePresentationVocabulary(t, store, "owner", "beta", now)
	resumed, err := store.LearningFocus(ctx, "owner", "start", nil, clockAt(now))
	if err != nil || resumed.Total != 1 || resumed.Items[0].ItemID != a.ItemID {
		t.Fatalf("new vocabulary displaced unfinished member: %#v %v", resumed, err)
	}
	if err := store.DeleteVocabulary(ctx, "owner", a.ItemID); err != nil {
		t.Fatal(err)
	}
	next, err := store.NextLearningItem(ctx, "owner", clockAt(now))
	if err != nil || next.Vocabulary.ItemID != b.ItemID || next.Focus.Total != 1 || next.Focus.BatchID != explicit.BatchID {
		t.Fatalf("deleted slot not refilled: %#v %v", next, err)
	}
}
