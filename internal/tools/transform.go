package tools

import (
	"context"

	"github.com/CappyT/beans-preserver/internal/prompts"
	"github.com/CappyT/beans-preserver/internal/tokenize"
)

type TransformInput struct {
	From  string `json:"from" jsonschema:"source format — e.g. 'JSON', 'CSV', 'YAML', 'TOML', 'XML'"`
	To    string `json:"to" jsonschema:"target format"`
	Path  string `json:"path,omitempty" jsonschema:"path to a file to transform; preferred over 'input' since the server reads the file itself, saving Claude tokens"`
	Input string `json:"input,omitempty" jsonschema:"raw content to transform — use only when content isn't a file"`
}

func (r *Runner) Transform(ctx context.Context, in TransformInput, prog ProgressFn) (*Result, error) {
	content, fetched, err := loadSource(in.Path, in.Input)
	if err != nil {
		return nil, err
	}
	return r.generate(
		ctx,
		"transform",
		[]string{in.From, in.To, content},
		func(string) string { return prompts.Transform(in.From, in.To, content) },
		tokenize.Estimate(content),
		fetched,
		prog,
	)
}
