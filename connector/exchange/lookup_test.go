package exchange

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/lookup"
)

func recipientQuery(v string) lookup.Query {
	return lookup.Query{Resource: LookupRecipients, Value: v, Properties: []string{"Alias"}}
}

// A recipient lookup is a value lookup on recipients and nothing else; an alias with a
// quote, a slash or a space never reaches an OPATH filter.
func TestCheckRecipientLookup(t *testing.T) {
	if err := CheckLookup(recipientQuery("fp-fin.kreditoren")); err != nil {
		t.Fatalf("a good alias: %v", err)
	}
	for name, q := range map[string]lookup.Query{
		"a quote":             recipientQuery("a'b"),
		"a space":             recipientQuery("a b"),
		"a wildcard":          recipientQuery("a*"),
		"empty":               recipientQuery(""),
		"too long":            recipientQuery(strings.Repeat("a", 65)),
		"another resource":    {Resource: "mailboxes", Value: "a", Properties: []string{"Alias"}},
		"a search":            {Resource: LookupRecipients, Value: "a", Search: "a"},
		"a filter":            {Resource: LookupRecipients, Value: "a", Filters: []lookup.Filter{{Property: "Alias", Value: "x"}}},
		"an unknown property": {Resource: LookupRecipients, Value: "a", Properties: []string{"PrimarySmtpAddress"}},
	} {
		var le *lookup.Error
		if err := CheckLookup(q); !errors.As(err, &le) || le.Code != lookup.CodeInvalid {
			t.Errorf("%s: err = %v, want invalid_query", name, err)
		}
	}
}

// The lookup runs Get-Recipient with a filter on the alias and on the local part of
// every SMTP address, bounded to one result, and answers one item or none; a refusal
// keeps its code and nothing of the alias.
func TestRunRecipientLookup(t *testing.T) {
	f := newFakeExchange(t,
		fakeReply{body: `{"value":[{"Alias":"fp-fin","RecipientTypeDetails":"SharedMailbox","PrimarySmtpAddress":"kreditoren@contoso.com"}]}`},
		fakeReply{body: `{"value":[]}`},
		fakeReply{status: http.StatusForbidden, body: `{"error":{"code":"Forbidden","message":"no"}}`},
	)
	reg := NewRegistry()
	reg.Register("ucc_exchange", f.client(""))

	page, err := RunLookup(context.Background(), "ucc_exchange", recipientQuery("fp-fin"), reg)
	if err != nil || len(page.Items) != 1 || page.Items[0]["Alias"] != "fp-fin" || len(page.Items[0]) != 1 {
		t.Fatalf("a held alias: %#v, %v", page, err)
	}
	name, params := sentInput(t, f.calls[0])
	if name != "Get-Recipient" || params["Filter"] != "(Alias -eq 'fp-fin') -or (EmailAddresses -like 'smtp:fp-fin@*')" {
		t.Errorf("sent %s %v", name, params)
	}
	if rs, _ := params["ResultSize"].(float64); rs != 1 {
		t.Errorf("ResultSize = %v, want 1", params["ResultSize"])
	}
	if page, err := RunLookup(context.Background(), "ucc_exchange", recipientQuery("frei"), reg); err != nil || len(page.Items) != 0 {
		t.Fatalf("a free alias: %#v, %v", page, err)
	}
	_, err = RunLookup(context.Background(), "ucc_exchange", recipientQuery("geheim"), reg)
	var le *lookup.Error
	if !errors.As(err, &le) || le.Code != lookup.CodeRefused || strings.Contains(err.Error(), "geheim") {
		t.Errorf("a refusal: %v, want provider_refused without the alias", err)
	}
	if _, err := RunLookup(context.Background(), "other", recipientQuery("a"), reg); !errors.As(err, &le) || le.Code != lookup.CodeUnconfigured {
		t.Errorf("an unknown Worker: %v", err)
	}
}
