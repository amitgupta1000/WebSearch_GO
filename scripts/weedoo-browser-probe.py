"""Probe: render WeeDoo in Playwright and check what the hydrated DOM contains.

1. The companies search page for a sample company — do rendered /company/ links appear?
2. The slug-only company URL (404 on plain GET) — does the hydrated SPA render it?
3. A company page's directors table and per-director other directorships.
"""
import re
import sys
from playwright.sync_api import sync_playwright

QUERY = "KLJ Resources Limited"


def links(page, pattern):
    return sorted(set(re.findall(pattern, page.content())))


def main():
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_context().new_page()

        # 1. The search page, hydrated.
        page.goto(f"https://weedoo.in/companies/search?q={QUERY.replace(' ', '%20')}",
                  wait_until="networkidle", timeout=60000)
        page.wait_for_timeout(3000)
        company_links = links(page, r'href="(/company/[^"]+)"')
        print(f"SEARCH: {len(company_links)} company link(s) after hydration")
        for link in company_links[:8]:
            print("   ", link)

        # 2. The slug-only URL that 404s on plain GET.
        page.goto("https://weedoo.in/company/KLJ-RESOURCES-LTD",
                  wait_until="domcontentloaded", timeout=60000)
        page.wait_for_timeout(5000)
        body = page.inner_text("body")[:600].replace("\n", " | ")
        din_hits = len(re.findall(r"\b\d{8}\b", page.content()))
        directors = links(page, r'href="(/director/[^"]+)"')
        print(f"SLUG-ONLY URL: body starts: {body[:220]}")
        print(f"SLUG-ONLY URL: 8-digit numbers on page: {din_hits}, director links: {len(directors)}")
        for link in directors[:6]:
            print("   ", link)

        # 3. The CIN-form company page, rendered.
        page.goto("https://weedoo.in/company/KLJ-RESOURCES-LIMITED/L67120WB1986PLC041487",
                  wait_until="networkidle", timeout=60000)
        page.wait_for_timeout(3000)
        din_hits = len(re.findall(r"\b\d{8}\b", page.content()))
        director_links = links(page, r'href="(/director/[^"]+)"')
        other = links(page, r'href="(/company/(?!KLJ-RESOURCES)[^"]+)"')
        print(f"CIN PAGE: 8-digit numbers: {din_hits}, director links: {len(director_links)}, other-company links: {len(other)}")
        for link in director_links[:6]:
            print("   ", link)
        for link in other[:8]:
            print("   other:", link)

        browser.close()


if __name__ == "__main__":
    main()