"""Record a detailed live browser walkthrough of the WebSearch_GO console.

Usage:
    $env:WALKTHROUGH_LIVE='1'; python scripts/record_walkthrough.py

The live flag is required because every mode submits a real research run.
External provider failures remain visible in the recording and do not stop the
tour. Use WALKTHROUGH_MODE to record one mode while iterating locally.
"""

from pathlib import Path
import os
from playwright.sync_api import (
    Error as PlaywrightError,
    TimeoutError as PlaywrightTimeoutError,
    sync_playwright,
)


ROOT = Path(__file__).resolve().parents[1]
OUTPUT_DIR = ROOT / "artifacts" / "walkthrough"
BASE_URL = os.environ.get("WALKTHROUGH_URL", "http://127.0.0.1:8090")
LIVE = os.environ.get("WALKTHROUGH_LIVE") == "1"
ONLY_MODE = os.environ.get("WALKTHROUGH_MODE", "").strip()

QUERIES = {
    "company": "Microsoft",
    "person": "Satya Nadella",
    "legal": "Microsoft Corporation",
    "related_entity": "Microsoft Corporation",
    "market_landscape": "cloud infrastructure companies in the United States",
    "demand_chain": "enterprise cloud software",
    "news": "Microsoft AI announcements",
    "deepsearch": "How does Microsoft make money from cloud services?",
}


def pause(page, seconds: float = 1.4) -> None:
    page.wait_for_timeout(int(seconds * 1000))


def select_mode(page, mode: str, query: str) -> None:
    page.locator(f'input[name="mode"][value="{mode}"]').check(force=True)
    page.locator("#primary-query").fill(query)
    pause(page, 0.8)


def wait_for_text_change(page, selector: str, initial: str = "", timeout: int = 30000) -> str:
    locator = page.locator(selector)
    deadline = page.evaluate("Date.now()") + timeout
    while page.evaluate("Date.now()") < deadline:
        text = locator.inner_text() if locator.count() else ""
        if text.strip() and text.strip() != initial.strip():
            return text
        page.wait_for_timeout(500)
    return locator.inner_text() if locator.count() else ""


def wait_for_result(page, timeout: int = 180000) -> None:
    result = page.locator("#final-result-container")
    result.wait_for(state="visible", timeout=30000)
    deadline = page.evaluate("Date.now()") + timeout
    while page.evaluate("Date.now()") < deadline:
        text = result.inner_text()
        if text.strip() and "Final output will appear below" not in text:
            pause(page, 3.0)
            return
        page.wait_for_timeout(1000)
    pause(page, 2.0)


def run_mode(page, mode: str, query: str) -> None:
    page.goto(BASE_URL, wait_until="networkidle")
    pause(page, 1.5)
    select_mode(page, mode, query)

    if mode == "company":
        page.locator('input[name="company_country"]').fill("United States")
        page.locator('input[name="company_domain"]').fill("microsoft.com")
    elif mode == "person":
        page.locator('input[name="company_name"]').fill("Microsoft")
        page.locator('input[name="location"]').fill("United States")
    elif mode == "legal":
        page.locator('input[name="legal_persons"]').fill("Satya Nadella")
    elif mode == "market_landscape":
        page.locator('input[name="market_region_scope"][value="global"]').check(force=True)
        page.locator('input[name="market_people"]').check(force=True)
    elif mode == "demand_chain":
        page.locator('input[name="demand_region_scope"][value="global"]').check(force=True)
    elif mode == "news":
        page.locator('input[name="news_max_articles"]').fill("8")
    elif mode == "deepsearch":
        page.locator('input[name="deep_max_rounds"]').fill("2")
        page.locator('input[name="deep_max_scrapes"]').fill("6")
    pause(page, 1.5)

    submit_selector = "#person-submit" if mode == "person" else "#company-submit"
    confirmation_modes = {"company", "person", "legal", "related_entity"}
    if mode in confirmation_modes:
        before = page.locator("#confirmation-step").inner_text()
        page.locator(submit_selector).click(force=True, timeout=10000)
        wait_for_text_change(page, "#confirmation-step", before, 60000)
        confirm = page.locator("#confirmation-step button").filter(
            has_text="Confirm"
        ).first
        if not confirm.count():
            confirm = page.locator("#confirmation-step button").first
        confirm.click(force=True, timeout=10000)
    else:
        page.locator(submit_selector).click(force=True, timeout=10000)

    wait_for_result(page)


def main() -> None:
    if not LIVE:
        raise SystemExit("Set WALKTHROUGH_LIVE=1 to submit live research runs.")
    OUTPUT_DIR.mkdir(parents=True, exist_ok=True)
    for old_video in OUTPUT_DIR.glob("*.webm"):
        old_video.unlink()

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        context = browser.new_context(
            viewport={"width": 1440, "height": 1100},
            record_video_dir=str(OUTPUT_DIR),
            record_video_size={"width": 1440, "height": 1100},
        )
        page = context.new_page()
        modes = [ONLY_MODE] if ONLY_MODE else list(QUERIES)
        for mode in modes:
            if mode not in QUERIES:
                raise SystemExit(f"Unknown WALKTHROUGH_MODE: {mode}")
            try:
                run_mode(page, mode, QUERIES[mode])
            except (PlaywrightError, RuntimeError) as error:
                # Keep the error state on screen long enough to make provider
                # outages and identity misses visible before continuing.
                print(f"{mode}: {error}")
                pause(page, 4.0)

        page.screenshot(path=str(OUTPUT_DIR / "walkthrough-final.png"), full_page=True)
        context.close()
        browser.close()

    videos = sorted(OUTPUT_DIR.glob("*.webm"), key=lambda path: path.stat().st_mtime)
    if not videos:
        raise RuntimeError("Playwright did not produce a walkthrough video")
    final_video = OUTPUT_DIR / "websearch-go-walkthrough.webm"
    videos[-1].replace(final_video)
    print(final_video)


if __name__ == "__main__":
    main()
