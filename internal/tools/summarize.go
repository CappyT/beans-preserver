package tools

import (
	"context"

	"github.com/CappyT/beans-preserver/internal/prompts"
	"github.com/CappyT/beans-preserver/internal/tokenize"
)

type SummarizeInput struct {
	Focus   string `json:"focus" jsonschema:"what aspect to emphasize — e.g. 'performance characteristics' or 'security implications'"`
	Content string `json:"content" jsonschema:"the text to summarize"`
}

func (r *Runner) Summarize(ctx context.Context, in SummarizeInput) (*Result, error) {
	return r.generate(
		ctx,
		"summarize",
		[]string{in.Focus, in.Content},
		func(string) string { return prompts.Summarize(in.Focus, in.Content) },
		tokenize.Estimate(in.Content),
	)
}
