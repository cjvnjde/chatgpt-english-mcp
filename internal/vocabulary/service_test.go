package vocabulary

import (
	"context"
	"encoding/json"
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
	saved, err := service.Save(ctx, "count clouds before breakfast", initial)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if saved.Item.Usefulness != domain.UsefulnessHigh || initial.Usefulness != domain.UsefulnessHigh {
		t.Fatalf("saved usefulness = %q, input = %#v", saved.Item.Usefulness, initial)
	}
	duplicate, err := service.Save(ctx, "count clouds before breakfast", InitialValues{Usefulness: domain.UsefulnessLow})
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
	preserved, err := service.Update(ctx, "", "count clouds before breakfast", UpdateChanges{Notes: &notes})
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
	normal, err := service.Save(ctx, "compare seven invisible umbrellas", empty)
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

func TestPersonalInterestUpdatesIndependentlyAndRejectsInvalidChanges(t *testing.T) {
	service := newTestService(t, "owner-one")
	ctx := context.Background()
	saved, err := service.Save(ctx, "bank", InitialValues{
		Status: domain.LearningStatusLearned, PersonalInterest: domain.PersonalInterestHigh,
		Notes: []string{"Keep this meaning."}, Context: "A river bank.",
	})
	if err != nil || saved.PersonalInterest != domain.PersonalInterestHigh {
		t.Fatalf("save = %#v, error %v", saved, err)
	}
	low := domain.PersonalInterestLow
	updated, err := service.Update(ctx, saved.ItemID, "", UpdateChanges{PersonalInterest: &low})
	if err != nil || updated.PersonalInterest != low || updated.Usefulness != saved.Usefulness ||
		updated.Status != saved.Status || updated.Context != saved.Context || !equalValues(updated.Notes, saved.Notes) {
		t.Fatalf("interest-only update = %#v, error %v", updated, err)
	}
	notes := []string{"Revised note."}
	updated, err = service.Update(ctx, saved.ItemID, "", UpdateChanges{Notes: &notes})
	if err != nil || updated.PersonalInterest != low || !equalValues(updated.Notes, notes) {
		t.Fatalf("omitted interest update = %#v, error %v", updated, err)
	}
	for _, invalid := range []domain.PersonalInterest{"urgent", "HIGH", ""} {
		if invalid != "" {
			_, err := service.Save(ctx, "invalid", InitialValues{PersonalInterest: invalid})
			assertApplicationError(t, err, apperr.InvalidArgument)
			_, err = service.Get(ctx, "", "invalid")
			assertApplicationError(t, err, apperr.NotFound)
		}
		replacement := []string{"Must not persist."}
		_, err := service.Update(ctx, saved.ItemID, "", UpdateChanges{PersonalInterest: &invalid, Notes: &replacement})
		assertApplicationError(t, err, apperr.InvalidArgument)
		current, err := service.Get(ctx, saved.ItemID, "")
		if err != nil || current.PersonalInterest != low || !equalValues(current.Notes, notes) {
			t.Fatalf("invalid %q changed item = %#v, error %v", invalid, current, err)
		}
	}
	other := NewService(service.store, "other-owner", service.currentSource)
	_, err = other.Update(ctx, saved.ItemID, "", UpdateChanges{PersonalInterest: &low})
	assertApplicationError(t, err, apperr.NotFound)
	normal := domain.PersonalInterestNormal
	reset, err := service.Update(ctx, saved.ItemID, "", UpdateChanges{PersonalInterest: &normal})
	if err != nil || reset.PersonalInterest != normal || reset.Usefulness != saved.Usefulness {
		t.Fatalf("explicit reset = %#v, error %v", reset, err)
	}
	listed, err := service.List(ctx, ListOptions{Limit: 1})
	if err != nil || len(listed.Items) != 1 || listed.Items[0].PersonalInterest != normal {
		t.Fatalf("list = %#v, error %v", listed, err)
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

func TestContextOnlySensesRemainDistinctAndReadable(t *testing.T) {
	service := newTestService(t, "owner")
	ctx := context.Background()
	publicContext := func(item domain.VocabularyItem) string {
		t.Helper()
		encoded, err := json.Marshal(item)
		if err != nil {
			t.Fatal(err)
		}
		var payload struct {
			Context string `json:"context"`
		}
		if err := json.Unmarshal(encoded, &payload); err != nil {
			t.Fatal(err)
		}
		return payload.Context
	}
	finance, err := service.Save(ctx, "bank", InitialValues{Context: "  a financial institution  "})
	if err != nil {
		t.Fatal(err)
	}
	river, err := service.Save(ctx, "bank", InitialValues{Context: "land beside a river"})
	if err != nil {
		t.Fatal(err)
	}
	if !finance.Created || !river.Created || finance.ItemID == river.ItemID {
		t.Fatalf("context senses were merged: %#v, %#v", finance, river)
	}
	duplicate, err := service.Save(ctx, "BANK", InitialValues{Context: "A  financial institution"})
	if err != nil || duplicate.Created || duplicate.ItemID != finance.ItemID {
		t.Fatalf("context retry = %#v, error %v", duplicate, err)
	}
	loaded, err := service.Get(ctx, finance.ItemID, "")
	if err != nil || publicContext(loaded) != "a financial institution" || loaded.Sense != nil {
		t.Fatalf("context-only item = %#v, error %v", loaded, err)
	}
	items, err := service.List(ctx, ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	contexts := map[string]string{finance.ItemID: "a financial institution", river.ItemID: "land beside a river"}
	for _, item := range items.Items {
		if publicContext(item) != contexts[item.ItemID] {
			t.Fatalf("listed meaning was lost: %#v", item)
		}
		delete(contexts, item.ItemID)
	}
	if len(contexts) != 0 {
		t.Fatalf("missing saved senses: %v", contexts)
	}
}

func TestSelectedSenseKeepsOriginalEntryAcrossDictionaryRefresh(t *testing.T) {
	service := newTestService(t, "owner")
	store := service.store.(*storage.DB)
	ctx := context.Background()
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	input := storage.DictionarySnapshotInsert{
		Provider: "cambridge", NormalizedTerm: "bank", ParserVersion: 12,
		FetchedAt: now, ExpiresAt: now,
		Data: domain.DictionarySnapshotData{Status: 200, Entries: []domain.DictionaryEntry{
			{Headword: "bank", PartOfSpeech: "noun", Pronunciations: domain.DictionaryPronunciations{UK: "original"}, Definitions: []domain.DictionaryDefinition{{Definition: "an institution"}}},
			{Headword: "bank", PartOfSpeech: "verb", Definitions: []domain.DictionaryDefinition{{Definition: "an institution"}}},
		}},
	}
	original, err := store.InsertDictionarySnapshot(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := service.Save(ctx, "bank", InitialValues{Definition: "an institution"})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Sense == nil || saved.Sense.PartOfSpeech != "noun" || saved.Sense.Pronunciations.UK != "original" {
		t.Fatalf("saved sense used another entry: %#v", saved.Sense)
	}
	input.Data.Entries = []domain.DictionaryEntry{{Headword: "bank", PartOfSpeech: "verb", Definitions: []domain.DictionaryDefinition{{Definition: "to deposit money"}}}}
	input.FetchedAt = now.Add(time.Hour)
	if _, err := store.InsertDictionarySnapshot(ctx, input); err != nil {
		t.Fatal(err)
	}
	loaded, err := service.Get(ctx, saved.ItemID, "")
	if err != nil || loaded.Lookup == nil || loaded.Lookup.LookupID != original.ID ||
		loaded.Sense == nil || loaded.Sense.PartOfSpeech != "noun" || loaded.Sense.Pronunciations.UK != "original" ||
		loaded.Lookup.Entries[loaded.Sense.EntryIndex].Definitions[loaded.Sense.DefinitionIndex].Definition != "an institution" {
		t.Fatalf("refresh changed selected meaning: %#v, error %v", loaded, err)
	}
}

func TestMalformedUTF8DoesNotCreateOrMutateVocabulary(t *testing.T) {
	service := newTestService(t, "owner")
	ctx := context.Background()
	invalid := string([]byte{0xff})
	description := "A valid description."
	_, err := service.Save(ctx, invalid, InitialValues{})
	assertApplicationError(t, err, apperr.InvalidArgument)
	for name, initial := range map[string]InitialValues{
		"tag":         {Tags: []string{invalid}},
		"description": {CustomDescription: &invalid},
		"source":      {CustomDescription: &description, DescriptionSource: &domain.DescriptionSource{Title: invalid}},
		"note":        {Notes: []string{invalid}},
		"example":     {Examples: []string{invalid}},
		"context":     {Context: invalid},
		"definition":  {Definition: invalid},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := service.Save(ctx, "bank", initial)
			assertApplicationError(t, err, apperr.InvalidArgument)
		})
	}
	items, err := service.List(ctx, ListOptions{})
	if err != nil || len(items.Items) != 0 {
		t.Fatalf("invalid saves persisted: %#v, error %v", items, err)
	}
	saved, err := service.Save(ctx, "bank", InitialValues{Notes: []string{"Keep this note."}})
	if err != nil {
		t.Fatal(err)
	}
	notes := []string{invalid}
	_, err = service.Update(ctx, saved.ItemID, "", UpdateChanges{Notes: &notes})
	assertApplicationError(t, err, apperr.InvalidArgument)
	loaded, err := service.Get(ctx, saved.ItemID, "")
	if err != nil || !equalValues(loaded.Notes, []string{"Keep this note."}) {
		t.Fatalf("invalid update changed notes: %#v, error %v", loaded, err)
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
