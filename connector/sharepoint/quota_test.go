package sharepoint

import (
	"context"
	"encoding/xml"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/job"
)

// quotaClient is a client whose tenant administration is the fake at base, and records
// which scope each admin token was asked for.
func quotaClient(t *testing.T, route func(w http.ResponseWriter, r *http.Request, body []byte)) (*fakeGraph, *GraphClient, *[]string) {
	t.Helper()
	fg, c := newFakeGraph(t, route)
	var scopes []string
	c.admin = &adminAccess{
		url: fg.srv.URL,
		newTokens: func(scope string) (TokenSource, error) {
			scopes = append(scopes, scope)
			return stubTokens{tok: "admin-token"}, nil
		},
	}
	return fg, c, &scopes
}

const csomOK = `[{"SchemaVersion":"15.0.0.0","LibraryVersion":"16.0.0.0","ErrorInfo":null,"TraceCorrelationId":"x"},2,{"IsNull":false},4,{"IsNull":false},8,{"IsNull":false},9,{"_ObjectType_":"Microsoft.Online.SharePoint.TenantAdministration.SpoOperation","IsComplete":true}]`

// The request is the client object model's: a ProcessQuery on the admin host with the
// admin host's token, which reads the site by its URL and sets both levels in megabytes.
func TestSetSiteQuotaSendsTheTenantAdministrationRequest(t *testing.T) {
	fg, c, scopes := quotaClient(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		answer(w, http.StatusOK, csomOK)
	})
	got, err := c.Do(context.Background(), Request{
		Operation: OpSetSiteQuota, Site: "contoso.sharepoint.com:/sites/projekt & co", QuotaGB: "50", WarningPercent: "80",
	})
	if err != nil {
		t.Fatal(err)
	}
	r := fg.last(t)
	if r.method != http.MethodPost || r.path != "/_vti_bin/client.svc/ProcessQuery" {
		t.Errorf("request = %s %s, want POST /_vti_bin/client.svc/ProcessQuery", r.method, r.path)
	}
	if auth := r.header.Get("Authorization"); auth != "Bearer admin-token" {
		t.Errorf("Authorization = %q, want the admin host's token, not Graph's", auth)
	}
	if ct := r.header.Get("Content-Type"); ct != "text/xml" {
		t.Errorf("Content-Type = %q, want text/xml", ct)
	}
	if len(*scopes) != 1 || (*scopes)[0] != fg.srv.URL+"/.default" {
		t.Errorf("admin token scopes = %v, want [%s/.default]", *scopes, fg.srv.URL)
	}
	// The body is well-formed XML — the ampersand in the URL is escaped — and carries
	// the URL, the Tenant type and both levels.
	if err := xml.Unmarshal(r.body, new(struct{})); err != nil {
		t.Fatalf("the request is not well-formed XML: %v\n%s", err, r.body)
	}
	body := string(r.body)
	for _, want := range []string{
		tenantTypeID,
		`Name="GetSitePropertiesByUrl"`,
		`<Parameter Type="String">https://contoso.sharepoint.com/sites/projekt%20&amp;%20co</Parameter>`,
		`Name="StorageMaximumLevel"><Parameter Type="Int64">51200</Parameter>`,
		`Name="StorageWarningLevel"><Parameter Type="Int64">40960</Parameter>`,
		`Name="Update"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the request lacks %s:\n%s", want, body)
		}
	}
	m := got.(map[string]any)
	if m["storageMaximumLevelMB"].(interface{ String() string }).String() != "51200" || m["complete"] != true {
		t.Errorf("answer = %v", m)
	}
	if m["site"] != "https://contoso.sharepoint.com/sites/projekt%20&%20co" {
		t.Errorf("answer site = %v", m["site"])
	}
}

// Without a warning share the quota warns at 90 percent; a second call to the same admin
// host reuses its token source.
func TestSetSiteQuotaDefaultsTheWarningAndKeepsItsTokenSource(t *testing.T) {
	fg, c, scopes := quotaClient(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		answer(w, http.StatusOK, csomOK)
	})
	for range 2 {
		if _, err := c.Do(context.Background(), Request{Operation: OpSetSiteQuota, Site: "https://contoso.sharepoint.com/sites/a", QuotaGB: "1"}); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(string(fg.last(t).body), `Name="StorageWarningLevel"><Parameter Type="Int64">921</Parameter>`) {
		t.Errorf("warning level is not 90%% of 1024 MB:\n%s", fg.last(t).body)
	}
	if len(*scopes) != 1 {
		t.Errorf("token sources built = %d, want 1", len(*scopes))
	}
}

// A Graph site id names no URL: Graph is asked for the site's webUrl first.
func TestSetSiteQuotaFindsAGraphSiteIDsURL(t *testing.T) {
	fg, c, _ := quotaClient(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/sites/") && strings.HasSuffix(r.URL.Path, "/drive"):
			answer(w, http.StatusOK, `{"quota":{"total":1}}`)
		case strings.HasPrefix(r.URL.Path, "/sites/"):
			answer(w, http.StatusOK, `{"id":"`+siteID+`","webUrl":"https://contoso.sharepoint.com/sites/hr"}`)
		default:
			answer(w, http.StatusOK, csomOK)
		}
	})
	if _, err := c.Do(context.Background(), Request{Operation: OpSetSiteQuota, Site: siteID, QuotaGB: "2"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fg.last(t).body), "https://contoso.sharepoint.com/sites/hr</Parameter>") {
		t.Errorf("the site's URL from Graph is not what was sent:\n%s", fg.last(t).body)
	}
}

// The refusals a tenant gives for good carry their codes; what can change on a retry
// does not.
func TestSetSiteQuotaRefusals(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		code   string // "" = an ordinary failure
	}{
		{"401", http.StatusUnauthorized, `{"error_description":"Unsupported app only token."}`, CodeAdminNotPermitted},
		{"403", http.StatusForbidden, ``, CodeAdminNotPermitted},
		{"unauthorized in the object model", http.StatusOK,
			`[{"ErrorInfo":{"ErrorMessage":"Access denied.","ErrorTypeName":"System.UnauthorizedAccessException","ErrorCode":-2147024891}}]`, CodeAdminNotPermitted},
		{"automatic storage", http.StatusOK,
			`[{"ErrorInfo":{"ErrorMessage":"Storage quota cannot be set when the tenant manages storage automatically.","ErrorTypeName":"Microsoft.SharePoint.SPException","ErrorCode":-2146232832}}]`, CodeQuotaRefused},
		{"site not known yet", http.StatusOK,
			`[{"ErrorInfo":{"ErrorMessage":"Cannot get site https://contoso.sharepoint.com/sites/x.","ErrorTypeName":"Microsoft.Online.SharePoint.Common.SpoNoSiteException","ErrorCode":-1}}]`, ""},
		{"not found", http.StatusOK,
			`[{"ErrorInfo":{"ErrorMessage":"File Not Found.","ErrorTypeName":"System.IO.FileNotFoundException","ErrorCode":-2147024894}}]`, ""},
		{"throttled", http.StatusTooManyRequests, ``, ""},
		{"server error", http.StatusInternalServerError, ``, ""},
		{"not the object model", http.StatusOK, `<html>sign in</html>`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, c, _ := quotaClient(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
				answer(w, tc.status, tc.body)
			})
			_, err := c.Do(context.Background(), Request{Operation: OpSetSiteQuota, Site: "contoso.sharepoint.com:/sites/x", QuotaGB: "5"})
			if err == nil {
				t.Fatal("completed")
			}
			var r *Refusal
			isRefusal := errors.As(err, &r)
			switch {
			case tc.code == "" && isRefusal:
				t.Errorf("err = refusal %s (%v), want an ordinary failure that keeps its retries", r.Code, err)
			case tc.code != "" && (!isRefusal || r.Code != tc.code):
				t.Errorf("err = %v, want a refusal %s", err, tc.code)
			}
			if !strings.Contains(err.Error(), OpSetSiteQuota) {
				t.Errorf("err = %v, want it to name the operation", err)
			}
		})
	}
}

// A credential without a certificate never reaches the admin host: the refusal says
// what to change rather than letting SharePoint answer 401.
func TestSetSiteQuotaWithoutACertificateIsRefused(t *testing.T) {
	fg, c := newFakeGraph(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		answer(w, http.StatusOK, csomOK)
	})
	_, err := c.Do(context.Background(), Request{Operation: OpSetSiteQuota, Site: "contoso.sharepoint.com:/sites/x", QuotaGB: "5"})
	var r *Refusal
	if !errors.As(err, &r) || r.Code != CodeAdminNotPermitted || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("err = %v, want %s naming the certificate", err, CodeAdminNotPermitted)
	}
	if len(fg.requests) != 0 {
		t.Errorf("%d requests sent, want none", len(fg.requests))
	}
}

// The bundle decides whether tenant administration is in reach: a certificate (as PEM
// or by thumbprint) is, a client secret or a refresh token is not.
func TestAdminAccessFollowsTheCredential(t *testing.T) {
	cases := []struct {
		b    credentialBundle
		want bool
	}{
		{credentialBundle{ClientID: "c", ClientSecret: "s"}, false},
		{credentialBundle{Method: methodRefreshToken, ClientID: "c", ClientSecret: "s", RefreshToken: "r"}, false},
		{credentialBundle{ClientID: "c", Certificate: "PEM"}, true},
		{credentialBundle{Method: methodClientCredentials, ClientID: "c", CertificateThumbprint: "ab"}, true},
	}
	for _, tc := range cases {
		if got := adminAccessFor(tc.b, nil) != nil; got != tc.want {
			t.Errorf("adminAccessFor(%+v) = %v, want %v", tc.b, got, tc.want)
		}
	}
	a := adminAccessFor(credentialBundle{ClientID: "c", Certificate: "PEM", AdminURL: " https://x-admin.sharepoint.com "}, nil)
	if a.url != "https://x-admin.sharepoint.com" {
		t.Errorf("adminUrl = %q", a.url)
	}
}

func TestAdminBaseFor(t *testing.T) {
	cases := map[string]string{
		"https://contoso.sharepoint.com/sites/hr":    "https://contoso-admin.sharepoint.com",
		"https://Contoso.SharePoint.com":             "https://contoso-admin.sharepoint.com",
		"https://contoso-my.sharepoint.com/personal": "https://contoso-admin.sharepoint.com",
		"https://contoso.sharepoint.us/sites/a":      "https://contoso-admin.sharepoint.us",
		"https://intranet.contoso.com/sites/a":       "",
		"https://contoso-admin.sharepoint.com":       "",
	}
	for in, want := range cases {
		got, ok := adminBaseFor(in)
		if got != want || ok != (want != "") {
			t.Errorf("adminBaseFor(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}

// What is not a quota is refused before anything is sent — by every client, because
// prepare checks it.
func TestSetSiteQuotaRefusesWhatIsNotAQuota(t *testing.T) {
	for _, req := range []Request{
		{QuotaGB: ""}, {QuotaGB: "viel"}, {QuotaGB: "0"}, {QuotaGB: "-5"}, {QuotaGB: "25601"}, {QuotaGB: "NaN"},
		{QuotaGB: "5", WarningPercent: "0"}, {QuotaGB: "5", WarningPercent: "101"}, {QuotaGB: "5", WarningPercent: "x"},
	} {
		req.Operation, req.Site = OpSetSiteQuota, "contoso.sharepoint.com:/sites/x"
		if _, err := prepare(req); err == nil {
			t.Errorf("prepare accepts quotaGB %q warningPercent %q", req.QuotaGB, req.WarningPercent)
		}
	}
	if _, err := prepare(Request{Operation: OpSetSiteQuota, Site: "s", QuotaGB: "25600", WarningPercent: "100"}); err != nil {
		t.Errorf("prepare refuses the ceiling: %v", err)
	}
}

// A refusal becomes the BPMN error only when the process catches its code.
func TestARefusalIsThrownOnlyWhenCaught(t *testing.T) {
	err := refused(CodeQuotaRefused, OpSetSiteQuota, "no")
	var be *job.BPMNError
	if !errors.As(thrown(Job{ErrorCodes: []string{CodeQuotaRefused}}, err), &be) || be.Code != CodeQuotaRefused {
		t.Error("a caught code is not thrown as a BPMN error")
	}
	if errors.As(thrown(Job{ErrorCodes: []string{"OTHER"}}, err), &be) {
		t.Error("an uncaught code is thrown as a BPMN error")
	}
}

// The mockup records the quota and get-site reports it as the site's total.
func TestTheMockupSetsAQuota(t *testing.T) {
	m := NewMockSites(nil).Site("w")
	ctx := context.Background()
	if _, err := m.Do(ctx, Request{Operation: OpCreateItem, Site: "contoso.sharepoint.com:/sites/x", List: "L"}); err != nil {
		t.Fatal(err)
	}
	got, err := m.Do(ctx, Request{Operation: OpSetSiteQuota, Site: "https://contoso.sharepoint.com/sites/X", QuotaGB: "2"})
	if err != nil {
		t.Fatal(err)
	}
	if got.(map[string]any)["storageMaximumLevelMB"].(interface{ String() string }).String() != "2048" {
		t.Errorf("answer = %v", got)
	}
	site, _ := m.Do(ctx, Request{Operation: OpGetSite, Site: "contoso.sharepoint.com:/sites/x"})
	total := site.(map[string]any)["quota"].(map[string]any)["total"].(interface{ String() string }).String()
	if total != "2147483648" {
		t.Errorf("get-site total = %s, want 2 GiB", total)
	}
	if _, err := m.Do(ctx, Request{Operation: OpSetSiteQuota, Site: siteID, QuotaGB: "2"}); err == nil {
		t.Error("the mockup accepts a Graph site id it cannot resolve")
	}
}
