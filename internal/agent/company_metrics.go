package agent

import (
	"regexp"
	"strings"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

var (
	companyEmployeesPattern     = regexp.MustCompile(`(?i)(?:employs|workforce(?:\s+(?:of|is|stands\s+at))?|headcount(?:\s+(?:of|is))?|staff(?:\s+strength)?|employee\s+count)\s*(?:about|around|approximately|over|more\s+than|nearly|~)?\s*([0-9][0-9,.]*(?:\s*(?:-|–|to)\s*[0-9][0-9,.]*)?\+?)\s*(?:employees|people|staff|workers)?`)
	companyEmployeeFallback     = regexp.MustCompile(`(?i)([0-9][0-9,.]*(?:\s*(?:-|–|to)\s*[0-9][0-9,.]*)?\+?)\s*(?:employees|people|staff|workers)`)
	companyTurnoverPattern      = regexp.MustCompile(`(?i)(?:annual\s+)?(?:turnover|revenue|sales|gross\s+revenue|net\s+sales)\s*(?:of|was|were|is|:|-)?\s*((?:US\s*\$|USD|INR|Rs\.?|₹|\$|€|£)?\s*[0-9][0-9,.]*\s*(?:crore|lakh|million|billion|bn|mn|trillion)?)(?:\s*(?:in|for|during)?\s*(FY\s?\d{2,4}|\d{4}[-/]\d{2,4})?)?`)
	companyFinancialYearPattern = regexp.MustCompile(`(?i)\b(?:FY\s?\d{2,4}|fiscal(?:\s+year)?\s+\d{2,4}|\d{4}[-/]\d{2,4})\b`)
)

type companyMetricText struct {
	Text string
	URL  string
}

func companyMetricsFromTexts(items []companyMetricText) (employees, turnover, financialYear, source string) {
	for _, item := range items {
		text := strings.Join(strings.Fields(item.Text), " ")
		if text == "" {
			continue
		}
		if employees == "" {
			if match := companyEmployeesPattern.FindStringSubmatch(text); len(match) > 1 {
				employees = strings.TrimSpace(match[1])
			} else if match := companyEmployeeFallback.FindStringSubmatch(text); len(match) > 1 {
				employees = strings.TrimSpace(match[1])
			}
		}
		if turnover == "" {
			if match := companyTurnoverPattern.FindStringSubmatch(text); len(match) > 1 {
				turnover = strings.TrimSpace(match[1])
				if len(match) > 2 && match[2] != "" {
					financialYear = strings.TrimSpace(match[2])
				}
			}
		}
		if financialYear == "" {
			financialYear = strings.TrimSpace(companyFinancialYearPattern.FindString(text))
		}
		if source == "" && (employees != "" || turnover != "" || financialYear != "") {
			source = strings.TrimSpace(item.URL)
		}
		if employees != "" && turnover != "" && financialYear != "" && source != "" {
			break
		}
	}
	return employees, turnover, financialYear, source
}

// applyProfileMetrics fills only missing profile fields from the run's retrieved
// evidence. The model may have already returned a supported value; the
// deterministic extractor is the fallback and always names the source URL.
func applyProfileMetrics(profile *models.CompanyProfile, evidence []models.Evidence) {
	if profile == nil {
		return
	}
	items := make([]companyMetricText, 0, len(evidence))
	for _, item := range evidence {
		items = append(items, companyMetricText{Text: item.Title + " " + item.Content, URL: item.URL})
	}
	employees, turnover, financialYear, source := companyMetricsFromTexts(items)
	if strings.TrimSpace(profile.Employees) == "" {
		profile.Employees = employees
	}
	if strings.TrimSpace(profile.Turnover) == "" {
		profile.Turnover = turnover
	}
	if strings.TrimSpace(profile.FinancialYear) == "" {
		profile.FinancialYear = financialYear
	}
	if strings.TrimSpace(profile.MetricsSource) == "" {
		profile.MetricsSource = source
	}
}

func applyMarketMetrics(company *models.MarketLandscapeCompany) {
	if company == nil {
		return
	}
	items := make([]companyMetricText, 0, len(company.Evidence))
	for _, evidence := range company.Evidence {
		items = append(items, companyMetricText{
			Text: evidence.Title + " " + evidence.Snippet + " " + evidence.Content,
			URL:  evidence.URL,
		})
	}
	employees, turnover, financialYear, source := companyMetricsFromTexts(items)
	if strings.TrimSpace(company.Employees) == "" {
		company.Employees = employees
	}
	if strings.TrimSpace(company.Turnover) == "" {
		company.Turnover = turnover
	}
	if strings.TrimSpace(company.FinancialYear) == "" {
		company.FinancialYear = financialYear
	}
	if strings.TrimSpace(company.MetricsSource) == "" {
		company.MetricsSource = source
	}
}
