package compiler

import "testing"

// UntriggeredStartAmbiguous is the question ADR-0426 asks of every create nobody
// triggered: does the process have an answer to where it begins? It has one while it
// has a none start, or only one start of any kind; it has none once several
// triggers stand side by side with nothing to press by hand.
func TestUntriggeredStartAmbiguous(t *testing.T) {
	cases := []struct {
		name  string
		build func(b *Builder)
		want  bool
	}{
		{"one none start", func(b *Builder) { b.AddStartEvent() }, false},
		{"one message start", func(b *Builder) { b.AddMessageStartEvent("m", nil, false) }, false},
		{"two none starts", func(b *Builder) { b.AddStartEvent(); b.AddStartEvent() }, false},
		{"none start beside a message start", func(b *Builder) {
			b.AddStartEvent()
			b.AddMessageStartEvent("m", nil, false)
		}, false},
		{"two message starts", func(b *Builder) {
			b.AddMessageStartEvent("provision", nil, false)
			b.AddMessageStartEvent("deprovision", nil, false)
		}, true},
		{"a message start beside a signal start", func(b *Builder) {
			b.AddMessageStartEvent("m", nil, false)
			b.AddSignalStartEvent("s")
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := NewBuilder(1, "p", 1)
			tc.build(b)
			// Every start needs somewhere to go for the process to build.
			for _, s := range b.startEventsForTest() {
				b.Connect(s, b.AddEndEvent())
			}
			cp, err := b.Build()
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if got := cp.UntriggeredStartAmbiguous(); got != tc.want {
				t.Errorf("UntriggeredStartAmbiguous() = %v, want %v", got, tc.want)
			}
		})
	}
}

// startEventsForTest lists the start events added so far, so the table above can
// connect each without naming them.
func (b *Builder) startEventsForTest() []int32 {
	var out []int32
	for i := range b.nodes {
		if isStartEvent(b.nodes[i].Type) {
			out = append(out, int32(i))
		}
	}
	return out
}
