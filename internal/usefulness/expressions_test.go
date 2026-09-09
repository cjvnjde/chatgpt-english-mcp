package usefulness

import (
	"encoding/binary"
	"testing"
)

const noExpressionRanks rankTable = "\x00\x00\x00\x00"

func TestExpressionSourceVotes(t *testing.T) {
	for _, test := range []struct {
		name   string
		entry  expressionEntry
		score  int
		weight int
	}{
		{"dictionary presence abstains", expressionEntry{Wiktionary: true, WordNet: true}, 0, 0},
		{"restricted senses vote low", expressionEntry{Wiktionary: true, Restricted: true}, -1, 1},
		{"labels require their source", expressionEntry{Restricted: true}, 0, 0},
		{"repeated instances are not distinct documents", expressionEntry{IdiomaticDocuments: 9, IdiomaticInstances: 200}, 0, 0},
		{"ten idiomatic documents attest", expressionEntry{IdiomaticDocuments: 10}, 1, 1},
		{"literal observations do not attest idiomatic usage", expressionEntry{LiteralInstances: 200}, 0, 0},
		{"four wordnet tags abstain", expressionEntry{WordNet: true, WordNetTags: 4}, 0, 0},
		{"five wordnet tags attest", expressionEntry{WordNet: true, WordNetTags: 5}, 1, 1},
		{"tags require wordnet source", expressionEntry{WordNetTags: 5}, 0, 0},
		{"independent sources retain disagreement", expressionEntry{Wiktionary: true, Restricted: true, IdiomaticDocuments: 10, WordNet: true, WordNetTags: 5}, 1, 3},
		{"large counts do not increase vote weight", expressionEntry{IdiomaticDocuments: 200, WordNet: true, WordNetTags: 1000}, 2, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			entry := test.entry
			entry.Term = "example expression"
			matcher := newExpressionMatcher(expressionDataset{Entries: []expressionEntry{entry}})
			assertExpressionVotes(t, matcher, entry.Term, noExpressionRanks, test.score, test.weight)
		})
	}
}

func TestExpressionAttestedVariantsAndExplicitTemplates(t *testing.T) {
	matcher := newExpressionMatcher(expressionDataset{
		Entries: []expressionEntry{
			{Term: "spill the beans", InflectFirst: true, IdiomaticDocuments: 10},
			{Term: "a wild-goose chase", Aliases: []string{"a wild goose chase"}, Wiktionary: true, Restricted: true},
			{Term: "give someone the cold shoulder", Aliases: []string{"give somebody the cold shoulder"}, InflectFirst: true, WordNet: true, WordNetTags: 5},
			{Term: "keep one's chin up", InflectFirst: true, IdiomaticDocuments: 10},
			{Term: "mind somebody's business", IdiomaticDocuments: 10},
			{Term: "bob's your uncle", IdiomaticDocuments: 10},
		},
		VerbForms: map[string][]string{
			"spilled": {"spill"},
			"gave":    {"give"},
			"kept":    {"keep"},
		},
	})
	for _, test := range []struct {
		term  string
		score int
	}{
		{"spill the beans", 1},
		{"spilled the beans", 1},
		{"a wild goose chase", -1},
		{"give her the cold shoulder", 1},
		{"gave them the cold shoulder", 1},
		{"keep my chin up", 1},
		{"kept their chin up", 1},
		{"mind your business", 1},
		{"mind someone's business", 1},
	} {
		t.Run(test.term, func(t *testing.T) {
			// Valid inflected words and pronouns are allowed before typo protection.
			assertExpressionVotes(t, matcher, test.term, ranks, test.score, 1)
		})
	}
	for _, term := range []string{
		"spilling the beans", // No supplied verb form, despite plausible morphology.
		"give alice the cold shoulder",
		"give she the cold shoulder",
		"mind her own business",
		"mind alice's business",
		"keep him chin up",
		"my your uncle", // A literal possessive is not a template slot.
		"give her a cold shoulder",
		"the beans spill",
		"spill beans",
		"please spill the beans",
		"spill the beans today",
	} {
		t.Run(term, func(t *testing.T) {
			assertExpressionVotes(t, matcher, term, noExpressionRanks, 0, 0)
		})
	}
}

func TestExpressionLengthBoundAllowsEveryPossessiveSlotExpansion(t *testing.T) {
	for _, test := range []struct {
		template string
		term     string
	}{
		{"one's word", "somebody's word"},
		{"one's word on one's honor", "somebody's word on somebody's honor"},
	} {
		t.Run(test.template, func(t *testing.T) {
			matcher := newExpressionMatcher(expressionDataset{Entries: []expressionEntry{
				{Term: test.template, IdiomaticDocuments: 10},
			}})
			assertExpressionVotes(t, matcher, test.term, noExpressionRanks, 1, 1)
		})
	}
}

func TestExpressionInflectionsRequireVerbalSourcePOS(t *testing.T) {
	matcher := newExpressionMatcher(expressionDataset{
		Entries: []expressionEntry{
			{Term: "bank holiday", Aliases: []string{"bank holidays"}, IdiomaticDocuments: 10},
			{Term: "bank on success", InflectFirst: true, IdiomaticDocuments: 10},
		},
		VerbForms: map[string][]string{"banked": {"bank"}},
	})
	assertExpressionVotes(t, matcher, "bank holiday", noExpressionRanks, 1, 1)
	assertExpressionVotes(t, matcher, "bank holidays", noExpressionRanks, 1, 1)
	assertExpressionVotes(t, matcher, "banked holiday", noExpressionRanks, 0, 0)
	assertExpressionVotes(t, matcher, "banked on success", noExpressionRanks, 1, 1)
}

func TestExpressionAmbiguityAndExactPrecedence(t *testing.T) {
	matcher := newExpressionMatcher(expressionDataset{
		Entries: []expressionEntry{
			{Term: "shared phrase", Wiktionary: true, Restricted: true},
			{Term: "first phrase", Aliases: []string{"shared phrase", "uncertain phrase", "give her credit"}, IdiomaticDocuments: 10},
			{Term: "second phrase", Aliases: []string{"uncertain phrase"}, WordNet: true, WordNetTags: 5},
			{Term: "give someone credit", IdiomaticDocuments: 10},
			{Term: "give somebody credit", Wiktionary: true, Restricted: true},
			{Term: "hang fire", InflectFirst: true, IdiomaticDocuments: 10},
			{Term: "hing fire", InflectFirst: true, Wiktionary: true, Restricted: true},
		},
		VerbForms: map[string][]string{"hung": {"hang", "hing"}},
	})
	assertExpressionVotes(t, matcher, "shared phrase", noExpressionRanks, -1, 1)
	for _, term := range []string{"uncertain phrase", "give him credit", "give her credit", "hung fire"} {
		t.Run(term, func(t *testing.T) {
			assertExpressionVotes(t, matcher, term, noExpressionRanks, 0, 0)
		})
	}
}

func TestExpressionTypoBoundaries(t *testing.T) {
	matcher := newExpressionMatcher(expressionDataset{Entries: []expressionEntry{
		{Term: "silver lining", Wiktionary: true, Restricted: true},
		{Term: "café society", IdiomaticDocuments: 10},
		{Term: "sun dog", IdiomaticDocuments: 10},
		{Term: "solitary", IdiomaticDocuments: 10},
	}})
	for _, test := range []struct {
		term  string
		score int
	}{
		{"silver linxng", -1},  // Substitution.
		{"silver linn ing", 0}, // Token insertion is not a character edit.
		{"silver liniing", -1}, // Insertion.
		{"silver liing", -1},   // Deletion.
		{"silver linign", -1},  // Adjacent transposition.
		{"silver lininx", -1},  // Final character substitution.
		{"silvver lining", -1}, // First token can change too.
		{"cafè society", 1},    // One Unicode rune, not two UTF-8 byte edits.
		{"silver linxxg", 0},
		{"silvver linign", 0},
		{"lining silver", 0},
		{"silver", 0},
		{"silver lining extra", 0},
		{"sun doog", 0}, // Both changed tokens must have at least four runes.
		{"solitxry", 0},
		{"solitary", 0}, // Single-word source records cannot change rank behavior.
		{"entirely unknown expression", 0},
	} {
		t.Run(test.term, func(t *testing.T) {
			weight := 0
			if test.score != 0 {
				weight = 1
			}
			assertExpressionVotes(t, matcher, test.term, noExpressionRanks, test.score, weight)
		})
	}
}

func TestExpressionTypoAmbiguityIncludesUnscoredCandidates(t *testing.T) {
	matcher := newExpressionMatcher(expressionDataset{Entries: []expressionEntry{
		{Term: "cold case", IdiomaticDocuments: 10},
		{Term: "cord case", Wiktionary: true},
	}})
	assertExpressionVotes(t, matcher, "cold case", noExpressionRanks, 1, 1)
	assertExpressionVotes(t, matcher, "coxd case", noExpressionRanks, 0, 0)

	// An ambiguous attested alias must not become a unique fuzzy target.
	matcher = newExpressionMatcher(expressionDataset{Entries: []expressionEntry{
		{Term: "first target", Aliases: []string{"shared alias"}, IdiomaticDocuments: 10},
		{Term: "second target", Aliases: []string{"shared alias"}, Wiktionary: true, Restricted: true},
	}})
	assertExpressionVotes(t, matcher, "sharxd alias", noExpressionRanks, 0, 0)

	// Plausible corrections at different token positions also compete.
	matcher = newExpressionMatcher(expressionDataset{Entries: []expressionEntry{
		{Term: "cold case", IdiomaticDocuments: 10},
		{Term: "cord care", IdiomaticDocuments: 10},
	}})
	assertExpressionVotes(t, matcher, "cord case", noExpressionRanks, 0, 0)
}

func TestExpressionTypoProtectsWordsInEitherRankSource(t *testing.T) {
	matcher := newExpressionMatcher(expressionDataset{Entries: []expressionEntry{
		{Term: "daily bread", IdiomaticDocuments: 10},
	}})
	for _, test := range []struct {
		name           string
		wordfreq       uint32
		frequencywords uint32
		want           int
	}{
		{"wordfreq protection inclusive", 50000, 0, 0},
		{"frequencywords protection inclusive", 0, 50000, 0},
		{"either source protects despite other tail", 50001, 1, 0},
		{"unranked token can be corrected", 0, 0, 1},
		{"long tail is not protected", 50001, 50001, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := binary.LittleEndian.AppendUint32(nil, 1)
			data = binary.LittleEndian.AppendUint32(data, 0)
			data = binary.LittleEndian.AppendUint32(data, test.wordfreq)
			data = binary.LittleEndian.AppendUint32(data, test.frequencywords)
			data = append(data, "break"...)
			assertExpressionVotes(t, matcher, "daily break", rankTable(data), test.want, test.want)
		})
	}
}

func assertExpressionVotes(t *testing.T, matcher *expressionMatcher, term string, rankSources rankTable, wantScore, wantWeight int) {
	t.Helper()
	score, weight := matcher.votes(term, rankSources)
	if score != wantScore || weight != wantWeight {
		t.Fatalf("expression votes for %q = (%d, %d), want (%d, %d)", term, score, weight, wantScore, wantWeight)
	}
}
