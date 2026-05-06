# beans-preserver — token-saving MCP

Local-LLM MCP server that offloads token-heavy preprocessing (filtering, extraction,
summarization, web fetch) to a Jetson AGX running Ollama at `your-ollama-host:11434`.

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
  "model": "gemma4:e2b",
  "cache_hit": true|false,
  "input_tokens": 0,
  "output_tokens": 0,
  "wall_ms": 0,
  "raw_available": true,
  "raw_token_estimate": 0
}
```

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

**Real savings (server-fetched tools):**
- `local_fetch` — server pulls the URL itself; you only pay for the extracted answer.
- `local_repo_index` — deterministic walk; output is much smaller than reading every file.

**No savings (inline-content tools):** `local_filter`, `local_extract`, `local_summarize`,
`local_transform` accept content as a call argument. You pay the input tokens for the
content when calling the tool, then pay again for the response. Use these only when
either (a) the work doesn't fit your context budget anyway and you'd rather the local
model deal with it, (b) the cache will be hit by repeated calls on the same content,
or (c) the user explicitly wants a structured local-LLM view rather than your own
analysis.

Call `local_stats` to see the running totals: `cumulative.net_claude_tokens_saved` is
the win, `net_claude_tokens_wasted` is the round-trip overhead from inline calls.
