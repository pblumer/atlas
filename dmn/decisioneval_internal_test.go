package dmn

import "testing"

// TestJsonObject covers jsonObject's three outcomes: an empty context renders as an
// empty JSON object, a populated one as its canonical JSON, and a value with no JSON
// image degrades to "{}" rather than producing an invalid record (ADR-0066).
func TestJsonObject(t *testing.T) {
	if got := JSONObject(nil); got != "{}" {
		t.Errorf("JSONObject(nil) = %q, want {}", got)
	}
	if got := JSONObject(map[string]any{}); got != "{}" {
		t.Errorf("JSONObject(empty) = %q, want {}", got)
	}
	if got := JSONObject(map[string]any{"Season": "Winter"}); got != `{"Season":"Winter"}` {
		t.Errorf("JSONObject(one) = %q, want {\"Season\":\"Winter\"}", got)
	}
	// A channel has no JSON image, so json.Marshal fails and jsonObject degrades.
	if got := JSONObject(map[string]any{"bad": make(chan int)}); got != "{}" {
		t.Errorf("JSONObject(unmarshalable) = %q, want {} (degraded)", got)
	}
}
