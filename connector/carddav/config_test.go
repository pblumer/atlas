package carddav

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/limits"
)

func TestParseBundle(t *testing.T) {
	b, err := ParseBundle(`{"username":"a","password":"p","groups":"group-vcards"}`)
	if err != nil || b.Username != "a" || b.Password != "p" || b.Groups != "group-vcards" {
		t.Fatalf("%+v %v", b, err)
	}
	for _, c := range []struct{ raw, want string }{
		{"", "empty"},
		{"{x", "syntax error"},
		{"{", "expected shape"},
		{`{"username":"a","password":"p","group":"group-vcards"}`, `unknown field "group"`},
		{`["a"]`, "expected shape"},
	} {
		_, err := ParseBundle(c.raw)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: err = %v, want mention of %q", c.raw, err, c.want)
		}
	}
	// A bundle that does not parse never quotes the password.
	if _, err := ParseBundle(`{"username":"a","password":TOPSECRET}`); err == nil || strings.Contains(err.Error(), "TOPSECRET") {
		t.Errorf("err = %v", err)
	}
}

func TestNewClientRefusals(t *testing.T) {
	good := `{"username":"a","password":"p"}`
	for _, c := range []struct{ endpoint, bundle, want string }{
		{"", good, "needs an endpoint"},
		{"contacts.icloud.com", good, "not an https URL"},
		{"ftp://dav.example.ch", good, "not an https URL"},
		{"https://user:pw@dav.example.ch/", good, "user name or password"},
		{"http://dav.example.ch/", good, "must be https"},
		{"https://dav.example.ch/", `{"username":"a"}`, "username and a password"},
		{"https://dav.example.ch/", `{"username":" ","password":"p"}`, "username and a password"},
		{"https://dav.example.ch/", `{"username":"a","password":"p","groups":"labels"}`, `"group-vcards"`},
		{"https://dav.example.ch/", "", "empty"},
	} {
		err := CheckBundle(c.endpoint, c.bundle)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q %s: err = %v, want mention of %q", c.endpoint, c.bundle, err, c.want)
		}
	}
	for _, ok := range []string{"https://dav.example.ch", "http://127.0.0.1:5232/", "http://localhost/dav/", "http://[::1]:8080/"} {
		if err := CheckBundle(ok, good); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	c, _ := NewClient("https://dav.example.ch?x=1#f", good, Options{})
	if c.endpoint.String() != "https://dav.example.ch/" || c.groups != "categories" {
		t.Errorf("endpoint %s groups %s", c.endpoint, c.groups)
	}
	if clip(strings.Repeat("x", 80)) != strings.Repeat("x", 60)+"…" {
		t.Error("clip")
	}
}

func client(t *testing.T, endpoint string) *Registry {
	t.Helper()
	c, err := NewClient(endpoint, `{"username":"a","password":"p"}`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	reg.Register("verkauf", c)
	return reg
}

func runErr(reg *Registry, j Job) error {
	j.Connector = "verkauf"
	switch j.ResultVariable {
	case "":
		j.ResultVariable = "r"
	case "-":
		j.ResultVariable = ""
	}
	_, err := Run(context.Background(), j, reg)
	return err
}

// The answers a server should not give are failures that say what happened, and never
// quote the URL — its path names the account.
func TestServerAnswersThatAreFailures(t *testing.T) {
	for _, c := range []struct {
		name    string
		handler http.HandlerFunc
		want    string
	}{
		{"forbidden", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }, "HTTP 403"},
		{"not XML", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusMultiStatus)
			_, _ = w.Write([]byte("<html>nein"))
		}, "not a WebDAV multistatus"},
		{"too large", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusMultiStatus)
			_, _ = w.Write(make([]byte, limits.Default().Payload+10))
		}, "more than 8 MiB"},
		{"a redirect without a location", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusFound) }, "HTTP 302"},
		{"a redirect to no URL", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "http://[::1")
			w.WriteHeader(http.StatusFound)
		}, "Location"},
		{"no home", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusMultiStatus)
			_, _ = w.Write([]byte(`<d:multistatus xmlns:d="DAV:"><d:response><d:href>/dav/</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response></d:multistatus>`))
		}, "no addressbook-home-set"},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(c.handler)
			defer srv.Close()
			err := runErr(client(t, srv.URL+"/dav/users/anna.muster/"), Job{Operation: "list-contacts"})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want mention of %q", err, c.want)
			}
			if err != nil && strings.Contains(err.Error(), "anna.muster") {
				t.Errorf("the error quotes the URL: %v", err)
			}
		})
	}
}

func TestAnUnreachableServer(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	err = runErr(client(t, "http://"+addr+"/dav/users/anna.muster/"), Job{Operation: "list-contacts"})
	if err == nil || !strings.Contains(err.Error(), "could not be reached") || strings.Contains(err.Error(), "anna.muster") {
		t.Errorf("err = %v", err)
	}
}

// A book answering a multiget with something other than a multistatus fails the read.
func TestAMultigetThatIsNotAMultistatus(t *testing.T) {
	f := seeded(t)
	inner := f.srv.Config.Handler
	f.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "REPORT" {
			w.WriteHeader(http.StatusOK)
			return
		}
		inner.ServeHTTP(w, r)
	})
	err := runErr(registry(t, f, "", ""), Job{Operation: "list-contacts"})
	if err == nil || !strings.Contains(err.Error(), "REPORT addressbook-multiget returned HTTP 200") {
		t.Errorf("err = %v", err)
	}
}

func TestStatusesWithoutAContact(t *testing.T) {
	f := seeded(t)
	inner := f.srv.Config.Handler
	f.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodPut, http.MethodDelete:
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		inner.ServeHTTP(w, r)
	})
	reg := registry(t, f, "", "")
	for _, j := range []Job{
		{Operation: "get-contact", ContactID: "anna.vcf"},
		{Operation: "update-contact", ContactID: "anna.vcf", Contact: map[string]any{"notes": "x"}},
		{Operation: "delete-contact", ContactID: "anna.vcf", ResultVariable: "-"},
		{Operation: "add-to-group", ContactID: "anna.vcf", Group: "x", ResultVariable: "-"},
		{Operation: "create-contact", CreateKey: CreateKey("n", 1), Contact: map[string]any{"familyName": "X"}},
	} {
		if err := runErr(reg, j); err == nil || !strings.Contains(err.Error(), "HTTP 500") {
			t.Errorf("%s: err = %v", j.Operation, err)
		}
	}
}

// A UID the address book already holds under another name is the server's no-uid-conflict
// precondition (RFC 6352 §6.3.2.1), said in words.
func TestAUIDConflict(t *testing.T) {
	f := seeded(t)
	inner := f.srv.Config.Handler
	f.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`<d:error xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav"><card:no-uid-conflict/></d:error>`))
			return
		}
		inner.ServeHTTP(w, r)
	})
	err := runErr(registry(t, f, "", ""), Job{Operation: "create-contact", CreateKey: CreateKey("n", 1), Contact: map[string]any{"familyName": "X"}})
	if err == nil || !strings.Contains(err.Error(), "another contact with this card's UID") {
		t.Errorf("err = %v", err)
	}
}

func TestDAVErrorWording(t *testing.T) {
	if got := (&DAVError{Call: "GET contact", HTTP: 500}).Error(); got != "carddav: GET contact returned HTTP 500 Internal Server Error" {
		t.Errorf("Error() = %q", got)
	}
	if isStatus(nil, 404) || isStatus(context.Canceled, 404) {
		t.Error("isStatus matched something that is no DAVError")
	}
}
