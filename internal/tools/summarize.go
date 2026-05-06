package tools

import (
	"context"

	"github.com/CappyT/beans-preserver/internal/prompts"
	"github.com/CappyT/beans-preserver/internal/tokenize"
)

type SummarizeInput struct {
	Focus   string `json:"focus" jsonschema:"what aspect to emphasize — e.g. 'performance characteristics' or 'security implications'"`
	Path    string `json:"path,omitempty" jsonschema:"path to a file to summarize; preferred over 'content' since the server reads the file itself, saving Claude tokens"`
	Content string `json:"content,omitempty" jsonschema:"raw text to summarize — use only when content isn't a file"`
}

func (r *Runner) Summarize(ctx context.Context, in SummarizeInput, prog ProgressFn) (*Result, error) {
	content, fetched, err := loadSource(in.Path, in.Content)
	if err != nil {
		return nil, err
	}
	return r.generate(
		ctx,
		"summarize",
		[]string{in.Focus, content},
		func(string) string { return prompts.Summarize(in.Focus, content) },
		tokenize.Estimate(content),
		fetched,
		prog,
	)
}
