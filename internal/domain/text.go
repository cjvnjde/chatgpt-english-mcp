package domain

import (
	"strings"
	"unicode/utf8"
)

func NormalizeWhitespace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func DisplayTerm(value string) string {
	return NormalizeWhitespace(value)
}

func NormalizeTerm(value string) string {
	normalized := strings.ToLower(DisplayTerm(value))
	normalized = strings.NewReplacer(
		"“", "\"",
		"”", "\"",
		"‘", "'",
		"’", "'",
	).Replace(normalized)
	return normalized
}

func NormalizeContext(value string) string {
	return NormalizeWhitespace(value)
}

func ValidTerm(value string) bool {
	length := utf8.RuneCountInString(value)
	return length >= 1 && length <= 200
}

func Preview(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	if limit <= 1 {
		return "…"
	}
	return strings.TrimSpace(string(runes[:limit-1])) + "…"
}
