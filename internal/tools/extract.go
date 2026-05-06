package tools

import (
	"context"

	"github.com/CappyT/beans-preserver/internal/prompts"
	"github.com/CappyT/beans-preserver/internal/tokenize"
)

type ExtractInput struct {
	Query  string `json:"query" jsonschema:"what to extract — e.g. 'the function that handles JWT refresh' or 'the section about rate limiting'"`
	Source string `json:"source" jsonschema:"the raw text to extract from"`
}

func (r *Runner) Extract(ctx context.Context, in ExtractInput, prog ProgressFn) (*Result, error) {
	return r.generate(
		ctx,
		"extract",
		[]string{in.Query, in.Source},
		func(string) string { return prompts.Extract(in.Query, in.Source) },
		tokenize.Estimate(in.Source),
		prog,
	)
}
