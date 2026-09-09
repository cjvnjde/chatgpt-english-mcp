package storage

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	"english-learning-mcp/internal/domain"
)

func TestSuggestionLikelihoodMatchesSelectionBoundaries(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	type draw struct {
		values []float64
		want   string
	}
	for _, test := range []struct {
		name          string
		cards         []selectionCard
		probabilities []float64
		reasons       []string
		draws         []draw
	}{
		{
			name: "mixed usefulness and review urgency",
			cards: []selectionCard{
				{cardID: "low", usefulness: domain.UsefulnessLow, dueAt: now.Add(time.Hour)},
				{cardID: "high", usefulness: domain.UsefulnessHigh, dueAt: now},
				{cardID: "review", fsrsState: 2, dueAt: now},
				{cardID: "overdue", fsrsState: 2, dueAt: now.Add(-24 * time.Hour)},
				{cardID: "future", fsrsState: 2, dueAt: now.Add(time.Hour)},
			},
			probabilities: []float64{0.04, 0.16, 0.8 / 3, 1.6 / 3, 0},
			reasons:       []string{"new", "new", "due", "due", "not_due"},
			draws: []draw{
				{[]float64{0, 0}, "low"},
				{[]float64{math.Nextafter(0.2, 0), math.Nextafter(0.2, 0)}, "low"},
				{[]float64{0, 0.2}, "high"},
				{[]float64{0.2, 0}, "review"},
				{[]float64{0.99, 1.0 / 3}, "overdue"},
				{[]float64{0.99, math.Nextafter(1, 0)}, "overdue"},
			},
		},
		{
			name: "learning steps precede mixed pools",
			cards: []selectionCard{
				{cardID: "new", dueAt: now},
				{cardID: "review", fsrsState: 2, dueAt: now},
				{cardID: "learning", fsrsState: 1, dueAt: now},
				{cardID: "relearning", fsrsState: 3, dueAt: now.Add(-10 * time.Minute)},
			},
			probabilities: []float64{0, 0, 1.0 / 3, 2.0 / 3},
			reasons:       []string{"learning_first", "learning_first", "due", "due"},
			draws:         []draw{{[]float64{0}, "learning"}, {[]float64{1.0 / 3}, "relearning"}},
		},
		{
			name: "cooldown applied before learning precedence",
			cards: []selectionCard{
				{cardID: "step", fsrsState: 1, dueAt: now, lastPresentationID: 3, lastShownAt: now},
				{cardID: "new", dueAt: now},
				{cardID: "review", fsrsState: 2, dueAt: now},
			},
			probabilities: []float64{0, 0.2, 0.8},
			reasons:       []string{"cooldown", "new", "due"},
			draws:         []draw{{[]float64{0, 0}, "new"}, {[]float64{0.2, 0}, "review"}},
		},
		{
			name: "small pool relaxes oldest two without immediate repeat",
			cards: []selectionCard{
				{cardID: "first", dueAt: now, lastPresentationID: 1, lastShownAt: now},
				{cardID: "second", dueAt: now, lastPresentationID: 2, lastShownAt: now},
				{cardID: "last", dueAt: now, lastPresentationID: 3, lastShownAt: now},
			},
			probabilities: []float64{0.5, 0.5, 0},
			reasons:       []string{"new", "new", "cooldown"},
			draws:         []draw{{[]float64{math.Nextafter(0.5, 0)}, "first"}, {[]float64{0.5}, "second"}},
		},
		{
			name: "future fallback excludes cooldown before due ordering",
			cards: []selectionCard{
				{cardID: "recent", fsrsState: 2, dueAt: now.Add(time.Minute), lastPresentationID: 3, lastShownAt: now},
				{cardID: "far", fsrsState: 2, dueAt: now.Add(time.Hour)},
				{cardID: "near", fsrsState: 1, dueAt: now.Add(2 * time.Minute)},
			},
			probabilities: []float64{0, 0, 1},
			reasons:       []string{"cooldown", "waiting", "early"},
			draws:         []draw{{nil, "near"}},
		},
		{
			name: "all future recent relaxes cooldown deterministically",
			cards: []selectionCard{
				{cardID: "near", fsrsState: 2, dueAt: now.Add(time.Minute), lastPresentationID: 3, lastShownAt: now},
				{cardID: "oldest", fsrsState: 2, dueAt: now.Add(time.Hour), lastPresentationID: 1, lastShownAt: now},
			},
			probabilities: []float64{0, 1},
			reasons:       []string{"waiting", "early"},
			draws:         []draw{{nil, "oldest"}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := planLearningSelection(test.cards, 1, now)
			var total float64
			for index := range test.cards {
				probability, reason := plan.likelihood(&test.cards[index])
				if math.Abs(probability-test.probabilities[index]) > 1e-12 || reason != test.reasons[index] {
					t.Fatalf("%s: probability=%g reason=%s, want %g %s", test.cards[index].cardID,
						probability, reason, test.probabilities[index], test.reasons[index])
				}
				total += probability
			}
			if math.Abs(total-1) > 1e-12 {
				t.Fatalf("next draw probability totals %g", total)
			}
			for _, draw := range test.draws {
				calls := 0
				selected, ok := selectLearningCard(test.cards, 1, now, func() float64 {
					if calls >= len(draw.values) {
						t.Fatal("selector consumed an extra random draw")
					}
					value := draw.values[calls]
					calls++
					return value
				})
				if !ok || selected.cardID != draw.want || calls != len(draw.values) {
					t.Fatalf("draw %v selected %s (%d calls), want %s (%d calls)", draw.values,
						selected.cardID, calls, draw.want, len(draw.values))
				}
			}
		})
	}
}

func TestAdminSuggestionsPaginationIsolationAndReadOnly(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	for _, fixture := range []struct {
		id, owner  string
		status     domain.LearningStatus
		usefulness domain.Usefulness
	}{
		{"a", "owner", domain.LearningStatusNew, domain.UsefulnessNormal},
		{"b", "owner", domain.LearningStatusNew, domain.UsefulnessNormal},
		{"c", "owner", domain.LearningStatusNew, domain.UsefulnessHigh},
		{"future", "owner", domain.LearningStatusLearning, domain.UsefulnessNormal},
		{"archived", "owner", domain.LearningStatusArchived, domain.UsefulnessHigh},
		{"foreign", "other", domain.LearningStatusNew, domain.UsefulnessHigh},
	} {
		term := "fixture suggestion " + fixture.id
		_, item, err := store.SaveVocabulary(ctx, VocabularyCreate{
			OwnerKey: fixture.owner, Term: term, NormalizedTerm: term,
			Context: "context " + fixture.id, Status: fixture.status, Usefulness: fixture.usefulness, Now: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.sql.ExecContext(ctx, `UPDATE learning_cards SET id = ? WHERE vocabulary_item_id = ?`, fixture.id, item.ItemID); err != nil {
			t.Fatal(err)
		}
		// Even an archived item with a stale card must not enter the preview.
		if fixture.status == domain.LearningStatusArchived {
			if _, err := store.sql.ExecContext(ctx, `INSERT OR IGNORE INTO learning_cards
				(id, vocabulary_item_id, exercise_mode, due_at, created_at, updated_at) VALUES (?, ?, 'production', ?, ?, ?)`,
				fixture.id, item.ItemID, TimeString(now), TimeString(now), TimeString(now)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := store.sql.ExecContext(ctx, `UPDATE learning_cards SET fsrs_state = 2, due_at = '9999-01-01T00:00:00Z' WHERE id = 'future'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.sql.ExecContext(ctx, `INSERT INTO learning_cards
		(id, vocabulary_item_id, exercise_mode, due_at, created_at, updated_at)
		SELECT 'recognition', vocabulary_item_id, 'recognition', due_at, created_at, updated_at FROM learning_cards WHERE id = 'a'`); err != nil {
		t.Fatal(err)
	}
	shownAt := TimeString(now.Add(-24 * time.Hour))
	if _, err := store.sql.ExecContext(ctx, `INSERT INTO learning_presentations
		(owner_key, vocabulary_item_id, learning_card_id, exercise_mode, review_token, shown_at, due_at, selection_kind)
		SELECT 'owner', vocabulary_item_id, id, 'production', review_token, ?, due_at, 'new'
		FROM learning_cards WHERE id = 'a'`, shownAt); err != nil {
		t.Fatal(err)
	}
	// Recent presentations from another owner or exercise must not cool down a.
	for _, history := range []struct{ owner, mode, cardID string }{{"other", "production", "a"}, {"owner", "recognition", "recognition"}} {
		if _, err := store.sql.ExecContext(ctx, `INSERT INTO learning_presentations
			(owner_key, vocabulary_item_id, learning_card_id, exercise_mode, review_token, shown_at, due_at, selection_kind)
			VALUES (?, 'unrelated', ?, ?, 'historical-token', ?, ?, 'new')`,
			history.owner, history.cardID, history.mode, TimeString(now), TimeString(now)); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := func() map[string][]map[string]any {
		t.Helper()
		state := make(map[string][]map[string]any)
		for _, table := range []string{"learning_cards", "learning_presentations", "review_attempts", "vocabulary_items"} {
			rows, err := store.sql.QueryContext(ctx, "SELECT * FROM "+adminIdentifier(table)+" ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			state[table], err = adminScan(rows)
			if err != nil {
				t.Fatal(err)
			}
		}
		return state
	}
	before := snapshot()
	for offset, want := range []string{"c", "a", "b", "future", ""} {
		page, err := store.AdminSuggestions(ctx, "owner", 1, offset)
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != 4 || page.Selectable != 3 || page.Offset != offset || page.Limit != 1 || page.Rows == nil {
			t.Fatalf("page %d: %#v", offset, page)
		}
		if want == "" {
			if len(page.Rows) != 0 {
				t.Fatalf("past-end page: %#v", page.Rows)
			}
			continue
		}
		if len(page.Rows) != 1 {
			t.Fatalf("page %d: %#v", offset, page.Rows)
		}
		row := page.Rows[0]
		probability := map[string]float64{"a": 0.25, "b": 0.25, "c": 0.5, "future": 0}[want]
		wantShownAt := ""
		if want == "a" {
			wantShownAt = shownAt
		}
		if row.CardID != want || row.Term != "fixture suggestion "+want || row.Context != "context "+want ||
			row.Probability != probability || row.VocabularyItemID == "" || row.LastShownAt != wantShownAt {
			t.Fatalf("page %d: %#v", offset, page.Rows)
		}
	}
	for _, owner := range []string{"other", "missing"} {
		page, err := store.AdminSuggestions(ctx, owner, 200, 0)
		if err != nil {
			t.Fatal(err)
		}
		if owner == "other" && (page.Total != 1 || len(page.Rows) != 1 || page.Rows[0].CardID != "foreign" || page.Rows[0].Probability != 1) {
			t.Fatalf("foreign owner page: %#v", page)
		}
		if owner == "missing" && (page.Total != 0 || page.Selectable != 0 || page.Rows == nil || len(page.Rows) != 0) {
			t.Fatalf("empty owner page: %#v", page)
		}
	}
	if after := snapshot(); !reflect.DeepEqual(before, after) {
		t.Fatal("preview changed cards, tokens, presentations, reviews, or vocabulary")
	}
	for _, query := range [][2]int{{0, 0}, {201, 0}, {1, -1}} {
		t.Run(fmt.Sprint(query), func(t *testing.T) {
			if _, err := store.AdminSuggestions(ctx, "owner", query[0], query[1]); err != ErrAdminQuery {
				t.Fatalf("invalid pagination: %v", err)
			}
		})
	}
}
