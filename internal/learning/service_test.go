package learning

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"english-learning-mcp/internal/apperr"
	"english-learning-mcp/internal/domain"
	"english-learning-mcp/internal/settings"
	"english-learning-mcp/internal/storage"
	fsrs "github.com/open-spaced-repetition/go-fsrs/v4"
)

func TestNextReturnsOneCompactNewItemThenClosestFutureReview(t *testing.T) {
	ctx := context.Background()
	store, service := newTestService(t)
	now := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	description := "Extremely careful about small details."
	created, item, err := store.SaveVocabulary(ctx, storage.VocabularyCreate{
		OwnerKey:          "owner",
		Term:              "meticulous",
		NormalizedTerm:    "meticulous",
		Status:            domain.LearningStatusNew,
		Tags:              []string{},
		CustomDescription: description,
		Notes:             []string{},
		Examples:          []string{"She was meticulous when checking the figures."},
		Now:               now.Add(-time.Hour),
	})
	if err != nil || !created {
		t.Fatalf("SaveVocabulary() = item %#v created %t error %v", item, created, err)
	}

	first, err := service.Next(ctx, false)
	if err != nil {
		t.Fatalf("Next(new) error = %v", err)
	}
	if first.Term != "meticulous" || first.Definition != description || first.Example == "" || first.Reason != "new" {
		t.Fatalf("Next(new) = %#v", first)
	}
	if first.ReviewToken == "" || first.LatestComment != nil || first.Comments != nil || first.Troublesome {
		t.Fatalf("Next(new) metadata = %#v", first)
	}

	now = now.Add(time.Second + time.Nanosecond)
	repeated := nextWord(t, service, false)
	if first.PresentationID <= 0 || repeated.PresentationID == first.PresentationID {
		t.Fatalf("presentations must have distinct identities: first = %#v, repeated = %#v", first, repeated)
	}
	if repeated.Term != first.Term || repeated.ReviewToken != first.ReviewToken {
		t.Fatalf("presenting again must preserve the pending review: first = %#v, repeated = %#v", first, repeated)
	}
	if repeated.ShownAt != storage.TimeString(now) || !mustParseTime(t, repeated.ShownAt).After(mustParseTime(t, first.ShownAt)) {
		t.Fatalf("presentation times must record each issuance: first = %q, repeated = %q", first.ShownAt, repeated.ShownAt)
	}

	review, err := service.Record(ctx, RecordOptions{
		ReviewToken: first.ReviewToken,
		Rating:      domain.ReviewRatingGood,
	})
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if !review.Recorded || review.Duplicate || review.NextReviewAt == "" || review.Troublesome {
		t.Fatalf("Record() = %#v", review)
	}

	closest, err := service.Next(ctx, false)
	if err != nil {
		t.Fatalf("Next(early) error = %v", err)
	}
	if closest.Term != first.Term || closest.Reason != "early" || closest.ReviewToken == first.ReviewToken {
		t.Fatalf("Next(early) = %#v, first = %#v", closest, first)
	}
}

func TestNextIncludesSelectedSenseAndPersonalContext(t *testing.T) {
	ctx := context.Background()
	store, service := newTestService(t)
	now := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	definition := domain.DictionaryDefinition{
		Definition: "a financial institution",
		Examples:   []string{"I went to the bank.", "The bank is closed."},
	}
	snapshot, err := store.InsertDictionarySnapshot(ctx, storage.DictionarySnapshotInsert{
		Provider: "cambridge", NormalizedTerm: "bank", ParserVersion: 12,
		Data: domain.DictionarySnapshotData{Status: 200, Entries: []domain.DictionaryEntry{{
			Headword: "bank", PartOfSpeech: "noun",
			Pronunciations: domain.DictionaryPronunciations{UK: "bæŋk", US: "bæŋk"},
			Definitions:    []domain.DictionaryDefinition{definition, {Definition: "the side of a river"}},
		}}}, FetchedAt: now, ExpiresAt: now.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	index := 0
	_, saved, err := store.SaveVocabulary(ctx, storage.VocabularyCreate{
		OwnerKey: "owner", Term: "bank", NormalizedTerm: "bank", LookupID: snapshot.ID,
		Status: domain.LearningStatusNew, SenseKey: "financial", Context: "money",
		SelectedEntryIndex: &index, SelectedDefinitionIndex: &index, SelectedDefinition: &definition,
		CustomDescription: "Where I keep my money.",
		DescriptionSource: &domain.DescriptionSource{Title: "My textbook", URL: "https://example.test/book"},
		Notes:             []string{"Not the river meaning."}, Tags: []string{"finance"},
		Examples: []string{"My bank offers savings accounts.", "I called my bank."}, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, includeComments := range []bool{false, true} {
		next := nextWord(t, service, includeComments)
		if next.Sense == nil || !reflect.DeepEqual(next.Sense, saved.Sense) ||
			next.Sense.PartOfSpeech != "noun" || next.Sense.Pronunciations.UK != "bæŋk" {
			t.Fatalf("selected dictionary sense lost: %#v", next.Sense)
		}
		if next.Definition != saved.CustomDescription || next.CustomDescription != saved.CustomDescription ||
			!reflect.DeepEqual(next.DescriptionSource, saved.DescriptionSource) ||
			!reflect.DeepEqual(next.Notes, saved.Notes) || !reflect.DeepEqual(next.Tags, saved.Tags) ||
			!reflect.DeepEqual(next.Examples, saved.Examples) || next.Example != saved.Examples[0] {
			t.Fatalf("personal context lost: %#v", next)
		}
		encoded, err := json.Marshal(next)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "the side of a river") || strings.Contains(string(encoded), `"lookup"`) {
			t.Fatalf("response included unrelated dictionary content: %s", encoded)
		}
	}
}

func TestNextChoosesNewVocabularyBeforeEarlyReview(t *testing.T) {
	store, service := newTestService(t)
	now := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	saveVocabulary(t, store, "reviewed", now.Add(-2*time.Hour), domain.LearningStatusNew)
	first := nextWord(t, service, false)
	recordReview(t, service, first.ReviewToken, domain.ReviewRatingEasy, "")
	saveVocabulary(t, store, "unseen", now.Add(-time.Hour), domain.LearningStatusNew)

	next := nextWord(t, service, false)
	if next.Term != "unseen" || next.Reason != "new" {
		t.Fatalf("Next() = %#v, want unseen new vocabulary", next)
	}
}

func TestRecordSupportsAllRatingsWithFSRSScheduling(t *testing.T) {
	now := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	ratings := []domain.ReviewRating{
		domain.ReviewRatingAgain,
		domain.ReviewRatingHard,
		domain.ReviewRatingGood,
		domain.ReviewRatingEasy,
	}
	dueTimes := make([]time.Time, 0, len(ratings))
	for _, rating := range ratings {
		store, service := newTestService(t)
		service.now = func() time.Time { return now }
		saveVocabulary(t, store, string(rating), now.Add(-time.Hour), domain.LearningStatusNew)
		next := nextWord(t, service, false)
		service.now = func() time.Time { return now.Add(2 * time.Minute) }
		result := recordReview(t, service, next.ReviewToken, rating, "")
		dueTimes = append(dueTimes, mustParseTime(t, result.NextReviewAt))
	}
	for index := 1; index < len(dueTimes); index++ {
		if !dueTimes[index].After(dueTimes[index-1]) {
			t.Fatalf("rating due times = %v, want strictly increasing", dueTimes)
		}
	}
}

func TestFastAnswerPromotionBoundaries(t *testing.T) {
	start := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name                     string
		elapsed                  time.Duration
		rating, want             domain.ReviewRating
		repeated, noPresentation bool
	}{
		{"fast good", 29*time.Second + 999*time.Millisecond, domain.ReviewRatingGood, domain.ReviewRatingEasy, false, false},
		{"exact boundary", 30 * time.Second, domain.ReviewRatingGood, domain.ReviewRatingGood, false, false},
		{"slow good", time.Hour, domain.ReviewRatingGood, domain.ReviewRatingGood, false, false},
		{"fast failure", time.Second, domain.ReviewRatingAgain, domain.ReviewRatingAgain, false, false},
		{"fast hard", time.Second, domain.ReviewRatingHard, domain.ReviewRatingHard, false, false},
		{"slow easy", time.Hour, domain.ReviewRatingEasy, domain.ReviewRatingEasy, false, false},
		{"repeated presentation does not restart timer", 31 * time.Second, domain.ReviewRatingGood, domain.ReviewRatingGood, true, false},
		{"backwards clock", -time.Second, domain.ReviewRatingGood, domain.ReviewRatingGood, false, false},
		{"missing presentation", time.Second, domain.ReviewRatingGood, domain.ReviewRatingGood, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, service := newTestService(t)
			now := start
			service.now = func() time.Time { return now }
			createdAt := start.Add(-time.Hour)
			saveVocabulary(t, store, "meticulous", createdAt, domain.LearningStatusNew)
			var token string
			if test.noPresentation {
				page, err := store.AdminRows(context.Background(), "learning_cards", storage.AdminQuery{Limit: 1})
				if err != nil {
					t.Fatal(err)
				}
				token = page.Rows[0]["review_token"].(string)
			} else {
				token = nextWord(t, service, false).ReviewToken
			}
			if test.repeated {
				now = start.Add(20 * time.Second)
				nextWord(t, service, false)
			}
			now = start.Add(test.elapsed)
			result := recordReview(t, service, token, test.rating, "Independent attempt.")
			expected, err := fsrs.NewFSRS(fsrs.DefaultParam()).Next(fsrs.Card{Due: createdAt}, now, toFSRSRating(test.want))
			if err != nil {
				t.Fatal(err)
			}
			if result.Rating != test.want || result.NextReviewAt != storage.TimeString(expected.Card.Due) {
				t.Fatalf("review = %#v; want grade %s and due %s", result, test.want, expected.Card.Due)
			}
			page, err := store.AdminRows(context.Background(), "review_attempts", storage.AdminQuery{Limit: 1})
			if err != nil {
				t.Fatal(err)
			}
			if page.Rows[0]["rating"] != string(test.want) {
				t.Fatalf("saved grade differs: %#v", page.Rows[0])
			}
		})
	}
}

func TestPromotedReviewRetryAndExplicitCorrection(t *testing.T) {
	ctx := context.Background()
	store, service := newTestService(t)
	now := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	saveVocabulary(t, store, "meticulous", now.Add(-time.Hour), domain.LearningStatusNew)
	next := nextWord(t, service, false)
	now = now.Add(10 * time.Second)
	original := recordReview(t, service, next.ReviewToken, domain.ReviewRatingGood, "Clear distinction.")
	if original.Rating != domain.ReviewRatingEasy {
		t.Fatalf("not promoted: %#v", original)
	}
	values := settings.Defaults()
	values.FastAnswerSeconds = 0
	if _, err := store.UpdateAlgorithmSettings(ctx, "owner", values, 0); err != nil {
		t.Fatal(err)
	}
	now = now.Add(4 * time.Hour)
	retry := recordReview(t, service, next.ReviewToken, domain.ReviewRatingGood, "Clear distinction.")
	if !retry.Duplicate || retry.Rating != original.Rating || retry.NextReviewAt != original.NextReviewAt {
		t.Fatalf("retry regraded saved result: %#v", retry)
	}
	_, err := service.Record(ctx, RecordOptions{ReviewToken: next.ReviewToken, Rating: domain.ReviewRatingEasy, Comment: "Clear distinction."})
	assertApplicationCode(t, err, apperr.InvalidArgument)
	values.FastAnswerSeconds = 30
	if _, err := store.UpdateAlgorithmSettings(ctx, "owner", values, 1); err != nil {
		t.Fatal(err)
	}
	corrected, err := service.UpdateLatest(ctx, UpdateOptions{ReviewToken: next.ReviewToken, Rating: domain.ReviewRatingGood})
	if err != nil || corrected.Rating != domain.ReviewRatingGood || corrected.Duplicate ||
		corrected.NextReviewAt != storage.TimeString(mustParseTime(t, next.ShownAt).Add(10*time.Second+10*time.Minute)) {
		t.Fatalf("explicit correction did not undo promotion: %#v, %v", corrected, err)
	}
	retry = recordReview(t, service, next.ReviewToken, domain.ReviewRatingGood, "Clear distinction.")
	if !retry.Duplicate || retry.Rating != domain.ReviewRatingGood || retry.NextReviewAt != corrected.NextReviewAt {
		t.Fatalf("corrected retry = %#v", retry)
	}
}

func TestSchedulingUsesLiveSettings(t *testing.T) {
	ctx := context.Background()
	store, service := newTestService(t)
	now := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	values := settings.Defaults()
	values.FastAnswerSeconds = 5
	values.LearningStepsMinutes = []float64{2, 20}
	values.MaximumIntervalDays = 3
	values.MasteryIntervalDays = 3
	values.MasteryDays = 1
	if _, err := store.UpdateAlgorithmSettings(ctx, "owner", values, 0); err != nil {
		t.Fatal(err)
	}
	saveVocabulary(t, store, "meticulous", now.Add(-time.Hour), domain.LearningStatusNew)
	next := nextWord(t, service, false)
	now = now.Add(5 * time.Second)
	first := recordReview(t, service, next.ReviewToken, domain.ReviewRatingGood, "")
	if first.Rating != domain.ReviewRatingGood || first.NextReviewAt != storage.TimeString(now.Add(20*time.Minute)) {
		t.Fatalf("configured threshold/steps ignored: %#v", first)
	}
	now = mustParseTime(t, first.NextReviewAt)
	next = nextWord(t, service, false)
	now = now.Add(time.Second)
	second := recordReview(t, service, next.ReviewToken, domain.ReviewRatingGood, "")
	if second.Rating != domain.ReviewRatingEasy || second.NextReviewAt != storage.TimeString(now.Add(3*24*time.Hour)) ||
		second.Status != domain.LearningStatusLearned {
		t.Fatalf("configured threshold/interval/mastery ignored: %#v", second)
	}
}

func TestCommentsAndTroublesomeStateGuideLaterReviews(t *testing.T) {
	ctx := context.Background()
	store, service := newTestService(t)
	now := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	saveVocabulary(t, store, "economical", now.Add(-time.Hour), domain.LearningStatusNew)

	first := nextWord(t, service, false)
	firstReview := recordReview(t, service, first.ReviewToken, domain.ReviewRatingAgain, "Confused it with economic.")
	now = mustParseTime(t, firstReview.NextReviewAt)
	second := nextWord(t, service, false)
	if second.LatestComment == nil || second.LatestComment.Text != "Confused it with economic." || second.Comments != nil {
		t.Fatalf("Next(latest comment) = %#v", second)
	}
	secondReview := recordReview(t, service, second.ReviewToken, domain.ReviewRatingAgain, "Could not recall the ending.")
	if !secondReview.Troublesome {
		t.Fatalf("second failed review = %#v", secondReview)
	}

	duplicate := recordReview(t, service, second.ReviewToken, domain.ReviewRatingAgain, "Could not recall the ending.")
	if !duplicate.Duplicate || duplicate.NextReviewAt != secondReview.NextReviewAt {
		t.Fatalf("duplicate review = %#v, first = %#v", duplicate, secondReview)
	}
	_, err := service.Record(ctx, RecordOptions{
		ReviewToken: second.ReviewToken,
		Rating:      domain.ReviewRatingAgain,
		Comment:     "Different retry data.",
	})
	assertApplicationCode(t, err, apperr.InvalidArgument)

	now = mustParseTime(t, secondReview.NextReviewAt)
	withHistory := nextWord(t, service, true)
	if !withHistory.Troublesome || withHistory.Reason != "troublesome" {
		t.Fatalf("Next(troublesome) = %#v", withHistory)
	}
	if withHistory.LatestComment == nil || withHistory.LatestComment.Text != "Could not recall the ending." {
		t.Fatalf("latest troublesome comment = %#v", withHistory.LatestComment)
	}
	if len(withHistory.Comments) != 2 || withHistory.Comments[1].Text != "Confused it with economic." {
		t.Fatalf("all troublesome comments = %#v", withHistory.Comments)
	}
}

func TestLatestCommentUsesChronologicalOrderWithinASecond(t *testing.T) {
	store, service := newTestService(t)
	start := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	now := start
	service.now = func() time.Time { return now }
	saveVocabulary(t, store, "economical", start.Add(-time.Hour), domain.LearningStatusNew)
	offsets := []time.Duration{0, 100 * time.Millisecond, 110 * time.Millisecond, 110*time.Millisecond + time.Nanosecond}
	texts := []string{"First attempt", "Second attempt", "Third attempt", "Latest attempt"}
	for index, offset := range offsets {
		now = start.Add(offset)
		next := nextWord(t, service, false)
		recordReview(t, service, next.ReviewToken, domain.ReviewRatingAgain, texts[index])
	}

	latest := nextWord(t, service, false)
	if latest.LatestComment == nil || latest.LatestComment.Text != texts[len(texts)-1] {
		t.Fatalf("latest comment = %#v", latest.LatestComment)
	}
	history := nextWord(t, service, true)
	if len(history.Comments) != len(texts) {
		t.Fatalf("comment history = %#v", history.Comments)
	}
	for index, comment := range history.Comments {
		if comment.Text != texts[len(texts)-1-index] {
			t.Fatalf("comment %d = %#v; want %q", index, comment, texts[len(texts)-1-index])
		}
	}
}

func TestArchivedVocabularyIsExcludedAndTokenCannotBeReviewed(t *testing.T) {
	ctx := context.Background()
	store, service := newTestService(t)
	now := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	saveVocabulary(t, store, "already archived", now.Add(-2*time.Hour), domain.LearningStatusArchived)
	active := saveVocabulary(t, store, "archive me", now.Add(-time.Hour), domain.LearningStatusNew)
	next := nextWord(t, service, false)
	archived := domain.LearningStatusArchived
	if _, err := store.UpdateVocabulary(ctx, storage.VocabularyUpdate{
		OwnerKey: "owner",
		ItemID:   active.ItemID,
		Status:   &archived,
		Now:      now,
	}); err != nil {
		t.Fatalf("UpdateVocabulary(archived) error = %v", err)
	}

	_, err := service.Next(ctx, false)
	assertApplicationCode(t, err, apperr.NotFound)
	_, err = service.Record(ctx, RecordOptions{
		ReviewToken: next.ReviewToken,
		Rating:      domain.ReviewRatingGood,
	})
	assertApplicationCode(t, err, apperr.InvalidArgument)
}

func TestRecordValidatesCommentLength(t *testing.T) {
	store, service := newTestService(t)
	now := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	saveVocabulary(t, store, "verbose", now, domain.LearningStatusNew)
	next := nextWord(t, service, false)
	_, err := service.Record(context.Background(), RecordOptions{
		ReviewToken: next.ReviewToken,
		Rating:      domain.ReviewRatingGood,
		Comment:     strings.Repeat("x", maximumCommentRunes+1),
	})
	assertApplicationCode(t, err, apperr.InvalidArgument)
}

func TestTutoringContentUsesSelectedSense(t *testing.T) {
	item := domain.VocabularyItem{
		Examples: []string{},
		Sense: &domain.VocabularySense{Definition: domain.DictionaryDefinition{
			Definition: "to move a boat using oars",
			Examples:   []string{"Row for your life!"},
		}},
		Lookup: &domain.DictionaryLookupResult{Entries: []domain.DictionaryEntry{{Definitions: []domain.DictionaryDefinition{{Definition: "a line of things"}}}}},
	}
	definition, example := tutoringContent(item)
	if definition != "to move a boat using oars" || example != "Row for your life!" {
		t.Fatalf("tutoringContent() = %q, %q", definition, example)
	}
}

func TestTutoringContentInfersLegacySenseFromLearnerMetadata(t *testing.T) {
	item := domain.VocabularyItem{
		Tags:     []string{"boats", "verbs"},
		Notes:    []string{"Move a boat through the water using oars."},
		Examples: []string{"Row for your life!"},
		Lookup: &domain.DictionaryLookupResult{Entries: []domain.DictionaryEntry{
			{Definitions: []domain.DictionaryDefinition{{Definition: "a line of things arranged next to each other"}}},
			{Definitions: []domain.DictionaryDefinition{{Definition: "to move a boat through water using oars"}}},
		}},
	}
	definition, example := tutoringContent(item)
	if definition != "to move a boat through water using oars" || example != "Row for your life!" {
		t.Fatalf("tutoringContent() = %q, %q", definition, example)
	}
}

func TestTutoringContentRejectsAmbiguousOrIrrelevantLegacyContext(t *testing.T) {
	for _, test := range []struct {
		name        string
		note        string
		definitions []domain.DictionaryDefinition
		want        string
	}{
		{
			"content words outrank function words", "The edge of the river where we sat.",
			[]domain.DictionaryDefinition{
				{Definition: "the place where the money that you save is kept", Guideword: "money"},
				{Definition: "sloping land beside a river", Guideword: "river"},
			}, "sloping land beside a river",
		},
		{
			"equal evidence requires clarification", "river",
			[]domain.DictionaryDefinition{{Definition: "land beside a river"}, {Definition: "the current in a river"}}, "",
		},
		{
			"irrelevant context does not pick the first sense", "wildlife",
			[]domain.DictionaryDefinition{{Definition: "a place to save money"}, {Definition: "sloping land beside a river"}}, "",
		},
		{
			"function words alone are not sense evidence", "the place where",
			[]domain.DictionaryDefinition{{Definition: "where the money is kept"}, {Definition: "sloping land beside a river"}}, "",
		},
		{
			"a later unique match resolves an earlier tie", "river wildlife",
			[]domain.DictionaryDefinition{{Definition: "a river"}, {Definition: "river current"}, {Definition: "river wildlife"}}, "river wildlife",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			item := domain.VocabularyItem{
				Term: "bank", NormalizedTerm: "bank", Notes: []string{test.note},
				Lookup: &domain.DictionaryLookupResult{Entries: []domain.DictionaryEntry{{Definitions: test.definitions}}},
			}
			definition, example := tutoringContent(item)
			if definition != test.want || example != "" {
				t.Fatalf("legacy content = %q, %q; want %q without borrowed examples", definition, example, test.want)
			}
		})
	}
}

func TestTutoringContentDoesNotUseTargetSpellingAsSenseEvidence(t *testing.T) {
	item := domain.VocabularyItem{
		Term: "well-being", NormalizedTerm: "well-being", Notes: []string{"well being"},
		Lookup: &domain.DictionaryLookupResult{Entries: []domain.DictionaryEntry{{
			Definitions: []domain.DictionaryDefinition{
				{Definition: "feeling well"}, {Definition: "general health"},
			},
		}}},
	}
	if definition, _ := tutoringContent(item); definition != "" {
		t.Fatalf("repeating the target is not evidence for a particular meaning: %q", definition)
	}
}

func TestNextPreservesContextOnlyMeanings(t *testing.T) {
	for _, withLookup := range []bool{false, true} {
		name := "without dictionary"
		if withLookup {
			name = "with dictionary"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			store, service := newTestService(t)
			now := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
			service.now = func() time.Time { return now }
			lookupID := ""
			if withLookup {
				snapshot, err := store.InsertDictionarySnapshot(ctx, storage.DictionarySnapshotInsert{
					Provider: "cambridge", NormalizedTerm: "row", ParserVersion: 12,
					Data: domain.DictionarySnapshotData{Status: 200, Entries: []domain.DictionaryEntry{
						{Definitions: []domain.DictionaryDefinition{
							{Definition: "a line of things", Examples: []string{"a row of houses"}},
							{Definition: "to move a boat using oars", Examples: []string{"She rowed across the lake."}},
						}},
					}}, FetchedAt: now, ExpiresAt: now.Add(24 * time.Hour),
				})
				if err != nil {
					t.Fatal(err)
				}
				lookupID = snapshot.ID
			}
			_, _, err := store.SaveVocabulary(ctx, storage.VocabularyCreate{
				OwnerKey: "owner", Term: "row", NormalizedTerm: "row",
				Status: domain.LearningStatusNew, LookupID: lookupID,
				SenseKey: "context:boat", Context: "boat", Now: now,
			})
			if err != nil {
				t.Fatal(err)
			}
			next := nextWord(t, service, false)
			encoded, err := json.Marshal(next)
			if err != nil {
				t.Fatal(err)
			}
			var response map[string]any
			if err := json.Unmarshal(encoded, &response); err != nil {
				t.Fatal(err)
			}
			if response["context"] != "boat" {
				t.Fatalf("learning response lost the saved meaning context: %s", encoded)
			}
			if withLookup && (next.Definition != "to move a boat using oars" || next.Example != "She rowed across the lake.") {
				t.Fatalf("learning response chose another meaning: %#v", next)
			}
		})
	}
}

func TestTutoringContentDoesNotBorrowExamplesFromAnotherMeaning(t *testing.T) {
	for _, test := range []struct {
		name        string
		description string
		definitions []domain.DictionaryDefinition
		want        string
	}{
		{
			name: "first sense has no example",
			definitions: []domain.DictionaryDefinition{
				{Definition: "a line of things"},
				{Definition: "to move a boat using oars", Examples: []string{"She rowed across the lake."}},
			},
			want: "a line of things",
		},
		{
			name:        "custom meaning has no selected dictionary sense",
			description: "A heated argument.",
			definitions: []domain.DictionaryDefinition{
				{Definition: "to move a boat using oars", Examples: []string{"She rowed across the lake."}},
			},
			want: "A heated argument.",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			item := domain.VocabularyItem{
				CustomDescription: test.description,
				Lookup: &domain.DictionaryLookupResult{Entries: []domain.DictionaryEntry{
					{Definitions: test.definitions},
				}},
			}
			definition, example := tutoringContent(item)
			if definition != test.want || example != "" {
				t.Fatalf("tutoring content = %q, %q; want %q without an unrelated example", definition, example, test.want)
			}
		})
	}
}

func TestTutoringContentRetainsSelectedSenseWithoutLookup(t *testing.T) {
	item := domain.VocabularyItem{
		Sense: &domain.VocabularySense{Definition: domain.DictionaryDefinition{
			Definition: "to move a boat using oars",
			Examples:   []string{"She rowed across the lake."},
		}},
	}
	definition, example := tutoringContent(item)
	if definition != item.Sense.Definition.Definition || example != item.Sense.Definition.Examples[0] {
		t.Fatalf("tutoring content dropped the independently saved sense: %q, %q", definition, example)
	}
}

func newTestService(t *testing.T) (*storage.DB, *Service) {
	t.Helper()
	store, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, NewService(store, "owner")
}

func saveVocabulary(
	t *testing.T,
	store *storage.DB,
	term string,
	now time.Time,
	status domain.LearningStatus,
) domain.VocabularyItem {
	t.Helper()
	created, item, err := store.SaveVocabulary(context.Background(), storage.VocabularyCreate{
		OwnerKey:       "owner",
		Term:           term,
		NormalizedTerm: term,
		Status:         status,
		Tags:           []string{},
		Notes:          []string{},
		Examples:       []string{},
		Now:            now,
	})
	if err != nil {
		t.Fatalf("SaveVocabulary(%q) error = %v", term, err)
	}
	if !created {
		t.Fatalf("SaveVocabulary(%q) created = false", term)
	}
	return item
}

func nextWord(t *testing.T, service *Service, includeComments bool) NextResult {
	t.Helper()
	result, err := service.Next(context.Background(), includeComments)
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	return result
}

func recordReview(
	t *testing.T,
	service *Service,
	reviewToken string,
	rating domain.ReviewRating,
	comment string,
) RecordResult {
	t.Helper()
	result, err := service.Record(context.Background(), RecordOptions{
		ReviewToken: reviewToken,
		Rating:      rating,
		Comment:     comment,
	})
	if err != nil {
		t.Fatalf("Record(%s) error = %v", rating, err)
	}
	return result
}

func mustParseTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatalf("time.Parse(%q) error = %v", value, err)
	}
	return parsed
}

func assertApplicationCode(t *testing.T, err error, code apperr.Code) {
	t.Helper()
	var applicationError *apperr.Error
	if !errors.As(err, &applicationError) || applicationError.Code != code {
		t.Fatalf("error = %v, want application code %s", err, code)
	}
}

func TestUpdateLatestReplaysOriginalFSRSStateAndTime(t *testing.T) {
	for _, mature := range []bool{false, true} {
		t.Run(map[bool]string{false: "new card", true: "mature card"}[mature], func(t *testing.T) {
			ctx := context.Background()
			store, service := newTestService(t)
			controlStore, control := newTestService(t)
			now := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
			service.now = func() time.Time { return now }
			control.now = func() time.Time { return now }
			saveVocabulary(t, store, "recall", now.Add(-time.Hour), domain.LearningStatusNew)
			saveVocabulary(t, controlStore, "recall", now.Add(-time.Hour), domain.LearningStatusNew)
			if mature {
				seed := recordReview(t, service, nextWord(t, service, false).ReviewToken, domain.ReviewRatingEasy, "")
				recordReview(t, control, nextWord(t, control, false).ReviewToken, domain.ReviewRatingEasy, "")
				now = mustParseTime(t, seed.NextReviewAt).Add(48 * time.Hour)
			}
			token := nextWord(t, service, false).ReviewToken
			controlToken := nextWord(t, control, false).ReviewToken
			now = now.Add(time.Minute)
			recordReview(t, service, token, domain.ReviewRatingGood, "Confused the meaning.")
			want := recordReview(t, control, controlToken, domain.ReviewRatingAgain, "Confused the meaning.")
			pending := nextWord(t, service, false)
			// Correction time must not become a second review time.
			now = now.Add(48 * time.Hour)
			got, err := service.UpdateLatest(ctx, UpdateOptions{ReviewToken: token, Rating: domain.ReviewRatingAgain})
			if err != nil || got != want {
				t.Fatalf("corrected result = %#v, %v; direct again = %#v", got, err, want)
			}
			candidate, err := store.NextLearningItem(ctx, "owner", service.now)
			if err != nil {
				t.Fatal(err)
			}
			expected, err := controlStore.NextLearningItem(ctx, "owner", control.now)
			if err != nil {
				t.Fatal(err)
			}
			gotCard, err := toFSRSCard(candidate.Card)
			if err != nil {
				t.Fatal(err)
			}
			wantCard, err := toFSRSCard(expected.Card)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(gotCard, wantCard) || candidate.Card.ConsecutiveFailures != expected.Card.ConsecutiveFailures {
				t.Fatalf("correction must match one direct again: got %#v; want %#v", candidate.Card, expected.Card)
			}
			if candidate.Card.ReviewToken != pending.ReviewToken {
				t.Fatal("correction invalidated the already presented next review")
			}
			comments, err := store.ReviewComments(ctx, "owner", candidate.Card.VocabularyItemID, true)
			if err != nil || len(comments) != 1 || comments[0].Rating != domain.ReviewRatingAgain || comments[0].Comment != "Confused the meaning." {
				t.Fatalf("corrected comment history = %#v, %v", comments, err)
			}
			retry, err := service.UpdateLatest(ctx, UpdateOptions{ReviewToken: token, Rating: domain.ReviewRatingAgain})
			if err != nil || !retry.Duplicate || retry.NextReviewAt != want.NextReviewAt {
				t.Fatalf("correction retry = %#v, %v", retry, err)
			}
			empty := ""
			_, err = service.UpdateLatest(ctx, UpdateOptions{ReviewToken: token, Rating: domain.ReviewRatingAgain, Comment: &empty})
			if err != nil {
				t.Fatal(err)
			}
			comments, err = store.ReviewComments(ctx, "owner", candidate.Card.VocabularyItemID, true)
			if err != nil || len(comments) != 0 {
				t.Fatalf("explicit empty comment did not clear feedback: %#v, %v", comments, err)
			}
			// A later real answer uses the corrected state, not the abandoned good schedule.
			now = now.Add(time.Minute) // Both presentations are outside the fast-answer window.
			next := recordReview(t, service, pending.ReviewToken, domain.ReviewRatingGood, "")
			controlNext := recordReview(t, control, expected.Card.ReviewToken, domain.ReviewRatingGood, "")
			if next != controlNext {
				t.Fatalf("subsequent schedule diverged: got %#v; want %#v", next, controlNext)
			}
			_, err = service.UpdateLatest(ctx, UpdateOptions{ReviewToken: token, Rating: domain.ReviewRatingAgain})
			assertApplicationCode(t, err, apperr.InvalidArgument)
		})
	}
}

func TestDailyMasteryRequiresFiveFlawlessSessions(t *testing.T) {
	ctx := context.Background()
	store, service := newTestService(t)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	item := saveVocabulary(t, store, "resilient", now, domain.LearningStatusNew)
	var fifth NextResult
	for session := uint64(1); session <= 5; session++ {
		next := nextWord(t, service, false)
		result := recordReview(t, service, next.ReviewToken, domain.ReviewRatingEasy, "")
		wantStatus := domain.LearningStatusLearning
		if session == 5 {
			wantStatus = domain.LearningStatusLearned
			fifth = next
		}
		current, err := store.VocabularyByID(ctx, "owner", item.ItemID)
		if err != nil {
			t.Fatal(err)
		}
		if result.MasteryStreak != session || result.Status != wantStatus || current.Status != wantStatus {
			t.Fatalf("session %d: result=%#v saved status=%s", session, result, current.Status)
		}
		if session < 5 {
			// Follow the real FSRS schedule; no fabricated successful history.
			now = mustParseTime(t, result.NextReviewAt)
		}
	}
	corrected, err := service.UpdateLatest(ctx, UpdateOptions{ReviewToken: fifth.ReviewToken, Rating: domain.ReviewRatingAgain})
	if err != nil || corrected.Status != domain.LearningStatusLearning || corrected.MasteryStreak != 0 {
		t.Fatalf("corrected failure: %#v, %v", corrected, err)
	}
	restored, err := service.UpdateLatest(ctx, UpdateOptions{ReviewToken: fifth.ReviewToken, Rating: domain.ReviewRatingEasy})
	if err != nil || restored.Status != domain.LearningStatusLearned || restored.MasteryStreak != 5 {
		t.Fatalf("restored fifth success: %#v, %v", restored, err)
	}
	retry := recordReview(t, service, fifth.ReviewToken, domain.ReviewRatingEasy, "")
	if !retry.Duplicate || retry.Status != restored.Status || retry.MasteryStreak != restored.MasteryStreak {
		t.Fatalf("retry counted an extra session: %#v", retry)
	}
	pending := nextWord(t, service, false)
	failed := recordReview(t, service, pending.ReviewToken, domain.ReviewRatingAgain, "")
	if failed.Status != domain.LearningStatusLearning || failed.MasteryStreak != 0 {
		t.Fatalf("learned word did not return to learning: %#v", failed)
	}
}

func TestDailyMasteryCountsSuccessOnceButEveryFailure(t *testing.T) {
	ctx := context.Background()
	store, service := newTestService(t)
	now := time.Date(2026, 9, 1, 23, 59, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	saveVocabulary(t, store, "persistent", now, domain.LearningStatusNew)
	for _, rating := range []domain.ReviewRating{domain.ReviewRatingGood, domain.ReviewRatingEasy, domain.ReviewRatingGood} {
		next := nextWord(t, service, false)
		result := recordReview(t, service, next.ReviewToken, rating, "")
		if result.MasteryStreak != 1 || result.Status != domain.LearningStatusLearning {
			t.Fatalf("same UTC day counted more than once: %#v", result)
		}
	}
	for range 2 {
		next := nextWord(t, service, false)
		result := recordReview(t, service, next.ReviewToken, domain.ReviewRatingAgain, "")
		if result.MasteryStreak != 0 {
			t.Fatalf("failure did not reset mastery: %#v", result)
		}
	}
	candidate, err := store.NextLearningItem(ctx, "owner", service.now)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Card.ConsecutiveFailures != 2 || candidate.Card.Repetitions != 5 {
		t.Fatalf("same-day failures must both update FSRS: %#v", candidate.Card)
	}
	recovered := recordReview(t, service, candidate.Card.ReviewToken, domain.ReviewRatingEasy, "")
	if recovered.MasteryStreak != 0 {
		t.Fatalf("recovery restored already-consumed daily credit: %#v", recovered)
	}
	now = now.Add(time.Minute) // A new UTC calendar day, not a rolling 24-hour window.
	next := nextWord(t, service, false)
	nextDay := recordReview(t, service, next.ReviewToken, domain.ReviewRatingGood, "")
	if nextDay.MasteryStreak != 1 {
		t.Fatalf("new UTC session did not count: %#v", nextDay)
	}
	hard := recordReview(t, service, nextWord(t, service, false).ReviewToken, domain.ReviewRatingHard, "")
	if hard.MasteryStreak != 0 {
		t.Fatalf("effortful recall counted as flawless: %#v", hard)
	}
	recovered = recordReview(t, service, nextWord(t, service, false).ReviewToken, domain.ReviewRatingGood, "")
	if recovered.MasteryStreak != 0 {
		t.Fatalf("same-day success after hard restored daily credit: %#v", recovered)
	}
}
