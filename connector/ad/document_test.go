package ad_test

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/ad"
)

// storedDocs is a document store that keeps what it is given.
type storedDocs struct {
	name, contentType string
	data              []byte
	limit             int64
	err               error
}

func (s *storedDocs) StoreDocument(_ context.Context, name, contentType string, data []byte) (map[string]any, error) {
	if s.err != nil {
		return nil, s.err
	}
	s.name, s.contentType, s.data = name, contentType, append([]byte(nil), data...)
	return map[string]any{"$document": "doc_ad", "name": name, "size": len(data)}, nil
}

func (s *storedDocs) DocumentLimit() int64 { return s.limit }

// users seeds n accounts under ou=users,dc=x, numbered so their DNs sort as numbered.
func users(n int) []ad.Entry {
	out := make([]ad.Entry, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, ad.Entry{
			DN: fmt.Sprintf("cn=user%06d,ou=users,dc=x", i),
			Attributes: map[string][]string{
				"objectClass":    {"top", "person", "organizationalPerson", "user"},
				"sAMAccountName": {fmt.Sprintf("user%06d", i)},
				"mail":           {fmt.Sprintf("user%06d@example.ch", i)},
				"memberOf":       {"CN=Alle,OU=Gruppen,DC=x", fmt.Sprintf("CN=Team %d,OU=Gruppen,DC=x", i%20)},
			},
		})
	}
	return out
}

func documentSearch(maxEntries int) ad.Job {
	return ad.Job{
		URL: mockTLSURL, Operation: "search", BaseDN: "ou=users,dc=x", Filter: "(objectClass=user)",
		Select: []string{"sAMAccountName", "MAIL", "memberOf"}, MaxEntries: maxEntries,
		AsDocument: true, DocumentName: "benutzer.csv", ResultVariable: "benutzer", FoundVariable: "da",
	}
}

func readCSV(t *testing.T, data []byte) [][]string {
	t.Helper()
	rows, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
	if err != nil {
		t.Fatalf("document is not CSV: %v", err)
	}
	return rows
}

// A search written as a document reads every page, writes one row per entry — the DN,
// then the selected attributes in the order selected, matched without regard to case —
// and completes with found, count, the first DN and the reference
// (docs/planning/ad-search-as-document.md).
func TestASearchIsWrittenAsADocument(t *testing.T) {
	seed := users(1200) // past two pages of 500
	seed[6].Attributes["memberOf"] = []string{`CN=A|B,DC=x`, `CN=C\D,DC=x`}
	delete(seed[7].Attributes, "mail")
	dir := ad.NewMockDirectory(seed...)
	docs := &storedDocs{}
	out, err := ad.RunWithDocuments(context.Background(), documentSearch(100000), dir, noSecret, nil, docs)
	if err != nil {
		t.Fatal(err)
	}
	res := out["benutzer"].(map[string]any)
	if res["count"] != 1200 || res["found"] != true || res["dn"] != "cn=user000001,ou=users,dc=x" {
		t.Fatalf("result = %v", res)
	}
	if res["document"].(map[string]any)["$document"] != "doc_ad" || out["da"] != true {
		t.Fatalf("reference = %v, found = %v", res["document"], out["da"])
	}
	if _, ok := res["entries"]; ok {
		t.Fatal("the entries are in the variable as well as in the document")
	}
	if docs.name != "benutzer.csv" || docs.contentType != "text/csv; charset=utf-8" {
		t.Fatalf("stored %q as %q", docs.name, docs.contentType)
	}
	rows := readCSV(t, docs.data)
	if len(rows) != 1201 || strings.Join(rows[0], ",") != "dn,sAMAccountName,MAIL,memberOf" {
		t.Fatalf("rows = %d, header = %v", len(rows), rows[0])
	}
	byDN := map[string][]string{}
	for _, r := range rows[1:] {
		byDN[r[0]] = r
	}
	if r := byDN["cn=user000001,ou=users,dc=x"]; r[2] != "user000001@example.ch" || r[3] != "CN=Alle,OU=Gruppen,DC=x|CN=Team 1,OU=Gruppen,DC=x" {
		t.Fatalf("row = %v", r)
	}
	// A separator or backslash inside a value is escaped, so the cell splits back exactly.
	if r := byDN["cn=user000007,ou=users,dc=x"]; r[3] != `CN=A\|B,DC=x|CN=C\\D,DC=x` {
		t.Fatalf("escaped cell = %q", r[3])
	}
	// An attribute the entry lacks is an empty cell, not a shifted row.
	if r := byDN["cn=user000008,ou=users,dc=x"]; len(r) != 4 || r[2] != "" {
		t.Fatalf("row without mail = %v", r)
	}
}

// Finding nothing is a result, written as a header with no rows.
func TestADocumentSearchThatFindsNothing(t *testing.T) {
	docs := &storedDocs{}
	out, err := ad.RunWithDocuments(context.Background(), documentSearch(10), ad.NewMockDirectory(), noSecret, nil, docs)
	if err != nil {
		t.Fatal(err)
	}
	if res := out["benutzer"].(map[string]any); res["found"] != false || res["count"] != 0 || res["dn"] != "" || out["da"] != false {
		t.Fatalf("result = %v", out)
	}
	if rows := readCSV(t, docs.data); len(rows) != 1 {
		t.Fatalf("rows = %v", rows)
	}
}

// What a document search cannot honestly finish fails the job, with the reason.
func TestADocumentSearchFailsRatherThanCuts(t *testing.T) {
	dir := ad.NewMockDirectory(users(30)...)
	ctx := context.Background()
	if _, err := ad.RunWithDocuments(ctx, documentSearch(20), dir, noSecret, nil, &storedDocs{}); err == nil || !strings.Contains(err.Error(), "20-entry cap") {
		t.Errorf("past maxEntries = %v", err)
	}
	if _, err := ad.RunWithDocuments(ctx, documentSearch(0), dir, noSecret, nil, &storedDocs{limit: 500}); err == nil || !strings.Contains(err.Error(), "500 bytes a document may be") {
		t.Errorf("past the document limit = %v", err)
	}
	if _, err := ad.RunWithDocuments(ctx, documentSearch(0), dir, noSecret, nil, nil); err == nil || !strings.Contains(err.Error(), "no document store") {
		t.Errorf("without a store = %v", err)
	}
	if _, err := ad.RunWithDocuments(ctx, documentSearch(0), dir, noSecret, nil, &storedDocs{err: errors.New("disk full")}); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Errorf("store refuses = %v", err)
	}
	// The limit the activating server sent with the job is the one held, ahead of the
	// store's and the default: on a Worker Instance it is the only one that is the
	// server's own.
	sent := documentSearch(0)
	sent.DocumentLimit = 700
	if _, err := ad.RunWithDocuments(ctx, sent, dir, noSecret, nil, &storedDocs{limit: 1 << 20}); err == nil || !strings.Contains(err.Error(), "700 bytes a document may be") {
		t.Errorf("past the limit the job carries = %v", err)
	}
	if _, err := ad.RunWithDocuments(ctx, sent, dir, noSecret, nil, &storedDocs{}); err == nil || !strings.Contains(err.Error(), "700 bytes a document may be") {
		t.Errorf("past the limit the job carries, to a store that reports none = %v", err)
	}
	sent.DocumentLimit = 1 << 20
	if _, err := ad.RunWithDocuments(ctx, sent, dir, noSecret, nil, &storedDocs{limit: 500}); err != nil {
		t.Errorf("within the limit the job carries, past the store's = %v", err)
	}
	noSelect := documentSearch(0)
	noSelect.Select = nil
	if _, err := ad.RunWithDocuments(ctx, noSelect, dir, noSecret, nil, &storedDocs{}); err == nil || !strings.Contains(err.Error(), "select") {
		t.Errorf("without select = %v", err)
	}
	noBase := documentSearch(0)
	noBase.BaseDN = " "
	if _, err := ad.RunWithDocuments(ctx, noBase, dir, noSecret, nil, &storedDocs{}); err == nil || !strings.Contains(err.Error(), "empty baseDN") {
		t.Errorf("without a base = %v", err)
	}
	// Run, which knows no document store, says so rather than writing a variable.
	if _, err := ad.Run(ctx, documentSearch(0), dir, noSecret, nil); err == nil || !strings.Contains(err.Error(), "no document store") {
		t.Errorf("Run = %v", err)
	}
}

// A connection that cannot page is read whole, and its own cap still holds.
func TestADocumentSearchOverAConnectionThatCannotPage(t *testing.T) {
	conn := &fakeConn{searchRes: users(3)}
	docs := &storedDocs{}
	j := documentSearch(10)
	j.DocumentName = ""
	out, err := ad.RunWithDocuments(context.Background(), j, &fakeDialer{conn: conn}, noSecret, nil, docs)
	if err != nil {
		t.Fatal(err)
	}
	if out["benutzer"].(map[string]any)["count"] != 3 || docs.name != "ad-search.csv" || conn.searchReq.MaxEntries != 10 {
		t.Fatalf("out = %v, name %q, request %+v", out, docs.name, conn.searchReq)
	}
}

// BenchmarkADocumentSearchOf50000Users is the plan's measurement: one search of 50,000
// accounts against the mock directory, written as a document. It reports the
// document's size and the heap in use afterwards.
//
//	go test -run=^$ -bench=ADocumentSearch -benchtime=3x ./connector/ad/
func BenchmarkADocumentSearchOf50000Users(b *testing.B) {
	dir := ad.NewMockDirectory(users(50000)...)
	j := documentSearch(100000)
	var size int
	var peak uint64
	for range b.N {
		docs := &storedDocs{}
		if _, err := ad.RunWithDocuments(context.Background(), j, dir, noSecret, nil, docs); err != nil {
			b.Fatal(err)
		}
		size = len(docs.data)
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		peak = max(peak, ms.HeapInuse)
	}
	b.ReportMetric(float64(size), "docB")
	b.ReportMetric(float64(peak), "heapB")
}
