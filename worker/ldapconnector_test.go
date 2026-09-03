package worker

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/ldap"
)

// recordingLdapDialer stands in for a directory: it records what a resolved job
// dialled with and what it then asked the connection to do.
type recordingLdapDialer struct {
	opts    ldap.DialOptions
	conn    *recordingLdapConn
	dialErr error
}

func (d *recordingLdapDialer) Dial(opts ldap.DialOptions) (ldap.Conn, error) {
	d.opts = opts
	if d.dialErr != nil {
		return nil, d.dialErr
	}
	if d.conn == nil {
		d.conn = &recordingLdapConn{}
	}
	return d.conn, nil
}

type recordingLdapConn struct {
	searched []ldap.SearchRequest
	entries  []ldap.Entry
	added    map[string]map[string][]string
	modified map[string][]ldap.Mod
	deleted  []string
	closed   bool
}

func (c *recordingLdapConn) Search(req ldap.SearchRequest) ([]ldap.Entry, error) {
	c.searched = append(c.searched, req)
	return c.entries, nil
}

func (c *recordingLdapConn) Add(dn string, attrs map[string][]string) error {
	if c.added == nil {
		c.added = map[string]map[string][]string{}
	}
	c.added[dn] = attrs
	return nil
}

func (c *recordingLdapConn) Modify(dn string, mods []ldap.Mod) error {
	if c.modified == nil {
		c.modified = map[string][]ldap.Mod{}
	}
	c.modified[dn] = mods
	return nil
}

func (c *recordingLdapConn) Delete(dn string) error {
	c.deleted = append(c.deleted, dn)
	return nil
}

func (c *recordingLdapConn) SetPassword(string, string) error { return nil }

func (c *recordingLdapConn) Close() error {
	c.closed = true
	return nil
}

func ldapJobFrom(t *testing.T, task ldap.Job) Job {
	t.Helper()
	raw, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("marshal job: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshal fields: %v", err)
	}
	return Job{Connector: &ConnectorPayload{Kind: "ldap", Fields: fields}}
}

// envFrom builds an env lookup from a map, for the worker's own secret resolution.
func envFrom(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

// A search dials what the engine resolved, asks for the authored base/scope/filter,
// and completes with the entries under the task's result variable — as real objects,
// not a stringified blob, which is what a model reading entry.dn depends on.
func TestRunLdapJobSearchesAndReturnsEntries(t *testing.T) {
	dialer := &recordingLdapDialer{conn: &recordingLdapConn{
		entries: []ldap.Entry{{DN: "cn=anna,dc=example,dc=com", Attributes: map[string][]string{"mail": {"anna@example.com"}}}},
	}}
	job := ldapJobFrom(t, ldap.Job{
		URL: "ldaps://dc.example.com", Operation: "search",
		BaseDN: "dc=example,dc=com", Scope: "sub", Filter: "(objectClass=person)",
		PageSize: 500, MaxEntries: 1000, ResultVariable: "found",
	})

	out, err := RunLdapJob(context.Background(), job, dialer, ldapSecretFromEnv(envFrom(nil)))
	if err != nil {
		t.Fatalf("RunLdapJob: %v", err)
	}
	if dialer.opts.URL != "ldaps://dc.example.com" {
		t.Errorf("dialled %q, want the server the job carried", dialer.opts.URL)
	}
	if len(dialer.conn.searched) != 1 {
		t.Fatalf("searches = %d, want exactly one", len(dialer.conn.searched))
	}
	if got := dialer.conn.searched[0]; got.BaseDN != "dc=example,dc=com" || got.Scope != "sub" || got.Filter != "(objectClass=person)" {
		t.Errorf("search = %+v, want the authored base, scope and filter", got)
	}
	rows, ok := out["found"].([]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("found = %#v, want one entry object", out["found"])
	}
	entry, ok := rows[0].(map[string]any)
	if !ok || entry["dn"] != "cn=anna,dc=example,dc=com" {
		t.Errorf("entry = %#v, want the dn as a real field", rows[0])
	}
	if !dialer.conn.closed {
		t.Error("the connection was not closed; a worker leasing jobs would leak one per job")
	}
}

// A mutating operation writes to the directory and completes with *nothing*. An empty
// object would be a variable the model never asked for, and the in-process path
// returns none either — which is the whole point of both halves going through
// ldap.Result.
func TestRunLdapJobModifyCompletesWithNoVariables(t *testing.T) {
	dialer := &recordingLdapDialer{}
	job := ldapJobFrom(t, ldap.Job{
		URL: "ldap://dc.example.com", Operation: "modify", DN: "cn=anna,dc=example,dc=com",
		Attributes: map[string][]string{"title": {"Engineer"}},
	})

	out, err := RunLdapJob(context.Background(), job, dialer, ldapSecretFromEnv(envFrom(nil)))
	if err != nil {
		t.Fatalf("RunLdapJob: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("variables = %#v, want none: a modify's effect is in the directory", out)
	}
	mods := dialer.conn.modified["cn=anna,dc=example,dc=com"]
	if len(mods) != 1 || mods[0].Attr != "title" || mods[0].Op != ldap.ModReplace {
		t.Errorf("mods = %+v, want one replace of title", mods)
	}
}

// The bind password is read from *this worker's* environment, under the same
// ATLAS_CONNECTOR_<REF>_TOKEN name the engine uses — the reference travels, the
// credential does not (ADR-0041/0168).
func TestRunLdapJobBindsWithTheWorkersOwnSecret(t *testing.T) {
	dialer := &recordingLdapDialer{}
	secret := ldapSecretFromEnv(envFrom(map[string]string{"ATLAS_CONNECTOR_DC_BIND_TOKEN": "s3cr3t"}))
	job := ldapJobFrom(t, ldap.Job{
		URL: "ldaps://dc.example.com", Operation: "delete", DN: "cn=old,dc=example,dc=com",
		BindDN: "cn=svc,dc=example,dc=com", BindSecret: "DC_BIND",
	})

	if _, err := RunLdapJob(context.Background(), job, dialer, secret); err != nil {
		t.Fatalf("RunLdapJob: %v", err)
	}
	if dialer.opts.BindPassword != "s3cr3t" {
		t.Errorf("bind password = %q, want the one resolved from the worker's environment", dialer.opts.BindPassword)
	}
}

// A reference nothing answers to fails the job naming the variable to set, rather
// than binding anonymously. Falling back to an anonymous bind would turn a missing
// credential into a search that silently returns whatever a stranger may read.
func TestRunLdapJobFailsWhenTheReferenceIsUnset(t *testing.T) {
	dialer := &recordingLdapDialer{}
	job := ldapJobFrom(t, ldap.Job{
		URL: "ldaps://dc.example.com", Operation: "search", BaseDN: "dc=example,dc=com",
		BindDN: "cn=svc,dc=example,dc=com", BindSecret: "DC_BIND",
	})

	_, err := RunLdapJob(context.Background(), job, dialer, ldapSecretFromEnv(envFrom(nil)))
	if err == nil {
		t.Fatal("a missing bind secret was accepted; the job would have bound anonymously")
	}
	if want := "ATLAS_CONNECTOR_DC_BIND_TOKEN"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %v, want it to name %s so an operator knows what to set", err, want)
	}
	if dialer.opts.URL != "" {
		t.Error("the directory was dialled anyway; the credential must be resolved before the bind")
	}
}

// A job that arrives with no resolved detail says so rather than dialling an empty
// url: it means this server is not offloading the kind, which is a deployment
// mistake and not a directory that happens to be unreachable.
func TestRunLdapJobWithoutAResolvedDetail(t *testing.T) {
	_, err := RunLdapJob(context.Background(), Job{}, &recordingLdapDialer{}, ldapSecretFromEnv(envFrom(nil)))
	if err == nil {
		t.Fatal("a job with no connector payload was accepted")
	}
}
