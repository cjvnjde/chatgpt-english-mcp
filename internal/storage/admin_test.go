package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAdminHistorySearchPaginationAndChronology(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "live.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, err = store.sql.Exec(`INSERT INTO vocabulary_items (id, owner_key, term, normalized_term, sense_key, created_at, updated_at) VALUES ('word', 'owner', 'Wringer', 'wringer', 'legacy', '2026-09-09T10:00:00Z', '2026-09-09T10:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	for _, shown := range []string{"2026-09-09T10:00:00Z", "2026-09-09T10:00:00.1Z", "2026-09-10T10:00:00Z"} {
		_, err := store.sql.Exec(`INSERT INTO learning_presentations (owner_key, vocabulary_item_id, learning_card_id, exercise_mode, review_token, shown_at, due_at, selection_kind) VALUES ('owner', 'word', 'word:production', 'production', 'token', ?, ?, 'new')`, shown, shown)
		if err != nil {
			t.Fatal(err)
		}
	}
	query := AdminQuery{Query: "WRINGER", Sort: "shown_at", Direction: "asc", From: "2026-09-09", To: "2026-09-09", Limit: 1}
	page, err := store.AdminRows(ctx, "learning_presentations", query)
	if err != nil || page.Total != 2 || len(page.Rows) != 1 || page.Rows[0]["shown_at"] != "2026-09-09T10:00:00Z" {
		t.Fatalf("first page: %#v %v", page, err)
	}
	query.Offset = 1
	page, err = store.AdminRows(ctx, "learning_presentations", query)
	if err != nil || page.Rows[0]["shown_at"] != "2026-09-09T10:00:00.1Z" {
		t.Fatalf("second page: %#v %v", page, err)
	}
	file, cleanup, err := store.AdminBackup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	path := file.Name()
	cleanup()
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("temporary backup directory not removed: %v", err)
	}
}

func TestAdminDateFilterPreservesNanosecondDayBoundaries(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "live.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, shown := range []string{
		"2026-09-08T23:59:59.999999999Z",
		"2026-09-09T00:00:00Z",
		"2026-09-09T23:59:59.999999999Z",
		"2026-09-10T00:00:00Z",
	} {
		_, err := store.sql.Exec(`INSERT INTO learning_presentations (owner_key, vocabulary_item_id, learning_card_id, exercise_mode, review_token, shown_at, due_at, selection_kind) VALUES ('owner', 'word', 'word:production', 'production', 'token', ?, ?, 'new')`, shown, shown)
		if err != nil {
			t.Fatal(err)
		}
	}
	page, err := store.AdminRows(ctx, "learning_presentations", AdminQuery{
		From: "2026-09-09", To: "2026-09-09", Sort: "shown_at", Direction: "asc", Limit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || len(page.Rows) != 2 ||
		page.Rows[0]["shown_at"] != "2026-09-09T00:00:00Z" ||
		page.Rows[1]["shown_at"] != "2026-09-09T23:59:59.999999999Z" {
		t.Fatalf("UTC day must include exactly midnight through its final nanosecond: %#v", page)
	}
}

func TestAdminAnalyticsActivityExcludesFutureAndOtherOwners(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "live.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	day := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	for _, review := range []struct {
		id, owner, at string
	}{
		{"recent", "owner", day + "T12:00:00Z"},
		{"other", "other-owner", day + "T12:00:00Z"},
		{"old", "owner", "2000-01-01T12:00:00Z"},
		{"future", "owner", "9999-01-01T12:00:00Z"},
	} {
		_, err := store.sql.Exec(`INSERT INTO review_attempts (
			id, owner_key, submission_id, vocabulary_item_id, learning_card_id,
			exercise_mode, rating, reviewed_at, due_before,
			stability_before, difficulty_before, retrievability_before,
			scheduled_days_before, repetitions_before, lapses_before, fsrs_state_before,
			remaining_steps_before, consecutive_failures_before, due_after,
			stability_after, difficulty_after, retrievability_after,
			scheduled_days_after, repetitions_after, lapses_after, fsrs_state_after,
			remaining_steps_after, consecutive_failures_after
		) VALUES (?, ?, ?, 'word', 'word:production', 'production', 'good', ?, ?,
			0, 0, 0, 0, 0, 0, 0, 0, 0, ?, 1, 1, 1, 1, 1, 0, 2, 0, 0)`,
			review.id, review.owner, review.id, review.at, review.at, review.at)
		if err != nil {
			t.Fatal(err)
		}
	}
	analytics, err := store.AdminAnalytics(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	activity := analytics["activity"].([]map[string]any)
	if len(activity) != 1 || activity[0]["day"] != day ||
		activity[0]["reviews"] != int64(1) || activity[0]["recalled"] != int64(1) {
		t.Fatalf("activity must contain only the owner's reviews in the last 30 UTC days: %#v", activity)
	}
}
