package agent

import "testing"

func TestNormalizeURLCollapsesFormattingAndTrackingVariants(t *testing.T) {
	cases := []struct{ input, want string }{
		{"https://www.example.com/about/", "https://example.com/about"},
		{"https://EXAMPLE.com/about", "https://example.com/about"},
		{"https://example.com/about#leadership", "https://example.com/about"},
		{"https://example.com/about?utm_source=newsletter&utm_medium=email", "https://example.com/about"},
		{"https://example.com/about?b=2&a=1", "https://example.com/about?a=1&b=2"},
		{"https://example.com/?utm_campaign=spring", "https://example.com"},
		{"https://www.example.com", "https://example.com"},
	}
	for _, testCase := range cases {
		if got := normalizeURL(testCase.input); got != testCase.want {
			t.Errorf("normalizeURL(%q) = %q, want %q", testCase.input, got, testCase.want)
		}
	}
}

func TestNormalizeURLKeepsMeaningfulQueryParameters(t *testing.T) {
	// Only campaign/referrer parameters are stripped: a query that selects a
	// document must keep distinguishing it, or two real pages would collide.
	// Remaining parameters are sorted, so parameter order cannot create a
	// phantom duplicate.
	cases := map[string]string{
		"https://example.com/profile?id=42":        "https://example.com/profile?id=42",
		"https://example.com/search?q=acme&page=2": "https://example.com/search?page=2&q=acme",
	}
	for target, want := range cases {
		if got := normalizeURL(target); got != want {
			t.Errorf("normalizeURL(%q) = %q, want %q", target, got, want)
		}
	}
}

func TestContentHashIgnoresWhitespaceAndCase(t *testing.T) {
	first := contentHash("Hello   World\n\nSecond   line")
	second := contentHash("hello world second line")
	if first != second {
		t.Error("whitespace and letter case must not change a page's fingerprint")
	}
	if contentHash("a completely different document") == first {
		t.Error("different text must produce a different fingerprint")
	}
}
