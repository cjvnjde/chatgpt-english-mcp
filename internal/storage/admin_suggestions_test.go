package storage

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	"english-learning-mcp/internal/domain"
	"english-learning-mcp/internal/settings"
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
			probabilities: []float64{0.1, 0.4, 1.0 / 6, 1.0 / 3, 0},
			reasons:       []string{"new", "new", "due", "due", "not_due"},
			draws: []draw{
				{[]float64{0, 0}, "low"},
				{[]float64{math.Nextafter(0.5, 0), math.Nextafter(0.2, 0)}, "low"},
				{[]float64{0, 0.2}, "high"},
				{[]float64{0.5, 0}, "review"},
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
			probabilities: []float64{0, 0.5, 0.5},
			reasons:       []string{"cooldown", "new", "due"},
			draws:         []draw{{[]float64{0, 0}, "new"}, {[]float64{0.5, 0}, "review"}},
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
			plan := planLearningSelection(test.cards, 1, now, settings.Defaults())
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
				}, settings.Defaults())
				if !ok || selected.cardID != draw.want || calls != len(draw.values) {
					t.Fatalf("draw %v selected %s (%d calls), want %s (%d calls)", draw.values,
						selected.cardID, calls, draw.want, len(draw.values))
				}
			}
		})
	}
}

func TestLearnedSelectionWorkloadSharesAndReachability(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name            string
		active, learned int
		want            float64
	}{
		{"baseline", 1, 1, 0.10},
		{"minimum share", 10, 1, 0.10},
		{"growing backlog", 1, 3, 0.25},
		{"capped backlog", 1, 20, 0.40},
		{"learned only", 0, 5, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			cards := make([]selectionCard, 0, test.active+test.learned)
			for index := range test.active + test.learned {
				card := selectionCard{cardID: fmt.Sprint(index), fsrsState: 2, dueAt: now}
				if index >= test.active {
					card.learningStatus = domain.LearningStatusLearned
				}
				cards = append(cards, card)
			}
			plan := planLearningSelection(cards, 1, now, settings.Defaults())
			var learnedTotal, total float64
			for index := range cards {
				probability, _ := plan.likelihood(&cards[index])
				total += probability
				if index < test.active {
					continue
				}
				learnedTotal += probability
				if probability <= 0 {
					t.Fatalf("learned card %s unreachable", cards[index].cardID)
				}
				draws := []float64{(float64(index-test.active) + 0.5) / float64(test.learned)}
				if test.active > 0 {
					draws = append([]float64{1 - test.want/2}, draws...)
				}
				call := 0
				selected, ok := selectLearningCard(cards, 1, now, func() float64 {
					value := draws[call]
					call++
					return value
				}, settings.Defaults())
				if !ok || selected.cardID != cards[index].cardID {
					t.Fatalf("learned draw selected %s, want %s", selected.cardID, cards[index].cardID)
				}
			}
			if math.Abs(learnedTotal-test.want) > 1e-12 || math.Abs(total-1) > 1e-12 {
				t.Fatalf("learned=%g total=%g, want %g and 1", learnedTotal, total, test.want)
			}
		})
	}
}

func TestLearnedSelectionWeightsBacklogAndPreservesActiveMix(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	cards := []selectionCard{
		{cardID: "new", dueAt: now, usefulness: domain.UsefulnessHigh},
		{cardID: "review", fsrsState: 2, dueAt: now},
		{cardID: "learned", learningStatus: domain.LearningStatusLearned, fsrsState: 2, dueAt: now},
		{cardID: "struggling", learningStatus: domain.LearningStatusLearned, fsrsState: 2,
			dueAt: now.Add(-24 * time.Hour), consecutiveFailures: 2, lapses: 2, personalInterest: domain.PersonalInterestHigh},
	}
	// Active weight is 8+4; learned weight is 4+(2 urgency * 2.5 failures * 4 exposure * 2 interest).
	// The remaining active share still splits equally by exposure, not by usefulness.
	learnedShare := 44.0 / (9*12 + 44)
	want := []float64{(1 - learnedShare) / 2, (1 - learnedShare) / 2, learnedShare / 11, learnedShare * 10 / 11}
	plan := planLearningSelection(cards, 1, now, settings.Defaults())
	for index := range cards {
		got, _ := plan.likelihood(&cards[index])
		if math.Abs(got-want[index]) > 1e-12 {
			t.Fatalf("%s probability=%g, want %g", cards[index].cardID, got, want[index])
		}
	}
}

func TestLearnedSelectionEligibilityAndFallback(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name  string
		cards []selectionCard
		want  []float64
	}{
		{"manually learned new card", []selectionCard{
			{cardID: "new", dueAt: now},
			{cardID: "learned", learningStatus: domain.LearningStatusLearned, dueAt: now},
		}, []float64{0.9, 0.1}},
		{"future learned excluded by new", []selectionCard{
			{cardID: "new", dueAt: now},
			{cardID: "learned", learningStatus: domain.LearningStatusLearned, fsrsState: 2, dueAt: now.Add(time.Hour)},
		}, []float64{1, 0}},
		{"future learned excluded by due", []selectionCard{
			{cardID: "due", fsrsState: 2, dueAt: now},
			{cardID: "learned", learningStatus: domain.LearningStatusLearned, fsrsState: 2, dueAt: now.Add(time.Hour)},
		}, []float64{1, 0}},
		{"steps first", []selectionCard{
			{cardID: "step", fsrsState: 3, dueAt: now},
			{cardID: "learned", learningStatus: domain.LearningStatusLearned, fsrsState: 2, dueAt: now},
		}, []float64{1, 0}},
		{"learned cooldown", []selectionCard{
			{cardID: "new", dueAt: now},
			{cardID: "learned", learningStatus: domain.LearningStatusLearned, fsrsState: 2, dueAt: now, lastPresentationID: 3, lastShownAt: now},
		}, []float64{1, 0}},
		{"learned cooldown expires", []selectionCard{
			{cardID: "new", dueAt: now},
			{cardID: "learned", learningStatus: domain.LearningStatusLearned, fsrsState: 2, dueAt: now, lastPresentationID: 3, lastShownAt: now.Add(-30 * time.Minute)},
		}, []float64{0.9, 0.1}},
		{"early active before older learned", []selectionCard{
			{cardID: "active", fsrsState: 2, dueAt: now.Add(2 * time.Hour), lastPresentationID: 3, lastShownAt: now.Add(-time.Hour)},
			{cardID: "learned", learningStatus: domain.LearningStatusLearned, fsrsState: 2, dueAt: now.Add(time.Hour)},
		}, []float64{1, 0}},
		{"early cooldown before status", []selectionCard{
			{cardID: "active", fsrsState: 2, dueAt: now.Add(2 * time.Hour), lastPresentationID: 3, lastShownAt: now},
			{cardID: "learned", learningStatus: domain.LearningStatusLearned, fsrsState: 2, dueAt: now.Add(time.Hour)},
		}, []float64{0, 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := planLearningSelection(test.cards, 1, now, settings.Defaults())
			for index := range test.cards {
				got, _ := plan.likelihood(&test.cards[index])
				if math.Abs(got-test.want[index]) > 1e-12 {
					t.Fatalf("%s probability=%g, want %g", test.cards[index].cardID, got, test.want[index])
				}
			}
		})
	}
}

func TestAdminSuggestionsLoadsLearnedStatusAndScopes(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	for _, fixture := range []struct {
		term, owner string
		status      domain.LearningStatus
	}{
		{"active", "owner", domain.LearningStatusNew},
		{"learned", "owner", domain.LearningStatusLearned},
		{"archived", "owner", domain.LearningStatusArchived},
		{"foreign", "other", domain.LearningStatusLearned},
	} {
		_, item, err := store.SaveVocabulary(ctx, VocabularyCreate{
			OwnerKey: fixture.owner, Term: fixture.term, NormalizedTerm: fixture.term,
			Status: fixture.status, Usefulness: domain.UsefulnessNormal, Now: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		if fixture.status == domain.LearningStatusArchived {
			if _, err := store.sql.ExecContext(ctx, `INSERT OR IGNORE INTO learning_cards
				(id, vocabulary_item_id, exercise_mode, due_at, created_at, updated_at)
				VALUES ('stale-archived', ?, 'production', ?, ?, ?)`,
				item.ItemID, TimeString(now), TimeString(now), TimeString(now)); err != nil {
				t.Fatal(err)
			}
		}
	}
	page, err := store.AdminSuggestions(ctx, "owner", 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || page.Selectable != 2 {
		t.Fatalf("owner preview: total=%d selectable=%d", page.Total, page.Selectable)
	}
	for _, row := range page.Rows {
		want, pool := 0.9, "new"
		if row.Term == "learned" {
			want, pool = 0.1, "learned"
		} else if row.Term != "active" {
			t.Fatalf("unexpected term %s", row.Term)
		}
		if math.Abs(row.Probability-want) > 1e-12 || row.Pool != pool {
			t.Fatalf("%s probability=%g pool=%s, want %g %s", row.Term, row.Probability, row.Pool, want, pool)
		}
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
	shownAt := TimeString(now.Add(-30 * 24 * time.Hour))
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
	for offset, want := range []string{"c", "b", "a", "future", ""} {
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
		probability := map[string]float64{"a": 1.0 / 7, "b": 2.0 / 7, "c": 4.0 / 7, "future": 0}[want]
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
