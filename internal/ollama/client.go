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
)

type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func New(baseURL string, timeout time.Duration) *Client {
	return &Client{
		BaseURL: baseURL,
		HTTP:    &http.Client{Timeout: timeout},
	}
}

type GenerateRequest struct {
	Model   string         `json:"model"`
	Prompt  string         `json:"prompt"`
	Stream  bool           `json:"stream"`
	Think   *bool          `json:"think,omitempty"`
	Options map[string]any `json:"options,omitempty"`
}

type Version struct {
	Version string `json:"version"`
}

// ServerVersion returns the running Ollama version. Useful as a liveness check.
func (c *Client) ServerVersion(ctx context.Context) (*Version, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/version", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("ollama version: status %d", resp.StatusCode)
	}
	var v Version
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return nil, err
	}
	return &v, nil
}

// HasModel returns true iff the named model is locally available on the Ollama
// server. Treats 404 as "not present" rather than an error.
func (c *Client) HasModel(ctx context.Context, name string) (bool, error) {
	body, _ := json.Marshal(map[string]string{"name": name})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/show", bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
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

type GenerateResponse struct {
	Response             string `json:"response"`
	PromptEvalCount      int    `json:"prompt_eval_count"`
	EvalCount            int    `json:"eval_count"`
	PromptEvalDurationNs int64  `json:"prompt_eval_duration"`
	EvalDurationNs       int64  `json:"eval_duration"`
	TotalDurationNs      int64  `json:"total_duration"`
	Done                 bool   `json:"done"`
}

// ChunkFn is invoked for each output chunk during streaming. Pass nil to skip
// streaming and just collect the final response.
type ChunkFn func(chunk string)

// Generate runs an /api/generate call. If onChunk is non-nil the request streams
// and onChunk is invoked once per token chunk; otherwise a non-streaming call is
// issued and the full response is returned in one shot. The returned
// GenerateResponse always carries the assembled body and the final stats fields.
func (c *Client) Generate(ctx context.Context, req GenerateRequest, onChunk ChunkFn) (*GenerateResponse, error) {
	streaming := onChunk != nil
	req.Stream = streaming

	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama %d: %s", resp.StatusCode, string(raw))
	}

	if !streaming {
		var out GenerateResponse
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return nil, fmt.Errorf("decode ollama response: %w", err)
		}
		return &out, nil
	}

	// Streaming: NDJSON, one chunk per line. Last line has done=true with stats.
	scanner := bufio.NewScanner(resp.Body)
	// Allow large lines; Ollama can emit ~16k-byte JSON lines for thinking models.
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var assembled strings.Builder
	var final GenerateResponse
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var chunk GenerateResponse
		if err := json.Unmarshal(line, &chunk); err != nil {
			return nil, fmt.Errorf("decode stream chunk: %w", err)
		}
		if chunk.Response != "" {
			assembled.WriteString(chunk.Response)
			onChunk(chunk.Response)
		}
		if chunk.Done {
			final = chunk
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read stream: %w", err)
	}
	final.Response = assembled.String()
	return &final, nil
}
