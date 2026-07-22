package dmn

import "testing"

// TestDecodeInputs exercises decodeInputs directly, including the empty-string
// shortcut (no inputs recorded → nil map) and the JSON-error path.
func TestDecodeInputs(t *testing.T) {
	t.Run("empty yields nil map", func(t *testing.T) {
		m, err := decodeInputs("")
		if err != nil {
			t.Fatalf("decodeInputs(\"\"): %v", err)
		}
		if m != nil {
			t.Fatalf("decodeInputs(\"\") = %v, want nil map", m)
		}
	})

	t.Run("valid JSON decodes", func(t *testing.T) {
		m, err := decodeInputs(`{"Season":"Winter","Guests":8}`)
		if err != nil {
			t.Fatalf("decodeInputs valid: %v", err)
		}
		if m["Season"] != "Winter" {
			t.Errorf("Season = %v, want Winter", m["Season"])
		}
	})

	t.Run("malformed JSON errors", func(t *testing.T) {
		if _, err := decodeInputs(`{not json`); err == nil {
			t.Fatal("decodeInputs of malformed JSON: got nil error, want an error")
		}
	})
}
