"""Probe 4: does the rendered WeeDoo DIRECTOR page list the other companies?"""
from playwright.sync_api import sync_playwright

URL = "https://weedoo.in/director/HEMANT-JAIN/00506995"


def main():
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_context().new_page()
        page.goto(URL, wait_until="networkidle", timeout=60000)
        page.wait_for_timeout(2000)
        text = page.evaluate("() => (document.body ? document.body.innerText : '')")
        lowered = text.lower()
        with open("artifacts/weedoo-director-text.txt", "w", encoding="utf-8") as out:
            out.write(f"TOTAL innerText length: {len(text)}\n")
            out.write(text)
        for marker in ("klj resorts", "cadillac", "sidhgun", "jito", "phd chamber", "directorship", "company"):
            print(f"'{marker}': {lowered.find(marker)}")
        print("WROTE artifacts/weedoo-director-text.txt")
        browser.close()


if __name__ == "__main__":
    main()