// Package openai implements provider.Provider against any OpenAI-compatible
// /v1/chat/completions endpoint — real OpenAI, OpenRouter, Together, vLLM,
// llama.cpp server, even Ollama's own /v1 surface.
package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/CappyT/beans-preserver/internal/provider"
)

type Provider struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// New constructs an OpenAI-compatible provider. baseURL must include the /v1
// (or equivalent) path prefix — e.g. "https://api.openai.com/v1" or
// "http://localhost:11434/v1" for Ollama in OpenAI-compat mode. apiKey may be
// empty for endpoints that don't require auth.
func New(baseURL, apiKey string, timeout time.Duration) *Provider {
	if timeout == 0 {
		timeout = 10 * time.Minute
	}
	return &Provider{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http:    &http.Client{Timeout: timeout},
	}
}

func (p *Provider) Type() string { return "openai" }

func (p *Provider) auth(req *http.Request) {
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
}

// errModelsUnsupported signals the endpoint doesn't expose /models. The
// health check downgrades that to a warning rather than failing.
var errModelsUnsupported = errors.New("provider does not expose /models")

type modelsResp struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

func (p *Provider) listModels(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	p.auth(req)
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("openai /models: 401 unauthorized — check api_key")
	}
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		return nil, errModelsUnsupported
	}
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openai /models: %d %s", resp.StatusCode, string(raw))
	}
	var m modelsResp
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(m.Data))
	for _, d := range m.Data {
		out = append(out, d.ID)
	}
	return out, nil
}

func (p *Provider) Hello(ctx context.Context) (string, error) {
	models, err := p.listModels(ctx)
	if err != nil {
		if errors.Is(err, errModelsUnsupported) {
			return "openai-compat (no /models endpoint)", nil
		}
		return "", err
	}
	return fmt.Sprintf("openai-compat (%d models listed)", len(models)), nil
}

func (p *Provider) HasModel(ctx context.Context, name string) (bool, error) {
	models, err := p.listModels(ctx)
	if err != nil {
		if errors.Is(err, errModelsUnsupported) {
			// Endpoint can't enumerate; assume the model is fine. Health
			// check logs a warning when listModels fails.
			return true, nil
		}
		return false, err
	}
	return slices.Contains(models, name), nil
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type streamOpts struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatReq struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Stream      bool          `json:"stream,omitempty"`
	Temperature *float64      `json:"temperature,omitempty"`
	MaxTokens   *int          `json:"max_tokens,omitempty"`
	TopP        *float64      `json:"top_p,omitempty"`
	Stop        []string      `json:"stop,omitempty"`
	StreamOpts  *streamOpts   `json:"stream_options,omitempty"`
}

type chatChoice struct {
	Index   int `json:"index"`
	Message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
	Delta struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"delta"`
	FinishReason string `json:"finish_reason"`
}

type chatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

type chatResp struct {
	ID      string       `json:"id"`
	Choices []chatChoice `json:"choices"`
	Usage   *chatUsage   `json:"usage"`
}

// translateOptions extracts well-known options from the YAML tier.options
// blob into typed Chat Completions fields. Ollama's num_predict is mapped to
// max_tokens; everything else passes through under its OpenAI name.
func translateOptions(opt map[string]any) (temp, topP *float64, maxTok *int, stop []string) {
	if v, ok := opt["temperature"]; ok {
		if f, ok := toFloat(v); ok {
			temp = &f
		}
	}
	if v, ok := opt["top_p"]; ok {
		if f, ok := toFloat(v); ok {
			topP = &f
		}
	}
	for _, k := range []string{"max_tokens", "num_predict"} {
		if v, ok := opt[k]; ok {
			if i, ok := toInt(v); ok {
				maxTok = &i
				break
			}
		}
	}
	if v, ok := opt["stop"]; ok {
		switch s := v.(type) {
		case string:
			stop = []string{s}
		case []string:
			stop = s
		case []any:
			for _, e := range s {
				if str, ok := e.(string); ok {
					stop = append(stop, str)
				}
			}
		}
	}
	return
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case float32:
		return int(n), true
	}
	return 0, false
}

func (p *Provider) Generate(ctx context.Context, req provider.GenerateRequest, onChunk provider.ChunkFn) (*provider.GenerateResponse, error) {
	streaming := onChunk != nil
	temp, topP, maxTok, stop := translateOptions(req.Options)
	body := chatReq{
		Model:       req.Model,
		Messages:    []chatMessage{{Role: "user", Content: req.Prompt}},
		Stream:      streaming,
		Temperature: temp,
		MaxTokens:   maxTok,
		TopP:        topP,
		Stop:        stop,
	}
	if streaming {
		body.StreamOpts = &streamOpts{IncludeUsage: true}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if streaming {
		httpReq.Header.Set("Accept", "text/event-stream")
	}
	p.auth(httpReq)
	resp, err := p.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		rawBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openai %d: %s", resp.StatusCode, string(rawBody))
	}

	tStart := time.Now()
	if !streaming {
		var c chatResp
		if err := json.NewDecoder(resp.Body).Decode(&c); err != nil {
			return nil, fmt.Errorf("decode openai response: %w", err)
		}
		text := ""
		if len(c.Choices) > 0 {
			text = c.Choices[0].Message.Content
		}
		out := &provider.GenerateResponse{
			Text:   text,
			WallNs: time.Since(tStart).Nanoseconds(),
		}
		if c.Usage != nil {
			out.InputTokens = c.Usage.PromptTokens
			out.OutputTokens = c.Usage.CompletionTokens
		}
		return out, nil
	}

	// SSE: lines of "data: {json}" terminated by "data: [DONE]". Other lines
	// (id:, event:, comments, blanks) are heartbeats — skip them.
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var assembled strings.Builder
	var usage *chatUsage
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var c chatResp
		if err := json.Unmarshal([]byte(payload), &c); err != nil {
			// Some servers occasionally interleave non-JSON keepalives.
			continue
		}
		if len(c.Choices) > 0 {
			delta := c.Choices[0].Delta.Content
			if delta != "" {
				assembled.WriteString(delta)
				onChunk(delta)
			}
		}
		if c.Usage != nil {
			usage = c.Usage
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read sse: %w", err)
	}
	out := &provider.GenerateResponse{
		Text:   assembled.String(),
		WallNs: time.Since(tStart).Nanoseconds(),
	}
	if usage != nil {
		out.InputTokens = usage.PromptTokens
		out.OutputTokens = usage.CompletionTokens
	}
	return out, nil
}
