package agent

import (
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

func TestCompanyMetricsExtractEvidenceBackedValues(t *testing.T) {
	employees, turnover, year, source := companyMetricsFromTexts([]companyMetricText{{
		Text: "The company employs 1,200 people and reported turnover of INR 500 crore in FY2024.",
		URL:  "https://acme.example/about",
	}})
	if employees != "1,200" || turnover != "INR 500 crore" || year != "FY2024" || source != "https://acme.example/about" {
		t.Fatalf("metrics = %q/%q/%q/%q", employees, turnover, year, source)
	}
}

func TestCompanyMetricsDoNotInventValues(t *testing.T) {
	employees, turnover, year, source := companyMetricsFromTexts([]companyMetricText{{Text: "A specialty chemicals company."}})
	if employees != "" || turnover != "" || year != "" || source != "" {
		t.Fatalf("metrics = %q/%q/%q/%q, want empty", employees, turnover, year, source)
	}
}

func TestMarketMetricsAttachEvidenceSource(t *testing.T) {
	company := &models.MarketLandscapeCompany{
		Name: "Acme Chemicals",
		Evidence: []models.MarketLandscapeEvidence{{
			URL: "https://acme.example/profile", Snippet: "Acme has 2,500 employees and annual revenue of USD 1.2 billion in FY2023.",
		}},
	}
	applyMarketMetrics(company)
	if company.Employees != "2,500" || company.Turnover != "USD 1.2 billion" || company.FinancialYear != "FY2023" || company.MetricsSource == "" {
		t.Fatalf("market metrics = %+v", company)
	}
}
