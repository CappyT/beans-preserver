package tools

import (
	"context"

	"github.com/CappyT/beans-preserver/internal/prompts"
	"github.com/CappyT/beans-preserver/internal/tokenize"
)

type ExtractInput struct {
	Query  string `json:"query" jsonschema:"what to extract — e.g. 'the function that handles JWT refresh' or 'the section about rate limiting'"`
	Path   string `json:"path,omitempty" jsonschema:"path to a file to extract from; preferred over 'source' since the server reads the file itself, saving Claude tokens"`
	Source string `json:"source,omitempty" jsonschema:"raw text to extract from — use only when source isn't a file"`
}

func (r *Runner) Extract(ctx context.Context, in ExtractInput, prog ProgressFn) (*Result, error) {
	source, fetched, err := loadSource(in.Path, in.Source)
	if err != nil {
		return nil, err
	}
	return r.generate(
		ctx,
		"extract",
		[]string{in.Query, source},
		func(string) string { return prompts.Extract(in.Query, source) },
		tokenize.Estimate(source),
		fetched,
		prog,
	)
}
