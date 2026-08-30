package explanation

import (
	"context"
	"errors"
	"testing"
	"time"

	"english-learning-mcp/internal/apperr"
	"english-learning-mcp/internal/domain"
	"english-learning-mcp/internal/storage"
	"english-learning-mcp/internal/vocabulary"
)

func TestExplanationCacheIsContextSpecificAndSeparateFromVocabulary(t *testing.T) {
	store := openTestStore(t)
	source := storage.SourceVersion{Provider: "cambridge", ParserVersion: 12}
	snapshot := insertBankSnapshot(t, store, source, time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC))
	service := NewService(store, "owner-one", source)
	now := time.Date(2026, time.August, 30, 13, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }

	written, err := service.Upsert(context.Background(), UpsertValue{
		Term:     " Bank ",
		Context:  "  I sat on the bank   of the river. ",
		LookupID: snapshot.ID,
		SelectedMeaning: &SelectedMeaningInput{
			EntryIndex:      0,
			DefinitionIndex: 0,
		},
		Learner: LearnerInput{
			Description: "Land beside a river.",
			Notes:       []string{"Used for rivers", " used  for rivers "},
			Examples:    []string{"They picnicked on the bank."},
		},
		CEFR: &domain.CEFR{Level: domain.CEFRLevelB1, Source: "dictionary"},
		LexicalRelations: &LexicalRelationsInput{
			Synonyms: []string{"shore", "Shore"},
			Antonyms: []string{},
			Source:   "mixed",
		},
		Generator: domain.Generator{Name: "chatgpt", Model: "gpt-test", Version: "english-explanation-v1"},
	})
	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if !written.Created {
		t.Fatal("Upsert() created = false, want true")
	}
	if written.Explanation.SelectedMeaning == nil || written.Explanation.SelectedMeaning.Definition != "land beside a river" {
		t.Fatalf("selected meaning = %#v", written.Explanation.SelectedMeaning)
	}
	if len(written.Explanation.Learner.Notes) != 1 || len(written.Explanation.LexicalRelations.Synonyms) != 1 {
		t.Fatalf("normalized explanation = %#v", written.Explanation)
	}
	if written.Explanation.Context != "I sat on the bank of the river." {
		t.Fatalf("context = %q", written.Explanation.Context)
	}

	vocabularyService := vocabulary.NewService(store, "owner-one", source)
	vocabularyList, err := vocabularyService.List(context.Background(), vocabulary.ListOptions{})
	if err != nil {
		t.Fatalf("vocabulary List() error = %v", err)
	}
	if len(vocabularyList.Items) != 0 {
		t.Fatalf("explanation implicitly saved vocabulary: %#v", vocabularyList.Items)
	}

	cached, err := service.Get(context.Background(), GetOptions{
		Term:    "bank",
		Context: "I sat on the bank of the river.",
		Generator: &GeneratorKey{
			Name:    "chatgpt",
			Version: "english-explanation-v1",
		},
	})
	if err != nil || !cached.Found || cached.Explanation.ExplanationID != written.Explanation.ExplanationID {
		t.Fatalf("Get() = %#v, error = %v", cached, err)
	}

	contextMiss, err := service.Get(context.Background(), GetOptions{
		Term:    "bank",
		Context: "I visited the bank to deposit money.",
		Generator: &GeneratorKey{
			Name:    "chatgpt",
			Version: "english-explanation-v1",
		},
	})
	if err != nil || contextMiss.Found || contextMiss.Reason != "not_cached" {
		t.Fatalf("context-specific Get() = %#v, error = %v", contextMiss, err)
	}

	now = now.Add(time.Minute)
	updated, err := service.Upsert(context.Background(), UpsertValue{
		Term:     "bank",
		Context:  "I sat on the bank of the river.",
		LookupID: snapshot.ID,
		SelectedMeaning: &SelectedMeaningInput{
			EntryIndex:      0,
			DefinitionIndex: 0,
		},
		Learner:   LearnerInput{Description: "The ground at the edge of a river."},
		Generator: domain.Generator{Name: "chatgpt", Version: "english-explanation-v1"},
	})
	if err != nil {
		t.Fatalf("update Upsert() error = %v", err)
	}
	if updated.Created || updated.Explanation.ExplanationID != written.Explanation.ExplanationID {
		t.Fatalf("update Upsert() = %#v", updated)
	}
}

func TestRefreshMakesExplanationStaleWithoutDeletingIt(t *testing.T) {
	store := openTestStore(t)
	source := storage.SourceVersion{Provider: "cambridge", ParserVersion: 12}
	firstSnapshot := insertBankSnapshot(t, store, source, time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC))
	service := NewService(store, "owner-one", source)

	written, err := service.Upsert(context.Background(), validBankUpsert(firstSnapshot.ID))
	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	insertBankSnapshot(t, store, source, time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC))

	defaultGet, err := service.Get(context.Background(), GetOptions{
		ExplanationID: written.Explanation.ExplanationID,
	})
	if err != nil || defaultGet.Found || defaultGet.Reason != "stale_only" {
		t.Fatalf("default stale Get() = %#v, error = %v", defaultGet, err)
	}
	staleGet, err := service.Get(context.Background(), GetOptions{
		ExplanationID: written.Explanation.ExplanationID,
		IncludeStale:  true,
	})
	if err != nil || !staleGet.Found || !staleGet.Explanation.Stale {
		t.Fatalf("included stale Get() = %#v, error = %v", staleGet, err)
	}

	_, err = service.Upsert(context.Background(), validBankUpsert(firstSnapshot.ID))
	var applicationError *apperr.Error
	if !errors.As(err, &applicationError) || applicationError.Code != apperr.StaleLookup {
		t.Fatalf("stale Upsert() error = %v, want STALE_LOOKUP", err)
	}
}

func TestExplanationValidatesSelectedMeaningAndDictionaryCEFR(t *testing.T) {
	store := openTestStore(t)
	source := storage.SourceVersion{Provider: "cambridge", ParserVersion: 12}
	snapshot := insertBankSnapshot(t, store, source, time.Now())
	service := NewService(store, "owner-one", source)

	value := validBankUpsert(snapshot.ID)
	value.SelectedMeaning = nil
	_, err := service.Upsert(context.Background(), value)
	assertApplicationCode(t, err, apperr.InvalidArgument)

	value = validBankUpsert(snapshot.ID)
	value.CEFR = &domain.CEFR{Level: domain.CEFRLevelA1, Source: "dictionary"}
	_, err = service.Upsert(context.Background(), value)
	assertApplicationCode(t, err, apperr.InvalidArgument)
}

func openTestStore(t *testing.T) *storage.DB {
	t.Helper()
	store, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return store
}

func insertBankSnapshot(t *testing.T, store *storage.DB, source storage.SourceVersion, fetchedAt time.Time) *storage.DictionarySnapshot {
	t.Helper()
	snapshot, err := store.InsertDictionarySnapshot(context.Background(), storage.DictionarySnapshotInsert{
		Provider:       source.Provider,
		NormalizedTerm: "bank",
		ParserVersion:  source.ParserVersion,
		DatasetVersion: source.DatasetVersion,
		Data: domain.DictionarySnapshotData{
			SourceURL: "https://dictionary.example/bank",
			Status:    200,
			Entries: []domain.DictionaryEntry{{
				Headword:     "bank",
				PartOfSpeech: "noun",
				Definitions: []domain.DictionaryDefinition{{
					Definition: "land beside a river",
					Examples:   []string{"We sat on the bank."},
					Phrases:    []string{},
					SeeAlso:    []string{},
					Images:     []domain.DictionaryImage{},
					Labels:     []string{"B1"},
				}},
			}},
			Suggestions: []string{},
			Images:      []domain.DictionaryImage{},
		},
		FetchedAt: fetchedAt,
		ExpiresAt: fetchedAt.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("InsertDictionarySnapshot() error = %v", err)
	}
	return snapshot
}

func validBankUpsert(lookupID string) UpsertValue {
	return UpsertValue{
		Term:     "bank",
		Context:  "I sat on the bank of the river.",
		LookupID: lookupID,
		SelectedMeaning: &SelectedMeaningInput{
			EntryIndex:      0,
			DefinitionIndex: 0,
		},
		Learner:   LearnerInput{Description: "Land beside a river."},
		Generator: domain.Generator{Name: "chatgpt", Version: "english-explanation-v1"},
	}
}

func assertApplicationCode(t *testing.T, err error, code apperr.Code) {
	t.Helper()
	var applicationError *apperr.Error
	if !errors.As(err, &applicationError) || applicationError.Code != code {
		t.Fatalf("error = %v, want %s", err, code)
	}
}
