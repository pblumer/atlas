package mimimport

import (
	"encoding/xml"
	"testing"
)

// The three helpers below carry the importer's knowledge of how MIM writes a
// serialised .NET collection and how a name is qualified. Each has a documented
// answer for input the happy path never produces — a <x:Key> that is plain text
// rather than a typed value, an element that is not a property at all, a
// namespace nothing bound — and those answers are what these tests pin. They
// were written after the code rather than before it (AGENTS.md § Testing
// conventions calls for the reverse); the honest reason is that they close a
// coverage gap the importer arrived with, and the behaviour they describe is
// the behaviour that was already there.

func TestEntryKey(t *testing.T) {
	tests := []struct {
		name string
		xml  string
		want string
	}{
		{
			// How MIMWAL writes one: the key is itself a typed value.
			name: "typed key",
			xml:  `<Item><Key><String>0:1</String></Key><Value>x</Value></Item>`,
			want: "0:1",
		},
		{
			// A <Key> whose text is the key itself, with no typed child.
			name: "plain text key",
			xml:  `<Item><Key>Attribute</Key><Value>x</Value></Item>`,
			want: "Attribute",
		},
		{
			// A typed child that holds only whitespace is not the key; the
			// next one that holds something is.
			name: "empty typed child is skipped",
			xml:  `<Item><Key><String>   </String><String>b</String></Key></Item>`,
			want: "b",
		},
		{
			// An entry with no <Key> at all — the loop has to walk past its
			// other children and report nothing.
			name: "no key element",
			xml:  `<Item><Value>x</Value></Item>`,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var n xnode
			if err := xml.Unmarshal([]byte(tt.xml), &n); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got := entryKey(n); got != tt.want {
				t.Errorf("entryKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPropertyName(t *testing.T) {
	tests := []struct {
		local string
		want  string
		ok    bool
	}{
		{local: "UpdateResourceActivity.ActorId", want: "ActorId", ok: true},
		{local: "A.B.C", want: "C", ok: true},
		// Not a property element: no dot, so there is nothing to split.
		{local: "SequenceActivity", want: "", ok: false},
		// A trailing dot names no property either, and must not yield "".
		{local: "UpdateResourceActivity.", want: "", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.local, func(t *testing.T) {
			got, ok := propertyName(tt.local)
			if got != tt.want || ok != tt.ok {
				t.Errorf("propertyName(%q) = (%q, %v), want (%q, %v)", tt.local, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestQName(t *testing.T) {
	tbl := &nsTable{prefix: map[string]string{
		"http://schemas.microsoft.com/winfx/2006/xaml": "x",
		"http://example.test/default":                  "", // bound as the default namespace
	}}

	tests := []struct {
		name  string
		space string
		local string
		want  string
	}{
		// An unqualified name stays unqualified — also the right answer for an
		// attribute, which a default namespace never applies to.
		{name: "no namespace", space: "", local: "Key", want: "Key"},
		{name: "bound to a prefix", space: "http://schemas.microsoft.com/winfx/2006/xaml", local: "Key", want: "x:Key"},
		// Bound as the default namespace: the name carries no prefix.
		{name: "bound as default", space: "http://example.test/default", local: "Item", want: "Item"},
		// Nothing bound this namespace, so there is no prefix to render with.
		{name: "unbound namespace", space: "http://example.test/unbound", local: "Item", want: "Item"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tbl.qname(tt.space, tt.local); got != tt.want {
				t.Errorf("qname(%q, %q) = %q, want %q", tt.space, tt.local, got, tt.want)
			}
		})
	}
}
