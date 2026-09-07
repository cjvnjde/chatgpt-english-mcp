package usefulness

import (
	"compress/gzip"
	_ "embed"
	"encoding/binary"
	"io"
	"strings"

	"english-learning-mcp/internal/domain"
)

// Revision changes when either dataset or the matching/scoring policy changes.
// Increment the policy version whenever rank thresholds, matching or votes change.
const Revision = "rank-v1-weighted-1-1-2-thirds-v1-expressions-v1:" + datasetRevision + ":" + expressionDatasetRevision

//go:embed assets/ranks.bin.gz
var compressedRanks string

var ranks = loadRanks()

// Estimate combines exact word ranks, matched expression evidence and an API hint.
// normalizedTerm must already be normalized by domain.NormalizeTerm; a nonempty
// hint must be a valid domain.Usefulness. Missing sources abstain independently.
func Estimate(normalizedTerm string, hint domain.Usefulness) domain.Usefulness {
	wordfreqRank, frequencywordsRank := ranks.lookup(normalizedTerm)
	expressionScore, expressionWeight := expressionVotes(normalizedTerm)
	return estimateRanks(wordfreqRank, frequencywordsRank, hint, expressionScore, expressionWeight)
}

func estimateRanks(wordfreqRank, frequencywordsRank uint32, hint domain.Usefulness, expressionScore, expressionWeight int) domain.Usefulness {
	score, weight := rankVote(wordfreqRank)
	frequencywordsScore, frequencywordsWeight := rankVote(frequencywordsRank)
	score += frequencywordsScore + expressionScore
	weight += frequencywordsWeight + expressionWeight

	switch hint {
	case domain.UsefulnessHigh:
		score += 2
		weight += 2
	case domain.UsefulnessNormal:
		weight += 2
	case domain.UsefulnessLow:
		score -= 2
		weight += 2
	}

	// Integer comparisons keep both +/-1/3 boundaries in the normal band.
	// With no votes both comparisons are false, also yielding normal.
	if 3*score > weight {
		return domain.UsefulnessHigh
	}
	if 3*score < -weight {
		return domain.UsefulnessLow
	}
	return domain.UsefulnessNormal
}

func rankVote(rank uint32) (score, weight int) {
	switch {
	case rank == 0:
		return 0, 0
	case rank <= 1000:
		return 1, 1
	case rank <= 5000:
		return 0, 1
	default:
		return -1, 1
	}
}

// rankTable stores sorted exact terms and both independent ranks in one immutable
// string. Binary search avoids millions of map entries and request-time copying.
// The generator writes a uint32 count, 12-byte records (text offset and two
// ranks), then concatenated UTF-8 terms. All integers are little-endian.
type rankTable string

func loadRanks() rankTable {
	reader, err := gzip.NewReader(strings.NewReader(compressedRanks))
	if err != nil {
		panic("usefulness: opening embedded ranks: " + err.Error())
	}
	defer reader.Close()
	// The gzip trailer records the decoded size. The generated format already
	// requires uint32-sized offsets, so reserve it once without a final copy.
	var data strings.Builder
	size := binary.LittleEndian.Uint32([]byte(compressedRanks[len(compressedRanks)-4:]))
	data.Grow(int(size))
	_, err = io.Copy(&data, reader)
	if err != nil {
		panic("usefulness: loading embedded ranks: " + err.Error())
	}
	return rankTable(data.String())
}

func (table rankTable) uint32At(offset int) uint32 {
	return uint32(table[offset]) |
		uint32(table[offset+1])<<8 |
		uint32(table[offset+2])<<16 |
		uint32(table[offset+3])<<24
}

func (table rankTable) lookup(term string) (wordfreqRank, frequencywordsRank uint32) {
	count := int(table.uint32At(0))
	textStart := 4 + count*12
	low, high := 0, count
	for low < high {
		middle := low + (high-low)/2
		record := 4 + middle*12
		start := textStart + int(table.uint32At(record))
		end := len(table)
		if middle+1 < count {
			end = textStart + int(table.uint32At(record+12))
		}
		candidate := string(table[start:end])
		switch {
		case candidate < term:
			low = middle + 1
		case candidate > term:
			high = middle
		default:
			return table.uint32At(record + 4), table.uint32At(record + 8)
		}
	}
	return 0, 0
}
