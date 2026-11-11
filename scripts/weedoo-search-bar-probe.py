"""Probe 2: interact with WeeDoo's search bar (fill + Enter) and read results."""
import re
from playwright.sync_api import sync_playwright

QUERY = "KLJ Resources Limited"


def links(page, pattern):
    return sorted(set(re.findall(pattern, page.content())))


def main():
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_context().new_page()

        page.goto("https://weedoo.in/", wait_until="networkidle", timeout=60000)
        page.wait_for_timeout(2000)
        inputs = page.locator("input")
        count = inputs.count()
        print(f"INPUTS on homepage: {count}")
        for index in range(count):
            print("   ", inputs.nth(index).get_attribute("name"),
                  "|", inputs.nth(index).get_attribute("placeholder"),
                  "|", inputs.nth(index).get_attribute("type"))

        # Fill the first visible text input and press Enter.
        target = None
        for index in range(count):
            if (inputs.nth(index).get_attribute("type") or "text") in ("text", "search"):
                target = inputs.nth(index)
                break
        if target is None:
            print("no text input found")
            return
        target.fill(QUERY)
        target.press("Enter")
        page.wait_for_timeout(6000)
        print("URL after search:", page.url)
        all_links = links(page, r'href="(/company/[^"]+)"')
        klj_links = [link for link in all_links if "KLJ" in link.upper()]
        print(f"ALL company links: {len(all_links)}; KLJ links: {len(klj_links)}")
        for link in klj_links:
            print("   KLJ:", link)
        # The typeahead suggestion rows: dump their text + hrefs.
        suggestions = page.locator("a, li, div").filter(has_text="L67120WB1986PLC041487")
        suggestion_count = suggestions.count()
        print(f"SUGGESTION nodes mentioning the CIN: {suggestion_count}")
        for index in range(min(suggestion_count, 5)):
            node = suggestions.nth(index)
            tag = node.evaluate("node => node.tagName")
            href = node.get_attribute("href")
            text = " | ".join(node.inner_text().split("\n"))[:160]
            print(f"   <{tag}> href={href} text={text}")
        # Click the suggestion that carries the company page link, if any.
        link_node = page.locator(f'a[href*="{QUERY.split()[0].upper()}" i]').first
        if link_node.count():
            href = link_node.get_attribute("href")
            print("CLICKING:", href)
            link_node.click()
            page.wait_for_timeout(5000)
            print("URL after click:", page.url)
            din_hits = len(re.findall(r"\b\d{8}\b", page.content()))
            director_links = links(page, r'href="(/director/[^"]+)"')
            other = [link for link in links(page, r'href="(/company/[^"]+)"') if "KLJ-RESOURCES" not in link.upper()]
            print(f"CIN/8-digit numbers: {din_hits}, director links: {len(director_links)}, other-company links: {len(other)}")
            for link in other[:8]:
                print("   other:", link)

        browser.close()


if __name__ == "__main__":
    main()