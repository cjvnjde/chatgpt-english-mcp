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

func TestPersonalInterestWeightsEveryPoolWithoutChangingPoolShares(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	for _, state := range []int{0, 1, 2, 3} {
		t.Run(fmt.Sprintf("state-%d", state), func(t *testing.T) {
			cards := []selectionCard{
				{cardID: "low", fsrsState: state, dueAt: now, personalInterest: domain.PersonalInterestLow},
				{cardID: "normal", fsrsState: state, dueAt: now, personalInterest: domain.PersonalInterestNormal},
				{cardID: "high", fsrsState: state, dueAt: now, personalInterest: domain.PersonalInterestHigh},
			}
			plan := planLearningSelection(cards, 0, now)
			for index, want := range []float64{1.0 / 7, 2.0 / 7, 4.0 / 7} {
				probability, _ := plan.likelihood(&cards[index])
				if probability != want {
					t.Fatalf("%s probability = %g, want %g", cards[index].cardID, probability, want)
				}
			}
			for _, test := range []struct {
				draw float64
				want string
			}{
				{0, "low"}, {0.14, "low"}, {0.15, "normal"}, {0.42, "normal"}, {0.43, "high"}, {0.999, "high"},
			} {
				selected, ok := selectLearningCard(cards, 0, now, func() float64 { return test.draw })
				if !ok || selected.cardID != test.want {
					t.Fatalf("draw %g selected %s, want %s", test.draw, selected.cardID, test.want)
				}
			}
		})
	}
	cards := []selectionCard{
		{cardID: "new", personalInterest: domain.PersonalInterestHigh},
		{cardID: "due", fsrsState: 2, dueAt: now, personalInterest: domain.PersonalInterestLow},
	}
	plan := planLearningSelection(cards, 0, now)
	for index, want := range []float64{0.2, 0.8} {
		probability, _ := plan.likelihood(&cards[index])
		if probability != want {
			t.Fatalf("%s pool probability = %g, want %g", cards[index].cardID, probability, want)
		}
	}
	// Interest cannot bypass cooldown or change deterministic future ordering.
	cards[0].fsrsState, cards[0].dueAt = 2, now.Add(time.Hour)
	cards[1].dueAt = now.Add(time.Minute)
	selected, ok := selectLearningCard(cards, 0, now, func() float64 {
		t.Fatal("future rotation consumed randomness")
		return 0
	})
	if !ok || selected.cardID != "due" {
		t.Fatalf("future selection = %#v, want earlier low-interest card", selected)
	}
	cards[0].dueAt = now
	cards[0].lastPresentationID, cards[0].lastShownAt = 1, now
	cards[1].dueAt = now
	selected, ok = selectLearningCard(cards, 1, now, func() float64 { return 0 })
	if !ok || selected.cardID != "due" {
		t.Fatalf("cooldown selection = %#v, want low-interest alternative", selected)
	}
}

func TestAdminLikelihoodLoadsPersistedPersonalInterest(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	expected := make(map[string]float64)
	for _, interest := range []domain.PersonalInterest{domain.PersonalInterestLow, domain.PersonalInterestHigh} {
		_, item, err := store.SaveVocabulary(ctx, VocabularyCreate{
			OwnerKey: "owner", Term: "bank", NormalizedTerm: "bank",
			SenseKey: string(interest), PersonalInterest: interest, Status: domain.LearningStatusNew, Now: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		expected[item.ItemID] = 0.2
		if interest == domain.PersonalInterestHigh {
			expected[item.ItemID] = 0.8
		}
	}
	page, err := store.AdminSuggestions(ctx, "owner", 10, 0)
	if err != nil || page.Selectable != 2 || len(page.Rows) != 2 {
		t.Fatalf("admin suggestions = %#v, error %v", page, err)
	}
	for _, row := range page.Rows {
		if row.Probability != expected[row.VocabularyItemID] {
			t.Fatalf("suggestion %s probability = %g, want %g", row.VocabularyItemID, row.Probability, expected[row.VocabularyItemID])
		}
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

func TestSelectLearningCardKeepsNewShareFixedAcrossMatureReviewExposure(t *testing.T) {
	now := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	for _, reviewCount := range []int{1, 4} {
		for _, elapsed := range []time.Duration{0, 24 * time.Hour} {
			t.Run(fmt.Sprintf("reviews-%d-after-%s", reviewCount, elapsed), func(t *testing.T) {
				cards := make([]selectionCard, 0, reviewCount+100)
				for index := range reviewCount {
					cards = append(cards, selectionCard{
						cardID: fmt.Sprintf("review-%d", index), fsrsState: 2, dueAt: now,
						lastPresentationID: int64(index + 1), lastShownAt: now,
					})
				}
				for index := range 100 {
					cards = append(cards, selectionCard{cardID: fmt.Sprintf("new-%d", index)})
				}
				newSelections := 0
				for index := range 100 {
					draw := (float64(index) + 0.5) / 100
					// Reviews have left the hard cooldown even when just shown.
					selected, ok := selectLearningCard(cards, 5, now.Add(elapsed), func() float64 { return draw })
					if !ok {
						t.Fatal("eligible cards were not selectable")
					}
					if selected.fsrsState == 0 {
						newSelections++
					}
				}
				if newSelections != 20 {
					t.Fatalf("new selections = %d/100, want 20/100", newSelections)
				}
			})
		}
	}
}

func TestSelectLearningCardPrioritizesDueLearningStepsAfterCooldown(t *testing.T) {
	now := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	dueAt := now.Add(time.Nanosecond)
	for _, state := range []int{1, 3} {
		for _, test := range []struct {
			name       string
			selectAt   time.Time
			presented  bool
			wantNew    int
			wantReview int
			wantStep   int
		}{
			{name: "future step does not compete", selectAt: now, wantNew: 20, wantReview: 80},
			{name: "step takes priority exactly when due", selectAt: dueAt, wantStep: 100},
			{name: "cooldown supplies other words first", selectAt: dueAt, presented: true, wantNew: 20, wantReview: 80},
			{name: "expired cooldown restores priority", selectAt: now.Add(30 * time.Minute), presented: true, wantStep: 100},
		} {
			t.Run(fmt.Sprintf("state-%d/%s", state, test.name), func(t *testing.T) {
				step := selectionCard{cardID: "step", fsrsState: state, dueAt: dueAt}
				if test.presented {
					step.lastPresentationID = 1
					step.lastShownAt = now
				}
				cards := []selectionCard{step, {cardID: "review", fsrsState: 2, dueAt: now}}
				for index := range 100 {
					cards = append(cards, selectionCard{cardID: fmt.Sprintf("new-%d", index)})
				}
				counts := make(map[int]int)
				for index := range 100 {
					draw := (float64(index) + 0.5) / 100
					selected, ok := selectLearningCard(cards, 1, test.selectAt, func() float64 { return draw })
					if !ok {
						t.Fatal("eligible cards were not selectable")
					}
					counts[selected.fsrsState]++
				}
				if counts[0] != test.wantNew || counts[2] != test.wantReview || counts[state] != test.wantStep {
					t.Fatalf("selections by state = %v, want new %d, review %d, step %d",
						counts, test.wantNew, test.wantReview, test.wantStep)
				}
			})
		}
	}
}

func TestSelectLearningCardLearningUrgencyRespondsToMinuteScaleOverdue(t *testing.T) {
	now := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	for _, state := range []int{1, 3} {
		t.Run(fmt.Sprintf("state-%d", state), func(t *testing.T) {
			cards := []selectionCard{
				{cardID: "on-time", fsrsState: state, dueAt: now, scheduledDays: 30},
				{cardID: "overdue", fsrsState: state, dueAt: now, scheduledDays: 30},
			}
			selected, ok := selectLearningCard(cards, 0, now, func() float64 { return 0.45 })
			if !ok || selected.cardID != "on-time" {
				t.Fatalf("equally due steps selected %q, want on-time", selected.cardID)
			}
			cards[1].dueAt = now.Add(-10 * time.Minute)
			selected, ok = selectLearningCard(cards, 0, now, func() float64 { return 0.45 })
			if !ok || selected.cardID != "overdue" {
				t.Fatalf("ten-minute overdue step selected %q, want overdue", selected.cardID)
			}
		})
	}
}

func TestSelectLearningCardRecoveryIgnoresUsefulnessAndSoftRecency(t *testing.T) {
	now := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	for _, state := range []int{1, 3} {
		for _, test := range []struct {
			name  string
			first selectionCard
			other selectionCard
		}{
			{
				name:  "usefulness does not suppress recovery",
				first: selectionCard{usefulness: domain.UsefulnessLow},
				other: selectionCard{usefulness: domain.UsefulnessHigh},
			},
			{
				name:  "recent exposure outside hard cooldown does not suppress recovery",
				first: selectionCard{lastPresentationID: 1, lastShownAt: now},
				other: selectionCard{},
			},
		} {
			t.Run(fmt.Sprintf("state-%d/%s", state, test.name), func(t *testing.T) {
				cards := []selectionCard{test.first, test.other}
				for index := range cards {
					cards[index].cardID = fmt.Sprintf("step-%d", index)
					cards[index].fsrsState = state
					cards[index].dueAt = now
				}
				for _, draw := range []float64{0.4, 0.6} {
					want := "step-0"
					if draw > 0.5 {
						want = "step-1"
					}
					selected, ok := selectLearningCard(cards, 2, now, func() float64 { return draw })
					if !ok || selected.cardID != want {
						t.Fatalf("draw %g selected %q, want equally weighted %q", draw, selected.cardID, want)
					}
				}
			})
		}
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
			name: "equal future exposure breaks ties by due time not failures",
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
		{
			name: "outside cooldown older exposure precedes nearer due time",
			cards: []selectionCard{
				{cardID: "near", fsrsState: 2, dueAt: now.Add(time.Minute), lastPresentationID: 2, lastShownAt: now.Add(-30 * time.Minute)},
				{cardID: "far", fsrsState: 2, dueAt: now.Add(time.Hour), lastPresentationID: 1, lastShownAt: now.Add(-30 * time.Minute)},
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

func TestNextLearningItemRotatesEntireFuturePoolBeforeRepeating(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	const poolSize = 7
	for index := poolSize; index > 0; index-- {
		term := fmt.Sprintf("future-%d", index)
		item := savePresentationVocabulary(t, store, "owner", term, now)
		if _, err := store.sql.ExecContext(ctx, `
			UPDATE learning_cards SET fsrs_state = 2, due_at = ?
			WHERE vocabulary_item_id = ?
		`, TimeString(now.Add(time.Duration(index)*time.Hour)), item.ItemID); err != nil {
			t.Fatal(err)
		}
	}

	// Keep scheduling and wall time fixed: only presentation order advances.
	for turn := range 2 * poolSize {
		selected, err := store.NextLearningItem(ctx, "owner", now)
		if err != nil {
			t.Fatal(err)
		}
		want := fmt.Sprintf("future-%d", turn%poolSize+1)
		if selected.Vocabulary.Term != want {
			t.Fatalf("future turn %d selected %q, want %q before repeating a more recently shown word",
				turn, selected.Vocabulary.Term, want)
		}
	}
}

func TestLearningSelectionUsesCurrentPersistedUsefulness(t *testing.T) {
	for _, state := range []int{0, 1, 2, 3} {
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
				samples := 0
				for _, count := range want {
					samples += count
				}
				for index := range samples {
					draw := (float64(index) + 0.5) / float64(samples)
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

			before, after := []int{5, 5, 5}, []int{5, 5, 5}
			if state == 0 {
				before, after = []int{2, 4, 8}, []int{8, 4, 2}
			}
			assertSelectionCounts(before)
			for index, level := range []domain.Usefulness{domain.UsefulnessHigh, domain.UsefulnessNormal, domain.UsefulnessLow} {
				if _, err := store.UpdateVocabulary(ctx, VocabularyUpdate{
					OwnerKey: "owner", ItemID: items[index].ItemID, Usefulness: &level, Now: now,
				}); err != nil {
					t.Fatal(err)
				}
			}
			assertSelectionCounts(after)
		})
	}
}
