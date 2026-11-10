package compiler

import (
	"strings"
	"testing"
)

const ldapSearchBase = `url="ldaps://ldap.example.com" operation="search" baseDN="ou=people,dc=x" resultVariable="personen"`

// An LDAP search gains AD's select and its document form, with the same defaults
// (docs/planning/ad-search-as-document.md, S3).
func TestLdapSearchAsDocument(t *testing.T) {
	cp, d := ldapDetailOf(t, ldapSearchBase+` select="uid, mail,memberOf" resultAs="document" documentName="personen.csv"`)
	if !d.LdapAsDocument || cp.Intern(d.LdapDocumentName) != "personen.csv" || d.LdapMaxEntries != defaultAdDocumentMaxEntries || cp.Intern(d.LdapSelect) != "uid,mail,memberOf" {
		t.Fatalf("document search = as %v, name %q, max %d, select %q", d.LdapAsDocument, cp.Intern(d.LdapDocumentName), d.LdapMaxEntries, cp.Intern(d.LdapSelect))
	}
	cp, d = ldapDetailOf(t, ldapSearchBase+` select="mail" resultAs="document" maxEntries="20"`)
	if cp.Intern(d.LdapDocumentName) != defaultLdapDocumentName || d.LdapMaxEntries != 20 {
		t.Fatalf("defaults = name %q, max %d", cp.Intern(d.LdapDocumentName), d.LdapMaxEntries)
	}
	// The variable form keeps its cap, and may now select too.
	cp, d = ldapDetailOf(t, ldapSearchBase+` select="cn"`)
	if d.LdapAsDocument || d.LdapMaxEntries != defaultLdapMaxEntries || cp.Intern(d.LdapSelect) != "cn" {
		t.Fatalf("variable search = as %v, max %d, select %q", d.LdapAsDocument, d.LdapMaxEntries, cp.Intern(d.LdapSelect))
	}
}

// What a document search could not be read back from is refused at deploy, in LDAP's
// own name.
func TestLdapSearchAsDocumentRefused(t *testing.T) {
	for _, tc := range []struct{ attrs, want string }{
		{ldapSearchBase + ` resultAs="document"`, "ldap task \"t\" writes its search as a document and needs select"},
		{ldapSearchBase + ` select="+" resultAs="document"`, "name the attributes"},
		{ldapSearchBase + ` select="mail" resultAs="xml"`, "unknown resultAs"},
		{ldapSearchBase + ` select="mail" documentName="x.csv"`, "sets documentName"},
		{ldapSearchBase + ` select="mail" resultAs="document" documentName="a/x.csv"`, "is a path"},
		{ldapSearchBase + ` select="mail,MAIL"`, "twice"},
		{`url="ldaps://x" operation="search" baseDN="ou=x" select="mail" resultAs="document"`, "needs a resultVariable"},
		{`url="ldaps://x" operation="delete" dn="cn=a" resultAs="document"`, "only a search"},
		{`url="ldaps://x" operation="delete" dn="cn=a" select="mail"`, "applies to search only"},
	} {
		_, err := Parse(1, 1, strings.NewReader(ldapTaskBPMN(tc.attrs)))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.attrs, err, tc.want)
		}
	}
}
