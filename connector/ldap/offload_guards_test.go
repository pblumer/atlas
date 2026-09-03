package ldap

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The refusals on the offload seam. None of them is a defensive nicety: each is a
// state with a right answer and a much worse wrong one, and the wrong one is
// reached in whichever process runs the job — so they are checked in Resolve and Run
// rather than in either half's own wrapper.

// A task with no compiled detail is a payload arm asking for a kind this node is not.
// An error rather than a zero Job, because a zero Job carries an empty url and would
// be reported as "search has an empty url" — sending whoever reads it to look at the
// model instead of at the code that resolved it.
func TestResolveRefusesATaskWithNoDetail(t *testing.T) {
	_, err := Resolve(nil, nil, nil, nil, 1)
	if err == nil {
		t.Fatal("a task with no detail resolved to a job")
	}
	if !strings.Contains(err.Error(), "no detail") {
		t.Errorf("error = %v, want it to say the task carries no detail", err)
	}
}

// An empty url is checked in Run, not in Resolve, so a FEEL url that evaluated to
// nothing fails the job identically whichever process runs it — and before a dial is
// attempted against "".
func TestRunRefusesAnEmptyURL(t *testing.T) {
	dialer := &countingDialer{}
	_, err := Run(context.Background(), Job{Operation: "search"}, dialer, nil)
	if err == nil {
		t.Fatal("a job with no url was accepted")
	}
	if dialer.dials != 0 {
		t.Errorf("dials = %d, want none: there was nothing to dial", dialer.dials)
	}
}

// The three refusals inside the operation switch. Each one would otherwise send a
// request that means something other than what the model said.
func TestRunRefusesOperationsItCannotPerform(t *testing.T) {
	for _, tc := range []struct {
		name string
		job  Job
		want string
	}{
		{
			// A modify with nothing to change is almost always an entryVariable that
			// resolved to nothing. Sending it would be an LDAP request with an empty
			// change list — accepted by some servers as a no-op, so the process would
			// carry on believing it had written.
			name: "modify with no attributes",
			job:  Job{URL: "ldap://dc", Operation: "modify", DN: "cn=a"},
			want: "no attributes",
		},
		{
			// An empty new password is the same mistake with a worse outcome: some
			// directories accept it and leave the account with no password at all.
			name: "modify-password with an empty password",
			job:  Job{URL: "ldap://dc", Operation: "modify-password", DN: "cn=a"},
			want: "newPassword",
		},
		{
			// An operation the compiler would never emit means the payload and this
			// switch disagree. Naming it is what turns that into a fixable report
			// rather than a job that silently does nothing.
			name: "an operation this switch does not know",
			job:  Job{URL: "ldap://dc", Operation: "reticulate-splines"},
			want: "reticulate-splines",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Run(context.Background(), tc.job, &countingDialer{}, nil)
			if err == nil {
				t.Fatalf("%s was accepted", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// A search whose connection fails is reported rather than completed with an empty
// entry list. The difference matters: a model that reads "no entries" concludes the
// person is not in the directory, which is the opposite of "the directory could not
// be asked".
func TestRunReportsASearchThatFails(t *testing.T) {
	dialer := &failingOpDialer{err: errors.New("connection reset")}
	_, err := Run(context.Background(), Job{
		URL: "ldap://dc", Operation: "search", BaseDN: "dc=example,dc=com", ResultVariable: "found",
	}, dialer, nil)
	if err == nil {
		t.Fatal("a failed search completed as if the directory were empty")
	}
	if !strings.Contains(err.Error(), "connection reset") {
		t.Errorf("error = %v, want the directory's own failure", err)
	}
}

// failingOpDialer dials fine and then fails every operation, which is the shape of a
// directory that accepted the bind and then went away.
type failingOpDialer struct{ err error }

func (d *failingOpDialer) Dial(DialOptions) (Conn, error) { return &poolFakeConn{opError: d.err}, nil }
