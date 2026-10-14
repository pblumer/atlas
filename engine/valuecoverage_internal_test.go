package engine

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Every value type the log can carry has to survive recovery.
//
// A record's payload reaches applyToState through two switches in value.go: one
// hands the fold a pointer to the right field of the inflight value, the other
// copies a decoded record into that field on replay. Miss either and the fact is
// written live, persisted correctly, and then rebuilt as a zero — the state after
// replay no longer equals the state built live, which is the one property every
// other guarantee in Atlas rests on (I4).
//
// It is silent, too. Nothing fails to compile, nothing errors at runtime: the
// entitlement that found this was granted, logged, read back, and came out of
// recovery as an empty record. The only sign was a test that restarted the engine.
//
// So the sets are compared rather than remembered. model.newValue is the list of
// value types that *have* a payload; both switches here must carry all of them.

var (
	valueCase = regexp.MustCompile(`case model\.(VT\w+):`)
	modelCase = regexp.MustCompile(`case (VT\w+):`)
)

// casesIn returns the value types named by the case labels of one function, found
// by name in a file's source. The name is matched as a declaration, with or
// without a receiver, so a method and a plain function are both reachable.
func casesIn(t *testing.T, path, fn string, re *regexp.Regexp) map[string]bool {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	decl := regexp.MustCompile(`(?m)^func (\([^)]*\) )?` + regexp.QuoteMeta(fn) + `\b`)
	loc := decl.FindStringIndex(string(src))
	if loc == nil {
		t.Fatalf("%s has no %s; this test now checks nothing and says so instead", path, fn)
	}
	body := string(src)[loc[0]:]
	if end := strings.Index(body, "\n}\n"); end > 0 {
		body = body[:end]
	}
	out := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(body, -1) {
		out[m[1]] = true
	}
	if len(out) == 0 {
		t.Fatalf("%s: %s names no value types; the pattern has gone stale", path, fn)
	}
	return out
}

func TestEveryPayloadValueTypeSurvivesRecovery(t *testing.T) {
	// The authority: a value type with a payload is one model can build.
	want := casesIn(t, "../model/value.go", "newValue", modelCase)
	if len(want) < 15 {
		t.Fatalf("only %d value types with a payload found; the walk or the pattern "+
			"has gone stale, and a green result here would mean nothing", len(want))
	}

	for _, fn := range []string{"asValue", "inflightFromRecord"} {
		got := casesIn(t, "value.go", fn, valueCase)
		var missing []string
		for vt := range want {
			if !got[vt] {
				missing = append(missing, vt)
			}
		}
		if len(missing) > 0 {
			t.Errorf("%s does not carry %v.\n"+
				"A payload it cannot carry is written live and rebuilt as a zero on "+
				"replay: the state after recovery stops equalling the state built "+
				"live, silently, and nothing but a restart shows it.", fn, missing)
		}
	}
}
