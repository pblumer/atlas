package ad_test

import (
	"context"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/ad"
)

// Which directory a job talks to, and how it binds
// (ADR-draft-ad-as-a-console-connector).
//
// A task carries one of two shapes: the name of a directory an operator configured, or
// the directory itself. The compiler refuses both at once, so there is no precedence
// rule here — these tests pin that each shape resolves to what it says and that a name
// nobody configured fails by name rather than dialling something else.

// dialRecorder is a Dialer that reports what it was asked to dial and bind as.
type dialRecorder struct {
	url, bindDN, password string
	startTLS              bool
	dir                   *ad.MockDirectory
}

func (d *dialRecorder) Dial(url, bindDN, password string, startTLS bool) (ad.Conn, error) {
	d.url, d.bindDN, d.password, d.startTLS = url, bindDN, password, startTLS
	if d.dir == nil {
		d.dir = ad.NewMockDirectory()
	}
	return d.dir.Dial(url, bindDN, password, startTLS)
}

func regWithDirs(t *testing.T, dirs map[string]ad.Directory) *ad.Registry {
	t.Helper()
	reg := ad.NewRegistry()
	for name, d := range dirs {
		reg.Register(name, d)
	}
	return reg
}

// A named directory is dialled at its own URL, as its own service account — and the
// password is a value the worker already holds, not a reference resolved here.
func TestANamedDirectoryIsDialledAtItsOwnAddress(t *testing.T) {
	dirs := regWithDirs(t, map[string]ad.Directory{
		"prod": {URL: "ldaps://dc-prod.example.com:636", BindDN: "cn=svc-prod,dc=example,dc=com", Password: "pw-prod"},
		"test": {URL: "ldap://dc-test.example.com:389", BindDN: "cn=svc-test,dc=example,dc=com", Password: "pw-test", StartTLS: true},
	})

	for name, want := range map[string]ad.Directory{
		"prod": {URL: "ldaps://dc-prod.example.com:636", BindDN: "cn=svc-prod,dc=example,dc=com", Password: "pw-prod"},
		"test": {URL: "ldap://dc-test.example.com:389", BindDN: "cn=svc-test,dc=example,dc=com", Password: "pw-test", StartTLS: true},
	} {
		t.Run(name, func(t *testing.T) {
			rec := &dialRecorder{}
			// A create, so the operation actually reaches the connection rather than
			// stopping at the bind.
			if _, err := ad.Run(context.Background(), ad.Job{
				Connector: name, Operation: "create-user", DN: "cn=Neu,dc=example,dc=com",
				Attributes: map[string][]string{"sAMAccountName": {"neu"}},
			}, rec, noSecret, dirs); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if rec.url != want.URL || rec.bindDN != want.BindDN || rec.password != want.Password {
				t.Errorf("dialled %q as %q/%q, want %q as %q/%q",
					rec.url, rec.bindDN, rec.password, want.URL, want.BindDN, want.Password)
			}
			if rec.startTLS != want.StartTLS {
				t.Errorf("startTLS = %v, want %v", rec.startTLS, want.StartTLS)
			}
		})
	}
}

// A task naming a directory this worker does not hold fails by name. The alternative —
// an empty URL reaching the dialer — reports "empty url" on a task that plainly names
// one, which sends the reader to the model instead of to the Console.
func TestAJobNamingAnUnconfiguredDirectoryFailsByName(t *testing.T) {
	for _, tc := range []struct {
		name string
		dirs *ad.Registry
	}{
		{"a registry holding other directories", regWithDirs(t, map[string]ad.Directory{
			"prod": {URL: "ldaps://dc:636", BindDN: "cn=svc,dc=x", Password: "pw"},
		})},
		{"an empty registry", ad.NewRegistry()},
		{"no registry at all", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ad.Run(context.Background(), ad.Job{
				Connector: "staging", Operation: "disable", DN: "cn=Arno,dc=x",
			}, &dialRecorder{}, noSecret, tc.dirs)
			if err == nil {
				t.Fatal("a job naming an unconfigured directory ran anyway")
			}
			if !strings.Contains(err.Error(), "staging") {
				t.Errorf("error = %v, want it to name the directory", err)
			}
		})
	}
}

// A configured directory with no URL behind it is refused rather than dialled as "".
// The engine leaves such a record out entirely, so reaching this means something else
// registered one — and an empty dial is a confusing way to find out.
func TestANamedDirectoryWithNoURLIsRefused(t *testing.T) {
	dirs := regWithDirs(t, map[string]ad.Directory{"prod": {BindDN: "cn=svc,dc=x", Password: "pw"}})
	_, err := ad.Run(context.Background(), ad.Job{
		Connector: "prod", Operation: "disable", DN: "cn=Arno,dc=x",
	}, &dialRecorder{}, noSecret, dirs)
	if err == nil || !strings.Contains(err.Error(), "no url") {
		t.Errorf("error = %v, want it to say the directory has no url", err)
	}
}

// The older shape is untouched by any of this: a task carrying its own url resolves its
// bind password from the worker's secrets exactly as it always did, and a registry
// being present changes nothing about it.
func TestATaskCarryingItsOwnURLIgnoresTheRegistry(t *testing.T) {
	dirs := regWithDirs(t, map[string]ad.Directory{
		"prod": {URL: "ldaps://wrong.example.com:636", BindDN: "cn=wrong,dc=x", Password: "wrong"},
	})
	rec := &dialRecorder{}
	if _, err := ad.Run(context.Background(), ad.Job{
		URL: "ldaps://dc.example.com:636", BindDN: "cn=svc,dc=example,dc=com", BindSecret: "AD_BIND",
		Operation: "create-user", DN: "cn=Neu,dc=example,dc=com",
		Attributes: map[string][]string{"sAMAccountName": {"neu"}},
	}, rec, func(ref string) string {
		if ref == "AD_BIND" {
			return "s3cr3t"
		}
		return ""
	}, dirs); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rec.url != "ldaps://dc.example.com:636" || rec.password != "s3cr3t" {
		t.Errorf("dialled %q as %q, want the task's own url and its resolved reference", rec.url, rec.password)
	}
}

// And the refusals the older shape already had still hold: no url at all, and a
// reference nothing resolves.
func TestTheOlderShapeKeepsItsRefusals(t *testing.T) {
	if _, err := ad.Run(context.Background(), ad.Job{
		Operation: "disable", DN: "cn=Arno,dc=x",
	}, &dialRecorder{}, noSecret, nil); err == nil || !strings.Contains(err.Error(), "empty url") {
		t.Errorf("error = %v, want an empty url refused", err)
	}
	if _, err := ad.Run(context.Background(), ad.Job{
		URL: "ldaps://dc:636", BindDN: "cn=svc,dc=x", BindSecret: "AD_BIND",
		Operation: "disable", DN: "cn=Arno,dc=x",
	}, &dialRecorder{}, noSecret, nil); err == nil || !strings.Contains(err.Error(), "AD_BIND") {
		t.Errorf("error = %v, want the unresolved reference named", err)
	}
}
