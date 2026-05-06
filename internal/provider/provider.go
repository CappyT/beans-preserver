// Package provider abstracts the LLM endpoint beans-preserver talks to. Two
// implementations live in subpackages: ollama (native /api/generate) and
// openai (any /v1/chat/completions-compatible endpoint — real OpenAI,
// OpenRouter, vLLM, llama.cpp server, even Ollama's own /v1 surface).
package provider

import "context"

// ChunkFn is invoked once per output chunk during streaming. Pass nil to
// Generate to disable streaming and receive the full response in one shot.
type ChunkFn func(chunk string)

// GenerateRequest is the inference call. Concrete providers translate it into
// their native shape; unknown options are passed through verbatim where the
// underlying API accepts them, otherwise dropped silently.
type GenerateRequest struct {
	Model   string
	Prompt  string
	// Think toggles "thinking" output on Ollama (gemma4 family). Other
	// providers ignore this field.
	Think *bool
	// Options is the YAML tier.options blob — temperature, num_predict,
	// top_p, stop, etc. Names follow Ollama conventions; the OpenAI provider
	// translates known keys (num_predict→max_tokens) automatically.
	Options map[string]any
}

// GenerateResponse is the assembled result. InputTokens/OutputTokens may be
// zero when the provider doesn't surface usage (e.g. some openai-compat
// endpoints without stream_options.include_usage).
type GenerateResponse struct {
	Text         string
	InputTokens  int
	OutputTokens int
	WallNs       int64
}

// Provider is one LLM endpoint. Implementations are HTTP clients with
// connection state; safe for concurrent use across goroutines.
type Provider interface {
	// Type identifies the implementation ("ollama", "openai") for logs and
	// cache keys. Distinct from the user-supplied provider name in config.
	Type() string
	// Hello probes connectivity, returning a short status string for logs
	// (typically a version) or an error if the endpoint is unreachable.
	Hello(ctx context.Context) (string, error)
	// HasModel reports whether the named model is usable. Providers that
	// can't enumerate models may return (true, nil) optimistically — the
	// health check downgrades that to a warning.
	HasModel(ctx context.Context, model string) (bool, error)
	// Generate runs an inference call. Streams when onChunk is non-nil.
	Generate(ctx context.Context, req GenerateRequest, onChunk ChunkFn) (*GenerateResponse, error)
}
