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
| `local_stats`       | Inspect call counts, cache hit rate, net Claude tokens saved | n/a |

Tier 1 → fast small model (default `gemma4:e2b`, ~12 s on 6 KB input).
Tier 2 → larger model (default `gemma4:26b`, ~30 s; cached aggressively).
Both configurable in `configs/default.yaml`.

Every LLM-backed tool returns a `confidence` (0–1), `notes`, and `raw_token_estimate`
so Claude can fall back to reading raw content when the summary doesn't look right.

## Install

### 1. Prerequisites

- **Go 1.25+** (the binary is built locally; no prebuilt releases yet).
- **Ollama** running somewhere reachable. Local install or remote box, doesn't
  matter — the server only needs HTTP access to the `/api` endpoint. On a
  Jetson/Pi/anything with a GPU is ideal, but a workstation works too.
- **Claude Code** installed and authenticated (this is what consumes the MCP).

### 2. Clone and build

```sh
git clone https://github.com/CappyT/beans-preserver
cd beans-preserver
go build -o bin/beans-preserver ./cmd/server
```

### 3. Pull the models on your Ollama host

```sh
ollama pull gemma4:e2b      # tier 1 — fast, default for hot path
ollama pull gemma4:26b      # tier 2 — accurate, used by local_fetch
ollama pull gemma3:latest   # fallback when a tier model errors
```

You can swap models in `configs/default.yaml`; whatever's listed under `tiers`
is what the startup health check verifies.

### 4. Point the server at your Ollama

Two options — pick one:

**a. Env var (recommended)** — leave the YAML alone, override at launch:

```sh
export OLLAMA_BASE_URL=http://your-ollama-host:11434
```

**b. Edit the config** — change `ollama.base_url` in `configs/default.yaml`.
Defaults to `http://localhost:11434`.

### 5. Verify

A standalone run prints the health-check output and waits on stdin for an MCP
client. Press Ctrl-D to exit:

```sh
$ OLLAMA_BASE_URL=http://your-host:11434 ./bin/beans-preserver
2026/05/06 13:25:34 ollama 0.23.0 reachable at http://your-host:11434
2026/05/06 13:25:35 verified 3 models: [gemma3:latest gemma4:26b gemma4:e2b]
```

If you see `models not present on ollama: [...]`, the health check is telling
you to `ollama pull` the missing ones.

### 6. Wire into Claude Code

**Project-scope** — when you `cd` into this repo, Claude Code auto-discovers
`.mcp.json` and loads the server. The shipped file has no `env` block, so the
server takes `OLLAMA_BASE_URL` from your shell environment (or falls back to
the YAML default). Export it once in your shell profile and you're set:

```sh
export OLLAMA_BASE_URL=http://your-ollama-host:11434
```

If you'd rather pin the URL to this checkout only, add an `env` block to
`.mcp.json` — but don't commit it to a shared repo.

**User-scope** — to use the tools in *any* directory:

```sh
BEANS_DIR=$(pwd)   # adjust if you're not inside the repo
claude mcp add beans-preserver \
  --scope user \
  --env OLLAMA_BASE_URL=http://your-ollama-host:11434 \
  -- "$BEANS_DIR/bin/beans-preserver" \
     --config "$BEANS_DIR/configs/default.yaml"
```

Then paste the "When to delegate to local tools" section from `CLAUDE.md` into
your global `~/.claude/CLAUDE.md` so Claude knows to reach for these tools.

### 7. Sanity check from Claude Code

Open a Claude Code session and ask it to call one of the tools:

> Call `local_repo_index` on the current directory and summarise what kinds of
> files you find.

If that returns a tree in <1 s, the MCP wiring is good. Then try
`local_stats` to see your first counter rolling.

## Updating

```sh
cd beans-preserver
git pull
go build -o bin/beans-preserver ./cmd/server
```

The cache lives at `~/.cache/beans-preserver/cache.db` and survives upgrades;
delete it if you want to start fresh.

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
- **v0.3** — startup health check (verify Ollama + every configured model),
  `local_stats` tool with persisted per-tool counters and net-tokens-saved
  separated between server-fetched (real win) and inline-content (no win)
  invocations.
- **v0.4** — `local_filter`, `local_extract`, `local_summarize`,
  `local_transform` accept a `path:` parameter (preferred) so the server reads
  the file itself. Closes the inline-content double-pay loophole; CLAUDE.md
  pushes Claude to use `path:` whenever the data is on disk. Files capped at
  4 MiB to keep request bodies bounded.
- **Dropped from roadmap**: PostToolUse hook for Bash compression
  (Claude Code's hook API only allows augmenting context, not replacing tool
  output — a wrapper would *increase* tokens, not save them); LLM-aided
  `repo_index` v2 (cost > value vs. on-demand `local_summarize`).

## License

TBD.
