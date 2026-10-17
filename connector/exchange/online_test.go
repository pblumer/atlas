package exchange

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/exchange/catalog"
)

// staticToken hands out a fixed bearer, so these tests are about Exchange's endpoint
// and not about OAuth, which connector/oauth2 covers on its own.
type staticToken struct {
	tok string
	err error
}

func (s staticToken) Token(context.Context) (string, error) { return s.tok, s.err }

// exoCall is one request the fake endpoint received.
type exoCall struct {
	path   string
	query  string
	auth   string
	anchor string
	ctype  string
	body   map[string]any
}

// fakeExchange stands in for outlook.office365.com: it records every InvokeCommand
// request and answers each with the next canned reply.
type fakeExchange struct {
	srv     *httptest.Server
	calls   []exoCall
	replies []fakeReply
}

type fakeReply struct {
	status int
	body   string
}

func newFakeExchange(t *testing.T, replies ...fakeReply) *fakeExchange {
	t.Helper()
	f := &fakeExchange{replies: replies}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		f.calls = append(f.calls, exoCall{
			path: r.URL.Path, query: r.URL.RawQuery, auth: r.Header.Get("Authorization"),
			anchor: r.Header.Get("X-AnchorMailbox"), ctype: r.Header.Get("Content-Type"), body: body,
		})
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		i := len(f.calls) - 1
		if i >= len(f.replies) {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if f.replies[i].status != 0 {
			w.WriteHeader(f.replies[i].status)
		}
		_, _ = w.Write([]byte(strings.ReplaceAll(f.replies[i].body, "{{base}}", f.srv.URL)))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeExchange) client(anchor string) *OnlineClient {
	return NewOnlineClient(OnlineConfig{
		Tokens: staticToken{tok: "tok-1"}, BaseURL: f.srv.URL,
		Organization: "contoso.onmicrosoft.com", AnchorMailbox: anchor, HTTPClient: f.srv.Client(),
	})
}

func sentInput(t *testing.T, c exoCall) (string, map[string]any) {
	t.Helper()
	in, ok := c.body["CmdletInput"].(map[string]any)
	if !ok {
		t.Fatalf("body carries no CmdletInput: %v", c.body)
	}
	name, _ := in["CmdletName"].(string)
	params, _ := in["Parameters"].(map[string]any)
	return name, params
}

// TestOnlineInvokesTheCmdletOverREST is the protocol the Exchange Online PowerShell
// module itself speaks: one POST per cmdlet to the tenant's InvokeCommand endpoint,
// with the cmdlet and its parameters in a CmdletInput envelope and a bearer for
// outlook.office365.com.
func TestOnlineInvokesTheCmdletOverREST(t *testing.T) {
	f := newFakeExchange(t, fakeReply{body: `{"@odata.context":"x","value":[{"Identity":"shared","AccessRights":["FullAccess"]}]}`})
	out, err := f.client("").Invoke(context.Background(), catalog.Invocation{
		Cmdlet:     "Add-MailboxPermission",
		Parameters: map[string]any{"Identity": "shared@contoso.com", "User": "anna@contoso.com", "AccessRights": []any{"FullAccess"}},
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 {
		t.Fatalf("%d calls", len(f.calls))
	}
	c := f.calls[0]
	if c.path != "/adminapi/beta/contoso.onmicrosoft.com/InvokeCommand" {
		t.Errorf("path = %q", c.path)
	}
	if c.auth != "Bearer tok-1" {
		t.Errorf("auth = %q", c.auth)
	}
	if !strings.HasPrefix(c.ctype, "application/json") {
		t.Errorf("content type = %q", c.ctype)
	}
	name, params := sentInput(t, c)
	if name != "Add-MailboxPermission" || params["User"] != "anna@contoso.com" {
		t.Errorf("CmdletInput = %v %v", name, params)
	}
	if len(out) != 1 {
		t.Fatalf("out = %v", out)
	}
}

// TestOnlineAnchorsOnTheMailboxItAddresses: Exchange Online routes a request by its
// anchor mailbox, and anchoring on the object a call is about is what keeps a call
// about a mailbox in another region from being served by the wrong one.
func TestOnlineAnchorsOnTheMailboxItAddresses(t *testing.T) {
	cases := []struct {
		name       string
		configured string
		params     map[string]any
		want       string
	}{
		{"identity address", "", map[string]any{"Identity": "anna@contoso.com"}, "UPN:anna@contoso.com"},
		{"folder identity", "", map[string]any{"Identity": `anna@contoso.com:\Kalender`}, "UPN:anna@contoso.com"},
		{"no address", "", map[string]any{"Identity": "Anna Muster"}, "APP:SystemMailbox{bb558c35-97f1-4cb9-8ff7-d53741dc928c}@contoso.onmicrosoft.com"},
		{"no identity", "", map[string]any{"Filter": "x"}, "APP:SystemMailbox{bb558c35-97f1-4cb9-8ff7-d53741dc928c}@contoso.onmicrosoft.com"},
		{"configured wins", "UPN:admin@contoso.com", map[string]any{"Identity": "anna@contoso.com"}, "UPN:admin@contoso.com"},
		{"configured bare address", "admin@contoso.com", nil, "UPN:admin@contoso.com"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeExchange(t, fakeReply{body: `{"value":[]}`})
			if _, err := f.client(c.configured).Invoke(context.Background(), catalog.Invocation{Cmdlet: "Get-Mailbox", Parameters: c.params}, 0); err != nil {
				t.Fatal(err)
			}
			if got := f.calls[0].anchor; got != c.want {
				t.Errorf("X-AnchorMailbox = %q, want %q", got, c.want)
			}
		})
	}
}

// TestOnlineMarksHashtables: a multi-valued property is changed with a hashtable, and
// the REST endpoint only binds an object as one when it says so.
func TestOnlineMarksHashtables(t *testing.T) {
	f := newFakeExchange(t, fakeReply{body: `{"value":[]}`})
	authored := map[string]any{"Add": "smtp:a@contoso.com"}
	inv := catalog.Invocation{Cmdlet: "Set-Mailbox", Parameters: map[string]any{"Identity": "a@contoso.com", "EmailAddresses": authored}}
	if _, err := f.client("").Invoke(context.Background(), inv, 0); err != nil {
		t.Fatal(err)
	}
	_, params := sentInput(t, f.calls[0])
	h, _ := params["EmailAddresses"].(map[string]any)
	if h["@odata.type"] != "#Exchange.GenericHashTable" || h["Add"] != "smtp:a@contoso.com" {
		t.Errorf("EmailAddresses = %v", params["EmailAddresses"])
	}
	if _, touched := authored["@odata.type"]; touched {
		t.Error("the authored parameter map was modified")
	}
}

// TestOnlineFollowsPagesAndStopsAtTheLimit: a listing is paged by the endpoint and the
// worker follows the pages — but stops as soon as it holds more than it may return,
// rather than reading a whole tenant to then refuse it.
func TestOnlineFollowsPagesAndStopsAtTheLimit(t *testing.T) {
	f := newFakeExchange(t,
		fakeReply{body: `{"value":[{"n":1},{"n":2}],"@odata.nextLink":"{{base}}/adminapi/beta/contoso.onmicrosoft.com/InvokeCommand?$skiptoken=a"}`},
		fakeReply{body: `{"value":[{"n":3}],"@odata.nextLink":"{{base}}/adminapi/beta/contoso.onmicrosoft.com/InvokeCommand?$skiptoken=b"}`},
		fakeReply{body: `{"value":[{"n":4}]}`},
	)
	out, err := f.client("").Invoke(context.Background(), catalog.Invocation{Cmdlet: "Get-Mailbox", Parameters: map[string]any{}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 4 || len(f.calls) != 3 {
		t.Fatalf("got %d items over %d calls", len(out), len(f.calls))
	}
	if f.calls[1].query != "$skiptoken=a" {
		t.Errorf("continuation query = %q", f.calls[1].query)
	}
	if name, _ := sentInput(t, f.calls[2]); name != "Get-Mailbox" {
		t.Error("a continuation must repeat the cmdlet it continues")
	}

	f = newFakeExchange(t,
		fakeReply{body: `{"value":[{"n":1},{"n":2}],"@odata.nextLink":"{{base}}/next"}`},
		fakeReply{body: `{"value":[{"n":3}]}`},
	)
	out, err = f.client("").Invoke(context.Background(), catalog.Invocation{Cmdlet: "Get-Mailbox"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 || len(out) != 2 {
		t.Errorf("with limit 2: %d items over %d calls", len(out), len(f.calls))
	}
}

// TestOnlineRefusesAForeignContinuation: the bearer can administer every mailbox in the
// tenant, and a continuation is the one place a response picks the next URL.
func TestOnlineRefusesAForeignContinuation(t *testing.T) {
	f := newFakeExchange(t, fakeReply{body: `{"value":[{"n":1}],"@odata.nextLink":"https://attacker.example/steal"}`})
	_, err := f.client("").Invoke(context.Background(), catalog.Invocation{Cmdlet: "Get-Mailbox"}, 0)
	if err == nil || !strings.Contains(err.Error(), "attacker.example") {
		t.Errorf("err = %v", err)
	}
	if len(f.calls) != 1 {
		t.Error("the foreign continuation was requested")
	}
}

// TestOnlineStripsAnnotations: OData annotations are about the protocol, not the
// mailbox, and have no business in a process variable.
func TestOnlineStripsAnnotations(t *testing.T) {
	f := newFakeExchange(t, fakeReply{body: `{"value":[{"@odata.id":"x","Name":"Anna","EmailAddresses@odata.type":"#Collection(String)","EmailAddresses":["SMTP:a@contoso.com"]}]}`})
	out, err := f.client("").Invoke(context.Background(), catalog.Invocation{Cmdlet: "Get-Mailbox"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	obj := out[0].(map[string]any)
	if len(obj) != 2 || obj["Name"] != "Anna" || obj["EmailAddresses"] == nil {
		t.Errorf("object = %v", obj)
	}
}

// TestOnlineErrorsSayWhatExchangeSaid: the difference between "HTTP 400" and "the
// recipient is already a member of the group" is the whole of what an operator needs.
func TestOnlineErrorsSayWhatExchangeSaid(t *testing.T) {
	cases := []struct {
		name  string
		reply fakeReply
		want  []string
	}{
		{"exchange envelope", fakeReply{status: 400, body: `{"error":{"code":"BadRequest","message":"|Microsoft.Exchange.Management.Tasks.MemberAlreadyExistsException|The recipient \"anna@contoso.com\" is already a member of the group \"Team\"."}}`},
			[]string{"Add-DistributionGroupMember", "HTTP 400", "already a member", "MemberAlreadyExistsException"}},
		{"plain envelope", fakeReply{status: 403, body: `{"error":{"code":"Forbidden","message":"The role assigned to application isn't supported in this scenario."}}`},
			[]string{"HTTP 403", "Forbidden", "role assigned"}},
		{"no envelope", fakeReply{status: 502, body: `bad gateway`}, []string{"HTTP 502", "bad gateway"}},
		{"not json", fakeReply{body: `<html>`}, []string{"not JSON"}},
		{"no value", fakeReply{body: `{"something":1}`}, []string{"value"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeExchange(t, c.reply)
			_, err := f.client("").Invoke(context.Background(), catalog.Invocation{Cmdlet: "Add-DistributionGroupMember"}, 0)
			if err == nil {
				t.Fatal("no error")
			}
			for _, w := range c.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not mention %q", err, w)
				}
			}
			if !strings.HasPrefix(err.Error(), "exchange:") {
				t.Errorf("error %q does not name the worker type", err)
			}
		})
	}
}

func TestOnlineTokenFailureIsTheError(t *testing.T) {
	f := newFakeExchange(t)
	c := NewOnlineClient(OnlineConfig{Tokens: staticToken{err: errors.New("exchange: token endpoint said no")}, BaseURL: f.srv.URL, Organization: "contoso.onmicrosoft.com", HTTPClient: f.srv.Client()})
	_, err := c.Invoke(context.Background(), catalog.Invocation{Cmdlet: "Get-Mailbox"}, 0)
	if err == nil || !strings.Contains(err.Error(), "said no") {
		t.Errorf("err = %v", err)
	}
	if len(f.calls) != 0 {
		t.Error("a request went out without a token")
	}
	if c.Target() != catalog.Online {
		t.Errorf("target = %v", c.Target())
	}
}

// TestOnlineRefusesAnOversizedPage: one page past the payload budget is refused, not
// cut off into JSON that no longer parses or, worse, still does.
func TestOnlineRefusesAnOversizedPage(t *testing.T) {
	f := newFakeExchange(t, fakeReply{body: `{"value":["` + strings.Repeat("x", 64) + `"]}`})
	c := f.client("")
	c.maxPage = 32
	_, err := c.Invoke(context.Background(), catalog.Invocation{Cmdlet: "Get-Mailbox"}, 0)
	if err == nil || !strings.Contains(err.Error(), "byte") {
		t.Errorf("err = %v", err)
	}
}

func TestOnlineDefaultsItsEndpoint(t *testing.T) {
	c := NewOnlineClient(OnlineConfig{Tokens: staticToken{tok: "t"}, Organization: "contoso.onmicrosoft.com"})
	if c.baseURL != DefaultOnlineBaseURL || c.httpc == nil {
		t.Errorf("base %q httpc %v", c.baseURL, c.httpc)
	}
}
