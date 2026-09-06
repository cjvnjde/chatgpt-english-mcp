package vocabulary

import (
	"context"
	"errors"
	"testing"
	"time"

	"english-learning-mcp/internal/apperr"
	"english-learning-mcp/internal/domain"
	"english-learning-mcp/internal/storage"
)

func TestSaveDoesNotOverwriteAndUpdateIsPartial(t *testing.T) {
	service := newTestService(t, "owner-one")
	ctx := context.Background()
	description := "Money kept by a financial institution."

	first, err := service.Save(ctx, "  Bank  ", InitialValues{
		Status:            domain.LearningStatusLearning,
		Tags:              []string{"Finance", "common", "finance"},
		CustomDescription: &description,
		DescriptionSource: &domain.DescriptionSource{
			Title: "External dictionary",
			URL:   "https://example.test/bank",
		},
		Notes:    []string{"Usually countable."},
		Examples: []string{"I went to the bank."},
	})
	if err != nil {
		t.Fatalf("Save(bank) error = %v", err)
	}
	if !first.Created || first.Item.Status != domain.LearningStatusLearning {
		t.Fatalf("first save = %#v", first)
	}
	if !equalValues(first.Item.Tags, []string{"common", "finance"}) || first.Item.DescriptionSource == nil {
		t.Fatalf("normalized first save = %#v", first.Item)
	}

	replacement := "This must not overwrite the item."
	duplicate, err := service.Save(ctx, "bank", InitialValues{
		Status:            domain.LearningStatusArchived,
		Tags:              []string{"replacement"},
		CustomDescription: &replacement,
	})
	if err != nil {
		t.Fatalf("duplicate Save(bank) error = %v", err)
	}
	if duplicate.Created || duplicate.Item.ItemID != first.Item.ItemID {
		t.Fatalf("duplicate save = %#v", duplicate)
	}
	if duplicate.Item.Status != domain.LearningStatusLearning || duplicate.Item.CustomDescription != description {
		t.Fatalf("duplicate save overwrote metadata = %#v", duplicate.Item)
	}

	status := domain.LearningStatusLearned
	tags := []string{"Core", "Finance"}
	notes := []string{"Review the financial and river meanings separately."}
	updated, err := service.Update(ctx, "", "bank", UpdateChanges{
		Status: &status,
		Tags:   &tags,
		Notes:  &notes,
	})
	if err != nil {
		t.Fatalf("Update(bank) error = %v", err)
	}
	if updated.Status != domain.LearningStatusLearned || !equalValues(updated.Tags, []string{"core", "finance"}) {
		t.Fatalf("updated metadata = %#v", updated)
	}
	if updated.CustomDescription != description || updated.DescriptionSource == nil || len(updated.Examples) != 1 {
		t.Fatalf("partial update did not preserve omitted fields = %#v", updated)
	}

	empty := ""
	cleared, err := service.Update(ctx, first.Item.ItemID, "", UpdateChanges{CustomDescription: &empty})
	if err != nil {
		t.Fatalf("clear description error = %v", err)
	}
	if cleared.CustomDescription != "" || cleared.DescriptionSource != nil {
		t.Fatalf("cleared description = %#v", cleared)
	}

	_, err = service.Update(ctx, first.Item.ItemID, "", UpdateChanges{})
	assertApplicationError(t, err, apperr.InvalidArgument)
}

func TestUsefulnessPersistsWithoutOverwritingOtherMetadata(t *testing.T) {
	service := newTestService(t, "owner-one")
	ctx := context.Background()
	initial := InitialValues{Usefulness: domain.UsefulnessHigh}
	saved, err := service.Save(ctx, "bank", initial)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if saved.Item.Usefulness != domain.UsefulnessHigh || initial.Usefulness != domain.UsefulnessHigh {
		t.Fatalf("saved usefulness = %q, input = %#v", saved.Item.Usefulness, initial)
	}
	duplicate, err := service.Save(ctx, "bank", InitialValues{Usefulness: domain.UsefulnessLow})
	if err != nil {
		t.Fatalf("duplicate Save() error = %v", err)
	}
	if duplicate.Created || duplicate.Item.ItemID != saved.Item.ItemID || duplicate.Item.Usefulness != domain.UsefulnessHigh {
		t.Fatalf("duplicate Save() changed usefulness: %#v", duplicate)
	}

	usefulness := domain.UsefulnessLow
	updated, err := service.Update(ctx, saved.Item.ItemID, "", UpdateChanges{Usefulness: &usefulness})
	if err != nil {
		t.Fatalf("Update(usefulness) error = %v", err)
	}
	if updated.Usefulness != domain.UsefulnessLow || updated.Status != saved.Item.Status {
		t.Fatalf("usefulness-only update = %#v", updated)
	}
	notes := []string{"A personal reminder."}
	preserved, err := service.Update(ctx, "", "bank", UpdateChanges{Notes: &notes})
	if err != nil {
		t.Fatalf("Update(notes) error = %v", err)
	}
	if preserved.Usefulness != domain.UsefulnessLow || !equalValues(preserved.Notes, notes) {
		t.Fatalf("partial update = %#v", preserved)
	}
	fetched, err := service.Get(ctx, saved.Item.ItemID, "")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if fetched.Usefulness != domain.UsefulnessLow {
		t.Fatalf("Get() usefulness = %q", fetched.Usefulness)
	}
	listed, err := service.List(ctx, ListOptions{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed.Items) != 1 || listed.Items[0].Usefulness != domain.UsefulnessLow {
		t.Fatalf("List() = %#v", listed)
	}

	empty := InitialValues{}
	normal, err := service.Save(ctx, "ordinary", empty)
	if err != nil {
		t.Fatalf("Save(default) error = %v", err)
	}
	if normal.Item.Usefulness != domain.UsefulnessNormal || empty.Usefulness != "" {
		t.Fatalf("default usefulness = %q, input = %#v", normal.Item.Usefulness, empty)
	}
}

func TestInvalidUsefulnessDoesNotMutateVocabulary(t *testing.T) {
	service := newTestService(t, "owner-one")
	ctx := context.Background()
	saved, err := service.Save(ctx, "bank", InitialValues{Usefulness: domain.UsefulnessHigh})
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	for _, invalid := range []domain.Usefulness{"urgent", "HIGH", ""} {
		t.Run(string(invalid), func(t *testing.T) {
			if invalid != "" {
				_, err := service.Save(ctx, "invalid", InitialValues{Usefulness: invalid})
				assertApplicationError(t, err, apperr.InvalidArgument)
				_, err = service.Get(ctx, "", "invalid")
				assertApplicationError(t, err, apperr.NotFound)
			}
			notes := []string{"Must not be persisted."}
			_, err := service.Update(ctx, saved.Item.ItemID, "", UpdateChanges{Usefulness: &invalid, Notes: &notes})
			assertApplicationError(t, err, apperr.InvalidArgument)
			fetched, err := service.Get(ctx, saved.Item.ItemID, "")
			if err != nil {
				t.Fatalf("Get() error = %v", err)
			}
			if fetched.Usefulness != domain.UsefulnessHigh || len(fetched.Notes) != 0 {
				t.Fatalf("invalid update mutated item: %#v", fetched)
			}
		})
	}
}

func TestListFiltersLearningMetadataAndUsesBoundCursor(t *testing.T) {
	service := newTestService(t, "owner-one")
	ctx := context.Background()
	description := "A financial institution."
	tests := []struct {
		term    string
		initial InitialValues
	}{
		{term: "apple", initial: InitialValues{Tags: []string{"food"}}},
		{term: "bank", initial: InitialValues{
			Status:            domain.LearningStatusLearning,
			Tags:              []string{"common", "finance"},
			CustomDescription: &description,
		}},
		{term: "zebra", initial: InitialValues{
			Status: domain.LearningStatusLearned,
			Tags:   []string{"animals", "common"},
		}},
	}
	for _, test := range tests {
		if _, err := service.Save(ctx, test.term, test.initial); err != nil {
			t.Fatalf("Save(%s) error = %v", test.term, err)
		}
	}

	common, err := service.List(ctx, ListOptions{Tags: []string{"COMMON"}, Sort: "alphabetical"})
	if err != nil {
		t.Fatalf("List(common) error = %v", err)
	}
	if len(common.Items) != 2 || common.Items[0].Term != "bank" || common.Items[1].Term != "zebra" {
		t.Fatalf("common items = %#v", common.Items)
	}

	hasDescription := true
	learning, err := service.List(ctx, ListOptions{
		Statuses:             []domain.LearningStatus{domain.LearningStatusLearning, domain.LearningStatusLearned},
		HasCustomDescription: &hasDescription,
	})
	if err != nil {
		t.Fatalf("List(learning with description) error = %v", err)
	}
	if len(learning.Items) != 1 || learning.Items[0].Term != "bank" {
		t.Fatalf("learning items = %#v", learning.Items)
	}

	firstPage, err := service.List(ctx, ListOptions{Sort: "alphabetical", Limit: 2})
	if err != nil {
		t.Fatalf("first List() error = %v", err)
	}
	if len(firstPage.Items) != 2 || firstPage.NextCursor == "" {
		t.Fatalf("first page = %#v", firstPage)
	}
	secondPage, err := service.List(ctx, ListOptions{
		Sort:   "alphabetical",
		Limit:  2,
		Cursor: firstPage.NextCursor,
	})
	if err != nil {
		t.Fatalf("second List() error = %v", err)
	}
	if len(secondPage.Items) != 1 || secondPage.Items[0].Term != "zebra" {
		t.Fatalf("second page = %#v", secondPage)
	}

	_, err = service.List(ctx, ListOptions{
		Tags:   []string{"common"},
		Sort:   "alphabetical",
		Limit:  2,
		Cursor: firstPage.NextCursor,
	})
	assertApplicationError(t, err, apperr.InvalidArgument)
}

func TestDescriptionSourceRequiresDescriptionAndHTTPURL(t *testing.T) {
	service := newTestService(t, "owner-one")
	ctx := context.Background()

	_, err := service.Save(ctx, "bank", InitialValues{
		DescriptionSource: &domain.DescriptionSource{Title: "External dictionary"},
	})
	assertApplicationError(t, err, apperr.InvalidArgument)

	description := "External definition."
	_, err = service.Save(ctx, "bank", InitialValues{
		CustomDescription: &description,
		DescriptionSource: &domain.DescriptionSource{URL: "ftp://example.test/bank"},
	})
	assertApplicationError(t, err, apperr.InvalidArgument)
}

func TestVocabularyIsOwnerScoped(t *testing.T) {
	store, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	source := storage.SourceVersion{Provider: "cambridge", ParserVersion: 12}
	ownerOne := NewService(store, "owner-one", source)
	ownerTwo := NewService(store, "owner-two", source)

	saved, err := ownerOne.Save(context.Background(), "private phrase", InitialValues{})
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	_, err = ownerTwo.Get(context.Background(), saved.Item.ItemID, "")
	assertApplicationError(t, err, apperr.NotFound)
}

func TestSavedVocabularyLinksCachedLookupAndFollowsRefresh(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	source := storage.SourceVersion{Provider: "cambridge", ParserVersion: 12}
	service := NewService(store, "owner-one", source)
	now := time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)
	firstLookup := insertLookup(t, ctx, store, now, "first definition")

	saved, err := service.Save(ctx, "bank", InitialValues{})
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if saved.Item.Lookup == nil || saved.Item.Lookup.LookupID != firstLookup.ID {
		t.Fatalf("saved lookup = %#v", saved.Item.Lookup)
	}

	secondLookup := insertLookup(t, ctx, store, now.Add(time.Hour), "refreshed definition")
	loaded, err := service.Get(ctx, saved.Item.ItemID, "")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if loaded.Lookup == nil || loaded.Lookup.LookupID != secondLookup.ID {
		t.Fatalf("refreshed saved lookup = %#v", loaded.Lookup)
	}
	if got := loaded.Lookup.Entries[0].Definitions[0].Definition; got != "refreshed definition" {
		t.Fatalf("refreshed definition = %q", got)
	}
}

func TestSaveCreatesSeparateItemsForDictionarySenses(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	_, err = store.InsertDictionarySnapshot(ctx, storage.DictionarySnapshotInsert{
		Provider: "cambridge", NormalizedTerm: "row", ParserVersion: 12,
		Data: domain.DictionarySnapshotData{Status: 200, Entries: []domain.DictionaryEntry{
			{Headword: "row", PartOfSpeech: "noun", Definitions: []domain.DictionaryDefinition{{Definition: "a line of things", Examples: []string{"a row of houses"}}}},
			{Headword: "row", PartOfSpeech: "verb", Definitions: []domain.DictionaryDefinition{{Definition: "to move a boat using oars", Examples: []string{"Row for your life!"}}}},
		}}, FetchedAt: now, ExpiresAt: now.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("InsertDictionarySnapshot() error = %v", err)
	}
	service := NewService(store, "owner", storage.SourceVersion{Provider: "cambridge", ParserVersion: 12})
	line, err := service.Save(ctx, "row", InitialValues{Definition: "a line of things", Context: "objects arranged next to each other"})
	if err != nil {
		t.Fatalf("Save(line) error = %v", err)
	}
	boat, err := service.Save(ctx, "row", InitialValues{Definition: "to move a boat using oars", Context: "boating"})
	if err != nil {
		t.Fatalf("Save(boat) error = %v", err)
	}
	if !line.Created || !boat.Created || line.ItemID == boat.ItemID {
		t.Fatalf("saved senses = line %#v boat %#v", line, boat)
	}
	if line.Lookup.LookupID != boat.Lookup.LookupID {
		t.Fatalf("lookup IDs differ: %q and %q", line.Lookup.LookupID, boat.Lookup.LookupID)
	}
	if boat.Sense == nil || boat.Sense.Definition.Definition != "to move a boat using oars" || boat.Sense.PartOfSpeech != "verb" {
		t.Fatalf("boat sense = %#v", boat.Sense)
	}
	_, err = service.Get(ctx, "", "row")
	assertApplicationError(t, err, apperr.InvalidArgument)
	loadedBoat, err := service.Get(ctx, boat.ItemID, "")
	if err != nil || loadedBoat.Sense.Definition.Definition != "to move a boat using oars" {
		t.Fatalf("Get(boat) = %#v, %v", loadedBoat, err)
	}
}

func newTestService(t *testing.T, ownerKey string) *Service {
	t.Helper()
	store, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return NewService(store, ownerKey, storage.SourceVersion{Provider: "cambridge", ParserVersion: 12})
}

func insertLookup(
	t *testing.T,
	ctx context.Context,
	store *storage.DB,
	now time.Time,
	definition string,
) *storage.DictionarySnapshot {
	t.Helper()
	snapshot, err := store.InsertDictionarySnapshot(ctx, storage.DictionarySnapshotInsert{
		Provider:       "cambridge",
		NormalizedTerm: "bank",
		ParserVersion:  12,
		Data: domain.DictionarySnapshotData{
			Status: 200,
			Entries: []domain.DictionaryEntry{{
				Headword: "bank",
				Definitions: []domain.DictionaryDefinition{{
					Definition: definition,
				}},
			}},
		},
		FetchedAt: now,
		ExpiresAt: now,
	})
	if err != nil {
		t.Fatalf("InsertDictionarySnapshot() error = %v", err)
	}
	return snapshot
}

func assertApplicationError(t *testing.T, err error, code apperr.Code) {
	t.Helper()
	var applicationError *apperr.Error
	if !errors.As(err, &applicationError) || applicationError.Code != code {
		t.Fatalf("error = %v, want %s", err, code)
	}
}

func equalValues(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
