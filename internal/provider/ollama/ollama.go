// Package ollama implements provider.Provider against an Ollama /api endpoint.
package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/CappyT/beans-preserver/internal/provider"
)

type Provider struct {
	baseURL string
	http    *http.Client
}

// New constructs an Ollama provider rooted at baseURL (e.g.
// "http://localhost:11434"). timeout caps the entire request including
// streaming body read.
func New(baseURL string, timeout time.Duration) *Provider {
	if timeout == 0 {
		timeout = 10 * time.Minute
	}
	return &Provider{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: timeout},
	}
}

func (p *Provider) Type() string { return "ollama" }

type versionResp struct {
	Version string `json:"version"`
}

func (p *Provider) Hello(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/api/version", nil)
	if err != nil {
		return "", err
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("ollama version: status %d", resp.StatusCode)
	}
	var v versionResp
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return "", err
	}
	return "ollama " + v.Version, nil
}

// HasModel uses /api/show — present-but-unloaded still returns 200. 404 means
// the model isn't pulled locally; any other non-2xx is surfaced as an error.
func (p *Provider) HasModel(ctx context.Context, name string) (bool, error) {
	body, _ := json.Marshal(map[string]string{"name": name})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/api/show", bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.http.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(resp.Body)
		return false, fmt.Errorf("ollama show %s: %d %s", name, resp.StatusCode, string(raw))
	}
	return true, nil
}

type generateReq struct {
	Model   string         `json:"model"`
	Prompt  string         `json:"prompt"`
	Stream  bool           `json:"stream"`
	Think   *bool          `json:"think,omitempty"`
	Options map[string]any `json:"options,omitempty"`
}

type generateChunk struct {
	Response        string `json:"response"`
	PromptEvalCount int    `json:"prompt_eval_count"`
	EvalCount       int    `json:"eval_count"`
	TotalDurationNs int64  `json:"total_duration"`
	Done            bool   `json:"done"`
}

func (p *Provider) Generate(ctx context.Context, req provider.GenerateRequest, onChunk provider.ChunkFn) (*provider.GenerateResponse, error) {
	streaming := onChunk != nil
	body, err := json.Marshal(generateReq{
		Model:   req.Model,
		Prompt:  req.Prompt,
		Stream:  streaming,
		Think:   req.Think,
		Options: req.Options,
	})
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := p.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama %d: %s", resp.StatusCode, string(raw))
	}

	if !streaming {
		var c generateChunk
		if err := json.NewDecoder(resp.Body).Decode(&c); err != nil {
			return nil, fmt.Errorf("decode ollama response: %w", err)
		}
		return &provider.GenerateResponse{
			Text:         c.Response,
			InputTokens:  c.PromptEvalCount,
			OutputTokens: c.EvalCount,
			WallNs:       c.TotalDurationNs,
		}, nil
	}

	// NDJSON stream: one chunk per line; last line has done=true with stats.
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var assembled strings.Builder
	var final generateChunk
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var c generateChunk
		if err := json.Unmarshal(line, &c); err != nil {
			return nil, fmt.Errorf("decode stream chunk: %w", err)
		}
		if c.Response != "" {
			assembled.WriteString(c.Response)
			onChunk(c.Response)
		}
		if c.Done {
			final = c
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read stream: %w", err)
	}
	return &provider.GenerateResponse{
		Text:         assembled.String(),
		InputTokens:  final.PromptEvalCount,
		OutputTokens: final.EvalCount,
		WallNs:       final.TotalDurationNs,
	}, nil
}
