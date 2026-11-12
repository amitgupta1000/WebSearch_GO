"""Diagnose why weedoo.in fails from GCP: run a matrix of access strategies
against the site and report what each strategy actually receives.

Run locally for the baseline, then as a Cloud Run Job in asia-south1 (same
egress as production) and compare. Usage:
    python scripts/weedoo-diagnose.py                     # homepage + default CIN page
    python scripts/weedoo-diagnose.py --homepage-only     # skip the company page
    python scripts/weedoo-diagnose.py --out artifacts/weedoo-diagnose.json

Strategies:
  http-plain          plain HTTP GET, default client UA
  http-browser-ua     plain HTTP GET, Chrome browser UA
  browser-default     headless Chromium, default context (the local probes' shape)
  browser-production  headless Chromium with the production renderer's exact
                      settings: images/media/fonts/stylesheets blocked, locale
                      en-US, 1440x900, domcontentloaded + network-idle + settle
  browser-stealth     headless Chromium with webdriver/UA/locale/timezone
                      overrides (anti-headless-detection)

Each record: strategy, url, http status, final URL, title, visible-text length,
search-input present, DIN-shaped numbers on the page, server headers, and the
screenshot path.
"""
import argparse
import json
import re
import time
from pathlib import Path
from urllib.request import Request, urlopen
from urllib.error import HTTPError, URLError

from playwright.sync_api import sync_playwright

BROWSER_UA = ("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
              "(KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
DIN_PATTERN = re.compile(r"\b\d{8}\b")
TITLE_PATTERN = re.compile(r"<title[^>]*>(.*?)</title>", re.S | re.I)
SEARCH_INPUT_SELECTOR = 'input[placeholder*="company name" i]'

CHALLENGE_MARKERS = ("just a moment", "cf-challenge", "attention required",
                     "captcha", "access denied")


def http_strategy(name, url, ua):
    record = {"strategy": name, "url": url, "kind": "http"}
    try:
        request = Request(url, headers={
            "User-Agent": ua,
            "Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
            "Accept-Language": "en-IN,en;q=0.9",
        })
        started = time.time()
        with urlopen(request, timeout=45) as response:
            body = response.read()
            record["status"] = response.status
            record["final_url"] = response.geturl()
            record["headers"] = {key: value for key, value in response.headers.items()
                                 if key.lower() in ("server", "content-type", "cf-ray",
                                                    "cf-mitigated", "x-powered-by")}
        record["bytes"] = len(body)
        record["seconds"] = round(time.time() - started, 2)
        decoded = body.decode("utf-8", "replace")
        title = TITLE_PATTERN.search(decoded)
        record["title"] = title.group(1).strip()[:120] if title else ""
        lowered = decoded.lower()
        record["challenge_markers"] = [marker for marker in CHALLENGE_MARKERS if marker in lowered]
        record["search_input_marker"] = "company name" in lowered
        record["din_count"] = len(DIN_PATTERN.findall(lowered))
        record["text_length"] = len(re.sub(r"<[^>]+>", " ", lowered))
    except HTTPError as error:
        record["status"] = error.code
        record["error"] = f"HTTP {error.code}"
    except URLError as error:
        record["error"] = f"URLError: {error.reason}"
    except Exception as error:  # noqa: BLE001 - diagnostics must never die mid-matrix
        record["error"] = str(error)
    return record
def browser_strategy(name, url, out_dir, production=False, stealth=False, wait_input=False):
    record = {"strategy": name, "url": url, "kind": "browser"}
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        options = {}
        if production:
            options["locale"] = "en-US"
            options["viewport"] = {"width": 1440, "height": 900}
        if stealth:
            options["locale"] = "en-IN"
            options["timezone_id"] = "Asia/Kolkata"
            options["user_agent"] = BROWSER_UA
        context = browser.new_context(**options)
        if production:
            # The production renderer blocks images, media, fonts and
            # stylesheets (blockHeavyResources in scraper_browser.go).
            def block(route):
                if route.request.resource_type in ("image", "media", "font", "stylesheet"):
                    route.abort()
                    return
                route.continue_()
            context.route("**/*", block)
        if stealth:
            context.add_init_script(
                "Object.defineProperty(navigator, 'webdriver', {get: () => undefined});"
                "Object.defineProperty(navigator, 'languages', {get: () => ['en-IN', 'en']});"
                "Object.defineProperty(navigator, 'plugins', {get: () => [1, 2, 3, 4, 5]});"
            )
        page = context.new_page()
        console_errors = []
        page.on("console", lambda message: console_errors.append(message.text)
                if message.type == "error" else None)
        try:
            response = page.goto(url, wait_until="domcontentloaded", timeout=45000)
            record["status"] = response.status if response else None
            record["final_url"] = page.url
            try:
                page.wait_for_load_state("networkidle", timeout=15000)
            except Exception:
                record["networkidle"] = "timeout"
            page.wait_for_timeout(3000)
            record["title"] = (page.title() or "")[:120]
            record["text_length"] = page.evaluate(
                "() => (document.body ? document.body.innerText : '').length")
            record["search_input_present"] = page.locator(SEARCH_INPUT_SELECTOR).count() > 0
            if wait_input:
                try:
                    page.wait_for_selector(SEARCH_INPUT_SELECTOR, timeout=20000)
                    record["search_input_after_wait"] = True
                except Exception:
                    record["search_input_after_wait"] = False
            record["din_count"] = len(DIN_PATTERN.findall(page.content()))
            record["console_errors"] = console_errors[:5]
            shot = Path(out_dir) / ("weedoo-" + name.replace("/", "-") + ".png")
            try:
                page.screenshot(path=str(shot), full_page=False)
                record["screenshot"] = str(shot)
            except Exception:
                pass
        except Exception as error:
            record["error"] = str(error)[:300]
        finally:
            browser.close()
    return record

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--homepage", default="https://weedoo.in/")
    parser.add_argument("--company-page",
                        default="https://weedoo.in/company/KLJ-RESOURCES-LTD/L67120WB1986PLC041487")
    parser.add_argument("--homepage-only", action="store_true")
    parser.add_argument("--out-dir", default=".", help="screenshot directory")
    arguments = parser.parse_args()

    targets = [("homepage", arguments.homepage)]
    if not arguments.homepage_only:
        targets.append(("company-page", arguments.company_page))

    records = []
    for label, url in targets:
        records.append(http_strategy(label + "/http-plain", url, "Python-urllib/3.x"))
        records.append(http_strategy(label + "/http-browser-ua", url, BROWSER_UA))
        records.append(browser_strategy(label + "/browser-default", url, arguments.out_dir))
        records.append(browser_strategy(label + "/browser-production", url, arguments.out_dir,
                                        production=True))
        if label == "homepage":
            records.append(browser_strategy(label + "/browser-stealth", url, arguments.out_dir,
                                            stealth=True, wait_input=True))

    for record in records:
        print(json.dumps(record, ensure_ascii=False))

    print()
    print("=== SUMMARY ===")
    for record in records:
        line = (record["strategy"].ljust(42) + " status=" + str(record.get("status"))
                + " text=" + str(record.get("text_length", record.get("bytes", "?")))
                + " input=" + str(record.get("search_input_present",
                                             record.get("search_input_marker", "?")))
                + " dins=" + str(record.get("din_count", "?")))
        if record.get("error"):
            line += " ERROR=" + str(record["error"])
        if record.get("challenge_markers"):
            line += " CHALLENGE=" + str(record["challenge_markers"])
        print(line)


if __name__ == "__main__":
    main()
