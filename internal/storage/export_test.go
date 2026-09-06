package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"english-learning-mcp/internal/domain"
)

func TestExportVocabularyIncludesEveryOwnerItemAndSense(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	definitions := []domain.DictionaryDefinition{
		{Definition: "A financial institution.", Examples: []string{"I visited the bank."}},
		{Definition: "Land beside a river.", Examples: []string{"We sat on the bank."}},
	}
	lookup, err := store.InsertDictionarySnapshot(ctx, DictionarySnapshotInsert{
		Provider: "cambridge", NormalizedTerm: "bank", ParserVersion: 1,
		Data: domain.DictionarySnapshotData{
			Status: 200, SourceURL: "https://dictionary.example/bank",
			Entries: []domain.DictionaryEntry{{
				Headword: "bank", PartOfSpeech: "noun",
				Pronunciations: domain.DictionaryPronunciations{UK: "bæŋk", US: "bæŋk"},
				Definitions:    definitions,
			}},
		},
		FetchedAt: now, ExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := make([]domain.VocabularyItem, 0)
	for index, status := range []domain.LearningStatus{
		domain.LearningStatusNew, domain.LearningStatusLearning, domain.LearningStatusLearned, domain.LearningStatusArchived,
	} {
		entryIndex, definitionIndex := 0, index%2
		_, item, err := store.SaveVocabulary(ctx, VocabularyCreate{
			OwnerKey: "owner:日本語", Term: "bank", NormalizedTerm: "bank", LookupID: lookup.ID,
			Status: status, SenseKey: fmt.Sprintf("sense-%d", index), Context: fmt.Sprintf("Context %d", index),
			SelectedEntryIndex: &entryIndex, SelectedDefinitionIndex: &definitionIndex,
			SelectedDefinition: &definitions[definitionIndex], Tags: []string{"saved tag"},
			CustomDescription: "My description", DescriptionSource: &domain.DescriptionSource{Title: "My source"},
			Notes: []string{"Personal note"}, Examples: []string{"Saved example"}, Now: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, item)
	}
	for index := range 120 {
		item := savePresentationVocabulary(t, store, "owner:日本語", fmt.Sprintf("term-%03d", index), now)
		want = append(want, item)
	}
	savePresentationVocabulary(t, store, "other-owner", "private", now)
	sort.Slice(want, func(left, right int) bool { return want[left].ItemID < want[right].ItemID })

	snapshot, err := store.ExportVocabulary(ctx, "owner:日本語", "english-mcp")
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Complete || snapshot.SchemaVersion != 1 || snapshot.Owner != "owner:日本語" || snapshot.Namespace != "english-mcp" || snapshot.ItemCount != len(want) {
		t.Fatalf("invalid snapshot metadata: %#v", snapshot)
	}
	if len(snapshot.Items) != len(want) {
		t.Fatalf("exported %d items, want %d", len(snapshot.Items), len(want))
	}
	for index, exported := range snapshot.Items {
		if !reflect.DeepEqual(exported.Vocabulary, want[index]) {
			t.Fatalf("item %d = %#v, want %#v", index, exported.Vocabulary, want[index])
		}
		if exported.SourceID != "english-mcp:b3duZXI65pel5pys6Kqe:"+want[index].ItemID {
			t.Fatalf("source identity = %q", exported.SourceID)
		}
	}
	encoded, err := json.Marshal(snapshot.Items)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(encoded)
	if snapshot.Digest != hex.EncodeToString(digest[:]) {
		t.Fatalf("digest = %q, want SHA256 of encoded items", snapshot.Digest)
	}
	repeated, err := store.ExportVocabulary(ctx, "owner:日本語", "english-mcp")
	if err != nil || !reflect.DeepEqual(snapshot, repeated) {
		t.Fatalf("unchanged export was not stable: error %v", err)
	}
}

func TestExportVocabularyEmptyIsAuthoritative(t *testing.T) {
	store, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	savePresentationVocabulary(t, store, "other-owner", "private", time.Now())
	snapshot, err := store.ExportVocabulary(context.Background(), "owner", "english-mcp")
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Complete || snapshot.ItemCount != 0 || snapshot.Items == nil || len(snapshot.Items) != 0 {
		t.Fatalf("empty snapshot is not authoritative: %#v", snapshot)
	}
	if snapshot.Digest != "4f53cda18c2baa0c0354bb5f9a3ecbe5ed12ab4d8e11ba873c2f11161202b945" {
		t.Fatalf("empty snapshot digest = %q", snapshot.Digest)
	}
}

func TestExportVocabularyRefusesCorruptRecords(t *testing.T) {
	for _, corruption := range []string{
		"tags_json = '{'", "notes_json = 'null'", "examples_json = '{}'",
		"created_at = 'invalid'", "selected_definition_json = '{'", "term = ''",
	} {
		t.Run(corruption, func(t *testing.T) {
			ctx := context.Background()
			store, err := Open(ctx, ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			savePresentationVocabulary(t, store, "owner", "valid", time.Now())
			broken := savePresentationVocabulary(t, store, "owner", "broken", time.Now())
			if _, err := store.sql.ExecContext(ctx, "PRAGMA ignore_check_constraints = ON"); err != nil {
				t.Fatal(err)
			}
			if _, err := store.sql.ExecContext(ctx, "UPDATE vocabulary_items SET "+corruption+" WHERE id = ?", broken.ItemID); err != nil {
				t.Fatal(err)
			}
			snapshot, err := store.ExportVocabulary(ctx, "owner", "english-mcp")
			if !errors.Is(err, ErrCorruptData) || snapshot.Complete || snapshot.Items != nil {
				t.Fatalf("corrupt export = %#v, error %v; want no authoritative snapshot", snapshot, err)
			}
			other, err := store.ExportVocabulary(ctx, "other-owner", "english-mcp")
			if err != nil || !other.Complete || other.ItemCount != 0 {
				t.Fatalf("other owner's corruption affected empty export: %#v, %v", other, err)
			}
		})
	}
}

func TestExportVocabularyWorksWithoutDatabaseWriteAccess(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "source.sqlite")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	saved := savePresentationVocabulary(t, store, "owner", "bank", time.Now())
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	uri := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	connection, err := sql.Open("sqlite", uri.String())
	if err != nil {
		t.Fatal(err)
	}
	connection.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = connection.Close() })
	readonly := &DB{sql: connection}
	if _, err := connection.ExecContext(ctx, "DELETE FROM vocabulary_items"); err == nil {
		t.Fatal("read-only fixture allowed database mutation")
	}
	snapshot, err := readonly.ExportVocabulary(ctx, "owner", "english-mcp")
	if err != nil || snapshot.ItemCount != 1 || !reflect.DeepEqual(snapshot.Items[0].Vocabulary, saved) {
		t.Fatalf("read-only export = %#v, error %v", snapshot, err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("export changed the application database")
	}
}
