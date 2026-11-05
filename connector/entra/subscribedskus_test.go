package entra

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// subscribedSkusPage is a tenant on an Enterprise Agreement as Graph answers
// GET /subscribedSkus: Microsoft 365 E3, E5 and F3, with a suspended trial beside them.
// The shape follows Microsoft's documented example (subscribedsku-list, v1.0).
const subscribedSkusPage = `{
  "@odata.context": "https://graph.microsoft.com/v1.0/$metadata#subscribedSkus",
  "value": [
    {"accountName": "Contoso", "appliesTo": "User", "capabilityStatus": "Enabled", "consumedUnits": 412,
     "id": "t_05e9a617-0261-4cee-bb44-138d3ef5d965", "skuId": "05e9a617-0261-4cee-bb44-138d3ef5d965", "skuPartNumber": "SPE_E3",
     "prepaidUnits": {"enabled": 450, "suspended": 0, "warning": 0, "lockedOut": 0},
     "servicePlans": [{"servicePlanId": "efb87545-963c-4e0d-99df-69c6916d9eb0", "servicePlanName": "EXCHANGE_S_ENTERPRISE", "provisioningStatus": "Success", "appliesTo": "User"}],
     "subscriptionIds": ["a1"]},
    {"accountName": "Contoso", "appliesTo": "User", "capabilityStatus": "Enabled", "consumedUnits": 61,
     "id": "t_06ebc4ee-1bb5-47dd-8120-11324bc54e06", "skuId": "06ebc4ee-1bb5-47dd-8120-11324bc54e06", "skuPartNumber": "SPE_E5",
     "prepaidUnits": {"enabled": 60, "suspended": 0, "warning": 5, "lockedOut": 0},
     "servicePlans": [], "subscriptionIds": ["a2"]},
    {"accountName": "Contoso", "appliesTo": "User", "capabilityStatus": "Enabled", "consumedUnits": 180,
     "id": "t_66b55226-6b4f-492c-910c-a3b7a3c9d993", "skuId": "66b55226-6b4f-492c-910c-a3b7a3c9d993", "skuPartNumber": "SPE_F3",
     "prepaidUnits": {"enabled": 250, "suspended": 0, "warning": 0, "lockedOut": 0},
     "servicePlans": [], "subscriptionIds": ["a3"]},
    {"accountName": "Contoso", "appliesTo": "User", "capabilityStatus": "Suspended", "consumedUnits": 0,
     "id": "t_f30db892-07e9-47e9-837c-80727f46fd3d", "skuId": "f30db892-07e9-47e9-837c-80727f46fd3d", "skuPartNumber": "FLOW_FREE",
     "prepaidUnits": {"enabled": 0, "suspended": 10000, "warning": 0, "lockedOut": 0},
     "servicePlans": [], "subscriptionIds": []}
  ]
}`

// skuGraph is a Graph that answers /subscribedSkus with the page above and records the
// request it was sent.
type skuGraph struct {
	client  *GraphClient
	path    string
	query   string
	auth    string
	consist string
}

func newSkuGraph(t *testing.T, status int, body string) *skuGraph {
	t.Helper()
	g := &skuGraph{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.path, g.query = r.URL.Path, r.URL.RawQuery
		g.auth, g.consist = r.Header.Get("Authorization"), r.Header.Get("ConsistencyLevel")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	g.client = NewGraphClient(staticToken{tok: "t"}, srv.URL, http.DefaultClient)
	return g
}

func skuJob() Job {
	return Job{Connector: "contoso", Operation: "list-subscribed-skus", ResultVariable: "lizenzen"}
}

// The result is the listing every Entra listing returns — a JSON array of the SKUs —
// and each SKU carries what a licence-quota process compares: bought (prepaidUnits)
// against assigned (consumedUnits), per skuPartNumber.
func TestListSubscribedSkusReturnsEveryLicenceWithItsUnits(t *testing.T) {
	g := newSkuGraph(t, http.StatusOK, subscribedSkusPage)
	out, err := Run(context.Background(), skuJob(), regWith("contoso", g.client))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if g.path != "/subscribedSkus" || g.query != "" {
		t.Errorf("request = %s?%s, want /subscribedSkus with no query", g.path, g.query)
	}
	if g.auth != "Bearer t" {
		t.Errorf("Authorization = %q", g.auth)
	}
	if g.consist != "" {
		t.Errorf("ConsistencyLevel = %q, want none: /subscribedSkus is no advanced query", g.consist)
	}
	list, ok := out["lizenzen"].([]any)
	if !ok || len(list) != 4 {
		t.Fatalf("result = %#v, want an array of 4 SKUs", out["lizenzen"])
	}
	byPart := map[string]map[string]any{}
	for _, it := range list {
		m := it.(map[string]any)
		byPart[m["skuPartNumber"].(string)] = m
	}
	e5 := byPart["SPE_E5"]
	if e5 == nil {
		t.Fatalf("no SPE_E5 in %v", byPart)
	}
	if e5["skuId"] != "06ebc4ee-1bb5-47dd-8120-11324bc54e06" || e5["capabilityStatus"] != "Enabled" || e5["appliesTo"] != "User" {
		t.Errorf("SPE_E5 = %#v", e5)
	}
	if e5["consumedUnits"] != float64(61) {
		t.Errorf("consumedUnits = %v, want 61", e5["consumedUnits"])
	}
	units, ok := e5["prepaidUnits"].(map[string]any)
	if !ok {
		t.Fatalf("prepaidUnits = %#v", e5["prepaidUnits"])
	}
	for k, want := range map[string]float64{"enabled": 60, "suspended": 0, "warning": 5, "lockedOut": 0} {
		if units[k] != want {
			t.Errorf("prepaidUnits.%s = %v, want %v", k, units[k], want)
		}
	}
	if byPart["FLOW_FREE"]["capabilityStatus"] != "Suspended" {
		t.Errorf("a suspended subscription must come back as Graph sent it: %#v", byPart["FLOW_FREE"])
	}
}

// A select is the one query /subscribedSkus takes, and it is passed through.
func TestListSubscribedSkusCarriesASelect(t *testing.T) {
	g := newSkuGraph(t, http.StatusOK, `{"value":[]}`)
	j := skuJob()
	j.Select = "skuId,skuPartNumber,consumedUnits,prepaidUnits"
	out, err := Run(context.Background(), j, regWith("contoso", g.client))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if g.query != "$select=skuId%2CskuPartNumber%2CconsumedUnits%2CprepaidUnits" {
		t.Errorf("query = %q", g.query)
	}
	// An empty tenant is an empty array, not null: a process sums over it.
	if list, ok := out["lizenzen"].([]any); !ok || len(list) != 0 {
		t.Errorf("result = %#v, want []", out["lizenzen"])
	}
}

// What Graph refuses on /subscribedSkus is refused here, naming the field, rather than
// sent to come back as a 400 an operator decodes from the far side. The compiler refuses
// it at deploy; this is a job built by hand or by an older engine.
func TestListSubscribedSkusRefusesWhatGraphRefuses(t *testing.T) {
	for name, mut := range map[string]func(*Job){
		"filter":        func(j *Job) { j.Filter = "capabilityStatus eq 'Enabled'" },
		"search":        func(j *Job) { j.Search = `"skuPartNumber:SPE"` },
		"advancedQuery": func(j *Job) { j.Advanced = true },
		"pageSize":      func(j *Job) { j.PageSize = 10 },
	} {
		j := skuJob()
		mut(&j)
		rc := &recordingClient{res: map[string]any{"value": []any{}}}
		_, err := Run(context.Background(), j, regWith("contoso", rc))
		if err == nil || !strings.Contains(err.Error(), "takes only a select") {
			t.Errorf("%s: err = %v, want a refusal", name, err)
		}
		if rc.path != "" {
			t.Errorf("%s: a refused job reached Graph (%s)", name, rc.path)
		}
	}
	// A result variable is required, as on every listing.
	j := skuJob()
	j.ResultVariable = ""
	if _, err := Run(context.Background(), j, regWith("contoso", &recordingClient{})); err == nil || !strings.Contains(err.Error(), "resultVariable") {
		t.Errorf("no resultVariable: err = %v", err)
	}
}

// Graph's refusal — the app lacks LicenseAssignment.Read.All — fails the job with
// Graph's own error, which names the missing permission.
func TestListSubscribedSkusReportsAMissingPermission(t *testing.T) {
	g := newSkuGraph(t, http.StatusForbidden, `{"error":{"code":"Authorization_RequestDenied","message":"Insufficient privileges to complete the operation."}}`)
	_, err := Run(context.Background(), skuJob(), regWith("contoso", g.client))
	if err == nil || !strings.Contains(err.Error(), "Authorization_RequestDenied") {
		t.Errorf("err = %v, want Graph's 403", err)
	}
}

// signInActivity is outside Graph's default property set: a process reads a user's last
// sign-in by naming it in the select of list-users or get-user, which pass it through
// as $select. Graph then answers the nested object as it is, and nothing here flattens
// it. Microsoft documents that it needs AuditLog.Read.All beside User.Read.All and an
// Entra ID P1 or P2 licence in the tenant (user-list, v1.0).
func TestSignInActivityIsReadThroughSelect(t *testing.T) {
	const user = `{"id":"u1","userPrincipalName":"arno@contoso.com","signInActivity":{"lastSignInDateTime":"2026-10-30T07:12:44Z","lastSuccessfulSignInDateTime":"2026-10-30T07:12:44Z","lastNonInteractiveSignInDateTime":"2026-11-04T18:01:02Z"}}`
	for _, tc := range []struct {
		name string
		job  Job
		path string
		body string
	}{
		{"list-users", Job{Operation: "list-users", Select: "id,userPrincipalName,signInActivity", ResultVariable: "konten"},
			"/users", `{"value":[` + user + `]}`},
		{"get-user", Job{Operation: "get-user", UserID: "u1", Select: "id,userPrincipalName,signInActivity", ResultVariable: "konto"},
			"/users/u1", user},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newSkuGraph(t, http.StatusOK, tc.body)
			tc.job.Connector = "contoso"
			out, err := Run(context.Background(), tc.job, regWith("contoso", g.client))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if g.path != tc.path || !strings.Contains(g.query, "$select=id%2CuserPrincipalName%2CsignInActivity") {
				t.Errorf("request = %s?%s", g.path, g.query)
			}
			var u map[string]any
			if list, ok := out[tc.job.ResultVariable].([]any); ok {
				u, _ = list[0].(map[string]any)
			} else {
				u, _ = out[tc.job.ResultVariable].(map[string]any)
			}
			act, _ := u["signInActivity"].(map[string]any)
			if act["lastSignInDateTime"] != "2026-10-30T07:12:44Z" || act["lastSuccessfulSignInDateTime"] != "2026-10-30T07:12:44Z" {
				t.Errorf("signInActivity = %#v", u["signInActivity"])
			}
		})
	}
}
