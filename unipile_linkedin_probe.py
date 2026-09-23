"""Unipile LinkedIn search contract probe.

Purpose: turn the [inferred] and [undocumented] items in the Unipile docs
(https://developer.unipile.com/docs/linkedin-search) into measured facts before
any export code is written against that endpoint.

It answers, empirically:
  Q1  is structured `company:{include,exclude}` supported for people search?
  Q1b is it actually *applied* (differential test - catches silently dropped filters)
  Q2  unknown / unsupported params: 4xx, or silently ignored?
  Q3  `cursor` termination, `start` step, per-call depth, truncation signal
  Q4  `limit` clamping per api x category
  Q5  Sales Nav `current_positions` coverage (org-chart grade rows?)
  Q6  mini-URNAcct `id` vs `member_urn` consistency (is a local join key possible?)
  Q7  profile_url license class (does an exported link open for the end user?)
  Q8  industry-include parity: does api=classic understand the nested form?
  Q9  does /search/parameters support type=FUNCTION (department taxonomy)?
  Q10 URL-paste mode vs structured parity (the department workaround)

Every request body is echoed back by the API in `config.params`, and that echo is
the oracle used throughout: a param that vanishes from the echo was dropped.

Credentials - all three required for a live run, none for --offline:
  UNIPILE_API_KEY      X-API-KEY header value
  UNIPILE_DSN          host[:port] or full https URL of your Unipile instance
  UNIPILE_ACCOUNT_ID   connected LinkedIn account id (the !!XXXX!! from the dashboard)
  optional: UNIPILE_COMPANY_ID, UNIPILE_COMPANY_NAME, UNIPILE_LOCATION_IDS (csv),
            UNIPILE_SALESNAV_URL (a people search you built in the LinkedIn UI)

Usage:
  python unipile_linkedin_probe.py --offline              # dry run, no creds, no net
  python unipile_linkedin_probe.py --plan                 # cost estimate, nothing sent
  python unipile_linkedin_probe.py --preflight-only       # auth + subscription only
  python unipile_linkedin_probe.py --suite all --confirm  # full probe, ~250 lines

Output: ./unipile_probe_out/  (report.md, report.json, raw_exchanges.json, ledger.json)

Deliberate constraints, matching this repo's conservative fetching policy:
  * no request is sent to a third party; only your own Unipile DSN
  * randomized pacing between calls (never chained, never fixed-interval)
  * a persistent daily ledger caps fetched lines; the run reports overruns
  * a 401/403/429/500 response latches a 15-minute cooldown and the run stops
  * --confirm is required to spend quota; without it only the plan is printed
"""

from __future__ import annotations

import argparse
import base64
import binascii
import dataclasses
import datetime as dt
import hashlib
import json
import os
import random
import re
import sys
import time
from pathlib import Path
from typing import Any, Callable

try:
    import requests
except ImportError:  # pragma: no cover - --offline does not need it
    requests = None  # type: ignore

SEARCH_PATH = "/api/v1/linkedin/search"
PARAMS_PATH = "/api/v1/linkedin/search/parameters"
ACCOUNT_PATH = "/api/v1/linkedin/account"

# Unipile's recommended daily fetched-line ceilings (provider-limits page).
BUDGET_LINES = {"classic": 1000, "sales_navigator": 2500, "recruiter": 2500}
# LinkedIn per-query retrieval ceilings; total_count above this is not reachable.
RETRIEVABLE_CAP = {"classic": 1000, "sales_navigator": 2500, "recruiter": 2500}
DOC_PAGE_LIMITS = {"classic_people": 10, "classic_companies": 10, "classic_jobs": 50,
                   "classic_posts": 3, "sales_navigator": 100, "recruiter": 100}

SENTINEL = "zzdefinitely_not_a_real_field"
SENTINEL_VALUE = 987654321


# ---------------------------------------------------------------------------
# config
# ---------------------------------------------------------------------------
@dataclasses.dataclass
class Config:
    base_url: str
    api_key: str
    account_id: str
    company_id: int | None = None
    company_name: str = ""
    location_ids: list[int] = dataclasses.field(default_factory=list)
    salesnav_url: str = ""

    @classmethod
    def from_env(cls) -> "Config | None":
        key = os.environ.get("UNIPILE_API_KEY", "").strip()
        dsn = os.environ.get("UNIPILE_DSN", "").strip()
        acct = os.environ.get("UNIPILE_ACCOUNT_ID", "").strip()
        if not (key and dsn and acct):
            return None
        base = dsn if dsn.startswith(("http://", "https://")) else f"https://{dsn}"
        base = base.rstrip("/")
        if not re.search(r":\d+/?$", base):
            print("[warn] UNIPILE_DSN carries no :port - the API reference shows "
                  "{subdomain}.unipile.com:{port}; add it if requests fail.", file=sys.stderr)
        comp = os.environ.get("UNIPILE_COMPANY_ID", "").strip()
        locs = [int(x) for x in re.split(r"[,\s]+", os.environ.get("UNIPILE_LOCATION_IDS", ""))
                if x.strip().isdigit()]
        return cls(base_url=base, api_key=key, account_id=acct.strip("!"),
                   company_id=int(comp) if comp.isdigit() else None,
                   company_name=os.environ.get("UNIPILE_COMPANY_NAME", "").strip(),
                   location_ids=locs,
                   salesnav_url=os.environ.get("UNIPILE_SALESNAV_URL", "").strip())


@dataclasses.dataclass
class Resp:
    """One transport outcome. Mocked responses share this shape with real ones."""
    status: int
    body: Any
    elapsed: float = 0.0
    transport: str = "http"
    error: str | None = None

    @property
    def items(self) -> list[dict]:
        b = self.body
        return list(b.get("items") or []) if isinstance(b, dict) else []

    @property
    def paging(self) -> dict:
        b = self.body
        return dict(b.get("paging") or {}) if isinstance(b, dict) else {}

    @property
    def echoed(self) -> dict:
        b = self.body
        if isinstance(b, dict):
            cfg = b.get("config") or {}
            if isinstance(cfg, dict) and isinstance(cfg.get("params"), dict):
                return cfg["params"]
        return {}


# ---------------------------------------------------------------------------
# response analysis primitives
# ---------------------------------------------------------------------------
def norm(v: Any) -> Any:
    """Type-insensitive canonical form: doc samples disagree on 6 vs '6'."""
    if isinstance(v, bool):
        return v
    if isinstance(v, int):
        return str(v)
    if isinstance(v, str) and re.fullmatch(r"-?\d+", v):
        return str(int(v))
    if isinstance(v, list):
        return [norm(x) for x in v]
    if isinstance(v, dict):
        return {k: norm(v[k]) for k in sorted(v)}
    return v


def echo_drift(sent: dict, echoed: dict) -> tuple[list[str], list[str], list[str]]:
    """Compare a request body with the API's config.params echo.

    dropped    - sent but absent from the echo (strong sign of an ignored filter)
    type_drift - equal under norm(), different JSON type (breaks naive model parsing)
    changed    - present on both sides with a genuinely different value
    """
    dropped: list[str] = []
    type_drift: list[str] = []
    changed: list[str] = []
    for k, v in sent.items():
        if k in ("cursor", "limit", "account_id"):
            continue
        if k not in echoed:
            dropped.append(k)
            continue
        if norm(v) != norm(echoed[k]):
            changed.append(f"{k}: sent {_tr(v)} echoed {_tr(echoed[k])}")
        elif json.dumps(v, sort_keys=True) != json.dumps(echoed[k], sort_keys=True):
            type_drift.append(k)
    return dropped, type_drift, changed


def decode_cursor(cur: Any) -> dict:
    out: dict = {"present": bool(cur)}
    if not isinstance(cur, str) or not cur:
        return out
    try:
        pad = cur + "=" * (-len(cur) % 4)
        dec = json.loads(base64.urlsafe_b64decode(pad))
        out.update({"keys": sorted(dec), "limit": dec.get("limit"), "start": dec.get("start")})
        out["params_echo"] = "params" in dec
        out["account_bound"] = "account_id" in dec
    except (binascii.Error, ValueError, UnicodeDecodeError):
        out["opaque"] = True
    return out


def classify_urls(rows: list[dict]) -> dict:
    kinds: dict[str, int] = {}
    for r in rows:
        for f in ("profile_url", "public_profile_url"):
            u = r.get(f)
            if isinstance(u, str) and u.startswith("http"):
                if "/sales/" in u:
                    k = "sales_nav_only"
                elif "/talent/" in u:
                    k = "recruiter_only"
                else:
                    k = "public"
            else:
                k = "absent"
            key = f"{f}:{k}"
            kinds[key] = kinds.get(key, 0) + 1
    return kinds


def employer_match(rows: list[dict], company_name: str) -> tuple[float | None, list[str]]:
    """String-level corroboration of the company filter. An ID check is not possible:
    the only doc sample carrying current_positions has company_id null."""
    toks = [t for t in re.split(r"[^A-Za-z0-9]+", company_name.lower()) if len(t) > 2]
    if not toks or not rows:
        return None, []
    hits, missed = 0, []
    for r in rows:
        hay = json.dumps(r, ensure_ascii=False).lower()
        if any(t in hay for t in toks):
            hits += 1
        else:
            missed.append(_row_name(r))
    return hits / len(rows), missed[:5]


def _row_name(r: dict) -> str:
    return str(r.get("name") or r.get("title") or r.get("id") or "?")


def _tr(v: Any, n: int = 60) -> str:
    s = v if isinstance(v, str) else json.dumps(v, ensure_ascii=False)
    return s if len(s) <= n else s[: n - 1] + "…"


def _id_key(row: dict) -> str:
    return str(row.get("member_urn") or row.get("id") or row.get("profile_url") or "")


def _urnacct_to_int(pid: Any) -> int | None:
    if not isinstance(pid, str):
        return None
    try:
        pad = pid + "=" * (-len(pid) % 4)
        raw = base64.urlsafe_b64decode(pad)
    except (binascii.Error, ValueError):
        return None
    nums = re.findall(rb"\d{6,}", raw)
    return int(nums[0]) if nums else None


def _err_text(body: Any) -> Any:
    if isinstance(body, dict):
        return {k: v for k, v in body.items() if k != "items"} or None
    return body or None


# ---------------------------------------------------------------------------
# probe framework
# ---------------------------------------------------------------------------
@dataclasses.dataclass
class Finding:
    probe: str
    question: str
    verdict: str
    status: str  # ANSWERED | PARTIAL | UNTESTABLE | ERROR
    evidence: dict = dataclasses.field(default_factory=dict)


class Recorder:
    def __init__(self, out_dir: Path) -> None:
        self.findings: list[Finding] = []
        self.raw: dict[str, Any] = {}
        self.out = out_dir
        self.out.mkdir(parents=True, exist_ok=True)

    def add(self, probe: str, question: str, verdict: str, status: str, **ev: Any) -> None:
        self.findings.append(Finding(probe, question, verdict, status, ev))
        icon = {"ANSWERED": "[ok]  ", "PARTIAL": "[~]   ", "UNTESTABLE": "[--]  ",
                "ERROR": "[err] "}.get(status, "[?]   ")
        print(f"{icon} {probe} {question}\n       -> {verdict}")
        for line in json.dumps(ev, ensure_ascii=False, indent=2, default=str).splitlines()[:24]:
            print(f"         {line}")

    def _markdown(self) -> str:
        lines = ["# Unipile LinkedIn search - measured contract",
                 f"\nGenerated {dt.datetime.now().isoformat(timespec='seconds')}",
                 "\nStatus legend: ANSWERED (measured), PARTIAL (one-sided view), "
                 "UNTESTABLE (missing credential), ERROR (transport/account failure).\n",
                 "\n| Probe | Question | Status | Verdict |\n| :-- | :-- | :-- | :-- |"]
        for f in self.findings:
            lines.append(f"| {f.probe} | {f.question} | {f.status} | {f.verdict.replace('|', '/')} |")
        for f in self.findings:
            if f.evidence:
                lines += [f"\n## {f.probe} - {f.question}", "", "```json",
                          json.dumps(f.evidence, indent=2, ensure_ascii=False, default=str),
                          "```"]
        return "\n".join(lines) + "\n"

    def save(self) -> None:
        (self.out / "report.json").write_text(
            json.dumps([dataclasses.asdict(f) for f in self.findings], indent=2,
                       ensure_ascii=False), encoding="utf-8")
        (self.out / "report.md").write_text(self._markdown(), encoding="utf-8")
        print(f"\nwrote {self.out}/report.md (+ report.json, raw_exchanges.json)")


class Budget:
    """Daily per-account line ledger, persisted next to the report."""

    def __init__(self, out_dir: Path, account_id: str, max_lines: int) -> None:
        self.path = out_dir / "ledger.json"
        self.key = (f"{dt.date.today().isoformat()}:"
                    f"{hashlib.sha256(account_id.encode()).hexdigest()[:8]}")
        self.max = max_lines
        db: dict = {}
        if self.path.exists():
            try:
                db = json.loads(self.path.read_text(encoding="utf-8"))
            except ValueError:
                db = {}
        self.db = db
        self.entry = dict(db.get(self.key) or {"lines": 0, "calls": 0})

    @property
    def lines(self) -> int:
        return int(self.entry.get("lines", 0))

    def remaining(self) -> int:
        return max(0, self.max - self.lines)

    def charge(self, calls: int, lines: int) -> None:
        self.entry = {"lines": self.lines + lines,
                      "calls": int(self.entry.get("calls", 0)) + calls}
        self.db[self.key] = self.entry
        self.path.parent.mkdir(parents=True, exist_ok=True)
        self.path.write_text(json.dumps(self.db, indent=2), encoding="utf-8")

    def __str__(self) -> str:
        return f"{self.lines} used / {self.max} cap today ({self.remaining()} left)"


class Abort(Exception):
    """Latched account-level danger: stop the run."""


class Transport:
    """Real HTTP with pacing, jitter and a cooldown latch on 401/403/429/5xx."""

    def __init__(self, cfg: Config, budget: Budget, jitter: float) -> None:
        if requests is None:
            raise SystemExit("requests is required for a live run: pip install requests")
        self.cfg = cfg
        self.budget = budget
        self.jitter = jitter
        self.last = 0.0
        self.s = requests.Session()
        self.s.headers.update({"X-API-KEY": cfg.api_key, "accept": "application/json",
                               "content-type": "application/json"})
        self.cooldown = budget.path.parent / "cooldown"
        self._guard_cooldown()

    def _guard_cooldown(self) -> None:
        if not self.cooldown.exists():
            return
        try:
            until = float(self.cooldown.read_text())
        except ValueError:
            return
        if time.time() < until:
            raise Abort(f"cooldown latched for {int(until - time.time())}s "
                        f"(delete {self.cooldown} to override)")

    def _latch(self, why: str) -> None:
        self.cooldown.write_text(str(time.time() + 900))
        raise Abort(why + "; 15m cooldown latched")

    def call(self, method: str, path: str, params: dict | None = None,
             json_body: dict | None = None, label: str = "") -> Resp:
        gap = time.time() - self.last
        if gap < self.jitter:
            time.sleep(self.jitter - gap + random.uniform(0, self.jitter))
        self.last = time.time()
        q = {"account_id": self.cfg.account_id}
        q.update(params or {})
        t0 = time.monotonic()
        try:
            r = self.s.request(method, self.cfg.base_url + path, params=q,
                               json=json_body, timeout=60)
        except Exception as exc:  # network / TLS / timeout
            return Resp(0, None, time.monotonic() - t0,
                        error=f"{type(exc).__name__}: {exc}")
        el = time.monotonic() - t0
        try:
            body: Any = r.json()
        except ValueError:
            body = r.text[:500]
        resp = Resp(r.status_code, body, el)
        if r.status_code == 401:
            self._latch("HTTP 401 - API key invalid")
        if r.status_code in (429, 500):
            self._latch(f"HTTP {r.status_code} on {label or path}: the limits page says "
                        "quota yields 429 *or 500*, so this is not distinguishable from a "
                        "real server error")
        if r.status_code == 403:
            self._latch("HTTP 403 - possible LinkedIn checkpoint or dead session")
        return resp


KNOWN_PARAMS = {"api", "category", "keywords", "location", "industry", "company", "url",
                "role", "skills", "network_distance", "tenure", "profile_language",
                "has_job_offers", "company_size", "seniority", "function"}


class MockTransport:
    """Offline simulator shaped like the documented responses.

    It deliberately reproduces the documentation's own inconsistencies (dropped
    company filter, page_count == len(items), null company_id, license-gated URLs)
    so the detectors themselves are exercised without spending quota.
    """

    def __init__(self, budget: Budget) -> None:
        self.budget = budget
        self.n = 0

    def call(self, method: str, path: str, params: dict | None = None,
             json_body: dict | None = None, label: str = "") -> Resp:
        self.n += 1
        body = dict(json_body or {})
        if path == ACCOUNT_PATH:
            return Resp(200, {"id": "mock", "display_name": "Mock User",
                              "product": "sales_navigator"}, 0.0, "mock")
        if path == PARAMS_PATH:
            ptype = (params or {}).get("type")
            if ptype == "FUNCTION":
                return Resp(400, {"error": "unsupported parameter type"}, 0.0, "mock")
            return Resp(200, {"object": "LinkedinSearchParametersList",
                              "items": [{"object": "LinkedinSearchParameter",
                                         "title": "Software Development", "id": "6"}]},
                        0.0, "mock")

        api = body.get("api", "classic")
        cat = body.get("category", "people")
        rows_want = _page_len(api, cat, body.get("limit"))
        rows = _mock_rows("sales_navigator" if "url" in body else api, cat, rows_want)
        echoed = {k: v for k, v in body.items()
                  if k in KNOWN_PARAMS and k not in ("company", "function", "url")}
        start = int(decode_cursor(body.get("cursor")).get("start") or 0)
        real_api = "sales_navigator" if "url" in body else api
        return Resp(200, {"object": "LinkedinSearch", "items": rows,
                          "config": {"params": echoed},
                          "paging": {"start": start, "page_count": len(rows),
                                     "total_count": RETRIEVABLE_CAP.get(real_api, 1000) * 10},
                          "cursor": _mock_cursor(real_api, cat, start + len(rows), body)},
                   0.01, "mock")


def _page_len(api: str, cat: str, limit: Any) -> int:
    want = int(limit) if isinstance(limit, int) else 25
    cap = DOC_PAGE_LIMITS.get(f"{api}_{cat}", DOC_PAGE_LIMITS.get(api, 25))
    return min(want, cap, 25)


def _mock_rows(api: str, cat: str, n: int) -> list[dict]:
    out = []
    for i in range(n):
        if cat == "companies":
            out.append({"type": "COMPANY", "id": str(1000 + i), "name": f"Co {i}",
                        "industry": "Software Development", "headcount": str(i)})
        elif api == "classic":
            out.append({"type": "PEOPLE", "id": f"ACoAAA{i:028d}", "name": f"Person {i}",
                        "member_urn": f"urn:li:member:{100000 + i}",
                        "profile_url": f"https://www.linkedin.com/in/person-{i}?miniProfileUrn=x",
                        "headline": "Engineer at Globex", "location": "San Francisco, CA",
                        "network_distance": "DISTANCE_2"})
        else:
            out.append({"type": "PEOPLE", "id": f"ACwAAA{i:031d}", "name": f"Person {i}",
                        "first_name": f"First{i}", "last_name": "Doe",
                        "member_urn": f"urn:li:member:{100000 + i}",
                        "public_identifier": f"person-{i}-a1b2c3",
                        "public_profile_url": f"https://www.linkedin.com/in/person-{i}-a1b2c3",
                        "profile_url": f"https://www.linkedin.com/sales/lead/ACwAAA{i:031d},NAME_SEARCH",
                        "industry": "Software Development", "premium": True,
                        "open_profile": True, "headline": "Engineer at Globex",
                        "location": "United States", "network_distance": "DISTANCE_3",
                        "current_positions": [{"company": "Globex", "company_id": None,
                                               "role": "Engineer",
                                               "tenure_at_company": {"years": 3}}]})
    return out


def _mock_cursor(api: str, cat: str, start: int, body: dict) -> str:
    payload = {"account_id": "mock", "limit": int(body.get("limit") or 25), "start": start,
               "params": {k: v for k, v in body.items() if k != "cursor"}}
    return base64.urlsafe_b64encode(json.dumps(payload, sort_keys=True).encode()).decode()


# ---------------------------------------------------------------------------
# probes
# ---------------------------------------------------------------------------
class Ctx:
    def __init__(self, tr: Any, cfg: Config, budget: Budget) -> None:
        self.tr, self.cfg, self.budget = tr, cfg, budget
        self.rec = Recorder(Path(budget.path.parent))
        self.calls = 0
        self.lines = 0
        self.responses: dict[str, Resp] = {}

    def _save(self, key: str, entry: dict) -> None:
        self.rec.raw[key] = entry

    def search(self, body: dict, label: str) -> Resp:
        self.calls += 1
        r = self.tr.call("POST", SEARCH_PATH, json_body=body, label=label)
        self.lines += len(r.items)
        self.responses[label] = r
        self._save(label, {"request": {"method": "POST", "path": SEARCH_PATH,
                                       "query": {"account_id": "***"}},
                           "body_sent": body, "http_status": r.status,
                           "transport": r.transport, "transport_error": r.error,
                           "elapsed_s": round(r.elapsed, 2), "response": r.body})
        return r

    def get_params(self, ptype: str, keywords: str) -> Resp:
        self.calls += 1
        label = f"parameters_{ptype}"
        r = self.tr.call("GET", PARAMS_PATH, params={"type": ptype, "keywords": keywords,
                                                     "limit": 5}, label=label)
        self._save(label, {"request": {"method": "GET", "path": PARAMS_PATH,
                                       "query": {"type": ptype, "keywords": keywords,
                                                 "limit": 5, "account_id": "***"}},
                           "http_status": r.status, "transport": r.transport,
                           "transport_error": r.error, "response": r.body})
        return r

    def account(self) -> Resp:
        self.calls += 1
        r = self.tr.call("GET", ACCOUNT_PATH, label="account")
        self._save("account", {"request": {"method": "GET", "path": ACCOUNT_PATH},
                               "http_status": r.status, "transport": r.transport,
                               "transport_error": r.error, "response": r.body})
        return r


def base_body(ctx: Ctx, api: str, cat: str = "people", limit: int = 25, **extra) -> dict:
    body: dict[str, Any] = {"api": api, "category": cat, "limit": limit}
    if ctx.cfg.location_ids and cat == "people":
        body["location"] = ctx.cfg.location_ids[:2]
    body.update(extra)
    return body


def p0_preflight(ctx: Ctx) -> None:
    r = ctx.account()
    if r.status != 200 or not isinstance(r.body, dict):
        ctx.rec.add("P0", "is auth valid and which subscription is attached?",
                    f"ERROR HTTP {r.status}: {r.error or _tr(r.body, 200)}. Nothing else can "
                    "be measured until the account id, DSN and key agree.", "ERROR",
                    http_status=r.status, transport=r.transport, transport_error=r.error)
        raise Abort("preflight failed - no further requests sent")
    prod = r.body.get("product") or r.body.get("linkedin_product") or "absent from response"
    ctx.rec.add("P0", "is auth valid and which subscription is attached?",
                f"auth OK; subscription field reads {prod!r}. If this is not "
                "'sales_navigator' or 'recruiter', the enrichment fields your modes A and C "
                "need (current_positions, open_profile, public_profile_url) are unavailable "
                "and the 2,500/day ceilings do not apply. Note the /account field name is "
                "itself unconfirmed - dump this payload.",
                "ANSWERED", account_body=r.body)


def p1_company_supported(ctx: Ctx) -> None:
    if ctx.cfg.company_id is None:
        ctx.rec.add("P1", "does the API accept structured company:{include,exclude}?",
                    "UNTESTABLE - set UNIPILE_COMPANY_ID (LinkedIn company universal ID; "
                    "get it from /search/parameters?type=COMPANY or a company URL)",
                    "UNTESTABLE")
        ctx.rec.add("P1b", "is the company filter actually applied?",
                    "UNTESTABLE - depends on P1", "UNTESTABLE")
        return
    body = base_body(ctx, "sales_navigator", limit=25,
                     company={"include": [ctx.cfg.company_id]})
    r = ctx.search(body, "p1_company_include")
    dropped, drift, changed = echo_drift(body, r.echoed)
    ctx.rec.add("P1", "does the API accept structured company:{include,exclude}?",
                ("ACCEPTED and echoed back - proceed to P1b to check it is *applied*."
                 if "company" in r.echoed else
                 "NOT in config.params (HTTP %d). Either rejected or silently dropped: "
                 "use URL-paste mode for company scoping, or ask Unipile for the field name."
                 % r.status),
                "ANSWERED" if r.status in (200, 400) else "PARTIAL",
                http_status=r.status, sent=body, echo=r.echoed, dropped=dropped,
                type_drift=drift, changed=changed, rows=len(r.items),
                error_body=_err_text(r.body) if r.status >= 400 else None)
    _p1b(ctx, r)


def _p1b(ctx: Ctx, filtered: Resp) -> None:
    if filtered.status != 200 or not filtered.items or not ctx.cfg.company_name:
        ctx.rec.add("P1b", "is the company filter actually applied?",
                    "PARTIAL - need HTTP 200 with rows AND UNIPILE_COMPANY_NAME for "
                    "row-level corroboration", "PARTIAL",
                    http_status=filtered.status, rows=len(filtered.items))
        return
    control = ctx.search(base_body(ctx, "sales_navigator", limit=25), "p1b_control")
    a = {_id_key(x) for x in filtered.items}
    b = {_id_key(x) for x in control.items}
    jac = len(a & b) / max(1, len(a | b))
    rate, missed = employer_match(filtered.items, ctx.cfg.company_name)
    ctx.rec.add("P1b", "is the company filter actually applied?",
                f"differential jaccard={jac:.2f} over {len(a)} filtered vs {len(b)} unfiltered "
                "ids: "
                + ("IDENTICAL => filter silently ignored. Never export on this filter."
                   if jac > 0.95 else "results differ => filter plausibly applied")
                + f"; row-level: {rate:.0%} of filtered rows mention "
                  f"{ctx.cfg.company_name!r} somewhere in the payload.",
                "ANSWERED", jaccard=round(jac, 3), employer_match_rate=round(rate, 3),
                non_matching_rows=missed, control_echo=r.echoed,
                filtered_sample=[_row_name(x) for x in filtered.items[:5]])


def p2_unknown_param(ctx: Ctx) -> None:
    body = base_body(ctx, "sales_navigator", limit=5, function={"include": ["13"]})
    body[SENTINEL] = SENTINEL_VALUE
    r = ctx.search(body, "p2_unknown_param")
    if r.status >= 400:
        v = (f"REJECTED with HTTP {r.status}: {_tr(r.body, 200)}. An error contract exists; "
             "capture the exact shape and code it.")
        s = "ANSWERED"
    else:
        v = ("HTTP 200 with rows returned - unknown fields are NOT validated. "
             f"function echoed={'function' in r.echoed}, sentinel echoed="
             f"{SENTINEL in r.echoed}. A typo'd or unsupported filter therefore degrades into "
             "an unfiltered, plausible-looking export; asserting config.params is mandatory, "
             "not optional.")
        s = "ANSWERED"
    ctx.rec.add("P2", "does an unsupported param error or get silently ignored?", v, s,
                http_status=r.status, echo=r.echoed, sentinel_echoed=SENTINEL in r.echoed,
                rows=len(r.items), error_body=_err_text(r.body) if r.status >= 400 else None)


def p3_cursor(ctx: Ctx) -> None:
    first = ctx.search(base_body(ctx, "sales_navigator", limit=25), "p3_page1")
    cur = first.body.get("cursor") if isinstance(first.body, dict) else None
    meta = decode_cursor(cur)
    if not meta["present"]:
        ctx.rec.add("P3", "cursor step, termination and truncation signal",
                    f"PARTIAL - no cursor on page 1 despite {len(first.items)} rows and "
                    f"total_count={first.paging.get('total_count')}. If the first page can "
                    "lack a cursor, 'cursor missing' is NOT a safe end-of-results test.",
                    "PARTIAL", paging=first.paging, echo=first.echoed)
        return
    body2 = base_body(ctx, "sales_navigator", limit=25)
    body2["cursor"] = cur
    second = ctx.search(body2, "p3_page2")
    a = {_id_key(x) for x in first.items}
    b = {_id_key(x) for x in second.items}
    overlap = len(a & b)
    adv = decode_cursor(second.body.get("cursor") if isinstance(second.body, dict) else None)
    ctx.rec.add("P3", "cursor step, termination and truncation signal",
                f"cursor decodes to keys {meta.get('keys') or '(opaque)'}; "
                f"account-bound={meta.get('account_bound')}; params embedded="
                f"{meta.get('params_echo')} (so it is forgeable, but start/offset is not a "
                f"documented param - send cursor only). Page1 start={first.paging.get('start')} "
                f"rows={len(first.items)}; page2 start={second.paging.get('start')} "
                f"rows={len(second.items)}; id overlap={overlap}. "
                + ("Cursor is honored (disjoint pages)." if overlap == 0 and second.items else
                   "OVERLAP or empty page 2 - the cursor may be ignored or single-use: "
                   "do not build the export loop on it until this reads clean."),
                "ANSWERED" if overlap == 0 and second.items else "PARTIAL",
                cursor_meta=meta, first_paging=first.paging, second_paging=second.paging,
                id_overlap=overlap, second_cursor_meta=adv,
                page_count_equals_items_page1=first.paging.get("page_count") == len(first.items),
                page_count_equals_items_page2=second.paging.get("page_count") == len(second.items),
                total_count=first.paging.get("total_count"),
                retrievable_cap=RETRIEVABLE_CAP.get("sales_navigator"))


def p4_limit(ctx: Ctx) -> None:
    r = ctx.search(base_body(ctx, "classic", "people", limit=100), "p4_classic_limit")
    got = len(r.items)
    adv = decode_cursor(r.body.get("cursor") if isinstance(r.body, dict) else None)
    doc = DOC_PAGE_LIMITS["classic_people"]
    ctx.rec.add("P4", "is limit clamped per api x category as documented?",
                f"api=classic category=people limit=100 -> HTTP {r.status}, rows={got}, "
                f"cursor.start advanced to {adv.get('start')} (doc says {doc}/page). "
                + ("Clamped to the documented cap; a classic export therefore needs ~10x the "
                   "calls of a Sales Nav export."
                   if r.status == 200 and got <= doc else
                   "Returned more than the documented cap, or failed - recheck per-category."),
                "ANSWERED" if r.status == 200 else "PARTIAL",
                http_status=r.status, requested_limit=100, rows=got,
                cursor_start=adv.get("start"), page_count_field=r.paging.get("page_count"),
                total_count=r.paging.get("total_count"),
                error_body=_err_text(r.body) if r.status >= 400 else None)


def p5_position_coverage(ctx: Ctx) -> None:
    r = ctx.search(base_body(ctx, "sales_navigator", limit=25), "p5_positions")
    rows = r.items
    n = len(rows) or 1
    withpos = sum(1 for x in rows if isinstance(x.get("current_positions"), list)
                  and x.get("current_positions"))
    ids_pop = sum(1 for x in rows for p in (x.get("current_positions") or [])
                  if isinstance(p, dict) and p.get("company_id"))
    ids_null = sum(1 for x in rows for p in (x.get("current_positions") or [])
                   if isinstance(p, dict) and "company_id" in p and p.get("company_id") is None)
    ctx.rec.add("P5", "does Sales Nav search carry current_positions for org-chart rows?",
                f"{withpos}/{len(rows)} rows ({withpos / n:.0%}) have non-empty "
                f"current_positions; company_id populated {ids_pop}x, explicit null {ids_null}x. "
                + ("Employer arrives as free text only => ID-exact verification from search is "
                   "impossible; your name->employer confirmation is a normalized string match, "
                   "or costs a profile fetch."
                   if ids_pop == 0 else
                   "company_id is sometimes populated: use it when present, fall back to text."),
                "ANSWERED" if rows else "PARTIAL",
                row_count=len(rows), rows_with_positions=withpos,
                company_id_populated=ids_pop, company_id_null=ids_null,
                observed_fields=sorted({k for x in rows for k in x}),
                position_fields=sorted({k for x in rows
                                        for p in (x.get("current_positions") or [])
                                        if isinstance(p, dict)})[:15])


def p6_id_consistency(ctx: Ctx) -> None:
    r = ctx.search(base_body(ctx, "sales_navigator", limit=25), "p6_id_join")
    pairs, mismatch, decoded = [], 0, 0
    for x in r.items:
        urn, pid = x.get("member_urn"), x.get("id")
        if not (isinstance(urn, str) and isinstance(pid, str)):
            continue
        num = urn.rsplit(":", 1)[-1]
        d = _urnacct_to_int(pid)
        pairs.append({"member_urn": urn, "id_prefix": pid[:16], "urn_numeric": num,
                      "decoded_from_id": d})
        if d is not None:
            decoded += 1
            if num.isdigit() and d != int(num):
                mismatch += 1
    clean = decoded == len(pairs) and mismatch == 0 and bool(pairs)
    ctx.rec.add("P6", "does `id` (mini-URNAcct) decode to the member_urn numeric id?",
                f"{len(pairs)} rows examined, {decoded} ids decoded to an integer, "
                f"{mismatch} mismatches. "
                + ("A local join key exists: you can batch profile lookups from search rows "
                   "without storing both fields."
                   if clean else
                   "Do NOT derive member ids locally. Persist member_urn and id side by side "
                   "and pass `id` through verbatim to profile retrieval."),
                "ANSWERED" if pairs else "PARTIAL",
                rows=len(pairs), decoded=decoded, mismatches=mismatch, sample=pairs[:4])


def p7_url_license(ctx: Ctx) -> None:
    sn = ctx.search(base_body(ctx, "sales_navigator", limit=25), "p7_url_salesnav")
    cl = ctx.search(base_body(ctx, "classic", "people", limit=10), "p7_url_classic")
    a, b = classify_urls(sn.items), classify_urls(cl.items)
    ctx.rec.add("P7", "are exported profile_url values openable without the license?",
                f"sales_navigator: {json.dumps(a)}; classic: {json.dumps(b)}. "
                + ("Sales Nav rows do expose a public profile_url, so a deliverable can ship "
                   "openable links."
                   if a.get("public_profile_url:public") else
                   "Sales Nav/Recruiter profile_url are license-gated (/sales/lead/...); a CSV "
                   "handed to a customer without that seat contains dead links. Derive "
                   "https://www.linkedin.com/in/{public_identifier}, which only Sales Nav "
                   "returns - so mode A and B should run on sales_navigator even when classic "
                   "would suffice for discovery."),
                "ANSWERED" if sn.items and cl.items else "PARTIAL",
                salesnav=a, classic=b,
                classic_has_public_identifier_field=any(
                    "public_identifier" in x for x in cl.items))


def p8_industry_parity(ctx: Ctx) -> None:
    body = base_body(ctx, "classic", "companies", limit=10, keywords="software",
                     industry={"include": [6]})
    r = ctx.search(body, "p8_industry_classic")
    dropped, drift, changed = echo_drift(body, r.echoed)
    if r.status >= 400:
        v = f"HTTP {r.status}: nested industry not accepted on api=classic - {_tr(r.body, 160)}"
        s = "ANSWERED"
    elif "industry" in dropped:
        v = ("HTTP 200 but industry DROPPED from config.params: the nested include/exclude form "
             "is Sales Nav/Recruiter-only. A classic + industry shard grid silently repeats the "
             "same unfiltered query and double-counts rows.")
        s = "ANSWERED"
    else:
        v = ("HTTP 200 and industry echoed. Parity is held for this field only; verify each "
             "param you shard on, because classic totals above the Sales Nav company ceiling "
             f"({RETRIEVABLE_CAP['sales_navigator']}) prove the two APIs disagree about the "
             f"same filter (classic total_count here = {r.paging.get('total_count')}).")
        s = "PARTIAL"
    ctx.rec.add("P8", "is industry:{include:[id]} treated identically on api=classic?", v, s,
                http_status=r.status, echo=r.echoed, dropped=dropped, type_drift=drift,
                changed=changed, total_count=r.paging.get("total_count"),
                error_body=_err_text(r.body) if r.status >= 400 else None)


def p9_function_taxonomy(ctx: Ctx) -> None:
    got: dict[str, Any] = {}
    for ptype in ("FUNCTION", "SENIORITY", "COMPANY"):
        r = ctx.get_params(ptype, "engineering" if ptype != "COMPANY" else "globex")
        items = r.body.get("items") or [] if isinstance(r.body, dict) else []
        got[ptype] = {"http_status": r.status, "n_items": len(items),
                      "sample": items[:2],
                      "error_body": _err_text(r.body) if r.status >= 400 else None}
    fn, co = got["FUNCTION"], got["COMPANY"]
    if fn["http_status"] == 200 and fn["n_items"]:
        v = ("FUNCTION resolves (SENIORITY "
             + ("resolves" if got["SENIORITY"]["n_items"] else "FAILS")
             + "). Department IDs are enumerable, so pair them with URL-paste mode or push "
               "Unipile for a structured `function` param.")
        s = "ANSWERED"
    else:
        v = ("FUNCTION does NOT resolve via /search/parameters (HTTP "
             f"{fn['http_status']}). With no structured `function` param in the docs either, "
             "department search has NO programmatic path except pasting a UI-built URL - "
             "that is your mode C blocker, and the strongest reason to ask support for the "
             "full valid type= enum.")
        s = "ANSWERED"
    ctx.rec.add("P9", "does /search/parameters resolve department (FUNCTION) IDs?", v, s,
                results=got, note="COMPANY is the control: it proves the route itself is alive")
    if not co["n_items"]:
        ctx.rec.add("P9c", "is the COMPANY type lookup alive (control for P9)?",
                    "COMPANY lookup returned nothing - P9's FUNCTION verdict is unreliable "
                    "until the route itself works", "PARTIAL", results=got)


def p10_url_parity(ctx: Ctx) -> None:
    if not ctx.cfg.salesnav_url:
        ctx.rec.add("P10", "does URL-paste mode accept a cleaned Sales Nav people URL?",
                    "UNTESTABLE - in the LinkedIn UI build one search with Company + Function "
                    "+ Seniority, copy the result URL, set UNIPILE_SALESNAV_URL, re-run. This is "
                    "the mode-C workaround and must be validated before you rely on it.",
                    "UNTESTABLE")
        return
    cleaned = re.sub(r"[?&]?\(?(recentSearchParam|doLogHistory)=[^)]*\)?&?", "",
                     ctx.cfg.salesnav_url)
    cleaned = re.sub(r"sessionId=[^&]*&?", "", cleaned)
    r = ctx.search({"url": cleaned, "limit": 25}, "p10_url_mode")
    ok = r.status == 200 and bool(r.items)
    ctx.rec.add("P10", "does URL-paste mode accept a cleaned Sales Nav people URL?",
                f"HTTP {r.status}, {len(r.items)} rows, "
                f"{len({_id_key(x) for x in r.items})} distinct ids, total_count="
                f"{r.paging.get('total_count')}. "
                + ("recentSearchParam and sessionId were stripped and it still worked: store "
                   "the cleaned URL as the query definition, so nothing is written into the "
                   "customer's LinkedIn search history and no session artifact ages out."
                   if ok else
                   f"Rejected: {_tr(r.body, 220)}. Send the URL verbatim next to isolate which "
                   "element the parser needs; if only the verbatim form works, the saved query "
                   "is session-bound and cannot be reused as a durable saved search."),
                "ANSWERED" if ok else "PARTIAL",
                http_status=r.status, stripped_url=cleaned[:400], echo=r.echoed,
                error_body=_err_text(r.body) if not ok else None)


PROBES: list[tuple[str, str, int, int, Callable[[Ctx], None]]] = [
    ("P0", "auth + subscription", 1, 0, p0_preflight),
    ("P1", "company:{include} accepted + applied", 2, 50, p1_company_supported),
    ("P2", "unknown param: error or silent", 1, 5, p2_unknown_param),
    ("P3", "cursor step + termination", 2, 50, p3_cursor),
    ("P4", "limit clamp (classic people)", 1, 100, p4_limit),
    ("P5", "current_positions coverage", 1, 25, p5_position_coverage),
    ("P6", "mini-URNAcct -> member_urn join", 1, 25, p6_id_consistency),
    ("P7", "profile_url license gating", 2, 35, p7_url_license),
    ("P8", "industry parity on classic", 1, 10, p8_industry_parity),
    ("P9", "FUNCTION taxonomy resolvable", 3, 0, p9_function_taxonomy),
    ("P10", "URL-mode department parity", 1, 25, p10_url_parity),
]
CHEAP = {"P0", "P1", "P2", "P3", "P9"}


def pick(suite: str, preflight_only: bool) -> list[tuple]:
    if preflight_only:
        return [p for p in PROBES if p[0] == "P0"]
    if suite == "cheap":
        return [p for p in PROBES if p[0] in CHEAP]
    return list(PROBES)


def main() -> int:
    ap = argparse.ArgumentParser(description="Unipile LinkedIn search contract probe")
    ap.add_argument("--suite", choices=["all", "cheap"], default="cheap",
                    help="cheap = P0-P3 + P9 (auth, filter trust, cursor, taxonomy)")
    ap.add_argument("--preflight-only", action="store_true", help="P0 only")
    ap.add_argument("--offline", action="store_true", help="mock transport: no creds, no net")
    ap.add_argument("--confirm", action="store_true", help="actually spend quota")
    ap.add_argument("--plan", action="store_true", help="print the cost estimate and exit")
    ap.add_argument("--jitter", type=float, default=8.0,
                    help="minimum seconds between calls; random extra is added")
    ap.add_argument("--max-lines", type=int, default=2400,
                    help="daily fetched-line ceiling for this account")
    ap.add_argument("--out", default="unipile_probe_out")
    args = ap.parse_args()

    suite = pick(args.suite, args.preflight_only)
    calls = sum(c for _, _, c, _, _ in suite)
    lines = sum(l for _, _, _, l, _ in suite)

    if args.plan:
        print(json.dumps({
            "probes": [p[0] for p in suite], "calls": calls, "estimated_lines": lines,
            "recommended_daily_lines": 2400,
            "fraction_of_daily_budget": round(lines / 2400, 4),
            "estimated_wall_clock_s": round(calls * args.jitter * 1.5),
            "note": "estimate_lines assumes full page sizes; rows actually returned are "
                    "recorded in ledger.json after a run",
        }, indent=2))
        return 0

    out = Path(args.out)
    out.mkdir(parents=True, exist_ok=True)
    cfg = Config.from_env()
    if cfg is None and not args.offline:
        print("Missing credentials: set UNIPILE_API_KEY, UNIPILE_DSN, UNIPILE_ACCOUNT_ID.\n"
              "Nothing was sent. Use --offline to exercise the logic with no quota and no net.",
              file=sys.stderr)
        return 2
    if cfg is None:
        cfg = Config(base_url="mock://offline", api_key="mock", account_id="mock",
                     company_id=1441, company_name="Globex", location_ids=[102277331])
        print("[offline] mock transport - no network, no quota spent\n")

    budget = Budget(out, cfg.account_id, args.max_lines)
    print(f"plan: {len(suite)} probes, {calls} calls, ~{lines} lines | ledger today: {budget}")
    if lines > budget.remaining() and not args.offline:
        print(f"[abort] estimate exceeds today's remaining budget; raise --max-lines or "
              f"resume tomorrow", file=sys.stderr)
        return 3
    if not args.confirm and not args.offline:
        print("\n--confirm not set: nothing sent. Re-run with --confirm to spend ~"
              f"{lines} lines and ~{round(calls * args.jitter * 1.5)}s of wall clock.")
        return 0

    tr = MockTransport(budget) if args.offline else Transport(cfg, budget, args.jitter)
    ctx = Ctx(tr, cfg, budget)
    try:
        for pid, _, _, _, fn in suite:
            fn(ctx)
            if pid != suite[-1][0] and not args.offline:
                time.sleep(random.uniform(args.jitter * 0.5, args.jitter * 1.5))
    except Abort as exc:
        print(f"\n[ABORT] {exc}", file=sys.stderr)
        ctx.rec.add("ABORT", "run halted early", str(exc), "ERROR")
    finally:
        budget.charge(ctx.calls, ctx.lines)
        (out / "raw_exchanges.json").write_text(
            json.dumps(ctx.rec.raw, indent=2, ensure_ascii=False, default=str),
            encoding="utf-8")
        ctx.rec.save()

    counts: dict[str, int] = {}
    for f in ctx.rec.findings:
        counts[f.status] = counts.get(f.status, 0) + 1
    print(f"\nfindings: {counts} | calls: {ctx.calls} | lines fetched: {ctx.lines} | "
          f"ledger: {budget}")
    open_items = [f for f in ctx.rec.findings if f.status in ("UNTESTABLE", "PARTIAL", "ERROR")]
    if open_items:
        print("\nstill open - ask Unipile these, attaching unipile_probe_out/report.md:")
        for f in open_items:
            print(f"  - {f.probe}: {f.question} [{f.status}]")
    return 1 if any(f.status == "ERROR" for f in ctx.rec.findings) else 0


if __name__ == "__main__":
    sys.exit(main())
