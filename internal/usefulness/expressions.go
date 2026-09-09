package usefulness

import (
	"compress/gzip"
	_ "embed"
	"encoding/json"
	"io"
	"strings"
	"unicode/utf8"
)

//go:embed assets/expressions.json.gz
var compressedExpressions string

var expressions = loadExpressions()

type expressionEntry struct {
	Term               string   `json:"term"`
	Aliases            []string `json:"aliases"`
	Wiktionary         bool     `json:"wiktionary"`
	WordNet            bool     `json:"wordnet"`
	InflectFirst       bool     `json:"inflect_first"`
	Restricted         bool     `json:"restricted"`
	IdiomaticDocuments int      `json:"idiomatic_documents"`
	IdiomaticInstances int      `json:"idiomatic_instances"`
	LiteralInstances   int      `json:"literal_instances"`
	WordNetTags        int      `json:"wordnet_tags"`
}

type expressionDataset struct {
	Entries   []expressionEntry   `json:"entries"`
	VerbForms map[string][]string `json:"verb_forms"`
}

type expressionVote struct {
	score  int
	weight int
}

type expressionContext struct {
	before string
	after  string
	length int
}

type expressionToken struct {
	text   string
	target int
}

type expressionAnchor struct {
	text     string
	position int
	count    int
}

type expressionTemplate struct {
	tokens []string
	target int
}

// Targets are one-based entry indexes; zero is absent and -1 is ambiguous.
// Maps and their backing slices are built once, then only read by requests.
type expressionMatcher struct {
	votesByTarget []expressionVote
	canonical     map[string]int
	variants      map[string]int
	templates     map[expressionAnchor][]expressionTemplate
	typos         map[expressionContext][]expressionToken
	maxBytes      int
}

func loadExpressions() *expressionMatcher {
	reader, err := gzip.NewReader(strings.NewReader(compressedExpressions))
	if err != nil {
		panic("usefulness: opening embedded expressions: " + err.Error())
	}
	defer reader.Close()

	var dataset expressionDataset
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&dataset); err != nil {
		panic("usefulness: loading embedded expressions: " + err.Error())
	}
	// Read through EOF to verify the gzip checksum and reject trailing JSON.
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		panic("usefulness: invalid embedded expressions trailer")
	}
	return newExpressionMatcher(dataset)
}

func newExpressionMatcher(dataset expressionDataset) *expressionMatcher {
	matcher := &expressionMatcher{
		votesByTarget: make([]expressionVote, len(dataset.Entries)+1),
		canonical:     make(map[string]int, len(dataset.Entries)),
		variants:      make(map[string]int, len(dataset.Entries)),
		templates:     make(map[expressionAnchor][]expressionTemplate),
		typos:         make(map[expressionContext][]expressionToken),
	}
	formsByLemma := make(map[string][]string)
	for form, lemmas := range dataset.VerbForms {
		for _, lemma := range lemmas {
			formsByLemma[lemma] = append(formsByLemma[lemma], form)
		}
	}
	for index, entry := range dataset.Entries {
		if !strings.Contains(entry.Term, " ") {
			continue
		}
		target := index + 1
		matcher.canonical[entry.Term] = mergeExpressionTarget(matcher.canonical[entry.Term], target)
		vote := expressionVote{}
		if entry.Wiktionary && entry.Restricted {
			vote.score--
			vote.weight++
		}
		// MAGPIE is capped and manually filtered: these thresholds indicate
		// attestation, not population frequency or rarity below the threshold.
		if entry.IdiomaticDocuments >= 10 {
			vote.score++
			vote.weight++
		}
		if entry.WordNet && entry.WordNetTags >= 5 {
			vote.score++
			vote.weight++
		}
		matcher.votesByTarget[target] = vote
		var entryForms map[string][]string
		if entry.InflectFirst {
			entryForms = formsByLemma
		}
		matcher.addForms(entry.Term, target, entryForms)
		for _, alias := range entry.Aliases {
			matcher.addForms(alias, target, entryForms)
		}
	}
	// Index only literal surfaces for typo matching. Template slots remain
	// explicit and are never converted into arbitrary wildcard corrections.
	for surface, target := range matcher.variants {
		tokens := strings.Split(surface, " ")
		anchor := expressionAnchor{position: -1, count: len(tokens)}
		hasSlot := false
		maxBytes := len(surface)
		for position, token := range tokens {
			if slot := expressionSlot(token); slot != 0 {
				if slot == 1 {
					maxBytes += len("somebody's") - len(token)
				} else {
					maxBytes += len("somebody") - len(token)
				}
				hasSlot = true
			} else if anchor.position < 0 {
				anchor.position = position
				anchor.text = token
			}
		}
		if hasSlot {
			// The length bound must include accepted slot expansions, not just
			// stored surfaces, regardless of other entries in the dataset.
			if maxBytes > matcher.maxBytes {
				matcher.maxBytes = maxBytes
			}
			matcher.templates[anchor] = append(matcher.templates[anchor], expressionTemplate{tokens, target})
			continue
		}
		for start := 0; start < len(surface); {
			end := expressionTokenEnd(surface, start)
			token := surface[start:end]
			length := utf8.RuneCountInString(token)
			if length >= 4 {
				context := expressionContext{surface[:start], surface[end:], length}
				matcher.typos[context] = append(matcher.typos[context], expressionToken{token, target})
			}
			start = end + 1
		}
	}
	return matcher
}

func (matcher *expressionMatcher) addForms(surface string, target int, formsByLemma map[string][]string) {
	firstSpace := strings.IndexByte(surface, ' ')
	if firstSpace < 0 {
		return
	}
	matcher.addSurface(surface, target)
	for _, form := range formsByLemma[surface[:firstSpace]] {
		matcher.addSurface(form+surface[firstSpace:], target)
	}
}

func (matcher *expressionMatcher) addSurface(surface string, target int) {
	matcher.variants[surface] = mergeExpressionTarget(matcher.variants[surface], target)
	if len(surface) > matcher.maxBytes {
		matcher.maxBytes = len(surface)
	}
}

func mergeExpressionTarget(current, next int) int {
	if current == 0 {
		return next
	}
	if next == 0 || current == next {
		return current
	}
	return -1
}

func expressionVotes(normalizedTerm string) (score, weight int) {
	return expressions.votes(normalizedTerm, ranks)
}

func (matcher *expressionMatcher) votes(term string, rankSources rankTable) (score, weight int) {
	if !strings.Contains(term, " ") {
		return 0, 0
	}
	if target := matcher.canonical[term]; target != 0 {
		return matcher.targetVotes(target)
	}
	// A single inserted UTF-8 rune is the largest accepted length increase.
	if len(term) > matcher.maxBytes+utf8.UTFMax {
		return 0, 0
	}
	target := matcher.variants[term]
	count := strings.Count(term, " ") + 1
	position := 0
	for start := 0; start < len(term); {
		end := expressionTokenEnd(term, start)
		anchor := expressionAnchor{term[start:end], position, count}
		for _, template := range matcher.templates[anchor] {
			if template.matches(term) {
				target = mergeExpressionTarget(target, template.target)
			}
		}
		start = end + 1
		position++
	}
	for _, template := range matcher.templates[expressionAnchor{position: -1, count: count}] {
		if template.matches(term) {
			target = mergeExpressionTarget(target, template.target)
		}
	}
	if target != 0 {
		return matcher.targetVotes(target)
	}

	for start := 0; start < len(term); {
		end := expressionTokenEnd(term, start)
		token := term[start:end]
		length := utf8.RuneCountInString(token)
		if length >= 4 && !protectedExpressionWord(token, rankSources) {
			for candidateLength := length - 1; candidateLength <= length+1; candidateLength++ {
				context := expressionContext{term[:start], term[end:], candidateLength}
				for _, candidate := range matcher.typos[context] {
					if expressionOneEdit(token, candidate.text) {
						target = mergeExpressionTarget(target, candidate.target)
						if target < 0 {
							return 0, 0
						}
					}
				}
			}
		}
		start = end + 1
	}
	return matcher.targetVotes(target)
}

func (matcher *expressionMatcher) targetVotes(target int) (score, weight int) {
	if target <= 0 {
		return 0, 0
	}
	vote := matcher.votesByTarget[target]
	return vote.score, vote.weight
}

func protectedExpressionWord(token string, rankSources rankTable) bool {
	wordfreq, frequencywords := rankSources.lookup(token)
	return wordfreq > 0 && wordfreq <= 50000 || frequencywords > 0 && frequencywords <= 50000
}

func expressionTokenEnd(term string, start int) int {
	if space := strings.IndexByte(term[start:], ' '); space >= 0 {
		return start + space
	}
	return len(term)
}

func expressionSlot(token string) int {
	switch token {
	case "someone's", "somebody's", "one's":
		return 1
	case "someone", "somebody":
		return 2
	default:
		return 0
	}
}

func expressionSlotMatches(slot int, token string) bool {
	switch slot {
	case 1:
		switch token {
		case "my", "your", "his", "her", "its", "our", "their", "someone's", "somebody's", "one's":
			return true
		}
	case 2:
		switch token {
		case "me", "you", "him", "her", "it", "us", "them", "someone", "somebody":
			return true
		}
	}
	return false
}

func (template expressionTemplate) matches(term string) bool {
	start := 0
	for _, expected := range template.tokens {
		if start >= len(term) {
			return false
		}
		end := expressionTokenEnd(term, start)
		token := term[start:end]
		if token != expected && !expressionSlotMatches(expressionSlot(expected), token) {
			return false
		}
		start = end + 1
	}
	return start == len(term)+1
}

// Compare UTF-8 rune boundaries without allocating rune slices. After the first
// mismatch, exactly one insertion, deletion, substitution or swap must finish it.
func expressionOneEdit(input, candidate string) bool {
	inputOffset, candidateOffset := 0, 0
	for inputOffset < len(input) && candidateOffset < len(candidate) {
		inputRune, inputSize := utf8.DecodeRuneInString(input[inputOffset:])
		candidateRune, candidateSize := utf8.DecodeRuneInString(candidate[candidateOffset:])
		if inputRune != candidateRune {
			inputRest := input[inputOffset+inputSize:]
			candidateRest := candidate[candidateOffset+candidateSize:]
			if inputRest == candidateRest || inputRest == candidate[candidateOffset:] || input[inputOffset:] == candidateRest {
				return true
			}
			if inputRest != "" && candidateRest != "" {
				nextInput, nextInputSize := utf8.DecodeRuneInString(inputRest)
				nextCandidate, nextCandidateSize := utf8.DecodeRuneInString(candidateRest)
				return inputRune == nextCandidate && candidateRune == nextInput && inputRest[nextInputSize:] == candidateRest[nextCandidateSize:]
			}
			return false
		}
		inputOffset += inputSize
		candidateOffset += candidateSize
	}
	return utf8.RuneCountInString(input[inputOffset:])+utf8.RuneCountInString(candidate[candidateOffset:]) == 1
}
