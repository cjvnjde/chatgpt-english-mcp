package vocabulary

import (
	"context"
	"errors"
	"testing"
	"time"

	"english-learning-mcp/internal/apperr"
	"english-learning-mcp/internal/storage"
)

func TestSaveIsIdempotentAndListUsesBoundCursor(t *testing.T) {
	store, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})

	service := NewService(store, "owner-one", storage.SourceVersion{
		Provider:      "cambridge",
		ParserVersion: 12,
	})
	now := time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }

	first, err := service.Save(context.Background(), "  Bank  ")
	if err != nil {
		t.Fatalf("Save(bank) error = %v", err)
	}
	if !first.Created || first.Item.Term != "Bank" || first.Item.NormalizedTerm != "bank" {
		t.Fatalf("first save = %#v", first)
	}

	now = now.Add(time.Minute)
	duplicate, err := service.Save(context.Background(), "bank")
	if err != nil {
		t.Fatalf("duplicate Save(bank) error = %v", err)
	}
	if duplicate.Created || duplicate.Item.ItemID != first.Item.ItemID || duplicate.Item.Term != "Bank" {
		t.Fatalf("duplicate save = %#v", duplicate)
	}

	for _, term := range []string{"apple", "zebra"} {
		now = now.Add(time.Minute)
		if _, err := service.Save(context.Background(), term); err != nil {
			t.Fatalf("Save(%s) error = %v", term, err)
		}
	}

	firstPage, err := service.List(context.Background(), ListOptions{Sort: "alphabetical", Limit: 2})
	if err != nil {
		t.Fatalf("first List() error = %v", err)
	}
	if len(firstPage.Items) != 2 || firstPage.Items[0].Term != "apple" || firstPage.Items[1].Term != "Bank" || firstPage.NextCursor == "" {
		t.Fatalf("first page = %#v", firstPage)
	}

	secondPage, err := service.List(context.Background(), ListOptions{
		Sort:   "alphabetical",
		Limit:  2,
		Cursor: firstPage.NextCursor,
	})
	if err != nil {
		t.Fatalf("second List() error = %v", err)
	}
	if len(secondPage.Items) != 1 || secondPage.Items[0].Term != "zebra" || secondPage.NextCursor != "" {
		t.Fatalf("second page = %#v", secondPage)
	}

	_, err = service.List(context.Background(), ListOptions{
		Query:  "bank",
		Sort:   "alphabetical",
		Limit:  2,
		Cursor: firstPage.NextCursor,
	})
	var applicationError *apperr.Error
	if !errors.As(err, &applicationError) || applicationError.Code != apperr.InvalidArgument {
		t.Fatalf("List() cursor/filter error = %v, want INVALID_ARGUMENT", err)
	}
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

	saved, err := ownerOne.Save(context.Background(), "private phrase")
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	_, err = ownerTwo.Get(context.Background(), saved.Item.ItemID, "")
	var applicationError *apperr.Error
	if !errors.As(err, &applicationError) || applicationError.Code != apperr.NotFound {
		t.Fatalf("other owner Get() error = %v, want NOT_FOUND", err)
	}
}
