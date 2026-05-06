package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/CappyT/beans-preserver/internal/cache"
	"github.com/CappyT/beans-preserver/internal/config"
	"github.com/CappyT/beans-preserver/internal/provider"
	ollamaprov "github.com/CappyT/beans-preserver/internal/provider/ollama"
	openaiprov "github.com/CappyT/beans-preserver/internal/provider/openai"
	"github.com/CappyT/beans-preserver/internal/tools"
)

const version = "v0.5.0"

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

	providers, err := buildProviders(cfg)
	if err != nil {
		log.Fatalf("build providers: %v", err)
	}

	// Verify every configured provider is reachable and the models its tiers
	// reference are actually available *before* we accept any tool calls.
	{
		boot, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		if err := healthCheck(boot, providers, cfg); err != nil {
			cancel()
			log.Fatalf("health check failed: %v", err)
		}
		cancel()
	}

	runner := &tools.Runner{
		Cfg:       cfg,
		Providers: providers,
		Cache:     cch,
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "beans-preserver",
		Version: version,
	}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "local_filter",
		Description: "Filter a file or block of text via a local LLM, keeping only lines matching a plain-language criterion. PREFER passing 'path' over 'content' — the server reads the file itself and you only pay tokens for the filtered result. Use 'content' only when the data isn't a file (e.g. piped Bash output).",
	}, wrap(runner.Filter))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "local_extract",
		Description: "Extract a specific section from a file or long source via a local LLM. PREFER 'path' over 'source' to save tokens. Use when you need 'the function that does X' or 'the part about Y' from a file too large to read fully. Outputs 'NOT FOUND' if the source has no answer.",
	}, wrap(runner.Extract))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "local_summarize",
		Description: "Produce a concise prose summary of a file or long content via a local LLM. PREFER 'path' over 'content' to save tokens. Use before deciding whether a file is worth a full Read.",
	}, wrap(runner.Summarize))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "local_transform",
		Description: "Mechanical format transformation via a local LLM (JSON↔YAML, CSV→JSON, etc). PREFER 'path' over 'input' to save tokens. No reasoning needed — preserves all data.",
	}, wrap(runner.Transform))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "local_fetch",
		Description: "Fetch an HTTP URL, strip HTML, and extract only the section answering your query. Saves substantial tokens vs the built-in WebFetch on long pages. Cached for 24h.",
	}, wrap(runner.Fetch))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "local_repo_index",
		Description: "Fast deterministic walk of a repository — returns paths, sizes and classified kinds. Use to map an unfamiliar repo before deciding which files to Read. No LLM call, no token cost.",
	}, wrapPlain(runner.RepoIndex))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "local_stats",
		Description: "Inspect the runtime: per-tool call counts, cache hit rate, latency, ollama tokens consumed, and net Claude tokens saved (vs wasted on inline-content calls). Pass reset:true to zero counters.",
	}, wrapPlain(runner.Stats))

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil {
		log.Fatalf("server: %v", err)
	}
}

// buildProviders instantiates a provider for each entry in cfg.Providers.
// Unknown types are a fatal misconfiguration.
func buildProviders(cfg *config.Config) (map[string]provider.Provider, error) {
	out := make(map[string]provider.Provider, len(cfg.Providers))
	for name, pcfg := range cfg.Providers {
		if pcfg.BaseURL == "" {
			return nil, fmt.Errorf("provider %q: base_url is empty (set it in config or via env)", name)
		}
		switch pcfg.Type {
		case "ollama":
			out[name] = ollamaprov.New(pcfg.BaseURL, pcfg.RequestTimeout)
		case "openai":
			out[name] = openaiprov.New(pcfg.BaseURL, pcfg.APIKey, pcfg.RequestTimeout)
		default:
			return nil, fmt.Errorf("provider %q: unknown type %q (must be 'ollama' or 'openai')", name, pcfg.Type)
		}
	}
	return out, nil
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

// healthCheck verifies every configured provider is reachable, then verifies
// each model referenced by a non-fallback tier exists on its provider. The
// fallback model is best-effort — a missing fallback only logs a warning.
//
// For OpenAI-compat providers that don't expose /models, HasModel returns
// (true, nil) optimistically and we log an info note rather than failing.
func healthCheck(ctx context.Context, providers map[string]provider.Provider, cfg *config.Config) error {
	// Stable provider order in logs.
	pnames := make([]string, 0, len(providers))
	for name := range providers {
		pnames = append(pnames, name)
	}
	sort.Strings(pnames)

	for _, name := range pnames {
		p := providers[name]
		pcfg := cfg.Providers[name]
		msg, err := p.Hello(ctx)
		if err != nil {
			return fmt.Errorf("provider %q (%s) unreachable at %s: %w", name, pcfg.Type, pcfg.BaseURL, err)
		}
		log.Printf("provider %q (%s @ %s): %s", name, pcfg.Type, pcfg.BaseURL, msg)
	}

	// Group required models by provider so we list each one at most once.
	requiredByProvider := map[string]map[string]bool{}
	addModel := func(pname, model string) {
		if model == "" {
			return
		}
		if requiredByProvider[pname] == nil {
			requiredByProvider[pname] = map[string]bool{}
		}
		requiredByProvider[pname][model] = true
	}
	for tname, tier := range cfg.Tiers {
		if tname == "fallback" {
			continue
		}
		_, pname, err := cfg.ResolveTier(tname)
		if err != nil {
			return err
		}
		addModel(pname, tier.Model)
	}

	var missing []string
	var checked []string
	pkeys := make([]string, 0, len(requiredByProvider))
	for k := range requiredByProvider {
		pkeys = append(pkeys, k)
	}
	sort.Strings(pkeys)
	for _, pname := range pkeys {
		p := providers[pname]
		mkeys := make([]string, 0, len(requiredByProvider[pname]))
		for m := range requiredByProvider[pname] {
			mkeys = append(mkeys, m)
		}
		sort.Strings(mkeys)
		for _, m := range mkeys {
			ok, err := p.HasModel(ctx, m)
			if err != nil {
				log.Printf("warning: provider %q model check failed for %s: %v (continuing)", pname, m, err)
				continue
			}
			if !ok {
				missing = append(missing, fmt.Sprintf("%s/%s", pname, m))
				continue
			}
			checked = append(checked, fmt.Sprintf("%s/%s", pname, m))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("models not present (run `ollama pull <name>` or check the provider): %v", missing)
	}
	log.Printf("verified %d models: %v", len(checked), checked)

	if fbTier, fbName, ok := cfg.Fallback(); ok && fbTier.Model != "" {
		p := providers[fbName]
		has, herr := p.HasModel(ctx, fbTier.Model)
		switch {
		case herr != nil:
			log.Printf("warning: fallback %q/%s check errored: %v (fallback may not work)", fbName, fbTier.Model, herr)
		case !has:
			log.Printf("warning: fallback %q/%s not present (fallback disabled)", fbName, fbTier.Model)
		default:
			log.Printf("verified fallback: %s/%s", fbName, fbTier.Model)
		}
	}
	return nil
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

