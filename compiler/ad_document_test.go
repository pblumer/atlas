package compiler

import (
	"strings"
	"testing"
)

const adSearchBase = `url="ldaps://dc.example.com" operation="search" baseDN="ou=users,dc=x" resultVariable="benutzer"`

// A search can write its entries into a CSV document the result variable names
// (docs/planning/ad-search-as-document.md): it is a large read, so its default cap is.
func TestAdSearchAsDocument(t *testing.T) {
	cp, d := adDetail(t, adSearchBase+` select="sAMAccountName,mail,memberOf" resultAs="document" documentName="benutzer.csv"`)
	if !d.AdAsDocument || cp.Intern(d.AdDocumentName) != "benutzer.csv" || d.AdMaxEntries != defaultAdDocumentMaxEntries {
		t.Fatalf("document search = as %v, name %q, max %d", d.AdAsDocument, cp.Intern(d.AdDocumentName), d.AdMaxEntries)
	}
	cp, d = adDetail(t, adSearchBase+` select="mail" resultAs="Document" maxEntries="5000"`)
	if !d.AdAsDocument || cp.Intern(d.AdDocumentName) != defaultAdDocumentName || d.AdMaxEntries != 5000 {
		t.Fatalf("defaults = as %v, name %q, max %d", d.AdAsDocument, cp.Intern(d.AdDocumentName), d.AdMaxEntries)
	}
	// The variable form is unchanged.
	_, d = adDetail(t, adSearchBase+` select="mail" resultAs="variable"`)
	if d.AdAsDocument || d.AdMaxEntries != defaultAdSearchMaxEntries {
		t.Fatalf("variable search = as %v, max %d", d.AdAsDocument, d.AdMaxEntries)
	}
}

// What a document search could not be read back from is refused at deploy.
func TestAdSearchAsDocumentRefused(t *testing.T) {
	for _, tc := range []struct{ attrs, want string }{
		{adSearchBase + ` resultAs="document"`, "needs select"},
		{adSearchBase + ` select="*" resultAs="document"`, "name the attributes"},
		{adSearchBase + ` select="mail,dn" resultAs="document"`, "dn is the first column"},
		{adSearchBase + ` select="mail" resultAs="file"`, "unknown resultAs"},
		{adSearchBase + ` select="mail" documentName="x.csv"`, "sets documentName"},
		{adSearchBase + ` select="mail" resultAs="document" documentName="../x.csv"`, "is a path"},
		{`url="ldaps://dc.example.com" operation="search" baseDN="ou=x" foundVariable="da" select="mail" resultAs="document"`, "needs a resultVariable"},
		{`url="ldaps://dc.example.com" operation="delete" dn="cn=a" resultAs="document"`, "only a search"},
	} {
		_, err := Parse(1, 1, strings.NewReader(adTaskBPMN(tc.attrs)))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.attrs, err, tc.want)
		}
	}
}
