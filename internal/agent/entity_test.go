package agent

import (
	"strings"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// cjShahBrief is the identity from the run that exposed this problem: a name that
// reads as initials plus a surname, with a domain nobody had confirmed.
func cjShahBrief() models.CompanyBrief {
	return models.CompanyBrief{CompanyName: "CJ Shah", Domain: "cjshah.com", EntitySource: "typed"}
}

// TestEntityGateRejectsTheUnrelatedSourcesFromTheCJShahRun uses the actual URLs and
// titles from that run's artifact. Every one of them looked topically relevant to a
// chemicals procurement brief and belongs to somebody else — a wealth-management
// roundup, a textbook, a name directory, an academic paper, a 2016 conference
// archive, an authors index, and a post about Adani Ports.
func TestEntityGateRejectsTheUnrelatedSourcesFromTheCJShahRun(t *testing.T) {
	matcher := newEntityMatcher(cjShahBrief())
	unrelated := []struct {
		label   string
		url     string
		title   string
		content string
	}{
		{
			label:   "Melinda Torres, global wealth management roundup",
			url:     "https://www.wealthbriefing.com/html/article.php/summary-of-executive-moves-in-global-wealth-management--january-2026",
			title:   "Summary of executive moves in global wealth management",
			content: "Melinda Torres has joined Brighton Jones as chief marketing officer.",
		},
		{
			label:   "Janat Shah, textbook author",
			url:     "https://pubhtml5.com/unmh/bfxe/Supply_Chain_Management_Text_and_Cases_by_Janat_Shah_%28z-lib.org%29/",
			title:   "Supply Chain Management: Text and Cases",
			content: "Supply Chain Management: Text and Cases by Janat Shah.",
		},
		{
			label:   "Rakshith CJ, initials in a profile slug",
			url:     "https://in.linkedin.com/in/rakshith-cj-411244106",
			title:   "Rakshith CJ",
			content: "Rakshith CJ — Head of Procurement",
		},
		{
			label:   "Pinal, a LinkedIn name directory",
			url:     "https://www.linkedin.com/pub/dir/Pinal/+/in-7150-Mumbai-Area,-India/",
			title:   "Pinal - Mumbai Area - India",
			content: "People named Pinal in the Mumbai Area.",
		},
		{
			label:   "A Shah, journal author",
			url:     "https://www.sciencedirect.com/science/article/pii/S2772662223000371",
			title:   "Supply chain resilience",
			content: "A Shah, B Kumar. Supply chain management research.",
		},
		{
			label:   "Shahedza Khairuddin, Malaysian construction procurement paper",
			url:     "https://www.researchgate.net/publication/326467628_Construction_procurement_systems_in_use_in_Malaysia",
			title:   "Construction procurement systems in use in Malaysia",
			content: "Shahedza Khairuddin, Head of Procurement and Project Delivery System Research.",
		},
		{
			label:   "Neeraj Shah, authors index page",
			url:     "https://www.supplychainbrain.com/authors",
			title:   "Authors",
			content: "Neeraj Shah, CEO and Founder.",
		},
		{
			label:   "Sanjay Kumar Gupta, post about Adani Ports",
			url:     "https://www.linkedin.com/posts/charanjit-singh-cj-76804016_adani-ports-and-sez-biodiversity-initiatives-activity-6898204114396827648-Nx24",
			title:   "Adani Ports and SEZ biodiversity initiatives",
			content: "Adani Ports celebrates its biodiversity work; congratulations to Sanjay Kumar Gupta.",
		},
		{
			label:   "Dev Arora, 2016 conference archive",
			url:     "https://www.elscconclave.com/archive/summit/2016.php",
			title:   "Supply Chain Leadership Summit 2016",
			content: "Speakers: Dev Arora, Vimal Shah, Amit Pandey, Nilesh Jha, S. Venuji.",
		},
		{
			label:   "Nirav Shah, Instagram reel",
			url:     "https://www.instagram.com/spgleaders/reel/Db8GkB0tlbR/",
			title:   "SPG Leaders on Instagram",
			content: "Nirav Shah — Director & Global Head of Procurement.",
		},
	}

	for _, source := range unrelated {
		if match := matcher.match(source.url, source.title, source.content); match.Matched {
			t.Errorf("%s must not match the entity, but matched by %s=%s", source.label, match.Key, match.Value)
		}
	}
}

// TestEntityGateKeepsSourcesThatNameTheCompany is the other half of the contract:
// the gate must not throw away the run's real material, and it must report *how*
// each match was made.
func TestEntityGateKeepsSourcesThatNameTheCompany(t *testing.T) {
	matcher := newEntityMatcher(cjShahBrief())
	related := []struct {
		label string
		url   string
		title string
		key   string
	}{
		{"the company's own site", "https://www.cjshah.com/leadership", "Leadership", models.EntityKeyDomain},
		{"a careers subdomain", "https://careers.cjshah.com/openings", "Openings", models.EntityKeyDomain},
		{"a URL naming the company", "https://www.example.com/press/cj-shah-expands-capacity", "Press coverage", models.EntityKeyName},
		{"a page whose title names the company", "https://www.ambitionbox.com/salaries/head-procurement-and-supply-chain", "CJ Shah salaries", models.EntityKeyName},
	}
	for _, source := range related {
		match := matcher.match(source.url, source.title, "")
		if !match.Matched {
			t.Errorf("%s must match the entity", source.label)
			continue
		}
		if match.Key != source.key {
			t.Errorf("%s matched by %s, want %s", source.label, match.Key, source.key)
		}
	}
}

// TestEntityGateReportsNameOnlyMatches pins the honesty rule that makes a same-named
// company visible instead of silent: "CJ Shah & Co" (a Mumbai recruitment firm) is a
// different company from the one at cjshah.com, and a source that shares only the
// name is reported as a name match so a reader can judge it.
func TestEntityGateReportsNameOnlyMatches(t *testing.T) {
	ambiguous := "https://www.ambitionbox.com/salaries/cj-shah-and-co-salaries/head-procurement-and-supply-chain"
	matcher := newEntityMatcher(cjShahBrief())
	match := matcher.match(ambiguous, "", "CJ Shah and Co")
	if !match.Matched {
		t.Fatal("a same-name source still matches; it is reported, not hidden")
	}
	if match.Key != models.EntityKeyName {
		t.Fatalf("expected a name match, got %s", match.Key)
	}

	// Confirm it as an alias of the researched entity and the alias key is used,
	// which is strictly stronger evidence than the bare name.
	aliased := newEntityMatcher(models.CompanyBrief{
		CompanyName: "CJ Shah", Domain: "cjshah.com", Aliases: []string{"CJ Shah & Co"}, EntityConfirmed: true,
	})
	if match := aliased.match(ambiguous, "", ""); match.Key != models.EntityKeyAlias {
		t.Fatalf("expected the alias key, got %s (matched=%t)", match.Key, match.Matched)
	}
}

// TestEntityGateRegistrationIDMatchesARenamedCompany covers the strongest key that
// is not a domain: registry sources publish the CIN, so a company that changed its
// name is still recognised.
func TestEntityGateRegistrationIDMatchesARenamedCompany(t *testing.T) {
	matcher := newEntityMatcher(models.CompanyBrief{
		CompanyName: "CJ Shah", Domain: "cjshah.com", RegistrationID: "U74999MH2005PTC152345",
	})
	match := matcher.match(
		"https://www.zaubacorp.com/company/OLD-NAME-PRIVATE-LIMITED/U74999MH2005PTC152345",
		"", "Formerly known as Old Name Private Limited")
	if !match.Matched || match.Key != models.EntityKeyRegistration {
		t.Fatalf("expected a registration-id match, got %+v", match)
	}
	// Separators vary between publishers.
	spaced := matcher.match("https://www.tofler.in/old-name/company/U74999MH-2005-PTC-152345", "", "")
	if !spaced.Matched || spaced.Key != models.EntityKeyRegistration {
		t.Fatalf("expected a registration-id match for the separated form, got %+v", spaced)
	}
}

// TestEntityGateRefusesWeakNameKeys keeps ambiguous short identities out of the gate:
// "CJ" alone would match half the internet, while a distinctive single token is a
// legitimate key.
func TestEntityGateRefusesWeakNameKeys(t *testing.T) {
	if _, ok := nameEntityKey(models.EntityKeyName, "CJ", 0.8); ok {
		t.Error("a two-letter name is not an identity")
	}
	if _, ok := nameEntityKey(models.EntityKeyName, "", 0.8); ok {
		t.Error("an empty name is not an identity")
	}
	if _, ok := nameEntityKey(models.EntityKeyName, "Anthropic", 0.8); !ok {
		t.Error("a distinctive single token is a usable identity")
	}

	// Legal suffixes do not change the identity.
	suffixed, ok := nameEntityKey(models.EntityKeyName, "KLJ Resources Limited", 0.8)
	if !ok {
		t.Fatal("expected a usable key")
	}
	plain, _ := nameEntityKey(models.EntityKeyName, "KLJ Resources", 0.8)
	if len(suffixed.tokens) != 2 || len(plain.tokens) != 2 {
		t.Fatalf("expected two tokens for both spellings, got %d and %d", len(suffixed.tokens), len(plain.tokens))
	}
	// "Co" is kept, because dropping it would merge the different "… & Co".
	co := nameTokens("CJ Shah & Co")
	if len(co) != 3 || co[2] != "co" {
		t.Fatalf("expected co to survive tokenisation, got %v", co)
	}
}

// TestEntityGateCoversPlannedQueries is the earliest gate: a query that is not
// anchored on the entity never runs, so industry-generic results are never fetched.
func TestEntityGateCoversPlannedQueries(t *testing.T) {
	matcher := newEntityMatcher(cjShahBrief())
	covered := []string{
		"site:cjshah.com leadership",
		`"CJ Shah" procurement head India`,
		"CJ Shah annual report 2025",
	}
	for _, query := range covered {
		if !matcher.covers(query) {
			t.Errorf("query %q should be anchored on the entity", query)
		}
	}
	rejected := []string{
		"procurement head India chemicals CPO CSCO VP Head Director",
		"supply chain management textbook author",
		"chemicals procurement sourcing supply chain speaker Argus Petrochemicals Conference",
	}
	for _, query := range rejected {
		if matcher.covers(query) {
			t.Errorf("query %q is not anchored on the entity and must be dropped", query)
		}
	}
}

// TestEntityGatePromptLineStatesConfirmation keeps the prompt honest about what was
// confirmed, so an unconfirmed identity is never presented as a fact.
func TestEntityGatePromptLineStatesConfirmation(t *testing.T) {
	typed := newEntityMatcher(cjShahBrief()).promptLine()
	if !strings.Contains(typed, "cjshah.com") || !strings.Contains(typed, "CJ Shah") {
		t.Fatalf("prompt line must state the keys, got %q", typed)
	}
	if !strings.Contains(typed, "not confirmed") {
		t.Fatalf("expected the unconfirmed wording, got %q", typed)
	}
	confirmed := newEntityMatcher(models.CompanyBrief{
		CompanyName: "CJ Shah", Domain: "cjshah.com", EntityConfirmed: true,
	}).promptLine()
	if !strings.Contains(confirmed, "confirmed by the operator") {
		t.Fatalf("expected the confirmed wording, got %q", confirmed)
	}
}

// TestEntityDomainsNormalizesAndKeepsThePrimaryFirst covers the multi-select identity: the
// operator confirms one company under several names, and every confirmed domain becomes a
// key — normalized, deduplicated, with the primary domain still reported first so the
// artifact reads it as canonical.
func TestEntityDomainsNormalizesAndKeepsThePrimaryFirst(t *testing.T) {
	brief := models.CompanyBrief{
		CompanyName: "Sanjay Chemicals",
		Domain:      "www.SanjayChemIndia.com/about",
		Domains:     []string{"sanjaychemindia.com", "https://sanjaychemicals.in/", "SANJAYCHEM.CO.IN", "  "},
	}
	got := entityDomains(brief)
	want := []string{"sanjaychemindia.com", "sanjaychemicals.in", "sanjaychem.co.in"}
	if len(got) != len(want) {
		t.Fatalf("expected %d domains, got %v", len(want), got)
	}
	for index, domain := range want {
		if got[index] != domain {
			t.Errorf("expected %q at position %d, got %v", domain, index, got)
		}
	}
}

// TestEntityGateAcceptsEveryConfirmedDomain is what "select two names" means to the gate: a
// source naming any confirmed domain is on-entity, while a domain that merely resembles one
// is not.
func TestEntityGateAcceptsEveryConfirmedDomain(t *testing.T) {
	brief := models.CompanyBrief{
		CompanyName:     "Sanjay Chemicals",
		Domain:          "sanjaychemindia.com",
		Domains:         []string{"sanjaychemicals.in"},
		Aliases:         []string{"Sanjay Chemicals India"},
		EntityConfirmed: true,
	}
	matcher := newEntityMatcher(brief)

	keys := matcher.describe()
	for _, want := range []string{"domain:sanjaychemindia.com", "domain:sanjaychemicals.in", "alias:Sanjay Chemicals India"} {
		if !contains(keys, want) {
			t.Errorf("expected the gate to carry the key %q, got %v", want, keys)
		}
	}

	cases := []struct {
		url   string
		title string
		want  bool
	}{
		{"https://sanjaychemindia.com/team", "Our team", true},
		{"https://www.sanjaychemicals.in/about-us", "About us", true},
		{"https://sanjaychemindia.in/team", "Our team", false},
		{"https://sanjaychemicalsllp.com/team", "Our team", false},
	}
	for _, tc := range cases {
		match := matcher.match(tc.url, tc.title, "")
		if match.Matched != tc.want {
			t.Errorf("%s: expected matched=%t, got %+v", tc.url, tc.want, match)
		}
		if tc.want && match.Key != models.EntityKeyDomain {
			t.Errorf("%s: expected a domain match, got %+v", tc.url, match)
		}
	}
}

// TestRootHostKeepsMultiPartSuffixes covers a gate leak the multi-select would have made
// routine: reducing every host to its last two labels turns a .co.in company into the key
// "co.in", which then matches every .co.in host on the web. A gate that accepts everything
// is worse than no gate, so the registrable name has to survive.
func TestRootHostKeepsMultiPartSuffixes(t *testing.T) {
	cases := []struct{ host, want string }{
		{"sanjaychem.co.in", "sanjaychem.co.in"},
		{"www.sanjaychem.co.in", "sanjaychem.co.in"},
		{"acme.co.uk", "acme.co.uk"},
		{"sub.acme.co.uk", "acme.co.uk"},
		{"example.com.br", "example.com.br"},
		{"mail.google.com", "google.com"},
		{"www.sanjaychemindia.com", "sanjaychemindia.com"},
		{"acme.com", "acme.com"},
		{"acme.co", "acme.co"},
		{"localhost", "localhost"},
	}
	for _, tc := range cases {
		if got := rootHost(tc.host); got != tc.want {
			t.Errorf("rootHost(%q) = %q, want %q", tc.host, got, tc.want)
		}
	}

	// The leak itself: a page on a different Indian company's site must not match.
	matcher := newEntityMatcher(models.CompanyBrief{CompanyName: "Sanjay Chemicals", Domain: "sanjaychem.co.in"})
	if match := matcher.match("https://unrelatedchemicals.co.in/team", "Our team", ""); match.Matched {
		t.Fatalf("a co.in sibling must not be on-entity, got %+v", match)
	}
	if match := matcher.match("https://sanjaychem.co.in/team", "Our team", ""); !match.Matched {
		t.Fatalf("the company's own .co.in site must be on-entity, got %+v", match)
	}
}
