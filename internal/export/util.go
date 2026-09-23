package export

import (
	"sort"
	"strings"
)

// trimmed returns value without leading or trailing whitespace.
func trimmed(value string) string { return strings.TrimSpace(value) }

// nonEmpty returns value, or fallback when value is blank.
func nonEmpty(value, fallback string) string {
	if trimmed(value) == "" {
		return fallback
	}
	return trimmed(value)
}

// sortedKeys returns a map's keys in a stable order, so a rendered document does
// not change shape between runs.
func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// joinNonEmpty joins the non-blank values with separator.
func joinNonEmpty(values []string, separator string) string {
	kept := make([]string, 0, len(values))
	for _, value := range values {
		if value = trimmed(value); value != "" {
			kept = append(kept, value)
		}
	}
	return strings.Join(kept, separator)
}
