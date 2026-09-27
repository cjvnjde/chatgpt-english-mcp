package storage

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"english-learning-mcp/internal/domain"
	"english-learning-mcp/internal/settings"
)

func TestConfiguredLearningSharesMatchSamplingBoundary(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	cards := []selectionCard{
		{cardID: "new", learningStatus: domain.LearningStatusNew},
		{cardID: "review", fsrsState: 2, dueAt: now},
		{cardID: "learned", fsrsState: 2, dueAt: now, learningStatus: domain.LearningStatusLearned},
	}
	values := settings.Defaults()
	values.NewShareMin, values.NewShareMax = .6, .6
	values.LearnedShareMin, values.LearnedShareMax = .3, .3
	plan := planLearningSelection(cards, 0, now, values)
	for index, want := range []float64{.42, .28, .3} {
		got, _ := plan.likelihood(&cards[index])
		if math.Abs(got-want) > 1e-12 {
			t.Fatalf("%s probability %g, want %g", cards[index].cardID, got, want)
		}
	}
	for _, test := range []struct {
		draw float64
		want string
	}{{.419999, "new"}, {.420001, "review"}, {.699999, "review"}, {.700001, "learned"}} {
		selected, ok := selectLearningCard(cards, 0, now, func() float64 { return test.draw }, values)
		if !ok || selected.cardID != test.want {
			t.Fatalf("draw %g: got %s, want %s", test.draw, selected.cardID, test.want)
		}
	}
}

func TestConfiguredPresentationCooldownAndExposureBoundaries(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	values := settings.Defaults()
	values.PresentationCooldownMinutes = 10
	values.ExposureRecoveryHours = 2
	values.UnseenExposureWeight = 7
	cards := []selectionCard{{cardID: "recent", lastPresentationID: 3, lastShownAt: now.Add(-10 * time.Minute)}, {cardID: "fresh"}}
	plan := planLearningSelection(cards, 1, now.Add(-time.Nanosecond), values)
	if probability, _ := plan.likelihood(&cards[0]); probability != 0 {
		t.Fatalf("before cooldown boundary = %g", probability)
	}
	plan = planLearningSelection(cards, 1, now, values)
	if probability, _ := plan.likelihood(&cards[0]); probability <= 0 {
		t.Fatalf("at cooldown boundary = %g", probability)
	}
	values.RecentPresentationCount = 0
	plan = planLearningSelection(cards, 1, now.Add(-time.Minute), values)
	if probability, _ := plan.likelihood(&cards[0]); probability <= 0 {
		t.Fatal("zero recent count did not disable exclusion")
	}
	values.RecentPresentationCount = 3
	values.PresentationCooldownMinutes = 0
	plan = planLearningSelection(cards, 1, now.Add(-time.Minute), values)
	if probability, _ := plan.likelihood(&cards[0]); probability <= 0 {
		t.Fatal("zero cooldown did not disable exclusion")
	}
	if got := exposurePriority(&cards[1], now, values); got != 7 {
		t.Fatalf("unseen exposure = %g", got)
	}
	for _, test := range []struct {
		elapsed time.Duration
		want    float64
	}{{0, .25}, {time.Hour, .625}, {2 * time.Hour, 1}, {2*time.Hour + 29*24*time.Hour, 2}} {
		cards[0].lastShownAt = now.Add(-test.elapsed)
		if got := exposurePriority(&cards[0], now, values); got != test.want {
			t.Fatalf("after %s exposure=%g want %g", test.elapsed, got, test.want)
		}
	}
}

func TestConfiguredReinforcementCapPreservesProbabilityMass(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	for _, cap := range []float64{.05, .1, .17, .2, .25, .4, 1} {
		values := settings.Defaults()
		values.ReinforcementMaxWordShare = cap
		minimum := reinforcementMinimumWords(values)
		for _, count := range []int{minimum, minimum + 3} {
			senses := make([]reinforcementSense, count)
			for index := range senses {
				senses[index] = reinforcementSense{normalizedTerm: fmt.Sprint(index), usefulness: domain.UsefulnessLow, interest: domain.PersonalInterestLow}
			}
			senses[0].usefulness, senses[0].interest = domain.UsefulnessHigh, domain.PersonalInterestHigh
			senses[0].practice.Difficulty, senses[0].commentCount = 4, 50
			words := planReinforcementSelection(senses, now, values)
			total := 0.0
			for _, word := range words {
				if word.probability <= 0 || word.probability > cap {
					t.Fatalf("cap=%g count=%d probability=%g", cap, count, word.probability)
				}
				total += word.probability
			}
			if math.Abs(total-1) > 1e-12 {
				t.Fatalf("cap=%g count=%d mass=%g", cap, count, total)
			}
		}
	}
}

func TestReinforcementReadsCooldownAndCapOnNextOperation(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	for index := range 5 {
		saveReinforcementVocabulary(t, store, fmt.Sprint("word", index), now)
	}
	values := settings.Defaults()
	values.ReinforcementMaxWordShare = .2
	values.ReinforcementCooldownHours = 2
	if _, err := store.UpdateAlgorithmSettings(ctx, "owner", values, 0); err != nil {
		t.Fatal(err)
	}
	candidate, err := store.NextReinforcementItem(ctx, "owner", clockAt(now))
	if err != nil || math.Abs(candidate.SelectionProbability-.2) > 1e-12 {
		t.Fatalf("first selection = %#v, %v", candidate, err)
	}
	if _, err := store.NextReinforcementItem(ctx, "owner", clockAt(now.Add(2*time.Hour-time.Nanosecond))); !errors.Is(err, ErrReinforcementShortage) {
		t.Fatalf("before cooldown boundary: %v", err)
	}
	if _, err := store.NextReinforcementItem(ctx, "owner", clockAt(now.Add(2*time.Hour))); err != nil {
		t.Fatalf("at cooldown boundary: %v", err)
	}
	values.ReinforcementCooldownHours = 0
	if _, err := store.UpdateAlgorithmSettings(ctx, "owner", values, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := store.NextReinforcementItem(ctx, "owner", clockAt(now.Add(2*time.Hour))); err != nil {
		t.Fatalf("disabled cooldown not immediate: %v", err)
	}
	values.ReinforcementMaxWordShare = .1
	if _, err := store.UpdateAlgorithmSettings(ctx, "owner", values, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := store.NextReinforcementItem(ctx, "owner", clockAt(now.Add(2*time.Hour))); !errors.Is(err, ErrReinforcementShortage) {
		t.Fatalf("new cap not immediate: %v", err)
	}
}
