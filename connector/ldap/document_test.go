package ldap

import (
	"context"
	"encoding/csv"
	"errors"
	"strings"
	"testing"
)

type docStore struct {
	name, contentType string
	data              []byte
	limit             int64
	err               error
}

func (s *docStore) StoreDocument(_ context.Context, name, contentType string, data []byte) (map[string]any, error) {
	if s.err != nil {
		return nil, s.err
	}
	s.name, s.contentType, s.data = name, contentType, append([]byte(nil), data...)
	return map[string]any{"$document": "doc_ldap", "name": name}, nil
}

func (s *docStore) DocumentLimit() int64 { return s.limit }

// wholeConn is a connection that cannot page: a search is read whole.
type wholeConn struct {
	entries []Entry
	req     SearchRequest
}

func (c *wholeConn) Search(req SearchRequest) ([]Entry, error) { c.req = req; return c.entries, nil }
func (c *wholeConn) Add(string, map[string][]string) error     { return nil }
func (c *wholeConn) Modify(string, []Mod) error                { return nil }
func (c *wholeConn) Delete(string) error                       { return nil }
func (c *wholeConn) SetPassword(string, string) error          { return nil }
func (c *wholeConn) Close() error                              { return nil }

type wholeDialer struct{ conn *wholeConn }

func (d wholeDialer) Dial(DialOptions) (Conn, error) { return d.conn, nil }

func people() []Entry {
	return []Entry{
		{DN: "uid=bob,ou=people,dc=x", Attributes: map[string][]string{"uid": {"bob"}, "MAIL": {"bob@x.ch"}, "memberOf": {"cn=a,dc=x", "cn=b|c,dc=x"}}},
		{DN: "uid=ada,ou=people,dc=x", Attributes: map[string][]string{"uid": {"ada"}}},
	}
}

func documentJob() Job {
	return Job{
		Operation: "search", BaseDN: "ou=people,dc=x", Scope: "sub", Filter: "(objectClass=person)",
		Select: []string{"uid", "mail", "memberOf"}, AsDocument: true, DocumentName: "personen.csv",
		ResultVariable: "personen",
	}
}

// A search written as a document over the real go-ldap adapter, borrowed from the
// pool: one row per entry, the DN, then select in its order, matched without regard
// to case; a missing attribute is an empty cell and a multi-valued one is joined and
// escaped. dn in the result is the smallest DN, not the first one sent.
func TestALdapSearchIsWrittenAsADocument(t *testing.T) {
	d := startTestDirectory(t, &testDirectory{paged: true, entries: people()})
	pool := NewPool(NewDialer(), PoolOptions{})
	defer pool.Close()
	j := documentJob()
	j.URL = d.URL
	docs := &docStore{}
	res, err := RunWithDocuments(context.Background(), j, pool, nil, docs)
	if err != nil {
		t.Fatal(err)
	}
	sum := res.Value.(map[string]any)
	if res.ResultVariable != "personen" || sum["count"] != 2 || sum["found"] != true || sum["dn"] != "uid=ada,ou=people,dc=x" {
		t.Fatalf("result = %+v", res)
	}
	if docs.name != "personen.csv" || docs.contentType != "text/csv; charset=utf-8" {
		t.Fatalf("stored %q as %q", docs.name, docs.contentType)
	}
	rows, err := csv.NewReader(strings.NewReader(string(docs.data))).ReadAll()
	if err != nil || len(rows) != 3 {
		t.Fatalf("rows = %v (%v)", rows, err)
	}
	if got := strings.Join(rows[0], ";") + "/" + strings.Join(rows[1], ";") + "/" + strings.Join(rows[2], ";"); got != `dn;uid;mail;memberOf/uid=bob,ou=people,dc=x;bob;bob@x.ch;cn=a,dc=x|cn=b\|c,dc=x/uid=ada,ou=people,dc=x;ada;;` {
		t.Fatalf("document = %s", got)
	}
	if pool.IdleCount() != 1 {
		t.Errorf("the connection did not go back to the pool: %d idle", pool.IdleCount())
	}
	if vars := res.Variables(); len(vars) != 1 || vars[0].Name != "personen" {
		t.Errorf("variables = %+v", vars)
	}
}

// A connection that cannot page is read whole, with the cap asked of it.
func TestALdapDocumentSearchOverAConnectionThatCannotPage(t *testing.T) {
	conn := &wholeConn{entries: people()}
	j := documentJob()
	j.URL, j.DocumentName, j.MaxEntries = "ldap://x", "", 5
	docs := &docStore{}
	res, err := RunWithDocuments(context.Background(), j, wholeDialer{conn}, nil, docs)
	if err != nil {
		t.Fatal(err)
	}
	if res.Value.(map[string]any)["count"] != 2 || docs.name != "ldap-search.csv" || conn.req.MaxEntries != 5 || len(conn.req.Attributes) != 3 {
		t.Fatalf("result = %+v, name %q, request %+v", res, docs.name, conn.req)
	}
}

// What a document search cannot honestly finish fails the job, with the reason; a
// refusal from the writer does not poison the borrowed connection.
func TestALdapDocumentSearchFailsRatherThanCuts(t *testing.T) {
	ctx := context.Background()
	d := startTestDirectory(t, &testDirectory{paged: true, entries: people()})
	pool := NewPool(NewDialer(), PoolOptions{})
	defer pool.Close()
	base := documentJob()
	base.URL = d.URL
	with := func(f func(*Job)) Job { j := base; f(&j); return j }

	if _, err := RunWithDocuments(ctx, with(func(j *Job) { j.MaxEntries = 1 }), pool, nil, &docStore{}); err == nil || !strings.Contains(err.Error(), "1-entry cap") {
		t.Errorf("past maxEntries = %v", err)
	}
	if pool.IdleCount() != 1 {
		t.Errorf("a refusal of the writer poisoned the connection: %d idle", pool.IdleCount())
	}
	if _, err := RunWithDocuments(ctx, with(func(j *Job) { j.DocumentLimit = 40 }), pool, nil, &docStore{limit: 1 << 20}); err == nil || !strings.Contains(err.Error(), "40 bytes a document may be") {
		t.Errorf("past the limit the job carries = %v", err)
	}
	if _, err := RunWithDocuments(ctx, base, pool, nil, &docStore{err: errors.New("disk full")}); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Errorf("store refuses = %v", err)
	}
	for name, tc := range map[string]struct {
		j    Job
		docs DocumentStore
		want string
	}{
		"no store":   {base, nil, "no document store"},
		"no base":    {with(func(j *Job) { j.BaseDN = " " }), &docStore{}, "empty baseDN"},
		"no select":  {with(func(j *Job) { j.Select = nil }), &docStore{}, "select"},
		"not search": {with(func(j *Job) { j.Operation = "delete" }), &docStore{}, "only a search"},
		"no url":     {with(func(j *Job) { j.URL = "" }), &docStore{}, "empty url"},
		"bad secret": {with(func(j *Job) { j.BindSecret = "LDAP_BIND" }), &docStore{}, "LDAP_BIND"},
	} {
		if _, err := RunWithDocuments(ctx, tc.j, pool, func(string) string { return "" }, tc.docs); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s = %v, want %q", name, err, tc.want)
		}
	}
	// Run, which knows no document store, says so rather than writing a variable.
	if _, err := Run(ctx, base, pool, nil); err == nil || !strings.Contains(err.Error(), "no document store") {
		t.Errorf("Run = %v", err)
	}
	// A search into a variable is Run's, document store or not.
	plain := with(func(j *Job) { j.AsDocument = false })
	if res, err := RunWithDocuments(ctx, plain, pool, nil, nil); err != nil || res.ResultVariable != "personen" {
		t.Errorf("variable form = %+v, %v", res, err)
	}
}

// A borrowed connection that cannot page is read whole through the pool, and a
// directory failure poisons it.
func TestAPooledConnectionPagesWhatItBorrows(t *testing.T) {
	inner := &wholeConn{entries: people()}
	c := &pooledConn{Conn: inner}
	n := 0
	if err := c.SearchPages(SearchRequest{BaseDN: "dc=x"}, func(e []Entry) error { n += len(e); return nil }); err != nil || n != 2 || c.poisoned {
		t.Fatalf("read whole = %d, %v, poisoned %v", n, err, c.poisoned)
	}
	failing := &pooledConn{Conn: &failConn{}}
	if err := failing.SearchPages(SearchRequest{BaseDN: "dc=x"}, func([]Entry) error { return nil }); err == nil || !failing.poisoned {
		t.Fatalf("directory failure = %v, poisoned %v", err, failing.poisoned)
	}
}

type failConn struct{ wholeConn }

func (failConn) Search(SearchRequest) ([]Entry, error) { return nil, errors.New("server down") }
