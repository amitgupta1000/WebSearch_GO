package main

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// handleCompanyResearchLaunch turns a landscape card's company into the
// Company research flow. The card supplies the name, the geography it was
// verified against and the landscape's product; the handler prefills the
// identity-confirmation card from them and renders it. The operator confirms
// with one Enter and the dossier runs exactly as it does from the console —
// including the entity gate — and its research roster then offers per-person
// Fetch RR for the people it identified. That is the conservative replacement
// for firing RocketReach searches from every card: provider quota is spent on
// the dossiers the operator chose, and then only on the people they pick.
func (s *Server) handleCompanyResearchLaunch(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	company := strings.TrimSpace(r.FormValue("company_name"))
	if company == "" {
		s.renderError(w, "A company is required to launch company research.")
		return
	}
	location := strings.TrimSpace(r.FormValue("location"))
	activity := strings.TrimSpace(r.FormValue("activity"))
	audience := strings.TrimSpace(r.FormValue("audience"))

	launch := companyResearchLaunchForm(company, location, activity, audience)
	launchRequest := r.Clone(r.Context())
	launchRequest.Body = io.NopCloser(strings.NewReader(launch.Encode()))
	launchRequest.ContentLength = int64(len(launch.Encode()))
	launchRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	launchRequest.PostForm = launch
	launchRequest.Form = launch
	s.handleResolveEntity(w, launchRequest)
}

// companyResearchLaunchForm maps a landscape card onto the identity-
// confirmation form the console flow already speaks. The card's geography
// prefills the brief's country, the landscape's product prefills the industry,
// and the run's provenance travels in the notes so the dossier says where it
// came from. RocketReach discovery stays off for the run itself: the roster's
// per-person Fetch RR spends provider quota only on the people the operator
// actually picks.
func companyResearchLaunchForm(company, location, activity, audience string) url.Values {
	audienceLabel := "Seller"
	if strings.EqualFold(strings.TrimSpace(audience), "buyer") {
		audienceLabel = "Buyer"
	}
	notes := fmt.Sprintf("Launched from the %s Landscape for %q.", audienceLabel, activity)
	if strings.TrimSpace(location) == "" {
		notes += " The card carried no verified location; confirm or add one before running."
	}
	form := url.Values{}
	form.Set("query", company)
	form.Set("company_country", strings.TrimSpace(location))
	form.Set("company_industry", strings.TrimSpace(activity))
	form.Set("research_notes", notes)
	form.Set("mode", "company")
	form.Set("use_rocketreach_discovery", "")
	return form
}
