package entra

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
)

// What a delta query refuses rather than answers.
//
// A change-tracking read is the one Graph call whose *failure modes* matter as much as
// its happy path, because its answer is consumed as "everything that changed" and its
// cursor is persisted for the next run. A page this worker misreads does not produce
// a visible error later — it produces a short change set a process acts on confidently,
// and a cursor that resumes from the wrong place. So each refusal below is a real
// outcome an operator can meet, and each says which of the two it is protecting.

// A transport failure mid-listing is the job's failure, not an empty change set. The
// job stays pending, is retried, and the cursor is never advanced past changes that
// were never read.
func TestDeltaReportsTheCallThatFailed(t *testing.T) {
	boom := errors.New("graph is unreachable")
	_, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "delta-users", ResultVariable: "changed",
	}, regWith("contoso", &pagingClient{err: boom}))
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want the transport failure itself", err)
	}
}

// A page whose shape is not the collection Graph documents is refused by name. Reading
// one of these as "no changes" is the failure that matters: it is indistinguishable, to
// the process, from a directory where nothing happened.
func TestDeltaRefusesAMalformedPage(t *testing.T) {
	cases := map[string]struct {
		page any
		want string
	}{
		"not an object":       {page: []any{"users"}, want: "expected a collection"},
		"no value":            {page: map[string]any{"@odata.deltaLink": "https://g/d"}, want: `carries no "value"`},
		"value is not a list": {page: map[string]any{"value": "u1"}, want: `that is a string`},
		"nextLink is not a URL": {
			page: map[string]any{"value": []any{}, "@odata.nextLink": 42},
			want: "@odata.nextLink that is a",
		},
		"deltaLink is not a URL": {
			page: map[string]any{"value": []any{}, "@odata.deltaLink": true},
			want: "@odata.deltaLink that is a",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Run(context.Background(), Job{
				Connector: "contoso", Operation: "delta-users", ResultVariable: "changed",
			}, regWith("contoso", &pagingClient{pages: []any{tc.page}}))
			if err == nil {
				t.Fatalf("a %s page was accepted", name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to name %q", err, tc.want)
			}
		})
	}
}

// A server that offers another page forever stops being followed, and the job fails
// rather than the worker looping until its lease elapses. The bound is the same one a
// listing has; what a delta adds is that the run also never persists a cursor.
func TestADeltaThatNeverEndsFailsRatherThanLoopingForever(t *testing.T) {
	c := &pagingClient{always: deltaResp("https://graph.microsoft.com/v1.0/users/delta?$skiptoken=A", "", "u1")}
	_, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "delta-users", ResultVariable: "changed",
	}, regWith("contoso", c))
	if err == nil {
		t.Fatal("an endless delta was followed to the end of time")
	}
	if !strings.Contains(err.Error(), "still offered another page") {
		t.Errorf("error = %v, want it to name the page bound", err)
	}
	if len(c.paths) != maxListPages {
		t.Errorf("followed %d pages, want the %d-page bound", len(c.paths), maxListPages)
	}
}

// Inline attributes are a FEEL context compiled from the modeler's JSON template. What
// Graph takes is an object; anything else is refused here rather than sent, because on
// the far side it is a 400 with no hint of which task authored it.
func TestInlineAttributesThatAreNotAnObjectAreRefused(t *testing.T) {
	cases := map[string]struct {
		src  string
		want string
	}{
		"a plain string": {src: `"just a name"`, want: "not a JSON object"},
		"a list":         {src: `["a","b"]`, want: "not a JSON object"},
		"null":           {src: `null`, want: "not a JSON object"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e, err := expr.CompileAuto(tc.src)
			if err != nil {
				t.Fatalf("compile %q: %v", tc.src, err)
			}
			if _, err := evalAttributes(compiler.RestExpr{Expr: e}, 1, nil); err == nil {
				t.Fatalf("%s was accepted as directory attributes", name)
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to say %q", err, tc.want)
			}
		})
	}
}

// checkRequired repeats the compiler's shape rules on the worker, for a job built by
// hand or by an older engine. A reset-password with no password is the one that would
// otherwise reach Graph as a PATCH with an empty passwordProfile.
func TestAResetPasswordWithNoPasswordIsRefused(t *testing.T) {
	err := checkRequired(Job{Operation: "reset-password", UserID: "u1"}, Ops["reset-password"])
	if err == nil || !strings.Contains(err.Error(), "newPassword") {
		t.Fatalf("error = %v, want one naming the missing newPassword", err)
	}
}

// An expression that reads no variable needs no binding, and toExprKind maps every
// stored kind onto the FEEL kind it binds as — including the fallthrough, which is what
// an unknown or unset variable kind must not silently become something else.
func TestBindingTheScopeIntoFEEL(t *testing.T) {
	if got := bindVars(1, nil, nil); got != nil {
		t.Errorf("bindVars with no names = %v, want nil", got)
	}
	for in, want := range map[model.VarKind]expr.ValueKind{
		model.VarBool:     expr.KindBool,
		model.VarNumber:   expr.KindNumber,
		model.VarString:   expr.KindString,
		model.VarJSON:     expr.KindJSON,
		model.VarNull:     expr.KindNull,
		model.VarKind(99): expr.KindNull,
	} {
		if got := toExprKind(in); got != want {
			t.Errorf("toExprKind(%v) = %v, want %v", in, got, want)
		}
	}
}
