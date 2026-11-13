// Diagnose why weedoo.in fails from GCP: run a matrix of access strategies
// against the site and report what each strategy actually receives. Runs on
// the SAME image as the production service (mcr playwright), so the browser
// build matches exactly. Usage:
//   node /app/weedoo-diagnose.js [--homepage-only]
'use strict';

const BROWSER_UA = 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36';
const SEARCH_INPUT_SELECTOR = 'input[placeholder*="company name" i]';
const CHALLENGE_MARKERS = ['just a moment', 'cf-challenge', 'attention required', 'captcha', 'access denied'];
// playwright is required lazily inside the browser strategies — the inertia
// test mode runs on plain HTTP and must work wherever node exists.
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
  const { chromium } = require('playwright'); // lazy require
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
// The Cloudflare managed challenge sometimes auto-clears in a real browser
// once its JS runs for a while. This strategy accepts the 403 challenge
// document and polls for the real app surfacing: the search input on the
// homepage, or DIN-shaped numbers / substantial text on the company page.
async function challengeWaitStrategy(name, url, stealth) {
  const { chromium } = require('playwright'); // lazy require
  const record = { strategy: name, url, kind: 'browser-challenge-wait' };
  const browser = await chromium.launch({ headless: true });
  try {
    const options = stealth
      ? { locale: 'en-IN', timezoneId: 'Asia/Kolkata', userAgent: BROWSER_UA }
      : {};
    const context = await browser.newContext(options);
    if (stealth) {
      await context.addInitScript(
        "Object.defineProperty(navigator, 'webdriver', {get: () => undefined});"
        + "Object.defineProperty(navigator, 'languages', {get: () => ['en-IN', 'en']});"
        + "Object.defineProperty(navigator, 'plugins', {get: () => [1, 2, 3, 4, 5]});");
    }
    const page = await context.newPage();
    const started = Date.now();
    await page.goto(url, { waitUntil: 'domcontentloaded', timeout: 45000 })
      .catch((error) => { record.goto_error = String(error).slice(0, 200); });
    const deadline = Date.now() + 75000;
    record.checks = [];
    let cleared = false;
    while (Date.now() < deadline) {
      await page.waitForTimeout(3000);
      let check = null;
      try {
        check = await page.evaluate(
          "() => ({ title: document.title, text: (document.body ? document.body.innerText : '').length,"
          + " dins: ((document.documentElement.innerHTML.match(/\\b\\d{8}\\b/g) || []).length),"
          + " input: !!document.querySelector('input[placeholder*=\"company name\" i]') })");
      } catch (error) {
        // The challenge navigation destroys the execution context mid-poll;
        // treat it as not-yet-cleared and keep waiting.
        record.checks.push({ at: Math.round((Date.now() - started) / 1000), note: 'context lost during challenge navigation' });
        continue;
      }
      if (!check) {
        continue;
      }
      const at = Math.round((Date.now() - started) / 1000);
      record.checks.push({ at, title: String(check.title).slice(0, 60), text: check.text, dins: check.dins, input: check.input });
      if (check.input || check.dins >= 5 || check.text > 2000) {
        cleared = true;
        break;
      }
    }
    record.cleared = cleared;
    record.cleared_after_seconds = cleared ? Math.round((Date.now() - started) / 1000) : null;
    if (cleared) {
      try {
        record.title = (await page.title() || '').slice(0, 120);
        record.text_length = await page.evaluate("() => (document.body ? document.body.innerText : '').length");
        record.din_count = ((await page.content()).match(/\b\d{8}\b/g) || []).length;
        record.search_input_present = (await page.locator(SEARCH_INPUT_SELECTOR).count()) > 0;
      } catch (error) {
        record.read_error = String(error).slice(0, 200);
      }
    }
  } catch (error) {
    record.error = String(error).slice(0, 300);
  } finally {
    await browser.close();
  }
  return record;
}

(async () => {
  console.log('[ARGV]', JSON.stringify(process.argv));
  const proxyEnv = process.env.HTTPS_PROXY || process.env.HTTP_PROXY || process.env.https_proxy || process.env.http_proxy || '';
  console.log('[INERTIA] egress: ' + (proxyEnv ? 'via proxy ' + proxyEnv.replace(/\/\/[^@]*@/, '//***@') : 'direct (no HTTPS_PROXY set)'));
  // --inertia-test (or DIAGNOSE_MODE=inertia) runs the sitemap + X-Inertia
  // JSON recipe instead of the rendering matrix. The env var exists because
  // Cloud Run Jobs passes --args as one concatenated string, which node
  // cannot split back into argv.
  if (process.argv.includes('--inertia-test') || process.env.DIAGNOSE_MODE === 'inertia') {
    await inertiaTestMain();
    return;
  }
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
    // The challenge-wait strategies: the Cloudflare managed challenge may
    // clear by itself once its JavaScript runs — poll for the real app.
    records.push(await challengeWaitStrategy(`${label}/challenge-wait`, url, false));
    records.push(await challengeWaitStrategy(`${label}/challenge-wait-stealth`, url, true));
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

// === X-Inertia JSON route (the user-verified recipe) ===
// A browser User-Agent passes Cloudflare; the X-Inertia header turns company
// and director pages into structured JSON (props.company) instead of HTML —
// no browser needed. A cache-busting query is required because Cloudflare
// caches HTML and ignores the Vary: X-Inertia header.

function inertiaHeaders() {
  return {
    'User-Agent': BROWSER_UA,
    'X-Inertia': 'true',
    'X-Requested-With': 'XMLHttpRequest',
    'Accept': 'text/html, application/xhtml+xml',
    'Accept-Language': 'en-US,en;q=0.9',
  };
}

function cacheBust(url) {
  return url + (url.includes('?') ? '&' : '?') + 'x=' + Date.now();
}

const inertiaSleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function fetchTextWithRetry(url, headers, attempt) {
  attempt = attempt || 1;
  try {
    const response = await fetch(cacheBust(url), { headers });
    if (response.status === 403 || response.status === 429) {
      throw new Error('HTTP ' + response.status + ' (cf-mitigated: '
        + (response.headers.get('cf-mitigated') || 'none') + ')');
    }
    if (!response.ok) {
      throw new Error('HTTP ' + response.status);
    }
    return await response.text();
  } catch (error) {
    if (attempt >= 3) throw error;
    const backoffMs = 2000 * attempt * 2; // 4s, 8s
    console.log('[INERTIA] retry ' + attempt + '/2 for ' + url.slice(0, 90)
      + ' after ' + backoffMs + 'ms (' + String(error).slice(0, 120) + ')');
    await inertiaSleep(backoffMs);
    return fetchTextWithRetry(url, headers, attempt + 1);
  }
}

function describeFetchError(error) {
	const text = String(error);
	if (error && error.cause) {
		return text + ' :: ' + String(error.cause).slice(0, 160);
	}
	return text;
}

async function fetchInertiaJson(url, attempt) {
  attempt = attempt || 1;
  try {
    const response = await fetch(cacheBust(url), { headers: inertiaHeaders() });
    const contentType = (response.headers.get('content-type') || '').toLowerCase();
    const mitigated = response.headers.get('cf-mitigated') || 'none';
    if (response.status === 403 || response.status === 429) {
      throw new Error('HTTP ' + response.status + ' (cf-mitigated: ' + mitigated + ')');
    }
    if (!contentType.includes('json')) {
      throw new Error('expected application/json, got ' + contentType.split(';')[0]);
    }
    const body = await response.json();
    return body.props || body;
  } catch (error) {
    if (attempt >= 3) throw error;
    const backoffMs = 2000 * attempt * 2; // 4s, 8s
    console.log('[INERTIA] retry ' + attempt + '/2 for ' + url.slice(0, 90)
      + ' after ' + backoffMs + 'ms (' + String(error).slice(0, 120) + ')');
    await inertiaSleep(backoffMs);
    return fetchInertiaJson(url, attempt + 1);
  }
}

async function inertiaTestMain() {
  const fs = require('fs');
  const argv = process.argv.slice(2);
  const flagValue = (flag) => {
    const index = argv.indexOf(flag);
    return index >= 0 && argv[index + 1] !== undefined ? argv[index + 1] : null;
  };
  const sitemapLimit = Number(flagValue('--sitemap-limit') || 2);
  const sampleSize = Number(flagValue('--sample') || 5);
  const csvPath = flagValue('--csv') || '/tmp/weedoo-inertia.csv';
  const singleUrl = flagValue('--url');
  const directorUrl = flagValue('--director') || 'https://weedoo.in/director/KANHAIYA-LAL-JAIN/00033519';

  const rows = [];
  if (singleUrl) {
    try {
      const props = await fetchInertiaJson(singleUrl);
      const company = props.company || {};
      rows.push({ url: singleUrl, cin: company.cin || '', name: company.name || '',
        status: company.status || '', roc: company.roc || '',
        directors_count: Array.isArray(company.directors) ? company.directors.length : '',
        keys: Object.keys(company).join(';') });
    } catch (error) {
      rows.push({ url: singleUrl, error: describeFetchError(error).slice(0, 220) });
    }
  } else {
    console.log('[INERTIA] enumerating sitemaps (limit ' + sitemapLimit + ')...');
    try {
    const indexXml = await fetchTextWithRetry('https://weedoo.in/sitemaps/index.xml',
      { 'User-Agent': BROWSER_UA });
    const sitemaps = Array.from(indexXml.matchAll(/<loc>([^<]+)<\/loc>/g)).map((m) => m[1])
      .filter((u) => u.includes('companies-')).slice(0, sitemapLimit);
    console.log('[INERTIA] testing ' + sitemaps.length + ' sitemap file(s)');
    const urls = [];
    for (const sitemap of sitemaps) {
      const xml = await fetchTextWithRetry(sitemap, { 'User-Agent': BROWSER_UA });
      for (const match of xml.matchAll(/<loc>(https:\/\/weedoo\.in\/company\/[^<]+)<\/loc>/g)) {
        urls.push(match[1]);
      }
      await inertiaSleep(800);
    }
    console.log('[INERTIA] enumerated ' + urls.length + ' company URL(s); sampling ' + Math.min(sampleSize, urls.length));
    for (const url of urls.slice(0, sampleSize)) {
      try {
        const props = await fetchInertiaJson(url);
        const company = props.company || {};
        const directors = Array.isArray(company.directors) ? company.directors : null;
        rows.push({ url, cin: company.cin || '', name: company.name || '',
          status: company.status || '', roc: company.roc || '',
          directors_count: directors ? directors.length : '',
          director_names: directors
            ? directors.map((d) => d.name || d.director_name || '').filter(Boolean).join('; ')
            : '' });
      } catch (error) {
        rows.push({ url, error: describeFetchError(error).slice(0, 220) });
      }
      await inertiaSleep(1200);
    }
    } catch (error) {
      console.log('[INERTIA] enumeration failed: ' + String(error).slice(0, 200));
      rows.push({ url: 'https://weedoo.in/sitemaps/index.xml', kind: 'sitemap-index',
        error: describeFetchError(error).slice(0, 220) });
    }
  }
  if (directorUrl) {
    try {
      const props = await fetchInertiaJson(directorUrl);
      const director = props.director || props.company || props;
      rows.push({ url: directorUrl, kind: 'director-page', name: director.name || '',
        cin: director.cin || '', keys: Object.keys(props).join(';'),
        companies: Array.isArray(director.companies) ? director.companies.length
          : Object.keys(director).length });
    } catch (error) {
      rows.push({ url: directorUrl, kind: 'director-page', error: describeFetchError(error).slice(0, 220) });
    }
  }

  const columns = ['url', 'kind', 'cin', 'name', 'status', 'roc', 'directors_count',
    'director_names', 'keys', 'companies', 'error'];
  const escapeCsv = (value) => {
    const text = value === undefined || value === null ? '' : String(value);
    return text.includes(',') || text.includes('"') || text.includes('\n')
      ? '"' + text.replace(/"/g, '""') + '"' : text;
  };
  const csvLines = [columns.join(',')];
  for (const row of rows) {
    csvLines.push(columns.map((column) => escapeCsv(row[column])).join(','));
  }
  fs.writeFileSync(csvPath, csvLines.join('\n') + '\n');
  console.log(csvLines.join('\n'));
  console.log('[INERTIA] CSV written to ' + csvPath + '; '
    + rows.filter((r) => r.cin).length + '/' + rows.length + ' fetched as structured JSON');
}
