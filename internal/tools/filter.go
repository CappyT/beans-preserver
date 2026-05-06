package tools

import (
	"context"

	"github.com/CappyT/beans-preserver/internal/prompts"
	"github.com/CappyT/beans-preserver/internal/tokenize"
)

type FilterInput struct {
	Criterion string `json:"criterion" jsonschema:"plain-language description of what to keep (e.g. 'lines containing ERROR or WARN with their nearest stack frame')"`
	Path      string `json:"path,omitempty" jsonschema:"path to a file to filter; preferred over 'content' since the server reads the file itself, saving Claude tokens"`
	Content   string `json:"content,omitempty" jsonschema:"raw text to filter — use only when content isn't a file (e.g. piped output already in context)"`
}

func (r *Runner) Filter(ctx context.Context, in FilterInput, prog ProgressFn) (*Result, error) {
	content, fetched, err := loadSource(in.Path, in.Content)
	if err != nil {
		return nil, err
	}
	return r.generate(
		ctx,
		"filter",
		[]string{in.Criterion, content},
		func(string) string { return prompts.Filter(in.Criterion, content) },
		tokenize.Estimate(content),
		fetched,
		prog,
	)
}
