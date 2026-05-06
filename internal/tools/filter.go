package tools

import (
	"context"

	"github.com/CappyT/beans-preserver/internal/prompts"
	"github.com/CappyT/beans-preserver/internal/tokenize"
)

type FilterInput struct {
	Criterion string `json:"criterion" jsonschema:"plain-language description of what to keep (e.g. 'lines containing ERROR or WARN with their nearest stack frame')"`
	Content   string `json:"content" jsonschema:"the raw text to filter"`
}

func (r *Runner) Filter(ctx context.Context, in FilterInput, prog ProgressFn) (*Result, error) {
	return r.generate(
		ctx,
		"filter",
		[]string{in.Criterion, in.Content},
		func(string) string { return prompts.Filter(in.Criterion, in.Content) },
		tokenize.Estimate(in.Content),
		false, // content provided inline by Claude — no real token saving
		prog,
	)
}
