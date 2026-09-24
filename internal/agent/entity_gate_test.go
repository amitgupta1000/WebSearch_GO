package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/cache"
	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// cjShahChart reproduces the shape of the CJ Shah run: a couple of people the
// company's own material supports, plus the off-entity people that run produced, each
// citing the URL it really cited. The sources exist and are found — they simply never
// name the company, which is the case the gate acts on.
func cjShahChart() (*models.OrgChart, *models.CompanyResearchContext, models.CompanyBrief) {
	brief := models.CompanyBrief{
		CompanyName: "CJ Shah", Domain: "cjshah.com", Industry: "chemicals",
		EntityConfirmed: true, EntitySource: "user",
	}
	leadershipSource := "https://www.cjshah.com/leadership"
	procurementSource := "https://www.business-standard.com/article/companies/cj-shah-expands-capacity-124051200123_1.html"
	wealthSource := "https://www.wealthbriefing.com/html/article.php/summary-of-executive-moves-in-global-wealth-management--january-2026"
	textbookSource := "https://pubhtml5.com/unmh/bfxe/Supply_Chain_Management_Text_and_Cases_by_Janat_Shah_%28z-lib.org%29/"
	summitSource := "https://www.elscconclave.com/archive/summit/2016.php"

	chart := &models.OrgChart{
		CompanyName: brief.CompanyName,
		Domain:      brief.Domain,
		Leadership: []models.Employee{
			{Name: "Asha Mehta", Title: "Managing Director", PublicSource: leadershipSource},
			{Name: "Melinda Torres", Title: "Chief Marketing Officer", PublicSource: wealthSource},
		},
		Departments: []models.Department{{
			Name: "Procurement",
			Head: "Dev Arora",
			Members: []models.Employee{
				{Name: "Ravi Menon", Title: "VP Procurement", PublicSource: procurementSource},
				{Name: "Dev Arora", Title: "Associate VP - Procurement", PublicSource: summitSource},
				{Name: "Janat Shah", Title: "Author", PublicSource: textbookSource},
			},
		}},
	}
	evidence := []models.Evidence{
		{URL: leadershipSource, Title: "Leadership", Content: "Asha Mehta, Managing Director of CJ Shah."},
		{URL: procurementSource, Title: "CJ Shah expands capacity", Content: "Ravi Menon, VP Procurement at CJ Shah, said the plant expands."},
		{URL: wealthSource, Title: "Summary of executive moves in global wealth management", Content: "Melinda Torres has joined Brighton Jones as chief marketing officer."},
		{URL: textbookSource, Title: "Supply Chain Management: Text and Cases", Content: "Supply Chain Management: Text and Cases by Janat Shah."},
		{URL: summitSource, Title: "Supply Chain Leadership Summit 2016", Content: "Speakers: Dev Arora, Vimal Shah, Amit Pandey, Nilesh Jha, S. Venuji."},
	}
	return chart, &models.CompanyResearchContext{Brief: brief, Evidence: evidence}, brief
}

// newGateEngine builds an engine with the freshness lookups off, so these tests are
// only about the entity gate.
func newGateEngine(mode string) *Engine {
	return NewEngine(&mockLLM{}, &mockSearcher{}, &failingScraper{}, cache.NewMemoryCache(50), 10, 5, 20,
		WithValidation(false, 0, 36, 18), WithEntityGate(mode))
}

// TestEntityGateRemovesOffEntityPeople is the outcome the CJ Shah run needed: the
// wealth-management marketer, the textbook author and the 2016 summit speaker leave the
// chart, and every removal is recorded with the source that failed to name the company.
func TestEntityGateRemovesOffEntityPeople(t *testing.T) {
	chart, state, brief := cjShahChart()
	gate := &models.EntityGate{Confirmed: brief.EntityConfirmed, Keys: newEntityMatcher(brief).describe()}
	agent := NewCompanyAgent(newGateEngine(EntityGateEnforce))

	agent.validateChart(context.Background(), "sess_test", brief, chart, state, nil, gate)

	if len(chart.Leadership) != 1 || chart.Leadership[0].Name != "Asha Mehta" {
		t.Fatalf("expected only the company's own director to survive, got %+v", chart.Leadership)
	}
	if got := len(chart.Departments[0].Members); got != 1 {
		t.Fatalf("expected one surviving procurement member, got %d: %+v", got, chart.Departments[0].Members)
	}
	if chart.Departments[0].Members[0].Name != "Ravi Menon" {
		t.Fatalf("expected Ravi Menon to survive, got %+v", chart.Departments[0].Members)
	}
	// The excluded head must not stay named at the top of the department.
	if chart.Departments[0].Head != "" {
		t.Fatalf("expected the excluded head to be cleared, got %q", chart.Departments[0].Head)
	}

	if gate.RowsOffEntity != 3 {
		t.Fatalf("expected 3 off-entity people, got %d (%+v)", gate.RowsOffEntity, gate.OffEntity)
	}
	names := make(map[string]bool, len(gate.OffEntity))
	for _, row := range gate.OffEntity {
		names[row.Name] = true
		if row.Reason == "" || row.Source == "" {
			t.Errorf("every exclusion needs a reason and a source, got %+v", row)
		}
	}
	for _, expected := range []string{"Melinda Torres", "Janat Shah", "Dev Arora"} {
		if !names[expected] {
			t.Errorf("expected %s to be listed as excluded, got %+v", expected, gate.OffEntity)
		}
	}
	// The survivors carry the key that kept them.
	if chart.Leadership[0].EntityKey != models.EntityKeyDomain {
		t.Errorf("expected a domain match for the company's own page, got %q", chart.Leadership[0].EntityKey)
	}
	if chart.Departments[0].Members[0].EntityKey != models.EntityKeyName {
		t.Errorf("expected a name match for the press article, got %q", chart.Departments[0].Members[0].EntityKey)
	}
}

// TestEntityGateReportModeKeepsRowsAndLabelsThem is the measurement mode: nothing is
// removed, but the excluded people are visible and counted.
func TestEntityGateReportModeKeepsRowsAndLabelsThem(t *testing.T) {
	chart, state, brief := cjShahChart()
	gate := &models.EntityGate{Confirmed: true}
	agent := NewCompanyAgent(newGateEngine(EntityGateReport))

	agent.validateChart(context.Background(), "sess_test", brief, chart, state, nil, gate)

	if len(chart.Leadership) != 2 || len(chart.Departments[0].Members) != 3 {
		t.Fatalf("report mode must not remove anybody: %d leaders, %d members", len(chart.Leadership), len(chart.Departments[0].Members))
	}
	if gate.RowsOffEntity != 3 {
		t.Fatalf("expected 3 off-entity people recorded, got %d", gate.RowsOffEntity)
	}
	if chart.Leadership[1].VerificationNote == "" {
		t.Error("report mode must label the off-entity row")
	}
}

// TestEntityGateOffModeChangesNothing keeps the gate a true comparison baseline: with
// the gate off nothing is tagged and nobody is removed.
func TestEntityGateOffModeChangesNothing(t *testing.T) {
	chart, state, brief := cjShahChart()
	gate := &models.EntityGate{Confirmed: true}
	agent := NewCompanyAgent(newGateEngine(EntityGateOff))

	agent.validateChart(context.Background(), "sess_test", brief, chart, state, nil, gate)

	if len(chart.Leadership) != 2 || len(chart.Departments[0].Members) != 3 {
		t.Fatal("gate off must not remove anybody")
	}
	if gate.RowsOffEntity != 0 || len(gate.OffEntity) != 0 {
		t.Fatalf("gate off must not record exclusions, got %+v", gate.OffEntity)
	}
	for _, person := range chart.Leadership {
		if person.EntityKey != "" {
			t.Errorf("gate off must not tag rows, got %q", person.EntityKey)
		}
	}
}

// TestEntityGateRescuesARowTheLookupTiesToTheCompany is the Sanjay Chemicals case: a CFO
// whose only source was a name-and-title aggregator page, with nothing tying him to the
// company, so the entity gate would drop him. One reverse lookup that returns a result
// naming both keeps him — and the rescue is recorded rather than silent.
func TestEntityGateRescuesARowTheLookupTiesToTheCompany(t *testing.T) {
	brief := models.CompanyBrief{CompanyName: "Sanjay Chemicals", Domain: "sanjaychemindia.com", EntityConfirmed: true}
	personPage := "https://www.datanyze.com/people/Chetan-Jain/12604398475"
	chart := &models.OrgChart{
		CompanyName: brief.CompanyName,
		Domain:      brief.Domain,
		Leadership: []models.Employee{
			{Name: "Chetan Jain", Title: "Chief Financial Officer", PublicSource: personPage},
		},
	}
	state := &models.CompanyResearchContext{Brief: brief, Evidence: []models.Evidence{
		// The collected source is name-and-title only: nothing here names the company.
		{URL: personPage, Title: "Chetan Jain", Content: "Chetan Jain — Chief Financial Officer at a chemicals distributor."},
	}}
	searcher := &datedSearcher{results: []models.SearchResult{{
		Title:   "Chetan Jain, CFO at Sanjay Chemicals (India) Pvt Ltd",
		Link:    "https://www.linkedin.com/in/chetan-jain-cfo",
		Snippet: "Chetan Jain is the Chief Financial Officer at Sanjay Chemicals (India) Pvt Ltd, Mumbai.",
	}}}
	engine := NewEngine(&mockLLM{}, searcher, &failingScraper{}, cache.NewMemoryCache(50), 10, 5, 20,
		WithValidation(true, 10, 36, 18), WithEntityGate(EntityGateEnforce))
	agent := NewCompanyAgent(engine)
	gate := &models.EntityGate{Confirmed: true, Mode: EntityGateEnforce}

	agent.validateChart(context.Background(), "sess_test", brief, chart, state, nil, gate)

	if len(chart.Leadership) != 1 {
		t.Fatalf("expected the lookup to rescue the CFO, got %d leader(s): %+v", len(chart.Leadership), chart.Leadership)
	}
	person := chart.Leadership[0]
	if person.EntityKey != models.EntityKeyName {
		t.Fatalf("expected the association to be recorded as a name match, got %q", person.EntityKey)
	}
	if !strings.Contains(person.VerificationNote, "reverse lookup") {
		t.Fatalf("expected the rescue to be noted, got %q", person.VerificationNote)
	}
	if gate.RowsOffEntity != 0 || gate.RescuedByLookup != 1 {
		t.Fatalf("expected a rescue and no exclusion, got %+v", gate)
	}
	if len(person.VerifiedBy) == 0 || !strings.Contains(person.VerifiedBy[0], "chetan-jain-cfo") {
		t.Errorf("expected the confirming source first in VerifiedBy, got %v", person.VerifiedBy)
	}
}

// TestEntityGateStillExcludesWhenTheLookupFindsNothing keeps the rescue honest: no
// company-naming result means the person is still not in the chart.
func TestEntityGateStillExcludesWhenTheLookupFindsNothing(t *testing.T) {
	brief := models.CompanyBrief{CompanyName: "Sanjay Chemicals", Domain: "sanjaychemindia.com", EntityConfirmed: true}
	personPage := "https://www.datanyze.com/people/Chetan-Jain/12604398475"
	chart := &models.OrgChart{
		CompanyName: brief.CompanyName,
		Domain:      brief.Domain,
		Leadership:  []models.Employee{{Name: "Chetan Jain", Title: "Chief Financial Officer", PublicSource: personPage}},
	}
	state := &models.CompanyResearchContext{Brief: brief, Evidence: []models.Evidence{
		{URL: personPage, Title: "Chetan Jain", Content: "Chetan Jain — Chief Financial Officer."},
	}}
	searcher := &datedSearcher{results: []models.SearchResult{{
		Title: "Chetan Jain", Link: "https://www.example.com/chetan-jain", Snippet: "Chetan Jain, finance professional.",
	}}}
	engine := NewEngine(&mockLLM{}, searcher, &failingScraper{}, cache.NewMemoryCache(50), 10, 5, 20,
		WithValidation(true, 10, 36, 18), WithEntityGate(EntityGateEnforce))
	agent := NewCompanyAgent(engine)
	gate := &models.EntityGate{Confirmed: true, Mode: EntityGateEnforce}

	agent.validateChart(context.Background(), "sess_test", brief, chart, state, nil, gate)

	if len(chart.Leadership) != 0 {
		t.Fatalf("expected the row to stay excluded, got %+v", chart.Leadership)
	}
	if gate.RowsOffEntity != 1 || gate.RescuedByLookup != 0 {
		t.Fatalf("expected one exclusion and no rescue, got %+v", gate)
	}
}

// page labelled "CJ SHAH" produced a roster row "CJ Shah — Chief Financial Officer",
// matched by the company's own name. The row is the entity, not a person, and the gate
// excludes it with a reason a reader can act on.
func TestEntityGateDropsARowNamedLikeTheCompany(t *testing.T) {
	brief := models.CompanyBrief{CompanyName: "CJ Shah", Domain: "cjshahgroup.in", EntityConfirmed: true}
	source := "https://www.zonebourse.com/insider/CJ-SHAH-A3O3EE/transactions/"
	chart := &models.OrgChart{
		CompanyName: brief.CompanyName,
		Domain:      brief.Domain,
		Departments: []models.Department{{
			Name: "Finance",
			Members: []models.Employee{
				{Name: "CJ Shah", Title: "Chief Financial Officer", PublicSource: source},
				{Name: "Asha Mehta", Title: "Director", PublicSource: "https://cjshahgroup.in/team"},
			},
		}},
	}
	state := &models.CompanyResearchContext{Brief: brief, Evidence: []models.Evidence{
		{URL: source, Title: "CJ SHAH insider transactions", Content: "CJ SHAH shareholding in CJ Shah and Co."},
		{URL: "https://cjshahgroup.in/team", Title: "Team", Content: "Asha Mehta, Director at CJ Shah Group."},
	}}
	gate := &models.EntityGate{Confirmed: true, Mode: EntityGateEnforce}
	agent := NewCompanyAgent(newGateEngine(EntityGateEnforce))

	agent.validateChart(context.Background(), "sess_test", brief, chart, state, nil, gate)

	if len(chart.Departments[0].Members) != 1 || chart.Departments[0].Members[0].Name != "Asha Mehta" {
		t.Fatalf("expected only the real person to survive, got %+v", chart.Departments[0].Members)
	}
	if gate.RowsOffEntity != 1 {
		t.Fatalf("expected one exclusion, got %d (%+v)", gate.RowsOffEntity, gate.OffEntity)
	}
	if reason := gate.OffEntity[0].Reason; !strings.Contains(reason, "the company itself") {
		t.Fatalf("expected the entity-name reason, got %q", reason)
	}
}
