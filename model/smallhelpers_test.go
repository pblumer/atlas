package model

import "testing"

// EncipheredSubject reads the data subject a sealed value names, and nothing from a
// value that is not sealed or not well formed.
func TestEncipheredSubject(t *testing.T) {
	for _, tc := range []struct {
		text, want string
		ok         bool
	}{
		{`{"atlas:personal":{"subject":"usr_1","ct":"x"}}`, "usr_1", true},
		{`{"atlas:personal":{"subject":""}}`, "", false},
		{`{"atlas:personal":`, "", false},
		{`{"atlas:personal":{"subject":"a"},"other":{}}`, "", false},
		{`"plain"`, "", false},
	} {
		if got, ok := EncipheredSubject(tc.text); got != tc.want || ok != tc.ok {
			t.Errorf("EncipheredSubject(%s) = %q, %v; want %q, %v", tc.text, got, ok, tc.want, tc.ok)
		}
	}
}

// The non-buffering records carry the message without its variables, and the feed
// retention record names its own value type.
func TestMessageWithoutPayloadAndFeedRetentionType(t *testing.T) {
	m := MessageValue{MessageName: "order", Variables: []VariableValue{{Name: "x"}}}
	if got := m.WithoutPayload(); got.Variables != nil || got.MessageName != "order" {
		t.Errorf("WithoutPayload = %+v", got)
	}
	if len(m.Variables) != 1 {
		t.Error("WithoutPayload changed the original")
	}
	if (&FeedRetentionValue{}).ValueType() != VTFeedRetention {
		t.Error("FeedRetentionValue.ValueType is not VTFeedRetention")
	}
}
