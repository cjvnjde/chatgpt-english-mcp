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
