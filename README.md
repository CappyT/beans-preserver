# beans-preserver

Token-saving MCP server for Claude Code: offload preprocessing (filter, extract,
summarize, transform, fetch, repo-index) to a local LLM so Claude only handles
the parts that actually need it.

Built around an Ollama instance running on a Jetson AGX, but works with any
Ollama endpoint.

## Why

A lot of what Claude reads is irrelevant: 5 000-line npm logs, full HTML pages,
giant grep results, repetitive boilerplate. Local LLMs are perfectly capable of
filtering and summarizing — they just don't get to. This server exposes those
capabilities as MCP tools so Claude can call them when an operation would
otherwise burn thousands of tokens.

## Tools

| Tool                | What it does                                    | Tier   |
| ------------------- | ----------------------------------------------- | ------ |
| `local_filter`      | Keep only lines matching a plain-language rule  | 1      |
| `local_extract`     | Pull the section answering a query from a doc   | 1      |
| `local_summarize`   | Concise prose summary of long content           | 1      |
| `local_transform`   | Mechanical format conversion (JSON↔YAML, etc)   | 1      |
| `local_fetch`       | HTTP fetch + HTML strip + targeted extraction   | 2      |
| `local_repo_index`  | Fast deterministic walk of a repo (no LLM call) | n/a    |

Tier 1 → fast small model (default `gemma4:e2b`, ~12 s on 6 KB input).
Tier 2 → larger model (default `gemma4:26b`, ~30 s; cached aggressively).
Both configurable in `configs/default.yaml`.

Every LLM-backed tool returns a `confidence` (0–1), `notes`, and `raw_token_estimate`
so Claude can fall back to reading raw content when the summary doesn't look right.

## Setup

```sh
git clone https://github.com/CappyT/beans-preserver
cd beans-preserver
go build -o bin/beans-preserver ./cmd/server
```

Edit `configs/default.yaml` to point at your Ollama endpoint:

```yaml
ollama:
  base_url: http://your-ollama-host:11434
```

Models the config expects on the server (pull as needed):

```sh
ollama pull gemma4:e2b
ollama pull gemma4:26b
ollama pull gemma3:latest   # fallback
```

## Wire into Claude Code

Project-scope (`.mcp.json` is already in this repo, points at `bin/beans-preserver`).

User-scope (use the tools in any directory):

```sh
claude mcp add beans-preserver \
  --scope user \
  -- /absolute/path/to/bin/beans-preserver \
     --config /absolute/path/to/configs/default.yaml
```

Then either copy `CLAUDE.md` into your global config or paste its policy section
into your existing CLAUDE.md so Claude knows when to reach for these tools.

## Cache

bbolt-backed LRU on disk at `~/.cache/beans-preserver/cache.db`. Cache key is
`sha256(tool ‖ model ‖ inputs)` — same call twice ≈ instant. `local_fetch`
entries get a 24 h TTL because URL contents drift; everything else lives until
the inputs change.

## Layout

```
cmd/server/        # MCP stdio server (single binary)
internal/
  config/          # YAML loader, tier resolution
  ollama/          # /api/generate client
  cache/           # bbolt LRU + TTL
  prompts/         # tool prompt templates with <<META>> trailer
  fetch/           # HTTP + HTML→text
  tokenize/        # rough char/4 estimator
  tools/           # one .go per tool, all share Runner.generate
configs/default.yaml
```

Adding a tool is ~20 lines: input struct, prompt template, a `Runner.X` method,
one `mcp.AddTool` in `cmd/server/main.go`.

## Status

- **v0.1** — 6 tools shipped, integration smoke + unit tests passing.
- **v0.2** — streaming + MCP progress notifications: tier-2 calls now emit
  intermediate progress messages so Claude doesn't see a 30 s stall.
- **Dropped from roadmap**: PostToolUse hook for Bash compression
  (Claude Code's hook API only allows augmenting context, not replacing tool
  output — a wrapper would *increase* tokens, not save them); LLM-aided
  `repo_index` v2 (cost > value vs. on-demand `local_summarize`).

## License

TBD.
