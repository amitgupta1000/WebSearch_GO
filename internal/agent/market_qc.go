package agent

import (
	"strings"

	"github.com/amitgupta1000/WebSearch_GO/internal/models"
)

// MarketQCPromptLimit bounds the operator-editable policy. The prompt is sent to
// the reviewer alongside the company rows, so an unbounded textarea would let one
// run consume an arbitrary amount of model context.
const MarketQCPromptLimit = 6000

// DefaultMarketQCPrompt is the QC policy shown in the console. It is a policy,
// not the full prompt: the runner appends the activity, coverage target and
// company evidence. Keeping the default in the agent makes the console and the
// CLI use exactly the same criteria.
const DefaultMarketQCPrompt = `Reject a company when it is a publisher, directory, procurement portal, person, category, article,
product page, association or generic aggregator; when it does not actually produce, manufacture or
sell the activity; when it belongs to a different activity; or when its evidence only mentions the
activity in passing. Reject engineering firms, plant/equipment vendors, automation suppliers and
process explainers unless the evidence shows they also operate the production/blending process
themselves.
Important process-activity rule: if the activity names a process such as blending, manufacturing or
processing, accept a company that operates that process for its own products or output, even when it
does not offer the process as a service to third parties. A lubricant manufacturer that runs a
blending plant is a valid producer/seller of lubricant blending; a plant/equipment vendor,
engineering consultancy, laboratory or directory is not. Do not require the word "blending" in the
evidence: evidence that a company manufactures or refines the product is sufficient unless it shows
the company is only a reseller, distributor, consultant or equipment vendor.
Do not invent companies or replacements. Report missing coverage rather than adding names that are
not in the supplied list.`

// marketQCPolicy resolves the editable policy and labels its source for the
// report. An empty field is the default, not an instruction to skip QC.
func marketQCPolicy(query models.MarketLandscapeQuery) (string, string) {
	policy := strings.TrimSpace(query.QCPrompt)
	if policy == "" {
		return DefaultMarketQCPrompt, "default"
	}
	// The console submits the prefilled default verbatim. Treat that as the
	// default policy, not as a custom edit, so the report distinguishes a
	// reviewed-but-unchanged run from a genuinely modified one.
	if strings.Join(strings.Fields(policy), " ") == strings.Join(strings.Fields(DefaultMarketQCPrompt), " ") {
		return DefaultMarketQCPrompt, "default"
	}
	if len(policy) > MarketQCPromptLimit {
		policy = policy[:MarketQCPromptLimit]
	}
	return policy, "custom"
}

// marketQCVerdicts turns the reviewer's rejection list into one row per company
// it saw. A pass still carries a reason: the QC tab is evidence about the review,
// not just a coloured badge.
func marketQCVerdicts(submitted []*models.MarketLandscapeCompany, rejected map[string]string, fallbackReason string) ([]models.MarketQCVerdict, int, int) {
	verdicts := make([]models.MarketQCVerdict, 0, len(submitted))
	passed, failed := 0, 0
	for _, company := range submitted {
		if company == nil {
			continue
		}
		verdict := "passed"
		reason := "No QC objection; the company stays in the landscape."
		if fallbackReason != "" {
			verdict = "unreviewed"
			reason = fallbackReason
		}
		if rejection, ok := rejected[normalizeMarketName(company.Name)]; ok {
			verdict = "failed"
			reason = rejection
			if strings.TrimSpace(reason) == "" {
				reason = "QC reviewer rejected the company without a reason."
			}
		}
		switch verdict {
		case "passed":
			passed++
		case "failed":
			failed++
		}
		evidenceURL := ""
		if len(company.Evidence) > 0 {
			evidenceURL = company.Evidence[0].URL
		}
		verdicts = append(verdicts, models.MarketQCVerdict{
			Name: company.Name, Verdict: verdict, Reason: reason,
			Status: marketStatus(company), Country: company.Country,
			Domain: company.Domain, EvidenceURL: evidenceURL,
		})
	}
	return verdicts, passed, failed
}
