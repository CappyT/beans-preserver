package tools

import (
	"strings"
	"testing"
)

func TestSplitMeta_Happy(t *testing.T) {
	in := "filtered body line 1\nfiltered body line 2\n<<META>>{\"confidence\": 0.85, \"notes\": \"kept errors only\"}"
	body, m := splitMeta(in)
	if body != "filtered body line 1\nfiltered body line 2" {
		t.Errorf("body mismatch: %q", body)
	}
	if m.Confidence != 0.85 {
		t.Errorf("confidence: got %v, want 0.85", m.Confidence)
	}
	if !strings.Contains(m.Notes, "errors") {
		t.Errorf("notes lost: %q", m.Notes)
	}
}

func TestSplitMeta_NoMeta(t *testing.T) {
	in := "  body without trailing meta  "
	body, m := splitMeta(in)
	if body != "body without trailing meta" {
		t.Errorf("body mismatch: %q", body)
	}
	if m.Confidence != 0.5 {
		t.Errorf("expected neutral 0.5 confidence, got %v", m.Confidence)
	}
}

func TestSplitMeta_FencedJSON(t *testing.T) {
	// Some models wrap the meta blob in ```json ... ```. The parser strips it.
	in := "result body\n<<META>>```json\n{\"confidence\": 0.9, \"notes\": \"ok\"}\n```"
	_, m := splitMeta(in)
	if m.Confidence != 0.9 {
		t.Errorf("confidence: got %v", m.Confidence)
	}
}

func TestSplitMeta_Clamp(t *testing.T) {
	in := "x\n<<META>>{\"confidence\": 1.7, \"notes\": \"\"}"
	_, m := splitMeta(in)
	if m.Confidence != 1.0 {
		t.Errorf("expected clamp to 1.0, got %v", m.Confidence)
	}
	in = "x\n<<META>>{\"confidence\": -0.4, \"notes\": \"\"}"
	_, m = splitMeta(in)
	if m.Confidence != 0.0 {
		t.Errorf("expected clamp to 0.0, got %v", m.Confidence)
	}
}

func TestSplitMeta_MalformedJSON(t *testing.T) {
	in := "body\n<<META>>{not valid json}"
	body, m := splitMeta(in)
	if body != "body" {
		t.Errorf("body mismatch: %q", body)
	}
	if m.Confidence != 0.5 {
		t.Errorf("expected neutral 0.5, got %v", m.Confidence)
	}
}
