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

// Result is the common shape every tool returns to the MCP client.
type Result struct {
	Result          string  `json:"result"`
	Confidence      float64 `json:"confidence"`
	Notes           string  `json:"notes,omitempty"`
	Model           string  `json:"model"`
	CacheHit        bool    `json:"cache_hit"`
	InputTokens     int     `json:"input_tokens,omitempty"`
	OutputTokens    int     `json:"output_tokens,omitempty"`
	WallMs          int64   `json:"wall_ms,omitempty"`
	RawAvailable    bool    `json:"raw_available"`
	RawTokenEstimate int    `json:"raw_token_estimate,omitempty"`
}

// generate runs an Ollama call for the given tool, with cache + tier resolution.
// promptBuilder takes the resolved model name and returns the final prompt text.
// cacheParts are the inputs that uniquely identify the request for cache keying.
func (r *Runner) generate(
	ctx context.Context,
	tool string,
	cacheParts []string,
	promptBuilder func(model string) string,
	rawTokenEstimate int,
) (*Result, error) {
	tier, ttl, err := r.Cfg.ResolveTool(tool)
	if err != nil {
		return nil, err
	}
	prompt := promptBuilder(tier.Model)

	key := cache.Key(tool, append([]string{tier.Model}, cacheParts...)...)
	if v, ok := r.Cache.Get(key); ok {
		var cached Result
		if json.Unmarshal(v, &cached) == nil {
			cached.CacheHit = true
			return &cached, nil
		}
	}

	out, err := r.callTier(ctx, tier, prompt)
	if err != nil {
		// Try fallback once.
		if fb, ok := r.Cfg.Fallback(); ok && fb.Model != tier.Model {
			out, err = r.callTier(ctx, fb, prompt)
			if err == nil {
				tier = fb
			}
		}
		if err != nil {
			return nil, err
		}
	}

	body, meta := splitMeta(out.Response)
	res := &Result{
		Result:           body,
		Confidence:       meta.Confidence,
		Notes:            meta.Notes,
		Model:            tier.Model,
		InputTokens:      out.PromptEvalCount,
		OutputTokens:     out.EvalCount,
		WallMs:           out.TotalDurationNs / 1e6,
		RawAvailable:     true,
		RawTokenEstimate: rawTokenEstimate,
	}
	if data, err := json.Marshal(res); err == nil {
		_ = r.Cache.Put(key, data, ttl)
	}
	return res, nil
}

func (r *Runner) callTier(ctx context.Context, tier config.Tier, prompt string) (*ollama.GenerateResponse, error) {
	req := ollama.GenerateRequest{
		Model:   tier.Model,
		Prompt:  prompt,
		Stream:  false,
		Think:   tier.Think,
		Options: tier.Options,
	}
	timeout := r.Cfg.Ollama.RequestTimeout
	if timeout == 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := r.Ollama.Generate(ctx, req)
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
