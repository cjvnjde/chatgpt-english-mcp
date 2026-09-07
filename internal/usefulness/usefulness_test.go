package usefulness

import (
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"strings"
	"testing"

	"english-learning-mcp/internal/domain"
)

func TestRankBoundariesAndAbsentSource(t *testing.T) {
	for _, test := range []struct {
		name string
		rank uint32
		want domain.Usefulness
	}{
		{"missing abstains", 0, domain.UsefulnessNormal},
		{"first rank", 1, domain.UsefulnessHigh},
		{"last high rank", 1000, domain.UsefulnessHigh},
		{"first normal rank", 1001, domain.UsefulnessNormal},
		{"last normal rank", 5000, domain.UsefulnessNormal},
		{"first low rank", 5001, domain.UsefulnessLow},
		{"full long tail", 1656996, domain.UsefulnessLow},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := estimateRanks(test.rank, 0, "", 0, 0); got != test.want {
				t.Fatalf("wordfreq rank %d = %q, want %q", test.rank, got, test.want)
			}
			if got := estimateRanks(0, test.rank, "", 0, 0); got != test.want {
				t.Fatalf("FrequencyWords rank %d = %q, want %q", test.rank, got, test.want)
			}
		})
	}
}

func TestWeightedCorpusAndHintVotes(t *testing.T) {
	for _, test := range []struct {
		name           string
		wordfreq       uint32
		frequencywords uint32
		hint           domain.Usefulness
		want           domain.Usefulness
	}{
		{"unknown hint high", 0, 0, domain.UsefulnessHigh, domain.UsefulnessHigh},
		{"unknown hint normal", 0, 0, domain.UsefulnessNormal, domain.UsefulnessNormal},
		{"unknown hint low", 0, 0, domain.UsefulnessLow, domain.UsefulnessLow},
		{"opposing corpora", 1, 5001, "", domain.UsefulnessNormal},
		{"two high balance low hint", 1, 1000, domain.UsefulnessLow, domain.UsefulnessNormal},
		{"two high overcome normal hint", 1, 1000, domain.UsefulnessNormal, domain.UsefulnessHigh},
		{"two low balance high hint", 5001, 6000, domain.UsefulnessHigh, domain.UsefulnessNormal},
		{"two low overcome normal hint", 5001, 6000, domain.UsefulnessNormal, domain.UsefulnessLow},
		{"positive third is inclusive", 1, 0, domain.UsefulnessNormal, domain.UsefulnessNormal},
		{"negative third is inclusive", 0, 5001, domain.UsefulnessNormal, domain.UsefulnessNormal},
		{"opposing hint reaches positive third", 5001, 0, domain.UsefulnessHigh, domain.UsefulnessNormal},
		{"opposing hint reaches negative third", 0, 1, domain.UsefulnessLow, domain.UsefulnessNormal},
		{"hint resolves corpus disagreement", 1, 5001, domain.UsefulnessHigh, domain.UsefulnessHigh},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := estimateRanks(test.wordfreq, test.frequencywords, test.hint, 0, 0)
			if got != test.want {
				t.Fatalf("estimateRanks(%d, %d, %q) = %q, want %q", test.wordfreq, test.frequencywords, test.hint, got, test.want)
			}
		})
	}
}

func TestEstimateUsesFullBundledCorpora(t *testing.T) {
	for _, test := range []struct {
		term string
		hint domain.Usefulness
		want domain.Usefulness
	}{
		{"the", "", domain.UsefulnessHigh},
		{"apple", "", domain.UsefulnessNormal},
		{"meticulous", "", domain.UsefulnessLow},
		{"sesquipedalian", "", domain.UsefulnessLow},
		{"so-called", "", domain.UsefulnessLow},
		{"so-called", domain.UsefulnessNormal, domain.UsefulnessNormal},
		{"don't", "", domain.UsefulnessHigh},
		{"don't", domain.UsefulnessNormal, domain.UsefulnessNormal},
		{"zzzxqvnotaword", "", domain.UsefulnessNormal},
		{"zzzxqvnotaword", domain.UsefulnessLow, domain.UsefulnessLow},
	} {
		t.Run(test.term+"/"+string(test.hint), func(t *testing.T) {
			if got := Estimate(test.term, test.hint); got != test.want {
				t.Fatalf("Estimate(%q, %q) = %q, want %q", test.term, test.hint, got, test.want)
			}
		})
	}
}

func TestEstimateNeverApproximatesPhrasesFromTokens(t *testing.T) {
	for _, term := range []string{"the and", "sesquipedalian defenestration"} {
		if got := Estimate(term, ""); got != domain.UsefulnessNormal {
			t.Fatalf("Estimate(%q) = %q; absent full phrases must abstain", term, got)
		}
		if got := Estimate(term, domain.UsefulnessHigh); got != domain.UsefulnessHigh {
			t.Fatalf("Estimate(%q, high) = %q; absent full phrases must leave the hint alone", term, got)
		}
	}
}

func TestRevisionTracksBundledRankContent(t *testing.T) {
	// A stale revision would skip persisted-value recomputation after a refresh.
	digest := sha256.Sum256([]byte(ranks))
	if !strings.Contains(Revision, ":"+fmt.Sprintf("%x", digest)+":") {
		t.Fatalf("Revision %q does not identify the bundled rank content", Revision)
	}
}

func TestRevisionTracksBundledExpressionContent(t *testing.T) {
	reader, err := gzip.NewReader(strings.NewReader(compressedExpressions))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, reader); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(Revision, ":"+fmt.Sprintf("%x", digest.Sum(nil))) {
		t.Fatalf("Revision %q does not identify the bundled expression content", Revision)
	}
}

func TestNormalizedEstimateDoesNotAllocate(t *testing.T) {
	allocations := testing.AllocsPerRun(100, func() {
		Estimate("meticulous", domain.UsefulnessNormal)
		Estimate("new york", "")
		Estimate("spill the beasn", "")
	})
	if allocations != 0 {
		t.Fatalf("normalized exact lookups allocate %g times, want zero", allocations)
	}
}

func TestExpressionRegionalAndDomainLabelsDoNotImplyFrequency(t *testing.T) {
	for _, term := range []string{"nasal cannula", "high court"} {
		if got := Estimate(term, ""); got != domain.UsefulnessNormal {
			t.Fatalf("regional or domain 'common' label promoted %q to %q", term, got)
		}
	}
}
