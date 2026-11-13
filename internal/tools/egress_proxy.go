package tools

import (
	"net/http"
	"net/url"
	"os"
	"strings"
)

// Per-host egress proxies. Some hosts gate their content on the *caller's IP
// reputation* rather than on anything a request can carry: Cloudflare issues
// "just a moment" challenges to Cloud Run's datacenter IPs on sight (weedoo.in),
// and LinkedIn soft-blocks datacenter ranges with authwalls. For those hosts the
// scraper's traffic routes through a residential proxy; every other host keeps
// its direct path, or the standard HTTP(S)_PROXY environment behavior.
//
// Each knob is an env read at startup, empty by default:
//
//	WEEDOO_PROXY_URL    — http://user:pass@host:port or socks5://... for weedoo.in
//	LINKEDIN_PROXY_URL  — the same format for linkedin.com and its subdomains
//
// Scoping the routing per host is what keeps a residential proxy's bandwidth
// budget — and its cost — to the traffic that actually needs it.
var (
	weedooProxyURL   = strings.TrimSpace(os.Getenv("WEEDOO_PROXY_URL"))
	linkedinProxyURL = strings.TrimSpace(os.Getenv("LINKEDIN_PROXY_URL"))
)

// proxiedHostRule returns the proxy URL configured for a request host, or ""
// when the host has no proxy (or its knob is unset).
func proxiedHostRule(host string) string {
	lowered := strings.ToLower(host)
	for _, rule := range []struct{ suffix, proxyURL string }{
		{"weedoo.in", weedooProxyURL},
		{"linkedin.com", linkedinProxyURL},
	} {
		if rule.proxyURL == "" {
			continue
		}
		if lowered == rule.suffix || strings.HasSuffix(lowered, "."+rule.suffix) {
			return rule.proxyURL
		}
	}
	return ""
}

// scraperProxyForRequest is the http.Transport.Proxy hook: requests to a host
// with a configured egress proxy route through it (credentials travel in the
// URL's userinfo), everything else falls back to the standard environment
// proxy behavior so a global HTTPS_PROXY keeps working.
func scraperProxyForRequest(req *http.Request) (*url.URL, error) {
	if req == nil || req.URL == nil {
		return http.ProxyFromEnvironment(req)
	}
	if proxy := proxiedHostRule(req.URL.Hostname()); proxy != "" {
		return url.Parse(proxy)
	}
	return http.ProxyFromEnvironment(req)
}

// egressProxySummary lists the hosts whose egress proxy is active, for startup
// logs. Empty when no knob is set.
func egressProxySummary() string {
	var hosts []string
	for _, rule := range []struct{ suffix, proxyURL string }{
		{"weedoo.in", weedooProxyURL},
		{"linkedin.com", linkedinProxyURL},
	} {
		if rule.proxyURL != "" {
			hosts = append(hosts, rule.suffix)
		}
	}
	return strings.Join(hosts, ", ")
}
