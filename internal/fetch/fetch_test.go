package fetch

import "testing"

func TestHTMLToText_StripsScriptStyle(t *testing.T) {
	html := []byte(`<html><head><style>.x{color:red}</style><script>alert(1)</script></head>
<body><p>Hello world</p><div>Foo <span>bar</span></div></body></html>`)
	out := HTMLToText(html)
	if want := "Hello world"; !contains(out, want) {
		t.Errorf("expected %q in output, got %q", want, out)
	}
	if contains(out, "alert(1)") || contains(out, "color:red") {
		t.Errorf("script/style leaked: %q", out)
	}
}

func TestHTMLToText_PlainTextPassthrough(t *testing.T) {
	in := []byte("just plain text, no tags here.\nsecond line.")
	out := HTMLToText(in)
	// Either returned unchanged (parse failed) or the parser added <html><body>
	// wrapping but the visible text survives.
	if !contains(out, "just plain text") || !contains(out, "second line") {
		t.Errorf("plain text lost: %q", out)
	}
}

func TestLooksHTML(t *testing.T) {
	cases := []struct {
		name string
		ct   string
		body string
		want bool
	}{
		{"content-type html", "text/html; charset=utf-8", "<p>x</p>", true},
		{"doctype sniff", "application/octet-stream", "<!DOCTYPE html><html>", true},
		{"plain", "text/plain", "this is plain", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := LooksHTML(c.ct, []byte(c.body)); got != c.want {
				t.Errorf("LooksHTML(%q,%q)=%v, want %v", c.ct, c.body, got, c.want)
			}
		})
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
