package agent

import (
	"context"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/cache"
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

	// Near-miss words must not join into the collapsed name: for a matcher with no
	// brand key (the confirmed domain does not spell the name out), "screwing" and
	// "pressers" cannot concatenate to "goyumscrewpress", and the strict whole-word
	// rule rejects them too.
	noBrand := newEntityMatcher(models.CompanyBrief{CompanyName: "Goyum Screw Press", Domain: "gsp-india.in", EntitySource: "typed"})
	if noBrand.match("", "Goyum brand screwing pressers accessories", "Third-party accessories.").Matched {
		t.Error("collapsed fallback must require the exact name from whole words")
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

// TestEntityMatcherDerivesTheBrandKeyFromTheDomain pins the brand-word fix: a
// multi-word name whose distinctive first token is spelled out by the confirmed
// domain also matches that token alone ("Zonal Sales Head at GOYUM" for
// "Goyum Screw Press" + goyumscrewpress.in), while generic corporate words and
// initialisms never become brand keys.
func TestEntityMatcherDerivesTheBrandKeyFromTheDomain(t *testing.T) {
	matcher := newEntityMatcher(goyumBrief())
	if !matcher.match("", "Ankur Chitroda - Zonal Sales Head at GOYUM", "Total 12+ years of experience in Technical Sales, Engineering and Design of plant and equipments.").Matched {
		t.Error("expected the brand word GOYUM to match via the domain-confirmed brand key")
	}

	// A generic first word never becomes a brand key, even when the domain repeats it.
	stateBank := newEntityMatcher(models.CompanyBrief{CompanyName: "State Bank of India", Domain: "statebankofindia.in", EntitySource: "typed"})
	if stateBank.match("", "STATE circular", "Notice for all branches.").Matched {
		t.Error("generic corporate words must not become brand keys")
	}

	// Initialism names keep the strict whole-word rule: no brand key for "cj".
	if (newEntityMatcher(cjShahBrief())).match("", "CJ notice", "Announcement for members.").Matched {
		t.Error("short initialism names must not derive brand keys")
	}
}

// TestValidationKeepsRowsWhenTheLookupSpellsTheCompanyDifferently pins the
// validation-pass fix: the reverse lookup's current-status tests run through the
// entity matcher, so results spelling the company the registered way ("Goyum
// Screw Press" for a "Goyum Screwpress" brief) read as "current" instead of
// "unknown", and the row keeps its place in the roster instead of being removed
// as current-unverified.
func TestValidationKeepsRowsWhenTheLookupSpellsTheCompanyDifferently(t *testing.T) {
	brief := models.CompanyBrief{CompanyName: "Goyum Screwpress", Domain: "goyumscrewpress.in", EntityConfirmed: true}
	brief.AreasOfInterest = []string{"sales"}
	source := "https://in.linkedin.com/in/gurpreet-grewal-682810141"
	chart := &models.OrgChart{
		CompanyName: brief.CompanyName,
		Domain:      brief.Domain,
		Departments: []models.Department{{
			Name: "Sales",
			Members: []models.Employee{
				{Name: "Gurpreet Grewal", Title: "Manager of Sales", PublicSource: source},
			},
		}},
	}
	state := &models.CompanyResearchContext{Brief: brief, Evidence: []models.Evidence{
		{URL: source, Title: "GURPREET GREWAL - Manager of Sales at Goyum", Content: "Manager of Sales at Goyum Screw Press. Apr 2023 - Present 3 years 7 months. Ludhiana, Punjab, India."},
	}}
	searcher := &datedSearcher{results: []models.SearchResult{{
		Title:   "Gurpreet Grewal - Manager of Sales - Goyum Screw Press",
		Link:    source,
		Snippet: "Manager of Sales at Goyum Screw Press. Apr 2023 - Present 3 years 7 months. Ludhiana, Punjab, India.",
	}}}
	engine := NewEngine(&mockLLM{}, searcher, &failingScraper{}, cache.NewMemoryCache(50), 10, 5, 20,
		WithValidation(true, 10, 36, 18), WithEntityGate(EntityGateEnforce))
	agent := NewCompanyAgent(engine)
	gate := &models.EntityGate{Confirmed: true, Mode: EntityGateEnforce}

	agent.validateChart(context.Background(), "sess_test", brief, chart, state, nil, gate)

	if len(chart.Departments[0].Members) != 1 {
		t.Fatalf("expected the sales row to survive a differently-spelled lookup, got %+v", chart.Departments[0].Members)
	}
	if row := chart.Departments[0].Members[0]; row.Verification == models.VerificationCurrentUnverified {
		t.Errorf("expected the current-status lookup to recognise the spaced spelling, got %+v", row)
	}
	if gate.RowsOffEntity != 0 {
		t.Errorf("expected no off-entity rows, got %d (%+v)", gate.RowsOffEntity, gate.OffEntity)
	}
}