package scim

import (
	"context"
	"strings"
	"testing"
)

// The refusals on the offload seam, tested here rather than only through the worker
// half: a package's coverage counts its own tests, and these branches are the reason
// the derivation lives in Run at all.

// A task with no compiled detail is a payload arm asking for a kind this node is not.
// An error rather than a zero Job, because a zero Job would be refused later as
// "needs a base url and resource" — sending whoever reads it to look at the model
// when the fault is in the code that resolved it.
func TestResolveRefusesATaskWithNoDetail(t *testing.T) {
	_, err := Resolve(nil, nil, nil, nil, 1, 2)
	if err == nil {
		t.Fatal("a task with no detail resolved to a job")
	}
	if !strings.Contains(err.Error(), "no detail") {
		t.Errorf("error = %v, want it to say the task carries no detail", err)
	}
}

// The derivation from operation + operands to a URL has two refusals, and both are
// in Run so that either half fails the job the same way. Neither is cosmetic: the
// alternative to each is a well-formed request that means something else.
func TestRunRefusesWhatItCannotAddress(t *testing.T) {
	for _, tc := range []struct {
		name string
		job  Job
		want string
	}{
		{
			// Without an id, "get one user" becomes "list every user": a request the
			// provider answers happily, with a payload the model would then read as
			// the one resource it asked for.
			name: "a get with no resource id",
			job:  Job{Operation: "get", BaseURL: "https://idp.example.com/scim/v2", Resource: "Users"},
			want: "resource id",
		},
		{
			// A FEEL base url or resource that evaluated to nothing would otherwise
			// produce a request to a path built from the empty string.
			name: "a resource with no base url",
			job:  Job{Operation: "search", Resource: "Users"},
			want: "base url and resource",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			client := clientFunc(func(context.Context, Request) (Response, error) {
				called = true
				return Response{}, nil
			})
			if _, err := Run(context.Background(), tc.job, client, nil); err == nil {
				t.Fatalf("%s was accepted", tc.name)
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
			if called {
				t.Error("the provider was called anyway; there was no address to call")
			}
		})
	}
}

// clientFunc adapts a function to [Client], so a case that must never reach the
// provider can prove it.
type clientFunc func(context.Context, Request) (Response, error)

func (f clientFunc) Do(ctx context.Context, r Request) (Response, error) { return f(ctx, r) }
