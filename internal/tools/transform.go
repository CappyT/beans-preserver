package tools

import (
	"context"

	"github.com/CappyT/beans-preserver/internal/prompts"
	"github.com/CappyT/beans-preserver/internal/tokenize"
)

type TransformInput struct {
	From  string `json:"from" jsonschema:"source format — e.g. 'JSON', 'CSV', 'YAML', 'TOML', 'XML'"`
	To    string `json:"to" jsonschema:"target format"`
	Input string `json:"input" jsonschema:"the content to transform"`
}

func (r *Runner) Transform(ctx context.Context, in TransformInput, prog ProgressFn) (*Result, error) {
	return r.generate(
		ctx,
		"transform",
		[]string{in.From, in.To, in.Input},
		func(string) string { return prompts.Transform(in.From, in.To, in.Input) },
		tokenize.Estimate(in.Input),
		false,
		prog,
	)
}
