package dmn

import "testing"

// TestJsonObject covers jsonObject's three outcomes: an empty context renders as an
// empty JSON object, a populated one as its canonical JSON, and a value with no JSON
// image degrades to "{}" rather than producing an invalid record (ADR-0064).
func TestJsonObject(t *testing.T) {
	if got := jsonObject(nil); got != "{}" {
		t.Errorf("jsonObject(nil) = %q, want {}", got)
	}
	if got := jsonObject(map[string]any{}); got != "{}" {
		t.Errorf("jsonObject(empty) = %q, want {}", got)
	}
	if got := jsonObject(map[string]any{"Season": "Winter"}); got != `{"Season":"Winter"}` {
		t.Errorf("jsonObject(one) = %q, want {\"Season\":\"Winter\"}", got)
	}
	// A channel has no JSON image, so json.Marshal fails and jsonObject degrades.
	if got := jsonObject(map[string]any{"bad": make(chan int)}); got != "{}" {
		t.Errorf("jsonObject(unmarshalable) = %q, want {} (degraded)", got)
	}
}
