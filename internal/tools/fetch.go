package tools

import (
	"context"
	"fmt"

	"github.com/CappyT/beans-preserver/internal/fetch"
	"github.com/CappyT/beans-preserver/internal/prompts"
	"github.com/CappyT/beans-preserver/internal/tokenize"
)

type FetchInput struct {
	URL      string `json:"url" jsonschema:"the URL to fetch"`
	Query    string `json:"query" jsonschema:"what information to extract from the page"`
	MaxBytes int64  `json:"max_bytes,omitempty" jsonschema:"cap on raw bytes to download (default 1 MiB)"`
}

func (r *Runner) Fetch(ctx context.Context, in FetchInput, prog ProgressFn) (*Result, error) {
	if prog != nil {
		prog(ctx, 0, 0, "fetching "+in.URL)
	}
	body, ctype, err := fetch.Get(ctx, in.URL, in.MaxBytes)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", in.URL, err)
	}
	var text string
	if fetch.LooksHTML(ctype, body) {
		text = fetch.HTMLToText(body)
	} else {
		text = string(body)
	}
	if prog != nil {
		prog(ctx, 0, 0, fmt.Sprintf("got %d bytes, extracting…", len(text)))
	}
	return r.generate(
		ctx,
		"fetch",
		[]string{in.URL, in.Query, text},
		func(string) string { return prompts.Fetch(in.URL, in.Query, text) },
		tokenize.Estimate(text),
		true, // tool retrieved content from URL — real Claude-token savings
		prog,
	)
}
