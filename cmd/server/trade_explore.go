package main

import (
	"crypto/subtle"
	"fmt"
	"html"
	"net/http"
	"strings"
)

// trade_explore.go gates the console's launch into the Crystal TradeDB
// frontend (the Firebase app over the same customs data).
//
// The link is deliberately server-side: the console carries a button and a
// password form, never the URL, and a successful POST answers with a redirect
// into the tab the form opened. The password is compared against the
// configuration (CRYSTAL_DB_PASSWORD, with a default), so it is not a
// client-side constant. It is a convenience gate in front of the link, not the
// app's authentication: the Firebase app's own sign-in remains the real
// boundary, and anyone who knows its URL can open it directly.

func (s *Server) handleCrystalExplore(w http.ResponseWriter, r *http.Request) {
	target, password := "", ""
	if s.cfg != nil {
		target = strings.TrimSpace(s.cfg.CrystalDBURL)
		password = s.cfg.CrystalDBPassword
	}
	if target == "" {
		s.renderCrystalExploreError(w, http.StatusServiceUnavailable,
			"The Crystal TradeDB URL is not configured on this deployment.")
		return
	}
	given := r.FormValue("password")
	if password == "" || subtle.ConstantTimeCompare([]byte(given), []byte(password)) != 1 {
		s.renderCrystalExploreError(w, http.StatusUnauthorized,
			"That password does not open the Crystal TradeDB. Try again below.")
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// renderCrystalExploreError answers in the tab the form opened, so a wrong
// password or an unconfigured URL is a page the operator can retry from rather
// than an empty fragment.
func (s *Server) renderCrystalExploreError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Frame-Options", "DENY")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, crystalExploreErrorPage, html.EscapeString(message))
}

// crystalExploreErrorPage is the standalone retry page. It carries its own
// styles because it is not part of the console's document, and it never names
// the app URL: the redirect is the only place the URL appears.
const crystalExploreErrorPage = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Crystal TradeDB</title>
<style>
	body { margin: 0; min-height: 100vh; display: grid; place-items: center; background: #020617; color: #e2e8f0; font-family: ui-sans-serif, system-ui, sans-serif; }
	main { width: min(24rem, 92vw); border: 1px solid #1e293b; background: #0f172a; border-radius: 1rem; padding: 1.5rem; }
	h1 { margin: 0 0 .5rem; font-size: 1rem; color: #fff; }
	p { margin: 0 0 1rem; font-size: .8rem; color: #94a3b8; }
	input { width: 100%%; box-sizing: border-box; background: #020617; border: 1px solid #1e293b; border-radius: .5rem; padding: .55rem .7rem; color: #fff; font-size: .85rem; }
	button { margin-top: .75rem; width: 100%%; background: #0891b2; color: #fff; border: 0; border-radius: .5rem; padding: .6rem; font-size: .85rem; cursor: pointer; }
</style>
</head>
<body>
<main>
	<h1>Crystal TradeDB</h1>
	<p>%s</p>
	<form method="post" action="/api/trade/explore">
		<input type="password" name="password" required autofocus autocomplete="current-password" placeholder="Access password">
		<button type="submit">Open Crystal TradeDB</button>
	</form>
</main>
</body>
</html>
`
