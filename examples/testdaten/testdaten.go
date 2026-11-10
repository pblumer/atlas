// Package testdaten generates the test data for the quality-check examples: one made-up
// population of employees, written as each source the examples read it from — a CSV
// file to upload, a seed for the SQL Server mock, an LDIF for an LDAP server and an
// LDIF for the AD mock (docs/planning/ad-search-as-document.md, S3).
//
// The data is deterministic: the same n gives the same bytes on every machine, so a
// test can pin the expected counts and the handbook can name them. The defects are not
// random either. Whether person i has one is decided by i modulo 100, so every source
// has six defective records in each hundred — 3,000 of 50,000 — and each defect names
// the rule of the example's decision it breaks.
//
// Every person is made up. The names are combined from two short lists, the addresses
// are under beispiel.ch, and nothing here was copied from a real directory.
//
//	go run ./examples/testdaten/generieren -n 50000 -out /tmp/testdaten
package testdaten

import (
	"bytes"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// The defect buckets: person i has the defect when i % 100 is the bucket. Each source
// reads the same buckets as its own defects, so a population of n has the same number
// of defective records in every source.
const (
	BucketNoMail      = 7  // no e-mail address
	BucketSecond      = 19 // CSV/SQL/LDAP: an address outside beispiel.ch; AD: account disabled
	BucketDepartment  = 31 // a department the decision does not know
	BucketThird       = 43 // CSV/SQL: pensum out of range; LDAP: no employee number; AD: no licence group
	BucketFourth      = 59 // CSV/SQL: exit before entry; LDAP: unknown employee type; AD: two licence groups
	BucketTwoFindings = 71 // no e-mail and an unknown department (AD: also disabled)
)

// Departments are the ones the examples' decisions know; UnknownDepartment is the one
// they do not.
var Departments = []string{"Buchhaltung", "Informatik", "Logistik", "Personal", "Vertrieb"}

// UnknownDepartment is what a record in BucketDepartment carries.
const UnknownDepartment = "Marketing"

var firstNames = []string{
	"Anna", "Beat", "Claudia", "Daniel", "Elena", "Fabian", "Gabriela", "Hans", "Ines", "Jonas",
	"Karin", "Lukas", "Monika", "Nico", "Olivia", "Patrick", "Rita", "Simon", "Tanja", "Urs",
	"Vera", "Walter", "Xenia", "Yves", "Zoé", "Andrea", "Bruno", "Corinne", "Dominik", "Esther",
	"Franziska", "Gian", "Heidi", "Ivo", "Jasmin", "Kurt", "Laura", "Marco", "Nadine", "Reto",
}

var lastNames = []string{
	"Maier", "Keller", "Brunner", "Frei", "Baumann", "Huber", "Schmid", "Meier", "Weber", "Moser",
	"Graf", "Steiner", "Wyss", "Fischer", "Bucher", "Gerber", "Müller", "Roth", "Zimmermann", "Brun",
	"Lehmann", "Suter", "Kunz", "Hofmann", "Bühler", "Widmer", "Kälin", "Zürcher", "Egli", "Sutter",
	"Marti", "Hug", "Studer", "Bachmann", "Lüthi", "Wenger", "Ammann", "Bosshard", "Felder", "Schnyder",
}

// Person is one made-up employee, the record every source is written from.
type Person struct {
	Index          int // 1-based position in the population
	Personalnummer string
	Vorname, Name  string
	Account        string // sAMAccountName and uid
	Mail           string // empty for a record without an address
	Abteilung      string
	Eintritt       string // ISO date
	Austritt       string // ISO date, empty while employed
	Pensum         int
}

// Bucket is i's defect bucket, or -1 for a record in order.
func Bucket(i int) int {
	switch b := i % 100; b {
	case BucketNoMail, BucketSecond, BucketDepartment, BucketThird, BucketFourth, BucketTwoFindings:
		return b
	}
	return -1
}

// Defective is how many of a population of n carry a defect: six in each hundred.
func Defective(n int) int {
	d := 0
	for i := 1; i <= n; i++ {
		if Bucket(i) >= 0 {
			d++
		}
	}
	return d
}

var epoch = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// Population is the n people every source is written from, in order.
func Population(n int) []Person {
	out := make([]Person, 0, n)
	for i := 1; i <= n; i++ {
		first := firstNames[(i*7)%len(firstNames)]
		last := lastNames[(i*13)%len(lastNames)]
		account := asciiLower(first[:1]+last) + strconv.Itoa(i)
		p := Person{
			Index:          i,
			Personalnummer: strconv.Itoa(100000 + i),
			Vorname:        first,
			Name:           last,
			Account:        account,
			Mail:           account + "@beispiel.ch",
			Abteilung:      Departments[(i*3)%len(Departments)],
			Pensum:         []int{40, 50, 60, 80, 90, 100}[i%6],
		}
		entry := epoch.AddDate(0, 0, (i*37)%9000)
		p.Eintritt = entry.Format(time.DateOnly)
		if i%10 == 3 {
			p.Austritt = entry.AddDate(0, 0, 400).Format(time.DateOnly)
		}
		switch Bucket(i) {
		case BucketNoMail:
			p.Mail = ""
		case BucketDepartment:
			p.Abteilung = UnknownDepartment
		case BucketTwoFindings:
			p.Mail = ""
			p.Abteilung = UnknownDepartment
		}
		out = append(out, p)
	}
	return out
}

// asciiLower spells a name the way an account name is spelled: lower case, umlauts
// written out, nothing but letters.
func asciiLower(s string) string {
	r := strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "Ä", "ae", "Ö", "oe", "Ü", "ue", "é", "e")
	s = strings.ToLower(r.Replace(s))
	var b strings.Builder
	for _, c := range s {
		if c >= 'a' && c <= 'z' {
			b.WriteRune(c)
		}
	}
	return b.String()
}

// The file and the table have the same columns, so one decision checks both.

// Columns are the columns of the employee file and of the employee table.
var Columns = []string{"personalnummer", "vorname", "name", "mail", "abteilung", "eintritt", "austritt", "pensum"}

// Statement is the query the SQL example runs, and the statement the mock seed answers.
const Statement = "SELECT personalnummer, vorname, name, mail, abteilung, eintritt, austritt, pensum FROM dbo.Mitarbeitende"

// employeeRow is a person as a row of the file and the table, with the defects of the
// two buckets only those sources read.
func employeeRow(p Person) []any {
	mail, austritt, pensum := p.Mail, any(p.Austritt), p.Pensum
	switch Bucket(p.Index) {
	case BucketSecond:
		mail = p.Account + "@gmail.com"
	case BucketThird:
		pensum = []int{0, 120}[(p.Index/100)%2]
	case BucketFourth:
		e, _ := time.Parse(time.DateOnly, p.Eintritt)
		austritt = e.AddDate(0, 0, -30).Format(time.DateOnly)
	}
	if austritt == "" {
		austritt = nil // NULL in the table, an empty cell in the file
	}
	return []any{p.Personalnummer, p.Vorname, p.Name, mail, p.Abteilung, p.Eintritt, austritt, pensum}
}

// CSV is the employee file a quality manager uploads.
func CSV(people []Person) []byte {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write(Columns)
	row := make([]string, len(Columns))
	for _, p := range people {
		for i, v := range employeeRow(p) {
			switch t := v.(type) {
			case nil:
				row[i] = ""
			case int:
				row[i] = strconv.Itoa(t)
			default:
				row[i] = fmt.Sprint(t)
			}
		}
		_ = w.Write(row)
	}
	w.Flush()
	return buf.Bytes()
}

// MSSQLSeed is the seed for the SQL Server mock: one answer to [Statement], whose rows
// are the table. It is the seed file the mock reads (connector/sqldb, ParseMockSeed).
func MSSQLSeed(people []Person) []byte {
	rows := make([][]any, 0, len(people))
	for _, p := range people {
		rows = append(rows, employeeRow(p))
	}
	seed := map[string]any{"answers": []map[string]any{{
		"statement": Statement,
		"columns":   Columns,
		"rows":      rows,
	}}}
	b, _ := json.Marshal(seed)
	return append(b, '\n')
}

// PeopleBase and AccountsBase are where the two directories keep the people.
const (
	PeopleBase   = "ou=Personen,dc=beispiel,dc=ch"
	AccountsBase = "ou=Benutzer,dc=beispiel,dc=ch"
)

// PeopleLDIF is the population as inetOrgPerson entries for an LDAP server, under
// [PeopleBase], with the organisational unit itself first so the file loads into an
// empty server with ldapadd.
func PeopleLDIF(people []Person) []byte {
	var b bytes.Buffer
	b.WriteString("# Testdaten für das Beispiel LDAP-Personen prüfen (examples/testdaten). Alle Personen sind erfunden.\n\n")
	attr(&b, "dn", PeopleBase)
	b.WriteString("objectClass: top\nobjectClass: organizationalUnit\nou: Personen\n\n")
	for _, p := range people {
		attr(&b, "dn", "uid="+p.Account+","+PeopleBase)
		b.WriteString("objectClass: top\nobjectClass: person\nobjectClass: organizationalPerson\nobjectClass: inetOrgPerson\n")
		attr(&b, "uid", p.Account)
		attr(&b, "cn", p.Vorname+" "+p.Name)
		attr(&b, "givenName", p.Vorname)
		attr(&b, "sn", p.Name)
		mail, number, kind := p.Mail, p.Personalnummer, []string{"intern", "extern"}[p.Index%2]
		switch Bucket(p.Index) {
		case BucketSecond:
			mail = p.Account + "@gmail.com"
		case BucketThird:
			number = ""
		case BucketFourth:
			kind = "praktikum"
		}
		if mail != "" {
			attr(&b, "mail", mail)
		}
		attr(&b, "departmentNumber", p.Abteilung)
		if number != "" {
			attr(&b, "employeeNumber", number)
		}
		attr(&b, "employeeType", kind)
		b.WriteString("\n")
	}
	return b.Bytes()
}

// AccountsLDIF is the population as Active Directory accounts for the AD mock, under
// [AccountsBase], with its two licence groups. It is the 50,000-account form of
// examples/ad-benutzer-pruefen/benutzer.ldif, and the decision there checks it.
func AccountsLDIF(people []Person) []byte {
	group := func(cn string) string { return "CN=" + cn + ",OU=Gruppen,DC=beispiel,DC=ch" }
	var b bytes.Buffer
	b.WriteString("# Testdaten für das Beispiel AD-Benutzer prüfen (examples/testdaten). Alle Personen sind erfunden.\n\n")
	attr(&b, "dn", AccountsBase)
	b.WriteString("objectClass: top\nobjectClass: organizationalUnit\nou: Benutzer\n\n")
	for _, p := range people {
		cn := p.Vorname + " " + p.Name + " " + strconv.Itoa(p.Index)
		attr(&b, "dn", "cn="+cn+","+AccountsBase)
		b.WriteString("objectClass: top\nobjectClass: person\nobjectClass: organizationalPerson\nobjectClass: user\n")
		attr(&b, "cn", cn)
		attr(&b, "givenName", p.Vorname)
		attr(&b, "sn", p.Name)
		attr(&b, "sAMAccountName", p.Account)
		attr(&b, "department", p.Abteilung)
		uac, mail := 512, p.Mail
		licences := []string{[]string{"Lizenz-E3", "Lizenz-F3"}[p.Index%2]}
		switch Bucket(p.Index) {
		case BucketSecond:
			uac = 514
		case BucketThird:
			licences = nil
		case BucketFourth:
			licences = []string{"Lizenz-E3", "Lizenz-F3"}
		case BucketTwoFindings:
			uac = 546 // 512 + 32 + 2: disabled; the address and the department are gone already
		}
		attr(&b, "userAccountControl", strconv.Itoa(uac))
		if mail != "" {
			attr(&b, "mail", mail)
		}
		attr(&b, "memberOf", group("Alle Mitarbeitenden"))
		for _, l := range licences {
			attr(&b, "memberOf", group(l))
		}
		b.WriteString("\n")
	}
	return b.Bytes()
}

// attr writes one LDIF line, base64 when the value is not plain ASCII (RFC 2849).
func attr(b *bytes.Buffer, name, value string) {
	plain := utf8.ValidString(value)
	for _, c := range value {
		if c > 126 || c < 32 {
			plain = false
			break
		}
	}
	if plain && value != "" && value[0] != ' ' && value[0] != ':' && value[0] != '<' {
		fmt.Fprintf(b, "%s: %s\n", name, value)
		return
	}
	fmt.Fprintf(b, "%s:: %s\n", name, base64.StdEncoding.EncodeToString([]byte(value)))
}
