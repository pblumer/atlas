package limits

import (
	"reflect"
	"strconv"
	"testing"
)

// TestDefaultsAreTheNumbersTheCodeAlreadyHad pins that gathering the budgets into
// one place did not quietly change any of them. Every value here was read off the
// constant it replaced; if one of them moves, that is a decision about how much a
// caller may make this server hold, and it should read as one in the diff.
func TestDefaultsAreTheNumbersTheCodeAlreadyHad(t *testing.T) {
	l := Default()
	for _, tc := range []struct {
		name string
		got  int64
		want int64
	}{
		{"ErrorBody", l.ErrorBody, 4 << 10},
		{"Theme", l.Theme, 4 << 10},
		{"Registration", l.Registration, 16 << 10},
		{"Request", l.Request, 64 << 10},
		{"Settings", l.Settings, 256 << 10},
		{"Asset", l.Asset, 512 << 10},
		{"Definition", l.Definition, 1 << 20},
		{"Generated", l.Generated, 2 << 20},
		{"ModelUpload", l.ModelUpload, 4 << 20},
		{"Payload", l.Payload, 8 << 20},
		{"DataUpload", l.DataUpload, 16 << 20},
		{"Import", l.Import, 24 << 20},
		{"AppBundle", l.AppBundle, 32 << 20},
		{"Archive", l.Archive, 1 << 30},
		{"TokenSteps", int64(l.TokenSteps), 10_000},
		{"Iterations", int64(l.Iterations), 100_000},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %d, want %d", tc.name, tc.got, tc.want)
		}
	}
}

// TestEveryBudgetHasADefault is the completeness half: not "is the list right" but
// "does anything unbudgeted exist". A field added to Limits and forgotten in Default
// would be a ceiling of zero, which admits nothing and would look like a bug
// somewhere else entirely.
func TestEveryBudgetHasADefault(t *testing.T) {
	v := reflect.ValueOf(Default())
	for i := range v.NumField() {
		if v.Field(i).Int() <= 0 {
			t.Errorf("%s has no default; a budget of zero admits nothing", v.Type().Field(i).Name)
		}
	}
}

// TestNamesFollowsTheStruct: Names is derived, not maintained. This fails if it ever
// goes back to being a hand-written list that can fall behind.
func TestNamesFollowsTheStruct(t *testing.T) {
	got, want := Names(), reflect.TypeOf(Limits{}).NumField()
	if len(got) != want {
		t.Fatalf("Names lists %d budgets, the struct has %d", len(got), want)
	}
	for i, name := range got {
		if field := reflect.TypeOf(Limits{}).Field(i).Name; name != field {
			t.Errorf("Names[%d] = %q, want %q", i, name, field)
		}
	}
}

func TestEnvVarSpelling(t *testing.T) {
	for _, tc := range []struct{ field, want string }{
		{"ModelUpload", "ATLAS_LIMIT_MODEL_UPLOAD"},
		{"Archive", "ATLAS_LIMIT_ARCHIVE"},
		{"TokenSteps", "ATLAS_LIMIT_TOKEN_STEPS"},
	} {
		if got := EnvVar(tc.field); got != tc.want {
			t.Errorf("EnvVar(%q) = %q, want %q", tc.field, got, tc.want)
		}
	}
}

// TestEveryBudgetIsConfigurable walks the struct and sets each budget through its
// own variable. "One way to configure them" is only true if it reaches all of them,
// and a per-field test would be a list that drifts.
func TestEveryBudgetIsConfigurable(t *testing.T) {
	for i, name := range Names() {
		t.Run(name, func(t *testing.T) {
			const want = 4242
			l, bad := fromEnviron(func(key string) string {
				if key == EnvVar(name) {
					return strconv.Itoa(want)
				}
				return ""
			})
			if len(bad) != 0 {
				t.Fatalf("setting %s reported %v", EnvVar(name), bad)
			}
			if got := reflect.ValueOf(l).Field(i).Int(); got != want {
				t.Errorf("%s = %d after setting %s, want %d", name, got, EnvVar(name), want)
			}
		})
	}
}

// TestAMalformedKnobKeepsTheCeiling is the point of the whole package. A typo in a
// deployment's environment must not remove a budget: "off" is precisely the state
// these exist to prevent, so every rejected value leaves the default standing and
// says so out loud.
func TestAMalformedKnobKeepsTheCeiling(t *testing.T) {
	for _, raw := range []string{"nonsense", "0", "-1", "4 MiB", "1e6", "9223372036854775808"} {
		t.Run(raw, func(t *testing.T) {
			l, bad := fromEnviron(func(key string) string {
				if key == EnvVar("ModelUpload") {
					return raw
				}
				return ""
			})
			if l.ModelUpload != Default().ModelUpload {
				t.Errorf("ModelUpload = %d after %q, want the default %d", l.ModelUpload, raw, Default().ModelUpload)
			}
			if len(bad) != 1 {
				t.Fatalf("reported %v, want exactly one complaint about %q", bad, raw)
			}
		})
	}
}

// TestACountBudgetRefusesWhatItCannotHold: TokenSteps and Iterations are counted in
// int32. A value past that must keep the default and say so, not wrap round to
// something small — a budget that silently became 1 would stop every process in the
// installation, and the environment would look like it had asked for the opposite.
func TestACountBudgetRefusesWhatItCannotHold(t *testing.T) {
	const tooBig = "4294967296" // 2^32, past int32
	l, bad := fromEnviron(func(key string) string {
		if key == EnvVar("TokenSteps") {
			return tooBig
		}
		return ""
	})
	if l.TokenSteps != Default().TokenSteps {
		t.Errorf("TokenSteps = %d, want the default %d", l.TokenSteps, Default().TokenSteps)
	}
	if len(bad) != 1 {
		t.Fatalf("reported %v, want one complaint", bad)
	}
}

// TestFromEnvReadsTheProcessEnvironment covers the seam fromEnviron is injected
// through, so the exported entry point is not the one part nothing exercises.
func TestFromEnvReadsTheProcessEnvironment(t *testing.T) {
	t.Setenv(EnvVar("Request"), "1234")
	l, bad := FromEnv()
	if len(bad) != 0 {
		t.Fatalf("FromEnv reported %v", bad)
	}
	if l.Request != 1234 {
		t.Errorf("Request = %d, want 1234 from the environment", l.Request)
	}
}
