"""Probe 3: where is the directors table in the rendered WeeDoo page text, and
does it carry the literal 'DIN <digits>' label the extractor expects?"""
from playwright.sync_api import sync_playwright

URL = "https://weedoo.in/company/KLJ-RESOURCES-LTD/L67120WB1986PLC041487"


def main():
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_context().new_page()
        page.goto(URL, wait_until="networkidle", timeout=60000)
        page.wait_for_timeout(2000)
        text = page.evaluate("() => (document.body ? document.body.innerText : '')")
        print(f"TOTAL innerText length: {len(text)}")
        lowered = text.lower()
        for marker in ("din", "director", "klj"):
            print(f"'{marker}' first at: {lowered.find(marker)}")
        # The directors section: print 700 chars around the first DIN mention.
        din_pos = lowered.find("din")
        with open("artifacts/weedoo-page-text.txt", "w", encoding="utf-8") as out:
            out.write(f"TOTAL innerText length: {len(text)}\n")
            out.write(text)
        print("WROTE artifacts/weedoo-page-text.txt")
        browser.close()


if __name__ == "__main__":
    main()