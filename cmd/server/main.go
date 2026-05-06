package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/CappyT/beans-preserver/internal/cache"
	"github.com/CappyT/beans-preserver/internal/config"
	"github.com/CappyT/beans-preserver/internal/ollama"
	"github.com/CappyT/beans-preserver/internal/tools"
)

const version = "v0.2.0"

func main() {
	configPath := flag.String("config", "configs/default.yaml", "path to YAML config")
	flag.Parse()

	log.SetOutput(os.Stderr) // stdout is reserved for MCP framing

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	cch, err := cache.Open(cfg.Cache.Path, cfg.Cache.MaxEntries)
	if err != nil {
		log.Fatalf("open cache: %v", err)
	}
	defer cch.Close()

	runner := &tools.Runner{
		Cfg:    cfg,
		Ollama: ollama.New(cfg.Ollama.BaseURL, cfg.Ollama.RequestTimeout),
		Cache:  cch,
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "beans-preserver",
		Version: version,
	}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "local_filter",
		Description: "Filter a block of text via a local LLM, keeping only lines matching a plain-language criterion. Use to compress verbose Bash output, logs, grep results before they enter Claude's context. Returns confidence + raw_token_estimate.",
	}, wrap(runner.Filter))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "local_extract",
		Description: "Extract a specific section from a long source via a local LLM. Use when you need 'the function that does X' or 'the part about Y' from a file too large to read fully. Outputs 'NOT FOUND' if the source has no answer.",
	}, wrap(runner.Extract))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "local_summarize",
		Description: "Produce a concise prose summary of long content via a local LLM, with a focus area you specify. Use before deciding whether a file is worth a full read.",
	}, wrap(runner.Summarize))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "local_transform",
		Description: "Mechanical format transformation via a local LLM (JSON↔YAML, CSV→JSON, etc). No reasoning needed — preserves all data.",
	}, wrap(runner.Transform))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "local_fetch",
		Description: "Fetch an HTTP URL, strip HTML, and extract only the section answering your query. Saves substantial tokens vs the built-in WebFetch on long pages. Cached for 24h.",
	}, wrap(runner.Fetch))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "local_repo_index",
		Description: "Fast deterministic walk of a repository — returns paths, sizes and classified kinds. Use to map an unfamiliar repo before deciding which files to Read. No LLM call, no token cost.",
	}, wrapPlain(runner.RepoIndex))

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil {
		log.Fatalf("server: %v", err)
	}
}

// wrap adapts a typed Runner method (in, prog -> *Result) into the SDK's tool
// handler shape, threading the request's progress token through to the runner.
func wrap[I any](fn func(context.Context, I, tools.ProgressFn) (*tools.Result, error)) func(context.Context, *mcp.CallToolRequest, I) (*mcp.CallToolResult, tools.Result, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, in I) (*mcp.CallToolResult, tools.Result, error) {
		prog := buildProgressFn(req)
		out, err := fn(ctx, in, prog)
		if err != nil {
			return nil, tools.Result{}, err
		}
		j, _ := json.MarshalIndent(out, "", "  ")
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: string(j)}},
		}, *out, nil
	}
}

// wrapPlain is the same as wrap but for tools whose output type isn't tools.Result
// (deterministic tools that bypass the LLM runner and never need progress).
func wrapPlain[I any, O any](fn func(context.Context, I) (*O, error)) func(context.Context, *mcp.CallToolRequest, I) (*mcp.CallToolResult, O, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in I) (*mcp.CallToolResult, O, error) {
		out, err := fn(ctx, in)
		if err != nil {
			var zero O
			return nil, zero, err
		}
		j, _ := json.MarshalIndent(out, "", "  ")
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: string(j)}},
		}, *out, nil
	}
}

// buildProgressFn returns a runner ProgressFn that emits MCP progress
// notifications, or nil if the client didn't supply a progress token.
func buildProgressFn(req *mcp.CallToolRequest) tools.ProgressFn {
	if req == nil || req.Params == nil {
		return nil
	}
	token := req.Params.GetProgressToken()
	if token == nil {
		return nil
	}
	return func(ctx context.Context, progress, total float64, message string) {
		_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
			ProgressToken: token,
			Progress:      progress,
			Total:         total,
			Message:       message,
		})
	}
}
