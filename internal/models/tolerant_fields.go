package models

// tolerant_fields.go holds the coercion helpers behind the package's shared
// tolerant JSON decoding.
//
// The models the LLM writes drift in the same handful of ways: a number where
// a string is declared (`"employees": 46000`), a lone string where an array is
// declared (`"missing_stages": ""`), a numeric string where an int is declared
// (`"priority": "1"`), a bare string where an object is declared. The strict
// decoder discarded an entire synthesis for one such value -- three live runs
// lost their output that way. Every helper here coerces or skips; nothing
// fails a decode.

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// rawIsAbsent reports a missing or JSON null value.
func rawIsAbsent(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

// flexibleText coerces a scalar JSON value to text: strings pass through,
// numbers keep their literal form, booleans become "true"/"false", and
// anything else (objects, arrays) yields "".
func flexibleText(raw json.RawMessage) string {
	if rawIsAbsent(raw) {
		return ""
	}
	if value, err := flexibleString(raw); err == nil {
		return value
	}
	var boolean bool
	if err := json.Unmarshal(raw, &boolean); err == nil {
		return strconv.FormatBool(boolean)
	}
	return ""
}

// flexibleTextList accepts an array of scalars or one lone scalar.
func flexibleTextList(raw json.RawMessage) []string {
	if rawIsAbsent(raw) {
		return nil
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		entries = []json.RawMessage{raw}
	}
	values := make([]string, 0, len(entries))
	for _, entry := range entries {
		if value := flexibleText(entry); strings.TrimSpace(value) != "" {
			values = append(values, value)
		}
	}
	return values
}

// flexibleIntNumber coerces int/float/numeric-string shapes.
func flexibleIntNumber(raw json.RawMessage) (int, bool) {
	if rawIsAbsent(raw) {
		return 0, false
	}
	var value int
	if err := json.Unmarshal(raw, &value); err == nil {
		return value, true
	}
	var floatValue float64
	if err := json.Unmarshal(raw, &floatValue); err == nil {
		return int(floatValue), true
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		trimmed := strings.TrimSpace(text)
		if parsed, err := strconv.Atoi(trimmed); err == nil {
			return parsed, true
		}
		if parsed, err := strconv.ParseFloat(trimmed, 64); err == nil {
			return int(parsed), true
		}
	}
	return 0, false
}

// flexibleFloatNumber coerces float/int/numeric-string shapes.
func flexibleFloatNumber(raw json.RawMessage) (float64, bool) {
	if rawIsAbsent(raw) {
		return 0, false
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err == nil {
		return value, true
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if parsed, err := strconv.ParseFloat(strings.TrimSpace(text), 64); err == nil {
			return parsed, true
		}
	}
	return 0, false
}

// flexibleBoolValue coerces bool or its common textual forms.
func flexibleBoolValue(raw json.RawMessage) (bool, bool) {
	if rawIsAbsent(raw) {
		return false, false
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err == nil {
		return value, true
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		switch strings.ToLower(strings.TrimSpace(text)) {
		case "true", "yes", "1":
			return true, true
		case "false", "no", "0":
			return false, true
		}
	}
	return false, false
}
