package vocabulary

import (
	"context"
	"reflect"
	"testing"

	"english-learning-mcp/internal/apperr"
	"english-learning-mcp/internal/domain"
)

func TestNULTextCannotCreateOrMutateVocabulary(t *testing.T) {
	service := newTestService(t, "owner")
	ctx := context.Background()
	invalid := "before\x00after"
	description := "A valid description."
	for name, initial := range map[string]InitialValues{
		"tag":          {Tags: []string{invalid}},
		"description":  {CustomDescription: &invalid},
		"source title": {CustomDescription: &description, DescriptionSource: &domain.DescriptionSource{Title: invalid}},
		"source URL":   {CustomDescription: &description, DescriptionSource: &domain.DescriptionSource{URL: "https://example.com/" + invalid}},
		"note":         {Notes: []string{invalid}},
		"example":      {Examples: []string{invalid}},
		"context":      {Context: invalid},
		"definition":   {Definition: invalid},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := service.Save(ctx, "bank", initial)
			assertApplicationError(t, err, apperr.InvalidArgument)
		})
	}
	_, err := service.Save(ctx, invalid, InitialValues{})
	assertApplicationError(t, err, apperr.InvalidArgument)
	items, err := service.List(ctx, ListOptions{})
	if err != nil || len(items.Items) != 0 {
		t.Fatalf("invalid saves persisted: %#v, error %v", items, err)
	}

	saved, err := service.Save(ctx, "bank", InitialValues{CustomDescription: &description, Notes: []string{"Keep this note."}})
	if err != nil {
		t.Fatal(err)
	}
	values := []string{invalid}
	for name, changes := range map[string]UpdateChanges{
		"tags":         {Tags: &values},
		"description":  {CustomDescription: &invalid},
		"source title": {DescriptionSource: &domain.DescriptionSource{Title: invalid}},
		"source URL":   {DescriptionSource: &domain.DescriptionSource{URL: "https://example.com/" + invalid}},
		"notes":        {Notes: &values},
		"examples":     {Examples: &values},
	} {
		t.Run("update/"+name, func(t *testing.T) {
			_, err := service.Update(ctx, saved.ItemID, "", changes)
			assertApplicationError(t, err, apperr.InvalidArgument)
			loaded, err := service.Get(ctx, saved.ItemID, "")
			if err != nil || !reflect.DeepEqual(loaded, saved.VocabularyItem) {
				t.Fatalf("invalid update changed vocabulary: %#v, error %v", loaded, err)
			}
		})
	}
}
