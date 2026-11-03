package formdecision

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestOptionsOf normalizes a decision's list as form-js normalizes a select's options,
// so what the server checks a submission against is what the person was shown.
func TestOptionsOf(t *testing.T) {
	got, skipped, err := OptionsOf([]any{
		"rot",
		"",
		nil,
		json.Number("12.5"),
		float64(3),
		true,
		false,
		map[string]any{"value": "gr", "label": "grün"},
		map[string]any{"value": "bl"},
		map[string]any{"label": "ohne Wert"},
		map[string]any{"value": map[string]any{"id": 7}, "label": "Objekt"},
		map[string]any{"value": map[string]any{}, "label": "leeres Objekt"},
		map[string]any{"value": map[string]any{"id": 8}},
		[]any{"verschachtelt"},
	})
	if err != nil {
		t.Fatalf("OptionsOf: %v", err)
	}
	want := []Option{
		{Label: "rot", Value: "rot"},
		{Label: "12.5", Value: json.Number("12.5")},
		{Label: "3", Value: float64(3)},
		{Label: "true", Value: true},
		{Label: "false", Value: false},
		{Label: "grün", Value: "gr"},
		{Label: "bl", Value: "bl"},
		{Label: "Objekt", Value: map[string]any{"id": 7}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("OptionsOf =\n%#v\nwant\n%#v", got, want)
	}
	if skipped != 6 {
		t.Errorf("skipped = %d; want 6", skipped)
	}
}

func TestOptionsOfRefuses(t *testing.T) {
	if _, _, err := OptionsOf("rot"); err == nil || !strings.Contains(err.Error(), "not a list") {
		t.Errorf("a string result: %v", err)
	}
	if _, _, err := OptionsOf(nil); err == nil || !strings.Contains(err.Error(), "null") {
		t.Errorf("a null result: %v", err)
	}
	if _, _, err := OptionsOf(map[string]any{"a": 1}); err == nil || !strings.Contains(err.Error(), "context") {
		t.Errorf("a context result: %v", err)
	}
	for v, want := range map[any]string{true: "a boolean", json.Number("3"): "a number", 1: "a int"} {
		if _, _, err := OptionsOf(v); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("OptionsOf(%#v) = %v; want it called %s", v, err, want)
		}
	}
	long := make([]any, MaxOptions+1)
	for i := range long {
		long[i] = "x"
	}
	if _, _, err := OptionsOf(long); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Errorf("a list over the cap: %v", err)
	}
	if got, _, err := OptionsOf([]any{}); err != nil || got == nil || len(got) != 0 {
		t.Errorf("an empty list is an empty set of options, not nil: %#v, %v", got, err)
	}
}

func TestOffers(t *testing.T) {
	opts := []Option{{Label: "a", Value: "a"}, {Label: "n", Value: json.Number("2")}, {Label: "o", Value: map[string]any{"id": 7}}, {Label: "t", Value: true}}
	for _, v := range []any{"a", json.Number("2"), float64(2), map[string]any{"id": json.Number("7")}, true} {
		if !offers(opts, v) {
			t.Errorf("offers(%#v) = false; want true", v)
		}
	}
	for _, v := range []any{"b", "2", json.Number("3"), map[string]any{"id": 8}, false, []any{"a"}} {
		if offers(opts, v) {
			t.Errorf("offers(%#v) = true; want false", v)
		}
	}
}
