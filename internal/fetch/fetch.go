// Package fetch retrieves an HTTP(S) resource and reduces it to plain text.
package fetch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const (
	DefaultMaxBytes = 1 << 20 // 1 MiB
	defaultTimeout  = 30 * time.Second
)

// Get fetches url and returns the response body capped at maxBytes (0 = default).
func Get(ctx context.Context, url string, maxBytes int64) ([]byte, string, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	client := &http.Client{Timeout: defaultTimeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "beans-preserver/0.1 (+local mcp)")
	req.Header.Set("Accept", "text/html,text/plain,application/xhtml+xml,*/*;q=0.5")
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, "", fmt.Errorf("http %d %s", resp.StatusCode, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(body)) > maxBytes {
		body = body[:maxBytes]
	}
	return body, resp.Header.Get("Content-Type"), nil
}

// HTMLToText extracts visible text from an HTML document, dropping script/style/nav
// blocks and collapsing whitespace. Returns the raw input unchanged if it doesn't
// parse as HTML — useful for plain-text endpoints.
func HTMLToText(htmlBody []byte) string {
	doc, err := html.Parse(strings.NewReader(string(htmlBody)))
	if err != nil {
		return string(htmlBody)
	}
	var b strings.Builder
	skip := map[string]bool{"script": true, "style": true, "noscript": true, "svg": true}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && skip[n.Data] {
			return
		}
		if n.Type == html.TextNode {
			t := strings.TrimSpace(n.Data)
			if t != "" {
				b.WriteString(t)
				b.WriteByte('\n')
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	out := b.String()
	// Collapse runs of blank lines.
	for strings.Contains(out, "\n\n\n") {
		out = strings.ReplaceAll(out, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(out)
}

// LooksHTML returns true if the content-type header or the body itself suggests HTML.
func LooksHTML(contentType string, body []byte) bool {
	if strings.Contains(strings.ToLower(contentType), "html") {
		return true
	}
	prefix := body
	if len(prefix) > 512 {
		prefix = prefix[:512]
	}
	low := strings.ToLower(string(prefix))
	return strings.Contains(low, "<html") || strings.Contains(low, "<!doctype html")
}
