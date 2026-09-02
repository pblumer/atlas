package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/sqldb"
)

// probeCalls records what a fake probe was asked to reach, so a test can assert that
// the *resolved* connection string got there — the half of this that no HTTP status
// would show.
type probeCalls struct {
	product string
	dsn     string
	err     error
}

func (p *probeCalls) probe(_ context.Context, product sqldb.Product, dsn string) error {
	p.product, p.dsn = product.Name, dsn
	return p.err
}

// The point of the check, for a database: it runs against what is *typed*, before
// anything is saved. A connection string is a SQL connector's whole configuration, so
// the alternative to answering here is saving a wrong one and learning about it as an
// incident on a parked task.
//
// It runs for all three products, because the one thing the check does that is *not*
// shared is picking the kind's own driver — a kind routed to the wrong product's
// driver would dial with a grammar the string is not written in and report the
// operator's correct connection string as broken.
func TestASQLConnectorIsCheckedBeforeItIsSaved(t *testing.T) {
	for _, tc := range []struct{ kind, dsn string }{
		{"mssql", "sqlserver://sa:pw@db.example.com:1433?database=hr"},
		{"mariadb", "atlas:pw@tcp(db.example.com:3306)/hr"},
		{"postgres", "postgres://atlas:pw@db.example.com:5432/hr?sslmode=require"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			calls := &probeCalls{}
			srv, _ := newValidateServer(t, WithSQLProbe(calls.probe))

			code, res := postTest(t, srv, `{"name":"hr-db","kind":"`+tc.kind+`","connectionString":"`+tc.dsn+`"}`)
			if code != http.StatusOK || !res.OK {
				t.Fatalf("check: status=%d ok=%v detail=%q error=%q", code, res.OK, res.Detail, res.Error)
			}
			if calls.product != tc.kind {
				t.Errorf("probed product %q, want %s — the check must use the kind's own driver", calls.product, tc.kind)
			}
			if calls.dsn != tc.dsn {
				t.Errorf("probed dsn = %q, want the typed one", calls.dsn)
			}
			// The verdict says what was proved and what was not, because "OK" does not
			// tell an operator whether the login may read the table their statement
			// names. A MariaDB DSN is not a URL, so it has no label to derive — the
			// verdict then says "the database" rather than guessing at one, which is
			// what redactedSQLTarget is written to do.
			if strings.Contains(res.Detail, "pw") {
				t.Errorf("detail = %q leaks the password", res.Detail)
			}
			want := "db.example.com"
			if redactedSQLTarget(tc.dsn) == "" {
				want = "the database"
			}
			if !strings.Contains(res.Detail, want) {
				t.Errorf("detail = %q, want it to name %q", res.Detail, want)
			}
			// Checking stores nothing.
			if recs, err := srv.connectors.LoadAll(); err != nil || len(recs) != 0 {
				t.Errorf("the check stored %d connector(s) (err=%v), want none", len(recs), err)
			}
		})
	}
}

// A database that does not answer is a 200 carrying ok:false — the request was served
// and the answer is "no", the same shape mail's check has.
func TestASQLConnectorThatCannotBeReachedIsAnAnswerNotAnError(t *testing.T) {
	calls := &probeCalls{err: errors.New("dial tcp 10.0.0.9:1433: connect: connection refused")}
	srv, _ := newValidateServer(t, WithSQLProbe(calls.probe))

	code, res := postTest(t, srv, `{"name":"hr-db","kind":"mssql","connectionString":"sqlserver://sa:pw@10.0.0.9:1433"}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200: an unreachable database is an answer", code)
	}
	if res.OK || !strings.Contains(res.Detail, "connection refused") {
		t.Errorf("ok=%v detail=%q, want the driver's own reason", res.OK, res.Detail)
	}
}

// A connector already saved is checked through its vault reference, which is the case
// an operator hits when they come back to a connector that stopped working.
func TestASavedSQLConnectorIsCheckedThroughItsVaultReference(t *testing.T) {
	calls := &probeCalls{}
	srv, _ := newValidateServer(t, WithSQLProbe(calls.probe))

	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/connectors",
		`{"name":"hr-db","kind":"mssql","connectionString":"sqlserver://sa:pw@db.example.com:1433?database=hr"}`, "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create: status=%d body=%s", code, body)
	}
	recs, err := srv.connectors.LoadAll()
	if err != nil || len(recs) != 1 {
		t.Fatalf("LoadAll: %v (%d records)", err, len(recs))
	}

	code, res := postTest(t, srv, `{"name":"hr-db","kind":"mssql","credentialsRef":"`+recs[0].CredentialsRef+`"}`)
	if code != http.StatusOK || !res.OK {
		t.Fatalf("check: status=%d ok=%v detail=%q error=%q", code, res.OK, res.Detail, res.Error)
	}
	if calls.dsn != "sqlserver://sa:pw@db.example.com:1433?database=hr" {
		t.Errorf("probed dsn = %q, want the one sealed at create", calls.dsn)
	}
}

// A check with neither a typed connection string nor a reference has nothing to dial,
// and that is a bad request rather than a failed connection: the difference is whether
// the operator should fix the form or the database.
func TestASQLCheckWithNothingToDialIsRefused(t *testing.T) {
	srv, _ := newValidateServer(t, WithSQLProbe((&probeCalls{}).probe))

	code, res := postTest(t, srv, `{"name":"hr-db","kind":"mssql"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; detail=%q error=%q", code, res.Detail, res.Error)
	}
	if !strings.Contains(res.Error, "connectionString") {
		t.Errorf("error = %q, want it to name what is missing", res.Error)
	}
}

// A reference the vault does not hold is the state a connector is in between being
// created and having its secret set. It is refused with that reason rather than
// handed to the driver as an empty string, which every driver reports differently.
func TestASQLCheckWithAnUnresolvableReferenceSaysSo(t *testing.T) {
	srv, _ := newValidateServer(t, WithSQLProbe((&probeCalls{}).probe))

	code, res := postTest(t, srv, `{"name":"hr-db","kind":"mssql","credentialsRef":"sql/nobody/dsn"}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if res.OK || !strings.Contains(res.Detail, "no connection string") {
		t.Errorf("ok=%v detail=%q, want the missing-secret reason", res.OK, res.Detail)
	}
}

// A server whose binary does not link a database driver cannot check a database, and
// says that instead of reporting the connector broken. The engine deliberately does
// not import the drivers (ADR-0173), so the probe is wired in by whoever also runs
// workers — and an embedder who wires nothing gets an honest answer.
func TestAServerWithNoSQLDriverSaysItCannotCheck(t *testing.T) {
	srv, _ := newValidateServer(t)

	code, res := postTest(t, srv, `{"name":"hr-db","kind":"mssql","connectionString":"sqlserver://sa:pw@db:1433"}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if res.OK || !strings.Contains(res.Detail, "cannot check") {
		t.Errorf("ok=%v detail=%q, want it to say this server cannot run the check", res.OK, res.Detail)
	}
}

// The kinds that still have no check say so by name, so the message does not become
// stale the next time one gains one.
func TestAKindWithNoCheckIsNamed(t *testing.T) {
	srv, _ := newValidateServer(t)

	code, res := postTest(t, srv, `{"name":"tickets","kind":"jira"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
	if !strings.Contains(res.Error, "jira") {
		t.Errorf("error = %q, want it to name the kind that cannot be checked", res.Error)
	}
}
