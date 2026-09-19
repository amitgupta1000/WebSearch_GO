package llm

import "testing"

func TestCleanJSONString(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain object", `{"a":1}`, `{"a":1}`},
		{"fenced json", "```json\n{\"a\":1}\n```", `{"a":1}`},
		{"fenced plain", "```\n{\"a\":1}\n```", `{"a":1}`},
		{"surrounding whitespace", "\n  \t{\"a\":1}\n", `{"a":1}`},
		{"prose prefix", `Here is the JSON: {"a":1}`, `{"a":1}`},
		{"prose suffix", `{"a":1} hope this helps`, `{"a":1}`},
		{"trailing fence", "{\"a\":1}```", `{"a":1}`},
		{"nested braces preserved", `{"a":{"b":2}}`, `{"a":{"b":2}}`},
		{"fence inside string value", "{\"code\":\"```go\"}", "{\"code\":\"```go\"}"},
		{"no braces untouched", "not json at all", "not json at all"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := cleanJSONString(tc.in); got != tc.want {
				t.Errorf("cleanJSONString(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
