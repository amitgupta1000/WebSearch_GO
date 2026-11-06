# Handover Notes

## Current state

- Repository: `amitgupta1000/WebSearch_GO`
- Latest commit: `1677dae` — `Narrow company research focus defaults`
- Worktree was clean after the commit.
- Full Go test suite passed: `472/472`.

## Company mode behavior

CompanyAgent now defaults to leadership-focused research:

- Leadership queries remain enabled by default.
- Location and company-context queries remain available for identity and background.
- Department/function searches are generated only when the user explicitly enters focus areas.
- The UI provides examples such as procurement, supply chain, and sales/marketing.
- The planner no longer adds default procurement, marketing, finance, engineering, or generic department searches.
- Indexed LinkedIn role searches are also limited to explicitly requested focus areas.

Relevant files:

- `internal/agent/company.go`
- `internal/agent/company_test.go`
- `internal/agent/sources_test.go`
- `internal/web/templates/index.html`

## Important finding

Search scope and output scope are separate.

The search planner can be narrowed while the final synthesis model still receives broad company evidence and may populate a full `departments` array. A deterministic post-synthesis department filter was tested, but rolled back at the user's request. Therefore, a full org chart can still appear even when the query plan is leadership-focused.

If output narrowing is revisited, decide explicitly whether to:

1. Keep the full org-chart schema but populate departments only from explicit focus areas; or
2. Return leadership plus company context by default and omit department output unless requested.

Any future output-scope change should be covered by synthesis-level regression tests, not only fallback-query tests.

## RocketReach and workflow context

- RocketReach discovery is opt-in in Company and Market Landscape modes.
- Company RR discovery starts before web research.
- Company RR uses `current_employer` and `current_title`.
- Market Landscape remains independent from CompanyAgent and is intended for broad company discovery and potential commercial contacts.
- CompanyAgent is reserved for deep, specific-company research.
- Company SSE terminal-session protection prevents completed logs from replaying on reconnect.

## Validation

The latest full validation command was the repository Go test suite, with all 472 tests passing. The focused CompanyAgent and web tests also passed before the full suite.

## Suggested next steps

1. Run a live Company-mode test with an empty Focus areas field and inspect:
   - planned queries,
   - selected evidence,
   - final department output.
2. Decide the desired output contract for default Company mode.
3. If departments must be absent by default, implement an explicit synthesis/output contract and add regression coverage around the final `OrgChart`, rather than filtering only after synthesis.
4. Preserve explicit focus-area behavior and confirmation-form propagation for RocketReach.
