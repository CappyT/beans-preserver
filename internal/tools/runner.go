// Package tools wires MCP tools to one or more LLM providers + cache.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/CappyT/beans-preserver/internal/cache"
	"github.com/CappyT/beans-preserver/internal/config"
	"github.com/CappyT/beans-preserver/internal/prompts"
	"github.com/CappyT/beans-preserver/internal/provider"
)

type Runner struct {
	Cfg       *config.Config
	Providers map[string]provider.Provider
	Cache     *cache.Cache
}

// Result is the common shape every LLM-backed tool returns to the MCP client.
type Result struct {
	Result           string  `json:"result"`
	Confidence       float64 `json:"confidence"`
	Notes            string  `json:"notes,omitempty"`
	Provider         string  `json:"provider"`
	Model            string  `json:"model"`
	CacheHit         bool    `json:"cache_hit"`
	InputTokens      int     `json:"input_tokens,omitempty"`
	OutputTokens     int     `json:"output_tokens,omitempty"`
	WallMs           int64   `json:"wall_ms,omitempty"`
	RawAvailable     bool    `json:"raw_available"`
	RawTokenEstimate int     `json:"raw_token_estimate,omitempty"`
	// ServerFetched is true when the tool retrieved content itself (URL,
	// path) rather than receiving it as a call argument. Only server-fetched
	// calls produce real Claude-token savings — see the local_stats tool.
	ServerFetched bool `json:"server_fetched"`
}

// ProgressFn is an optional callback the runner invokes to surface progress to
// MCP clients. The runner calls it with informational messages during prompt
// eval and per-chunk during output streaming. Pass nil to disable.
type ProgressFn func(ctx context.Context, progress, total float64, message string)

// progressEmitter is a small throttler around a ProgressFn so per-token
// streaming doesn't flood the JSON-RPC channel. Emits at most every minPeriod.
type progressEmitter struct {
	fn         ProgressFn
	minPeriod  time.Duration
	lastEmit   time.Time
	totalChars int
}

func (p *progressEmitter) tick(ctx context.Context, chunk string) {
	if p == nil || p.fn == nil {
		return
	}
	p.totalChars += len(chunk)
	if time.Since(p.lastEmit) < p.minPeriod {
		return
	}
	p.lastEmit = time.Now()
	p.fn(ctx, float64(p.totalChars), 0, fmt.Sprintf("streaming… %d chars", p.totalChars))
}

func (p *progressEmitter) note(ctx context.Context, msg string) {
	if p == nil || p.fn == nil {
		return
	}
	p.fn(ctx, float64(p.totalChars), 0, msg)
}

// generate runs an LLM call for the given tool, with cache + tier resolution.
// promptBuilder takes the resolved model name and returns the final prompt text.
// cacheParts are the inputs that uniquely identify the request for cache keying.
// serverFetched is recorded on the cached Result and surfaced in stats to mark
// real Claude-token savings vs no-savings inline-content calls.
func (r *Runner) generate(
	ctx context.Context,
	tool string,
	cacheParts []string,
	promptBuilder func(model string) string,
	rawTokenEstimate int,
	serverFetched bool,
	prog ProgressFn,
) (res *Result, err error) {
	tStart := time.Now()
	defer func() {
		if r.Cache == nil {
			return
		}
		ev := cache.StatEvent{
			Tool:          tool,
			ServerFetched: serverFetched,
			WallMs:        time.Since(tStart).Milliseconds(),
			RawEstimate:   rawTokenEstimate,
			Failed:        err != nil,
		}
		if res != nil {
			ev.CacheHit = res.CacheHit
			ev.InputTokens = res.InputTokens
			ev.OutputTokens = res.OutputTokens
		}
		_ = r.Cache.RecordCall(ev)
	}()

	tier, pname, ttl, terr := r.Cfg.ResolveTool(tool)
	if terr != nil {
		err = terr
		return
	}
	prompt := promptBuilder(tier.Model)

	// Cache key includes provider name: the same model on different
	// endpoints (or backed by different weights) shouldn't share cache.
	key := cache.Key(tool, append([]string{pname, tier.Model}, cacheParts...)...)
	if v, ok := r.Cache.Get(key); ok {
		var cached Result
		if json.Unmarshal(v, &cached) == nil {
			cached.CacheHit = true
			res = &cached
			return
		}
	}

	emitter := &progressEmitter{fn: prog, minPeriod: 250 * time.Millisecond}
	emitter.note(ctx, fmt.Sprintf("calling %s/%s", pname, tier.Model))

	out, gerr := r.callTier(ctx, pname, tier, prompt, emitter)
	if gerr != nil {
		if fbTier, fbName, ok := r.Cfg.Fallback(); ok && (fbTier.Model != tier.Model || fbName != pname) {
			emitter.note(ctx, fmt.Sprintf("falling back to %s/%s", fbName, fbTier.Model))
			out, gerr = r.callTier(ctx, fbName, fbTier, prompt, emitter)
			if gerr == nil {
				tier = fbTier
				pname = fbName
			}
		}
		if gerr != nil {
			err = gerr
			return
		}
	}

	body, meta := splitMeta(out.Text)
	res = &Result{
		Result:           body,
		Confidence:       meta.Confidence,
		Notes:            meta.Notes,
		Provider:         pname,
		Model:            tier.Model,
		InputTokens:      out.InputTokens,
		OutputTokens:     out.OutputTokens,
		WallMs:           out.WallNs / 1e6,
		RawAvailable:     true,
		RawTokenEstimate: rawTokenEstimate,
		ServerFetched:    serverFetched,
	}
	if data, mErr := json.Marshal(res); mErr == nil {
		_ = r.Cache.Put(key, data, ttl)
	}
	return
}

func (r *Runner) callTier(ctx context.Context, pname string, tier config.Tier, prompt string, emitter *progressEmitter) (*provider.GenerateResponse, error) {
	p, ok := r.Providers[pname]
	if !ok {
		return nil, fmt.Errorf("provider %q not registered", pname)
	}
	req := provider.GenerateRequest{
		Model:   tier.Model,
		Prompt:  prompt,
		Think:   tier.Think,
		Options: tier.Options,
	}
	var onChunk provider.ChunkFn
	if emitter != nil && emitter.fn != nil {
		onChunk = func(chunk string) { emitter.tick(ctx, chunk) }
	}
	tStart := time.Now()
	out, err := p.Generate(ctx, req, onChunk)
	if err != nil {
		return nil, fmt.Errorf("%s/%s generate: %w", pname, tier.Model, err)
	}
	if out.WallNs == 0 {
		out.WallNs = time.Since(tStart).Nanoseconds()
	}
	return out, nil
}

type meta struct {
	Confidence float64 `json:"confidence"`
	Notes      string  `json:"notes"`
}

func splitMeta(s string) (string, meta) {
	idx := strings.LastIndex(s, prompts.MetaSeparator)
	if idx < 0 {
		// No metadata block — return body as-is with neutral confidence.
		return strings.TrimSpace(s), meta{Confidence: 0.5}
	}
	body := strings.TrimRight(s[:idx], " \n\r\t")
	tail := strings.TrimSpace(s[idx+len(prompts.MetaSeparator):])
	tail = strings.TrimPrefix(tail, "```json")
	tail = strings.TrimPrefix(tail, "```")
	tail = strings.TrimSuffix(tail, "```")
	tail = strings.TrimSpace(tail)
	var m meta
	if err := json.Unmarshal([]byte(tail), &m); err != nil {
		return body, meta{Confidence: 0.5}
	}
	if m.Confidence < 0 {
		m.Confidence = 0
	} else if m.Confidence > 1 {
		m.Confidence = 1
	}
	return body, m
}
