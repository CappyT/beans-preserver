package tokenize

// Estimate returns a rough token count using the standard ~4 chars/token
// heuristic. Good enough for hook thresholds; we are never billed on this number.
func Estimate(s string) int {
	return (len(s) + 3) / 4
}
