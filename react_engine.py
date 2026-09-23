#!/usr/bin/env python3
"""WebSearch_PY - Python replica of the Go ReAct Engine (see docs/ARCHITECTURE.md).

Module mapping to the Go codebase (WebSearch_GO):
    internal/agent/engine.go    -> ReActEngine       (thought -> action -> observation loop)
    internal/llm/gemini.go      -> GeminiLLM         (Gemini 2.5 Flash Lite + function calling)
    internal/tools/registry.go  -> ToolRegistry      (declarations + dispatch)
    internal/tools/serper.go    -> SerperSearchTool  (web search via Serper API)
    internal/tools/scraper.go   -> ScraperTool       (fetch + sanitize page text)
    cmd/server/main.go          -> main()            (CLI entry)

Run:
    pip install google-genai requests
    export GEMINI_API_KEY=...      # AI Studio key (or GOOGLE_API_KEY)
    export SERPER_API_KEY=...      # optional; falls back to mock search results

    python react_engine.py --task "Find the leadership team of Acme Robotics"
    python react_engine.py --demo   # fully offline scripted run, no keys needed
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
from html import unescape
from html.parser import HTMLParser

import requests

MODEL = "gemini-2.5-flash-lite"   # matches internal/llm/gemini.go
DEFAULT_MAX_ITERATIONS = 10       # runaway-loop guard, matches engine.go
SCRAPER_TIMEOUT = 15              # seconds
DEFAULT_MAX_CHARS = 4000          # truncated text fed back to the LLM


# ----------------------------------------------------------------------------
# internal/tools/scraper.go equivalent: HTML fetch + text sanitization
# ----------------------------------------------------------------------------
class _TextExtractor(HTMLParser):
    """Strips scripts/styles/nav boilerplate, keeps visible text (stdlib only)."""

    SKIP_TAGS = {"script", "style", "noscript", "nav", "header", "footer",
                 "aside", "form", "svg", "iframe"}

    def __init__(self) -> None:
        super().__init__()
        self.parts: list[str] = []
        self._skip_depth = 0

    def handle_starttag(self, tag, attrs):
        if tag in self.SKIP_TAGS:
            self._skip_depth += 1

    def handle_endtag(self, tag):
        if tag in self.SKIP_TAGS and self._skip_depth:
            self._skip_depth -= 1

    def handle_data(self, data):
        if not self._skip_depth:
            text = data.strip()
            if text:
                self.parts.append(text)


def _html_to_text(raw_html: str) -> str:
    extractor = _TextExtractor()
    extractor.feed(raw_html)
    return re.sub(r"\s+", " ", " ".join(extractor.parts)).strip()


# ----------------------------------------------------------------------------
# Tool base + implementations (internal/tools/*.go equivalents)
# ----------------------------------------------------------------------------
class Tool:
    name = ""
    description = ""
    parameters = {}  # JSON-schema dict; converted to types.Schema by the LLM wrapper

    def run(self, args: dict) -> dict:
        raise NotImplementedError


class SerperSearchTool(Tool):
    """internal/tools/serper.go equivalent."""

    name = "web_search"
    description = "Search the public web via the Serper API. Returns sanitized snippets and links."
    parameters = {
        "type": "object",
        "properties": {
            "query": {"type": "string", "description": "The search query."},
            "num_results": {"type": "integer",
                            "description": "How many results to return (default 5)."},
        },
        "required": ["query"],
    }

    def __init__(self, api_key: str) -> None:
        self.api_key = api_key

    def run(self, args: dict) -> dict:
        resp = requests.post(
            "https://google.serper.dev/search",
            headers={"X-API-KEY": self.api_key, "Content-Type": "application/json"},
            json={"q": args["query"], "num": int(args.get("num_results", 5))},
            timeout=SCRAPER_TIMEOUT,
        )
        resp.raise_for_status()
        organic = resp.json().get("organic", [])
        return {"results": [
            {"title": r.get("title"), "link": r.get("link"), "snippet": r.get("snippet")}
            for r in organic
        ]}


class ScraperTool(Tool):
    """internal/tools/scraper.go equivalent."""

    name = "scrape_page"
    description = "Fetch a webpage, strip HTML boilerplate, and return concise text for LLM ingestion."
    parameters = {
        "type": "object",
        "properties": {
            "url": {"type": "string", "description": "Absolute URL to fetch."},
            "max_chars": {"type": "integer",
                          "description": "Truncate extracted text to this length (default 4000)."},
        },
        "required": ["url"],
    }

    def run(self, args: dict) -> dict:
        url = args["url"]
        resp = requests.get(
            url,
            headers={"User-Agent": "Mozilla/5.0 (WebSearch_PY research agent)"},
            timeout=SCRAPER_TIMEOUT,
        )
        resp.raise_for_status()
        text = _html_to_text(resp.text)[: int(args.get("max_chars", DEFAULT_MAX_CHARS))]
        return {"url": url, "text": text}


# ---- Offline mock tools for --demo mode -------------------------------------
class MockSearchTool(Tool):
    name = "web_search"
    description = SerperSearchTool.description
    parameters = SerperSearchTool.parameters

    def run(self, args: dict) -> dict:
        return {"results": [
            {"title": "Acme Robotics - Leadership Team",
             "link": "https://acmerobotics.example.com/about/leadership",
             "snippet": "Meet the executives guiding Acme Robotics: CEO Dana Reyes, CFO ..."},
            {"title": "Acme Robotics News", "link": "https://news.example.com/acme",
             "snippet": "Acme Robotics expands into the EU market with a new Berlin office."},
        ]}


class MockScraperTool(Tool):
    name = "scrape_page"
    description = ScraperTool.description
    parameters = ScraperTool.parameters

    def run(self, args: dict) -> dict:
        return {"url": args["url"],
                "text": ("Leadership Team - Dana Reyes, Chief Executive Officer. "
                         "Samir Patel, Chief Financial Officer. Lena Ortiz, "
                         "VP of Engineering. Marco Chen, VP of Sales. "
                         "Regional leads: EMEA - Berlin office; APAC - Singapore office.")}


# ----------------------------------------------------------------------------
# internal/tools/registry.go equivalent
# ----------------------------------------------------------------------------
class ToolRegistry:
    def __init__(self) -> None:
        self._tools: dict[str, Tool] = {}

    def register(self, tool: Tool) -> None:
        self._tools[tool.name] = tool

    def declarations(self) -> list[dict]:
        return [{"name": t.name, "description": t.description,
                 "parameters": t.parameters} for t in self._tools.values()]

    def execute(self, name: str, args: dict) -> dict:
        """Dispatch a tool call. Never raises - errors are fed back as observations."""
        tool = self._tools.get(name)
        if tool is None:
            return {"ok": False, "error": f"unknown tool: {name}"}
        try:
            return {"ok": True, "data": tool.run(args)}
        except Exception as exc:  # model can retry with a different query
            return {"ok": False, "error": f"{type(exc).__name__}: {exc}"}


# ----------------------------------------------------------------------------
# internal/llm/gemini.go equivalent
# ----------------------------------------------------------------------------
class GeminiLLM:
    """Thin wrapper over google-genai; converts internal dict messages to SDK types."""

    def __init__(self, api_key: str, registry: ToolRegistry) -> None:
        from google import genai
        from google.genai import types

        self._types = types
        self._client = genai.Client(api_key=api_key)
        decls = [types.FunctionDeclaration(**d) for d in registry.declarations()]
        self._config = types.GenerateContentConfig(
            system_instruction=(
                "You are an autonomous research agent following the ReAct pattern: "
                "reason (Thought), invoke a tool (Action), read the result (Observation). "
                "Iterate until you can answer with cited evidence, then produce the final "
                "markdown report and stop calling tools."
            ),
            tools=[types.Tool(function_declarations=decls)],
            temperature=0.2,
        )

    def generate(self, messages: list[dict]) -> dict:
        types = self._types
        contents = []
        for m in messages:
            parts = []
            for p in m["parts"]:
                if "text" in p:
                    parts.append(types.Part(text=p["text"]))
                elif "function_call" in p:
                    fc = p["function_call"]
                    parts.append(types.Part(function_call=types.FunctionCall(
                        name=fc["name"], args=fc.get("args", {}))))
                elif "function_response" in p:
                    fr = p["function_response"]
                    parts.append(types.Part(function_response=types.FunctionResponse(
                        name=fr["name"], response=fr["response"])))
            contents.append(types.Content(role=m["role"], parts=parts))

        resp = self._client.models.generate_content(
            model=MODEL, contents=contents, config=self._config)

        cand = resp.candidates[0]
        out_parts = []
        for p in (cand.content.parts or []):
            if p.text:
                out_parts.append({"text": p.text})
            elif p.function_call:
                out_parts.append({"function_call": {
                    "name": p.function_call.name,
                    "args": dict(p.function_call.args or {})}})
        return {"role": "model", "parts": out_parts}


class MockLLM:
    """Scripted LLM for --demo: emits 2 tool calls, then a final report."""

    SCRIPT = [
        ({"text": "Thought: I need to find Acme Robotics' leadership page first."},
         {"function_call": {"name": "web_search",
                            "args": {"query": "Acme Robotics leadership team"}}}),
        ({"text": "Thought: The about/leadership page looks authoritative; scrape it."},
         {"function_call": {"name": "scrape_page",
                            "args": {"url": "https://acmerobotics.example.com/about/leadership"}}}),
    ]

    def __init__(self) -> None:
        self._turn = 0

    def generate(self, messages: list[dict]) -> dict:
        if self._turn < len(self.SCRIPT):
            thought, call = self.SCRIPT[self._turn]
            self._turn += 1
            return {"role": "model", "parts": [thought, call]}
        last = messages[-1]["parts"][-1]
        obs = last["function_response"]["response"]["result"]
        assert isinstance(obs, dict) and "text" in obs
        names = re.findall(r"([A-Z][a-z]+ [A-Z][a-z]+),", obs["text"])
        report = "## Acme Robotics - Executive Org Chart\n" + "\n".join(
            f"- **{n}**" for n in dict.fromkeys(names))
        return {"role": "model",
                "parts": [{"text": f"Thought: I have enough evidence.\n\n{report}"}]}


# ----------------------------------------------------------------------------
# internal/agent/engine.go equivalent: the ReAct loop
# ----------------------------------------------------------------------------
class MaxIterationsExceeded(Exception):
    pass


class ReActEngine:
    def __init__(self, llm, registry: ToolRegistry, max_iterations: int = DEFAULT_MAX_ITERATIONS,
                 trace: bool = True) -> None:
        self.llm = llm
        self.registry = registry
        self.max_iterations = max_iterations
        self.trace = trace

    def _log(self, msg: str) -> None:
        if self.trace:
            print(msg, file=sys.stderr)

    def run(self, task: str) -> str:
        messages: list[dict] = [{"role": "user", "parts": [{"text": task}]}]

        for step in range(1, self.max_iterations + 1):
            self._log(f"\n== ReAct step {step}/{self.max_iterations} " + "=" * 30)
            response = self.llm.generate(messages)
            messages.append(response)

            thought = "\n".join(p["text"] for p in response["parts"] if "text" in p)
            calls = [p["function_call"] for p in response["parts"] if "function_call" in p]

            if thought:
                self._log(f"Thought: {thought}")

            if not calls:
                return thought  # no tool call -> final answer

            for call in calls:
                self._log(f"Action: {call['name']}({json.dumps(call.get('args', {}))})")
                observation = self.registry.execute(call["name"], call.get("args", {}))
                self._log(f"Observation: {json.dumps(observation)[:300]} ...")
                messages.append({"role": "user", "parts": [{
                    "function_response": {"name": call["name"], "response": observation}
                }]})

        raise MaxIterationsExceeded(
            f"Agent exceeded {self.max_iterations} iterations without a final answer.")


# ----------------------------------------------------------------------------
# cmd/server/main.go equivalent
# ----------------------------------------------------------------------------
def main() -> None:
    parser = argparse.ArgumentParser(description="WebSearch_PY ReAct demo")
    parser.add_argument("--task", help="Research goal (interactive prompt if omitted)")
    parser.add_argument("--demo", action="store_true",
                        help="Offline scripted run with mock tools and mock LLM")
    parser.add_argument("--max-iters", type=int, default=DEFAULT_MAX_ITERATIONS)
    args = parser.parse_args()

    registry = ToolRegistry()
    llm = None

    if args.demo:
        registry.register(MockSearchTool())
        registry.register(MockScraperTool())
        llm = MockLLM()
    else:
        api_key = os.environ.get("GEMINI_API_KEY") or os.environ.get("GOOGLE_API_KEY")
        serper_key = os.environ.get("SERPER_API_KEY")
        if not api_key:
            sys.exit("Set GEMINI_API_KEY (or use --demo).")
        registry.register(SerperSearchTool(serper_key) if serper_key else MockSearchTool())
        registry.register(ScraperTool())
        llm = GeminiLLM(api_key, registry)

    task = args.task or input("Research goal: ").strip()
    engine = ReActEngine(llm, registry, max_iterations=args.max_iters)

    print(f"\nGoal: {task}\n", file=sys.stderr)
    try:
        report = engine.run(task)
    except MaxIterationsExceeded as exc:
        sys.exit(str(exc))

    print("\n===== FINAL REPORT =====")
    print(report)


if __name__ == "__main__":
    main()