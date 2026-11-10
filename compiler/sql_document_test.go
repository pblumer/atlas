package compiler

import (
	"fmt"
	"strings"
	"testing"
)

// A query can write its rows into a CSV document the result variable names, in every
// SQL product, with the document form's cap (docs/planning/ad-search-as-document.md, S3).
func TestSqlQueryAsDocument(t *testing.T) {
	for _, p := range sqlProductCases {
		t.Run(p.elem, func(t *testing.T) {
			ext := func(attrs string) string {
				return fmt.Sprintf(`<atlas:%s connector="hr" operation="query" statement="SELECT id FROM personen" resultVariable="personen" %s/>`, p.elem, attrs)
			}
			cp, d := sqlDetail(t, ext(`resultAs="document" documentName="personen.csv"`))
			if !d.SqlAsDocument || cp.Intern(d.SqlDocumentName) != "personen.csv" || d.SqlMaxRows != defaultAdDocumentMaxEntries {
				t.Fatalf("document query = as %v, name %q, max %d", d.SqlAsDocument, cp.Intern(d.SqlDocumentName), d.SqlMaxRows)
			}
			cp, d = sqlDetail(t, ext(`resultAs="document" maxRows="0"`))
			if cp.Intern(d.SqlDocumentName) != defaultSqlDocumentName || d.SqlMaxRows != 0 {
				t.Fatalf("defaults = name %q, max %d", cp.Intern(d.SqlDocumentName), d.SqlMaxRows)
			}
			_, d = sqlDetail(t, ext(``))
			if d.SqlAsDocument || d.SqlMaxRows != 0 {
				t.Fatalf("variable query = as %v, max %d", d.SqlAsDocument, d.SqlMaxRows)
			}
		})
	}
}

// A document form a query could not be read back from is refused at deploy.
func TestSqlQueryAsDocumentRefused(t *testing.T) {
	for _, tc := range []struct{ ext, want string }{
		{`operation="query-one" statement="SELECT 1" resultVariable="r" resultAs="document"`, `only query writes its result as a document`},
		{`operation="execute" statement="DELETE FROM x" resultAs="document"`, `only query writes`},
		{`operation="query" statement="SELECT 1" resultVariable="r" resultAs="table"`, "unknown resultAs"},
		{`operation="query" statement="SELECT 1" resultVariable="r" documentName="x.csv"`, "sets documentName"},
		{`operation="query" statement="SELECT 1" resultVariable="r" resultAs="document" documentName="c:\x.csv"`, "is a path"},
	} {
		_, err := Parse(1, 1, strings.NewReader(sqlTaskBPMN(`<atlas:mssqlConnector connector="hr" `+tc.ext+`/>`)))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.ext, err, tc.want)
		}
	}
}
