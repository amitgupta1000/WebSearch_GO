// Diagnose why weedoo.in fails from GCP: run a matrix of access strategies
// against the site and report what each strategy actually receives. Runs on
// the SAME image as the production service (mcr playwright), so the browser
// build matches exactly. Usage:
//   node /app/weedoo-diagnose.js [--homepage-only]
'use strict';

const { chromium } = require('playwright');

const BROWSER_UA = 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36';
const SEARCH_INPUT_SELECTOR = 'input[placeholder*="company name" i]';
const CHALLENGE_MARKERS = ['just a moment', 'cf-challenge', 'attention required', 'captcha', 'access denied'];
const HOME = process.env.DIAGNOSE_HOME || 'https://weedoo.in/';
const COMPANY = process.env.DIAGNOSE_COMPANY || 'https://weedoo.in/company/KLJ-RESOURCES-LTD/L67120WB1986PLC041487';

async function httpStrategy(name, url, ua) {
  const record = { strategy: name, url, kind: 'http' };
  try {
    const started = Date.now();
    const response = await fetch(url, { headers: { 'User-Agent': ua, 'Accept-Language': 'en-IN,en;q=0.9' } });
    const body = await response.text();
    record.status = response.status;
    record.headers = {};
    for (const header of ['server', 'content-type', 'cf-ray', 'cf-mitigated', 'x-powered-by']) {
      if (response.headers.has(header)) record.headers[header] = response.headers.get(header);
    }
    record.bytes = body.length;
    record.seconds = (Date.now() - started) / 1000;
    const title = body.match(/<title[^>]*>([\s\S]*?)<\/title>/i);
    record.title = title ? title[1].trim().slice(0, 120) : '';
    const lowered = body.toLowerCase();
    record.challenge_markers = CHALLENGE_MARKERS.filter((marker) => lowered.includes(marker));
    record.search_input_marker = lowered.includes('company name');
    record.din_count = (lowered.match(/\b\d{8}\b/g) || []).length;
    record.text_length = lowered.replace(/<[^>]+>/g, ' ').length;
  } catch (error) {
    record.error = String(error).slice(0, 300);
  }
  return record;
}

async function browserStrategy(name, url, production, stealth, waitInput) {
  const record = { strategy: name, url, kind: 'browser' };
  const browser = await chromium.launch({ headless: true });
  try {
    const options = {};
    if (production) { options.locale = 'en-US'; options.viewport = { width: 1440, height: 900 }; }
    if (stealth) { options.locale = 'en-IN'; options.timezoneId = 'Asia/Kolkata'; options.userAgent = BROWSER_UA; }
    const context = await browser.newContext(options);
    if (production) {
      // The production renderer blocks images, media, fonts and stylesheets
      // (blockHeavyResources in scraper_browser.go).
      await context.route('**/*', (route) => {
        const type = route.request().resourceType();
        if (type === 'image' || type === 'media' || type === 'font' || type === 'stylesheet') {
          return route.abort();
        }
        return route.continue();
      });
    }
    if (stealth) {
      await context.addInitScript(
        "Object.defineProperty(navigator, 'webdriver', {get: () => undefined});"
        + "Object.defineProperty(navigator, 'languages', {get: () => ['en-IN', 'en']});"
        + "Object.defineProperty(navigator, 'plugins', {get: () => [1, 2, 3, 4, 5]});");
    }
    const page = await context.newPage();
    const consoleErrors = [];
    page.on('console', (message) => { if (message.type() === 'error') consoleErrors.push(message.text()); });
    try {
      const response = await page.goto(url, { waitUntil: 'domcontentloaded', timeout: 45000 });
      record.status = response ? response.status() : null;
      record.final_url = page.url();
      try { await page.waitForLoadState('networkidle', { timeout: 15000 }); } catch (e) { record.networkidle = 'timeout'; }
      await page.waitForTimeout(3000);
      record.title = (await page.title() || '').slice(0, 120);
      record.text_length = await page.evaluate("() => (document.body ? document.body.innerText : '').length");
      record.search_input_present = (await page.locator(SEARCH_INPUT_SELECTOR).count()) > 0;
      if (waitInput) {
        try { await page.waitForSelector(SEARCH_INPUT_SELECTOR, { timeout: 20000 }); record.search_input_after_wait = true; }
        catch (e) { record.search_input_after_wait = false; }
      }
      record.din_count = ((await page.content()).match(/\b\d{8}\b/g) || []).length;
      record.console_errors = consoleErrors.slice(0, 5);
    } catch (error) {
      record.error = String(error).slice(0, 300);
    } finally {
      await context.close();
    }
  } finally {
    await browser.close();
  }
  return record;
}
(async () => {
  const homepageOnly = process.argv.includes('--homepage-only');
  const targets = [['homepage', HOME]];
  if (!homepageOnly) targets.push(['company-page', COMPANY]);

  const records = [];
  for (const [label, url] of targets) {
    records.push(await httpStrategy(`${label}/http-plain`, url, 'node-fetch'));
    records.push(await httpStrategy(`${label}/http-browser-ua`, url, BROWSER_UA));
    records.push(await browserStrategy(`${label}/browser-default`, url, false, false));
    records.push(await browserStrategy(`${label}/browser-production`, url, true, false));
    if (label === 'homepage') {
      records.push(await browserStrategy(`${label}/browser-stealth`, url, false, true, true));
    }
  }

  for (const record of records) console.log(JSON.stringify(record));
  console.log('');
  console.log('=== SUMMARY ===');
  for (const record of records) {
    let line = record.strategy.padEnd(42)
      + ' status=' + (record.status === undefined ? '?' : record.status)
      + ' text=' + (record.text_length === undefined ? (record.bytes === undefined ? '?' : record.bytes) : record.text_length)
      + ' input=' + String(record.search_input_present !== undefined
        ? record.search_input_present
        : (record.search_input_marker === undefined ? '?' : record.search_input_marker))
      + ' dins=' + (record.din_count === undefined ? '?' : record.din_count);
    if (record.error) line += ' ERROR=' + record.error;
    if (record.challenge_markers && record.challenge_markers.length) {
      line += ' CHALLENGE=' + record.challenge_markers.join(',');
    }
    console.log(line);
  }
})().catch((error) => { console.error('MATRIX FAILED:', error); process.exit(1); });
