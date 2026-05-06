package tools

import (
	"context"
	"sort"
	"time"

	"github.com/CappyT/beans-preserver/internal/cache"
)

// StatsInput parameters for the local_stats tool.
type StatsInput struct {
	Reset bool `json:"reset,omitempty" jsonschema:"if true, zero counters and re-anchor StartedAt to now"`
}

// PerToolStats is the public per-tool view exposed by local_stats.
type PerToolStats struct {
	Calls          int64   `json:"calls"`
	CacheHits      int64   `json:"cache_hits"`
	CacheHitRate   float64 `json:"cache_hit_rate"`
	Errors         int64   `json:"errors"`
	AvgWallMs      int64   `json:"avg_wall_ms"`
	AvgInputTokens int64   `json:"avg_input_tokens,omitempty"`
	AvgOutputTokens int64  `json:"avg_output_tokens,omitempty"`
	LastCallAt     string  `json:"last_call_at,omitempty"`
}

// Cumulative is the aggregate across all tools.
type Cumulative struct {
	TotalCalls           int64   `json:"total_calls"`
	CacheHitRate         float64 `json:"cache_hit_rate"`
	OllamaCalls          int64   `json:"ollama_calls"`
	OllamaInputTokens    int64   `json:"ollama_input_tokens"`
	OllamaOutputTokens   int64   `json:"ollama_output_tokens"`
	NetClaudeTokensSaved int64   `json:"net_claude_tokens_saved"`
	NetClaudeTokensWasted int64  `json:"net_claude_tokens_wasted"`
	Note                 string  `json:"note"`
}

// StatsOutput is what local_stats returns.
type StatsOutput struct {
	Uptime           string                  `json:"uptime"`
	StartedAt        string                  `json:"started_at"`
	CacheEntries     int                     `json:"cache_entries"`
	CacheBytesOnDisk int64                   `json:"cache_bytes_on_disk"`
	Tools            map[string]PerToolStats `json:"tools"`
	Cumulative       Cumulative              `json:"cumulative"`
}

func (r *Runner) Stats(_ context.Context, in StatsInput) (*StatsOutput, error) {
	if in.Reset {
		if err := r.Cache.ResetStats(); err != nil {
			return nil, err
		}
	}

	all, err := r.Cache.AllStats()
	if err != nil {
		return nil, err
	}
	startedAt, _ := r.Cache.StartedAt()
	entries, bytes := r.Cache.CacheDiagnostics()

	out := &StatsOutput{
		Tools:            map[string]PerToolStats{},
		CacheEntries:     entries,
		CacheBytesOnDisk: bytes,
	}
	if !startedAt.IsZero() {
		out.StartedAt = startedAt.Format(time.RFC3339)
		out.Uptime = time.Since(startedAt).Round(time.Second).String()
	}

	var (
		totalCalls   int64
		totalHits    int64
		totalIn      int64
		totalOut     int64
		netSaved     int64
		netWasted    int64
		ollamaCalls  int64
		toolNames    []string
	)
	for name, s := range all {
		toolNames = append(toolNames, name)
		var avgWall, avgIn, avgOut int64
		nonHits := s.Calls - s.CacheHits
		if s.Calls > 0 {
			avgWall = s.TotalWallMs / s.Calls
		}
		if nonHits > 0 {
			avgIn = s.TotalInputTokens / nonHits
			avgOut = s.TotalOutputTokens / nonHits
		}
		hitRate := 0.0
		if s.Calls > 0 {
			hitRate = float64(s.CacheHits) / float64(s.Calls)
		}
		out.Tools[name] = PerToolStats{
			Calls:           s.Calls,
			CacheHits:       s.CacheHits,
			CacheHitRate:    hitRate,
			Errors:          s.Errors,
			AvgWallMs:       avgWall,
			AvgInputTokens:  avgIn,
			AvgOutputTokens: avgOut,
			LastCallAt:      formatTime(s.LastCallAt),
		}
		totalCalls += s.Calls
		totalHits += s.CacheHits
		totalIn += s.TotalInputTokens
		totalOut += s.TotalOutputTokens
		// Only LLM-backed tools count as ollama calls; deterministic tools
		// (repo_index) have zero input tokens so they're naturally excluded.
		if s.TotalInputTokens > 0 {
			ollamaCalls += nonHits
		}
		// Real Claude-token savings only happen when the tool fetched content
		// itself: Claude pays the tool result (output_tokens) instead of the
		// raw content (raw_estimate). Net = raw_estimate - output_tokens.
		netSaved += s.TotalRawEstFetched - s.TotalOutputTokensFetched
		// Inline-content calls have no savings — Claude paid raw_estimate to
		// invoke the tool *and* output_tokens for the response. The wasted
		// figure here is just the overhead vs. simply reading the raw.
		// Best heuristic without per-call output: amortise inline output
		// proportionally to the inline-vs-fetched split.
		var inlineOutput int64
		if s.TotalOutputTokens > s.TotalOutputTokensFetched {
			inlineOutput = s.TotalOutputTokens - s.TotalOutputTokensFetched
		}
		netWasted += inlineOutput
	}
	sort.Strings(toolNames)
	hitRate := 0.0
	if totalCalls > 0 {
		hitRate = float64(totalHits) / float64(totalCalls)
	}
	out.Cumulative = Cumulative{
		TotalCalls:            totalCalls,
		CacheHitRate:          hitRate,
		OllamaCalls:           ollamaCalls,
		OllamaInputTokens:     totalIn,
		OllamaOutputTokens:    totalOut,
		NetClaudeTokensSaved:  netSaved,
		NetClaudeTokensWasted: netWasted,
		Note: "net_claude_tokens_saved counts only server-fetched tools (local_fetch, local_repo_index). " +
			"Inline-content tools (filter, extract, summarize, transform) receive content as call args " +
			"so Claude already paid for it — they show up under net_claude_tokens_wasted.",
	}
	return out, nil
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

// silence unused-import false positives during partial edits.
var _ = cache.ToolStats{}
