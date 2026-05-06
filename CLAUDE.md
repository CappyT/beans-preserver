# beans-preserver — token-saving MCP

Local-LLM MCP server that offloads token-heavy preprocessing (filtering, extraction,
summarization, web fetch) to a configurable LLM provider — Ollama natively or any
OpenAI-compatible endpoint (real OpenAI, OpenRouter, vLLM, llama.cpp server, etc).
Provider URLs are picked up from `OLLAMA_BASE_URL`, `OPENAI_BASE_URL`,
`OPENAI_API_KEY` env vars; defaults target Ollama at `http://localhost:11434`.

## When to delegate to local tools

The local tools (prefix `mcp__beans-preserver__local_*`) trade Claude tokens for a few
seconds of latency. Use them whenever an operation would otherwise dump >2k tokens of
mostly-irrelevant content into context.

| Situation | Tool to call |
| --- | --- |
| `Bash` output is verbose (npm install, build, kubectl logs, test runs) and only the outcome matters | `local_filter` with criterion describing what you want to keep |
| Need a slice of a large file without reading the full content | (coming) `local_extract` |
| Need a one-paragraph summary of a long file or doc | (coming) `local_summarize` |
| Fetching an HTML page just to find one section | (coming) `local_fetch` |
| About to read >5 files in a repo you don't know yet | (coming) `local_repo_index` |

## Output contract

Every tool returns:

```json
{
  "result": "...",
  "confidence": 0.0-1.0,
  "notes": "one-line note from the model",
  "provider": "ollama",
  "model": "gemma4:e2b",
  "cache_hit": true|false,
  "input_tokens": 0,
  "output_tokens": 0,
  "wall_ms": 0,
  "raw_available": true,
  "raw_token_estimate": 0,
  "server_fetched": true|false
}
```

`provider` is the name of the configured endpoint that served the call (set in
`configs/default.yaml` under `providers:`); cache keys include it so the same
model name on different endpoints never collides.

**Trust policy:**

- `confidence >= 0.7` → trust the result, proceed.
- `confidence < 0.7` → consider asking the user, or re-run with a more specific
  criterion, or fall back to reading the raw content directly (cost is shown in
  `raw_token_estimate`).
- `cache_hit: true` is free — no quality concern.

## Don't delegate

- Architectural decisions, code review, cross-file diagnosis — these need Claude.
- Anything user-facing (writing a reply, drafting a commit message).
- Tasks that fit in <2k tokens to begin with — the local model adds ~12s for nothing.

## Important: when do these tools actually save tokens?

**Always prefer `path:` over inline content** for `local_filter`, `local_extract`,
`local_summarize`, `local_transform`. When you pass `path`, the server reads the
file directly — you only pay for the (small) result. When you pass inline content
(`content` / `source` / `input`), you pay for the content twice: once in the call
arg, once in the response. Same goes for `local_fetch` (URL-based by design) and
`local_repo_index` (no input at all).

Inline-content variants are still there for the cases where data isn't a file
(piped Bash output you've already captured, generated text, a snippet from
another tool's response). Avoid them otherwise.

Call `local_stats` to see the running totals: `cumulative.net_claude_tokens_saved`
is the win, `net_claude_tokens_wasted` is the round-trip overhead from inline calls.
A healthy ratio means most of your calls are using `path` / URLs.
