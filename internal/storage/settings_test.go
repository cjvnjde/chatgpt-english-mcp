package storage

import (
	"context"
	"math"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"english-learning-mcp/internal/apperr"
	"english-learning-mcp/internal/domain"
	"english-learning-mcp/internal/settings"
)

func TestAlgorithmSettingsPersistenceIsolationAndAtomicValidation(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "settings.sqlite")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.AlgorithmSettings(ctx, "owner")
	if err != nil || snapshot.Revision != 0 || !reflect.DeepEqual(snapshot.Values, settings.Defaults()) {
		t.Fatalf("absent snapshot = %#v, %v", snapshot, err)
	}
	values := snapshot.Values
	values.FastAnswerSeconds = 0
	values.NewShareMin, values.NewShareMax = .6, .7
	values.LearningStepsMinutes = []float64{2, 15}
	saved, err := store.UpdateAlgorithmSettings(ctx, "owner", values, 0)
	if err != nil || saved.Revision != 1 {
		t.Fatalf("first save = %#v, %v", saved, err)
	}
	invalid := values
	invalid.FastAnswerSeconds = 42
	invalid.NewShareMin = .9
	if _, err := store.UpdateAlgorithmSettings(ctx, "owner", invalid, 1); apperr.From(err) == nil || apperr.From(err).Code != apperr.InvalidArgument {
		t.Fatalf("invalid update error = %v", err)
	}
	if _, err := store.UpdateAlgorithmSettings(ctx, "owner", settings.Defaults(), 0); apperr.From(err) == nil || apperr.From(err).Code != apperr.Conflict {
		t.Fatalf("stale create error = %v", err)
	}
	if _, err := store.UpdateAlgorithmSettings(ctx, "other", values, 1); apperr.From(err) == nil || apperr.From(err).Code != apperr.Conflict {
		t.Fatalf("nonexistent revision error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	got, err := store.AlgorithmSettings(ctx, "owner")
	if err != nil || !reflect.DeepEqual(got, saved) {
		t.Fatalf("persisted snapshot = %#v, %v; want %#v", got, err, saved)
	}
	other, err := store.AlgorithmSettings(ctx, "other")
	if err != nil || other.Revision != 0 || !reflect.DeepEqual(other.Values, settings.Defaults()) {
		t.Fatalf("owner leak = %#v, %v", other, err)
	}
	updated, err := store.UpdateAlgorithmSettings(ctx, "owner", settings.Defaults(), saved.Revision)
	if err != nil || updated.Revision != 2 {
		t.Fatalf("replacement = %#v, %v", updated, err)
	}
	if _, err := store.UpdateAlgorithmSettings(ctx, "owner", values, saved.Revision); apperr.From(err) == nil || apperr.From(err).Code != apperr.Conflict {
		t.Fatalf("stale replacement error = %v", err)
	}
}

func TestConcurrentAlgorithmSettingsUpdatesHaveOneWinner(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "settings.sqlite")
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
	for _, revision := range []int64{0, 1} {
		start := make(chan struct{})
		results := make(chan error, 2)
		var group sync.WaitGroup
		for index, store := range []*DB{first, second} {
			group.Add(1)
			go func(index int, store *DB) {
				defer group.Done()
				values := settings.Defaults()
				values.FastAnswerSeconds = 40 + index
				<-start
				_, err := store.UpdateAlgorithmSettings(ctx, "owner", values, revision)
				results <- err
			}(index, store)
		}
		close(start)
		group.Wait()
		close(results)
		wins, conflicts := 0, 0
		for err := range results {
			if err == nil {
				wins++
			} else if apperr.From(err).Code == apperr.Conflict {
				conflicts++
			} else {
				t.Fatal(err)
			}
		}
		if wins != 1 || conflicts != 1 {
			t.Fatalf("revision %d: wins=%d conflicts=%d", revision, wins, conflicts)
		}
	}
}

func TestAdminLikelihoodUsesNewSettingsOnNextOperation(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	for _, owner := range []string{"owner", "other"} {
		for _, term := range []string{"low", "high"} {
			usefulness := domain.UsefulnessLow
			if term == "high" {
				usefulness = domain.UsefulnessHigh
			}
			_, _, err := store.SaveVocabulary(ctx, VocabularyCreate{OwnerKey: owner, Term: term, NormalizedTerm: term, Status: domain.LearningStatusNew, Usefulness: usefulness, Now: now})
			if err != nil {
				t.Fatal(err)
			}
			// Isolate selection weights from the separate usefulness inference policy.
			if _, err := store.sql.ExecContext(ctx, "UPDATE vocabulary_items SET usefulness = ? WHERE owner_key = ? AND normalized_term = ?", usefulness, owner, term); err != nil {
				t.Fatal(err)
			}
		}
	}
	values := settings.Defaults()
	values.LowUsefulnessWeight, values.HighUsefulnessWeight = 3, 1
	if _, err := store.UpdateAlgorithmSettings(ctx, "owner", values, 0); err != nil {
		t.Fatal(err)
	}
	for _, owner := range []string{"owner", "other"} {
		page, err := store.AdminSuggestions(ctx, owner, 20, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range page.Rows {
			want := .75
			if owner == "other" {
				want = .2
			}
			if row.Term == "high" {
				want = 1 - want
			}
			if math.Abs(row.Probability-want) > 1e-12 {
				t.Fatalf("%s %s probability=%g want %g", owner, row.Term, row.Probability, want)
			}
		}
	}
}

func TestRecentPresentationCountChangesNextSelectionEligibility(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	for index := range 6 {
		term := string(rune('a' + index))
		if _, _, err := store.SaveVocabulary(ctx, VocabularyCreate{OwnerKey: "owner", Term: term, NormalizedTerm: term, Status: domain.LearningStatusNew, Now: now}); err != nil {
			t.Fatal(err)
		}
	}
	for range 4 {
		if _, err := store.NextLearningItem(ctx, "owner", clockAt(now)); err != nil {
			t.Fatal(err)
		}
	}
	values := settings.Defaults()
	revision := int64(0)
	for _, count := range []int{3, 1, 0} {
		values.RecentPresentationCount = count
		saved, err := store.UpdateAlgorithmSettings(ctx, "owner", values, revision)
		if err != nil {
			t.Fatal(err)
		}
		revision = saved.Revision
		page, err := store.AdminSuggestions(ctx, "owner", 20, 0)
		if err != nil {
			t.Fatal(err)
		}
		if page.Selectable != 6-count {
			t.Fatalf("recent count %d: selectable=%d, want %d", count, page.Selectable, 6-count)
		}
	}
}
