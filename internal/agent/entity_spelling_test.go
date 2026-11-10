package agent

import (
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// goyumBrief is the spelling-variant identity from the Goyum Screwpress run: the
// operator typed the name run together, the legal name spaces it, and directory
// pages use both — which the whole-word token rule alone cannot bridge.
func goyumBrief() models.CompanyBrief {
	return models.CompanyBrief{CompanyName: "Goyum Screwpress", Domain: "goyumscrewpress.in", EntitySource: "typed"}
}

// TestEntityMatcherAcceptsNameSpellingVariants pins the leak the Goyum Screwpress
// run exposed: the gate held every source to the typed spelling and excluded the
// legal name's pages wholesale, even though the collapsed names are identical.
func TestEntityMatcherAcceptsNameSpellingVariants(t *testing.T) {
	matcher := newEntityMatcher(goyumBrief())

	kept, excluded := splitEntitySnippets([]models.SearchResult{
		{Title: "Goyum Screw Press - Manufacturer of Oil Expellers & Oil Mill Plants", Link: "https://www.indiamart.com/goyum-screw-press/", Snippet: "Manufacturer and exporter of oil extraction machinery based in Ludhiana."},
		{Title: "Vegetable Oil Refinery Exporter", Link: "https://www.indiamart.com/someone-else/", Snippet: "Exporting refineries from Mumbai."},
	}, matcher)
	if len(kept) != 1 || len(excluded) != 1 {
		t.Fatalf("expected the spaced legal name to be kept and the other company excluded, got %d kept / %d excluded", len(kept), len(excluded))
	}
	if kept[0].Link != "https://www.indiamart.com/goyum-screw-press/" {
		t.Errorf("unexpected kept hit: %+v", kept[0])
	}

	// The reverse spelling also matches: a brief of "Goyum Screw Press" must admit
	// sources that run the words together.
	reverse := newEntityMatcher(models.CompanyBrief{CompanyName: "Goyum Screw Press", Domain: "goyumscrewpress.in", EntitySource: "typed"})
	if !reverse.match("https://www.tradeindia.com/goyum-screwpress/", "Goyum Screwpress - Exporter from Ludhiana", "Screw presses and oil mill machinery.").Matched {
		t.Error("expected the run-together spelling to match a spaced brief name")
	}

	// Queries anchored on either spelling stay covered, so the planner does not
	// drop them as unanchored.
	if !matcher.covers(`"Goyum Screw Press" leadership`) {
		t.Error("expected a query using the spaced legal spelling to be covered")
	}

	// A registry row that names the company in the other spelling is still the
	// entity, not a person.
	if !matcher.namesTheEntity("Goyum Screw Press Pvt Ltd") {
		t.Error("expected the spaced legal name to be recognised as the entity itself")
	}

	// A person named near the other spelling still counts as company evidence.
	if !mentionsCompanyNear("Chetan Jain, Chief Financial Officer at Goyum Screw Press, Ludhiana.", "Chetan Jain", matcher) {
		t.Error("expected a company mention in the spaced spelling to anchor the person")
	}

	// A page that merely uses two of the words still fails: the collapsed form has
	// to appear whole.
	if matcher.match("", "Goyum brand screw press accessories", "Third-party accessories.").Matched {
		t.Error("collapsed fallback must require the whole name, not its words scattered")
	}
}

// TestEntityMatcherKeepsShortNamesStrict guards the collapsed fallback's guard:
// short and initialism names do not get it, because a collapsed form that short
// can sit inside an unrelated word ("samson" contains "sam son").
func TestEntityMatcherKeepsShortNamesStrict(t *testing.T) {
	matcher := newEntityMatcher(cjShahBrief())
	if matcher.match("", "Cjshah Enterprises", "A different company.").Matched {
		t.Error("collapsed fallback must not admit a lookalike for a short initialism name")
	}
	if matcher.covers("cjshah enterprises") {
		t.Error("short names keep the strict whole-word rule")
	}
	// The strict rule itself is unchanged: Janat Shah is still not CJ Shah.
	if matcher.match("", "Supply Chain Management by Janat Shah", "Textbook.").Matched {
		t.Error("Janat Shah must stay excluded from a CJ Shah run")
	}
}