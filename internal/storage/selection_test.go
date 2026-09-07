package storage

import (
	"context"
	"fmt"
	"testing"
	"time"

	"english-learning-mcp/internal/domain"
)

func TestSelectLearningCardBalancesNewAndDueWithoutShortlists(t *testing.T) {
	now := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	cards := []selectionCard{
		{cardID: "troublesome", dueAt: now.Add(-100 * 24 * time.Hour), fsrsState: 2, consecutiveFailures: 100, lapses: 100, usefulness: domain.UsefulnessLow},
		{cardID: "new", dueAt: now.Add(24 * time.Hour), usefulness: domain.UsefulnessHigh},
	}
	for _, test := range []struct {
		draw float64
		want string
	}{
		{draw: 0.199999, want: "new"},
		{draw: 0.2, want: "troublesome"},
		{draw: 0.999999, want: "troublesome"},
	} {
		selected, ok := selectLearningCard(cards, 0, now, func() float64 { return test.draw })
		if !ok || selected.cardID != test.want {
			t.Fatalf("draw %g selected %q, want %q", test.draw, selected.cardID, test.want)
		}
	}

	unseen := make([]selectionCard, 128)
	for index := range unseen {
		unseen[index] = selectionCard{cardID: fmt.Sprintf("new-%03d", index), dueAt: now}
	}
	selected, ok := selectLearningCard(unseen, 0, now, func() float64 { return 0.999999 })
	if !ok || selected.cardID != "new-127" {
		t.Fatalf("unseen pool selected %q, want final card beyond any oldest-only shortlist", selected.cardID)
	}
}

func TestSelectLearningCardUsesWeightedSamplingWithinPools(t *testing.T) {
	now := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name  string
		cards []selectionCard
		draw  float64
		want  string
	}{
		{
			name: "bounded trouble leaves ordinary due card reachable",
			cards: []selectionCard{
				{cardID: "trouble", fsrsState: 2, dueAt: now.Add(-100 * 24 * time.Hour), consecutiveFailures: 100, lapses: 100},
				{cardID: "ordinary", fsrsState: 2, dueAt: now},
			},
			draw: 0.95, want: "ordinary",
		},
		{
			name: "trouble receives more mass than ordinary due",
			cards: []selectionCard{
				{cardID: "trouble", fsrsState: 2, dueAt: now.Add(-100 * 24 * time.Hour), consecutiveFailures: 100, lapses: 100},
				{cardID: "ordinary", fsrsState: 2, dueAt: now},
			},
			draw: 0.9, want: "trouble",
		},
		{
			name: "unseen gets more mass than recently shown",
			cards: []selectionCard{
				{cardID: "shown", lastPresentationID: 1, lastShownAt: now},
				{cardID: "unseen"},
			},
			draw: 0.3, want: "unseen",
		},
		{
			name: "recently shown stays reachable after leaving last three",
			cards: []selectionCard{
				{cardID: "shown", lastPresentationID: 1, lastShownAt: now},
				{cardID: "unseen"},
			},
			draw: 0.1, want: "shown",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			selected, ok := selectLearningCard(test.cards, 2, now, func() float64 { return test.draw })
			if !ok || selected.cardID != test.want {
				t.Fatalf("selected %q, want %q", selected.cardID, test.want)
			}
		})
	}
}

func TestSelectLearningCardRecencyPenaltyRecoversByNextDay(t *testing.T) {
	now := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	cards := []selectionCard{
		{cardID: "shown", lastPresentationID: 1, lastShownAt: now},
		{cardID: "unseen"},
	}
	draw := func() float64 { return 0.4 }
	recent, ok := selectLearningCard(cards, 2, now, draw)
	if !ok || recent.cardID != "unseen" {
		t.Fatalf("recent presentation selected %q, want unseen alternative", recent.cardID)
	}
	recovered, ok := selectLearningCard(cards, 2, now.Add(24*time.Hour), draw)
	if !ok || recovered.cardID != "shown" {
		t.Fatalf("next-day selection = %q, want previously shown card competing equally", recovered.cardID)
	}
}

func TestSelectLearningCardSmallPoolsLeaveMultipleChoices(t *testing.T) {
	now := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	for _, size := range []int{3, 4, 5} {
		t.Run(fmt.Sprintf("pool-%d", size), func(t *testing.T) {
			cards := make([]selectionCard, size)
			for index := range cards {
				cards[index] = selectionCard{
					cardID: fmt.Sprintf("card-%d", index), fsrsState: 1, dueAt: now,
					lastPresentationID: int64(index + 1), lastShownAt: now,
				}
			}
			var firstChoice string
			for index, draw := range []float64{0, 0.999999} {
				selected, ok := selectLearningCard(cards, int64(max(1, size-2)), now, func() float64 { return draw })
				if !ok {
					t.Fatal("eligible cards were not selectable")
				}
				if selected.lastPresentationID > 2 {
					t.Fatalf("selected %q instead of preserving spacing from the most recent cards", selected.cardID)
				}
				if index == 0 {
					firstChoice = selected.cardID
				} else if selected.cardID == firstChoice {
					t.Fatalf("both random draws forced %q, locking the pool into a fixed rotation", selected.cardID)
				}
			}
		})
	}
}

func TestSelectLearningCardRelaxesCooldownWhenLatestCardIsNotDue(t *testing.T) {
	now := time.Date(2026, 9, 7, 10, 2, 0, 0, time.UTC)
	// Four failed words, asked 30 seconds apart and answered after 10 seconds.
	// The two newest cards are not due yet; neither due card is an immediate repeat.
	cards := []selectionCard{
		{cardID: "first", fsrsState: 1, dueAt: now.Add(-50 * time.Second), lastPresentationID: 1, lastShownAt: now.Add(-2 * time.Minute)},
		{cardID: "second", fsrsState: 1, dueAt: now.Add(-20 * time.Second), lastPresentationID: 2, lastShownAt: now.Add(-90 * time.Second)},
		{cardID: "third", fsrsState: 1, dueAt: now.Add(10 * time.Second), lastPresentationID: 3, lastShownAt: now.Add(-time.Minute)},
		{cardID: "latest", fsrsState: 1, dueAt: now.Add(40 * time.Second), lastPresentationID: 4, lastShownAt: now.Add(-30 * time.Second)},
	}
	for _, test := range []struct {
		draw float64
		want string
	}{
		{draw: 0, want: "first"},
		{draw: 0.999999, want: "second"},
	} {
		selected, ok := selectLearningCard(cards, 2, now, func() float64 { return test.draw })
		if !ok || selected.cardID != test.want {
			t.Fatalf("draw %g selected %q, want due alternative %q", test.draw, selected.cardID, test.want)
		}
	}
}

func TestSelectLearningCardKeepsFreshAlternativeAheadOfCooldownRelaxation(t *testing.T) {
	now := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	for _, fresh := range []selectionCard{
		{cardID: "unseen"},
		{cardID: "cooldown-expired", fsrsState: 2, dueAt: now, lastPresentationID: 1, lastShownAt: now.Add(-30 * time.Minute)},
	} {
		t.Run(fresh.cardID, func(t *testing.T) {
			cards := []selectionCard{
				fresh,
				{cardID: "recent", fsrsState: 1, dueAt: now, lastPresentationID: 2, lastShownAt: now},
				{cardID: "latest", fsrsState: 1, dueAt: now, lastPresentationID: 3, lastShownAt: now},
			}
			for _, draw := range []float64{0, 0.999999} {
				selected, ok := selectLearningCard(cards, 1, now, func() float64 { return draw })
				if !ok || selected.cardID != fresh.cardID {
					t.Fatalf("draw %g selected %q, want fresh alternative %q", draw, selected.cardID, fresh.cardID)
				}
			}
		})
	}
}

func TestSelectLearningCardMixAccountsForRecentExposureAndRecovers(t *testing.T) {
	now := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	cards := make([]selectionCard, 0, 104)
	for index := range 4 {
		cards = append(cards, selectionCard{
			cardID: fmt.Sprintf("due-%d", index), fsrsState: 1, dueAt: now,
			lastPresentationID: int64(index + 1), lastShownAt: now,
		})
	}
	for index := range 100 {
		cards = append(cards, selectionCard{cardID: fmt.Sprintf("new-%d", index)})
	}
	for _, test := range []struct {
		name    string
		elapsed time.Duration
		wantNew int
	}{
		{name: "recent due pool yields room to unseen words", wantNew: 50},
		{name: "next day restores baseline review priority", elapsed: 24 * time.Hour, wantNew: 20},
	} {
		t.Run(test.name, func(t *testing.T) {
			newSelections := 0
			for index := range 100 {
				draw := (float64(index) + 0.5) / 100
				// The four due cards have left the hard cooldown, but their
				// recent exposure must still affect the pool choice.
				selected, ok := selectLearningCard(cards, 5, now.Add(test.elapsed), func() float64 { return draw })
				if !ok {
					t.Fatal("eligible cards were not selectable")
				}
				if selected.fsrsState == 0 {
					newSelections++
				}
			}
			if newSelections != test.wantNew {
				t.Fatalf("new selections = %d/100, want %d/100", newSelections, test.wantNew)
			}
		})
	}
}

func TestSelectLearningCardCooldownExpiresAndSmallPoolsRotate(t *testing.T) {
	now := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	cards := []selectionCard{
		{cardID: "shown", lastPresentationID: 1, lastShownAt: now},
		{cardID: "unseen"},
	}
	for _, test := range []struct {
		elapsed time.Duration
		want    string
	}{
		{elapsed: 30*time.Minute - time.Nanosecond, want: "unseen"},
		{elapsed: 30 * time.Minute, want: "shown"},
	} {
		selected, ok := selectLearningCard(cards, 1, now.Add(test.elapsed), func() float64 { return 0 })
		if !ok || selected.cardID != test.want {
			t.Fatalf("after %s selected %q, want %q", test.elapsed, selected.cardID, test.want)
		}
	}

	// Equal timestamps still rotate by issuance order, not random tie-breaking.
	cards[1].lastPresentationID = 2
	cards[1].lastShownAt = now
	for index := range 6 {
		selected, ok := selectLearningCard(cards, int64(max(1, index)), now, func() float64 { return 0.99 })
		want := index % 2
		if !ok || selected.cardID != cards[want].cardID {
			t.Fatalf("small-pool turn %d selected %q, want %q", index, selected.cardID, cards[want].cardID)
		}
		cards[want].lastPresentationID = int64(index + 3)
	}
	selected, ok := selectLearningCard(cards[:1], 1, now, func() float64 { return 0 })
	if !ok || selected.cardID != "shown" {
		t.Fatalf("single active card selected %q, want shown", selected.cardID)
	}
}

func TestSelectLearningCardAppliesCooldownBeforePoolChoice(t *testing.T) {
	now := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	for _, recentState := range []int{0, 2} {
		cards := []selectionCard{
			{cardID: "recent", fsrsState: recentState, dueAt: now, lastPresentationID: 1, lastShownAt: now, usefulness: domain.UsefulnessHigh},
			{cardID: "alternative", fsrsState: 2 - recentState, dueAt: now, usefulness: domain.UsefulnessLow},
		}
		for _, draw := range []float64{0, 0.99} {
			selected, ok := selectLearningCard(cards, 1, now, func() float64 { return draw })
			if !ok || selected.cardID != "alternative" {
				t.Fatalf("recent state %d, draw %g selected %q, want alternative", recentState, draw, selected.cardID)
			}
		}
	}
}

func TestSelectLearningCardFutureFallbackRespectsEligibilityAndCooldown(t *testing.T) {
	now := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name  string
		cards []selectionCard
		want  string
	}{
		{
			name: "recent due still precedes unseen future",
			cards: []selectionCard{
				{cardID: "due", fsrsState: 2, dueAt: now, lastPresentationID: 1, lastShownAt: now, usefulness: domain.UsefulnessLow},
				{cardID: "future", fsrsState: 2, dueAt: now.Add(time.Minute), usefulness: domain.UsefulnessHigh},
			},
			want: "due",
		},
		{
			name: "FSRS new is eligible even with future due date",
			cards: []selectionCard{
				{cardID: "new", dueAt: now.Add(24 * time.Hour), lastPresentationID: 1, lastShownAt: now},
				{cardID: "future", fsrsState: 2, dueAt: now.Add(time.Minute)},
			},
			want: "new",
		},
		{
			name: "future failures never outrank earlier due time",
			cards: []selectionCard{
				{cardID: "failed", fsrsState: 1, dueAt: now.Add(24 * time.Hour), consecutiveFailures: 100, lapses: 100, usefulness: domain.UsefulnessHigh},
				{cardID: "near", fsrsState: 2, dueAt: now.Add(time.Minute), usefulness: domain.UsefulnessLow},
			},
			want: "near",
		},
		{
			name: "future cooldown skips nearer recently presented card",
			cards: []selectionCard{
				{cardID: "near", fsrsState: 2, dueAt: now.Add(time.Minute), lastPresentationID: 1, lastShownAt: now},
				{cardID: "far", fsrsState: 2, dueAt: now.Add(time.Hour)},
			},
			want: "far",
		},
		{
			name: "all future cards recent chooses least recently presented",
			cards: []selectionCard{
				{cardID: "near", fsrsState: 2, dueAt: now.Add(time.Minute), lastPresentationID: 2, lastShownAt: now},
				{cardID: "far", fsrsState: 2, dueAt: now.Add(time.Hour), lastPresentationID: 1, lastShownAt: now},
			},
			want: "far",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			selected, ok := selectLearningCard(test.cards, 1, now, func() float64 { return 0.99 })
			if !ok || selected.cardID != test.want {
				t.Fatalf("selected %q, want %q", selected.cardID, test.want)
			}
		})
	}
}

func TestLearningSelectionUsesCurrentPersistedUsefulness(t *testing.T) {
	for _, state := range []int{0, 2} {
		t.Run(fmt.Sprintf("fsrs-state-%d", state), func(t *testing.T) {
			ctx := context.Background()
			now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
			store, err := Open(ctx, ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			levels := []domain.Usefulness{domain.UsefulnessLow, domain.UsefulnessNormal, domain.UsefulnessHigh}
			var items []domain.VocabularyItem
			var cardIDs []string
			for _, level := range levels {
				term := "fixture usefulness " + string(level)
				_, item, err := store.SaveVocabulary(ctx, VocabularyCreate{
					OwnerKey: "owner", Term: term, NormalizedTerm: term,
					Status: domain.LearningStatusNew, Usefulness: level, Now: now,
				})
				if err != nil {
					t.Fatal(err)
				}
				items = append(items, item)
				var cardID string
				if err := store.sql.QueryRowContext(ctx, "SELECT id FROM learning_cards WHERE vocabulary_item_id = ?", item.ItemID).Scan(&cardID); err != nil {
					t.Fatal(err)
				}
				cardIDs = append(cardIDs, cardID)
			}
			if _, err := store.sql.ExecContext(ctx, "UPDATE learning_cards SET fsrs_state = ?, due_at = ?", state, TimeString(now)); err != nil {
				t.Fatal(err)
			}

			assertSelectionCounts := func(want []int) {
				t.Helper()
				transaction, err := store.sql.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer transaction.Rollback()
				cards, recentSinceID, err := loadSelectionCards(ctx, transaction, "owner")
				if err != nil {
					t.Fatal(err)
				}
				counts := make(map[string]int)
				for index := range 14 {
					draw := (float64(index) + 0.5) / 14
					selected, ok := selectLearningCard(cards, recentSinceID, now, func() float64 { return draw })
					if !ok {
						t.Fatal("persisted vocabulary was not selectable")
					}
					counts[selected.cardID]++
				}
				for index, cardID := range cardIDs {
					if counts[cardID] != want[index] {
						t.Fatalf("usefulness selection counts = %v, card %s got %d, want %d", counts, cardID, counts[cardID], want[index])
					}
				}
			}

			assertSelectionCounts([]int{2, 4, 8})
			for index, level := range []domain.Usefulness{domain.UsefulnessHigh, domain.UsefulnessNormal, domain.UsefulnessLow} {
				if _, err := store.UpdateVocabulary(ctx, VocabularyUpdate{
					OwnerKey: "owner", ItemID: items[index].ItemID, Usefulness: &level, Now: now,
				}); err != nil {
					t.Fatal(err)
				}
			}
			assertSelectionCounts([]int{8, 4, 2})
		})
	}
}
