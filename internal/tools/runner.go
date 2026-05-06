// Package tools wires MCP tools to the Ollama client + cache.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/CappyT/beans-preserver/internal/cache"
	"github.com/CappyT/beans-preserver/internal/config"
	"github.com/CappyT/beans-preserver/internal/ollama"
	"github.com/CappyT/beans-preserver/internal/prompts"
)

type Runner struct {
	Cfg    *config.Config
	Ollama *ollama.Client
	Cache  *cache.Cache
}

// Result is the common shape every LLM-backed tool returns to the MCP client.
type Result struct {
	Result           string  `json:"result"`
	Confidence       float64 `json:"confidence"`
	Notes            string  `json:"notes,omitempty"`
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

// generate runs an Ollama call for the given tool, with cache + tier resolution.
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

	tier, ttl, terr := r.Cfg.ResolveTool(tool)
	if terr != nil {
		err = terr
		return
	}
	prompt := promptBuilder(tier.Model)

	key := cache.Key(tool, append([]string{tier.Model}, cacheParts...)...)
	if v, ok := r.Cache.Get(key); ok {
		var cached Result
		if json.Unmarshal(v, &cached) == nil {
			cached.CacheHit = true
			res = &cached
			return
		}
	}

	emitter := &progressEmitter{fn: prog, minPeriod: 250 * time.Millisecond}
	emitter.note(ctx, fmt.Sprintf("calling %s", tier.Model))

	out, gerr := r.callTier(ctx, tier, prompt, emitter)
	if gerr != nil {
		if fb, ok := r.Cfg.Fallback(); ok && fb.Model != tier.Model {
			emitter.note(ctx, fmt.Sprintf("falling back to %s", fb.Model))
			out, gerr = r.callTier(ctx, fb, prompt, emitter)
			if gerr == nil {
				tier = fb
			}
		}
		if gerr != nil {
			err = gerr
			return
		}
	}

	body, meta := splitMeta(out.Response)
	res = &Result{
		Result:           body,
		Confidence:       meta.Confidence,
		Notes:            meta.Notes,
		Model:            tier.Model,
		InputTokens:      out.PromptEvalCount,
		OutputTokens:     out.EvalCount,
		WallMs:           out.TotalDurationNs / 1e6,
		RawAvailable:     true,
		RawTokenEstimate: rawTokenEstimate,
		ServerFetched:    serverFetched,
	}
	if data, mErr := json.Marshal(res); mErr == nil {
		_ = r.Cache.Put(key, data, ttl)
	}
	return
}

func (r *Runner) callTier(ctx context.Context, tier config.Tier, prompt string, emitter *progressEmitter) (*ollama.GenerateResponse, error) {
	req := ollama.GenerateRequest{
		Model:   tier.Model,
		Prompt:  prompt,
		Think:   tier.Think,
		Options: tier.Options,
	}
	timeout := r.Cfg.Ollama.RequestTimeout
	if timeout == 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var onChunk ollama.ChunkFn
	if emitter != nil && emitter.fn != nil {
		onChunk = func(chunk string) { emitter.tick(ctx, chunk) }
	}
	out, err := r.Ollama.Generate(ctx, req, onChunk)
	if err != nil {
		return nil, fmt.Errorf("ollama generate (%s): %w", tier.Model, err)
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
