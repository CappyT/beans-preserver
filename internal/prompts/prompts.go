package prompts

import "strings"

// MetaSeparator marks the boundary between the model's primary output and a
// trailing JSON metadata blob (`{"confidence":<float>,"notes":"..."}`).
// Using a delimiter avoids quoting/escaping the result body inside JSON.
const MetaSeparator = "<<META>>"

const metaTrailer = `

After your output, on its own line, write exactly:
` + MetaSeparator + `{"confidence": <float 0..1>, "notes": "<one short sentence>"}`

const filterTemplate = `You are a text filter. Extract from the INPUT only the parts that match the CRITERION.
Preserve original formatting (line breaks, indentation, line numbers if present).
Be conservative — when in doubt, keep the line.

CRITERION:
%s

INPUT:
---
%s
---` + metaTrailer

const extractTemplate = `From the SOURCE below, extract the section that answers QUERY.
You may quote and rephrase as needed. Stay grounded — do not add facts that are not in the source.
If the source does not contain an answer, output the literal string "NOT FOUND".

QUERY:
%s

SOURCE:
---
%s
---` + metaTrailer

const summarizeTemplate = `Summarize the CONTENT below into a concise prose summary, 3-7 sentences.
Focus on FOCUS. Stay factual and specific — include names, numbers, and identifiers when present.

FOCUS:
%s

CONTENT:
---
%s
---` + metaTrailer

const transformTemplate = `Transform the INPUT from %s to %s. Preserve all data; do not omit fields.
Output only the transformed content, no commentary.

INPUT:
---
%s
---` + metaTrailer

const fetchTemplate = `Below is the cleaned text content of a web page (HTML stripped).
Extract the section that answers QUERY. Quote relevant passages and include URLs when helpful.
If the page does not address the query, output the literal string "NOT FOUND".

URL: %s

QUERY:
%s

CONTENT:
---
%s
---` + metaTrailer

func Filter(criterion, input string) string {
	return formatTemplate(filterTemplate, criterion, input)
}

func Extract(query, source string) string {
	return formatTemplate(extractTemplate, query, source)
}

func Summarize(focus, content string) string {
	return formatTemplate(summarizeTemplate, focus, content)
}

func Transform(from, to, input string) string {
	return formatTemplate(transformTemplate, from, to, input)
}

func Fetch(url, query, content string) string {
	return formatTemplate(fetchTemplate, url, query, content)
}

func formatTemplate(tpl string, args ...string) string {
	out := tpl
	for _, a := range args {
		out = strings.Replace(out, "%s", a, 1)
	}
	return out
}
