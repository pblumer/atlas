package sharepoint

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/limits"
)

// Setting a site's storage quota (ADR-0141, amended 2026-11-04).
//
// Graph reads a site's storage but cannot set it: the quota is a property of the site in
// SharePoint's tenant administration, and the only interface that writes it is the one
// Set-SPOSite and Set-PnPTenantSite use — the client object model's ProcessQuery
// endpoint on the tenant's admin host, https://<tenant>-admin.sharepoint.com. That is
// where set-site-quota goes, with a token for that host rather than for Graph.
//
// The request is the one those cmdlets send, written out: construct the Tenant object,
// read the site's properties by URL, set StorageMaximumLevel and StorageWarningLevel
// (both in megabytes), and call Update. The client object model has been the stable
// contract of SharePoint's tenant administration since 2013; a REST route under
// /_api/SPO.Tenant exists for some of it, but Microsoft documents none for setting a
// site's properties, so the stable one is the documented one.

// tenantTypeID is the client object model's type id of
// Microsoft.Online.SharePoint.TenantAdministration.Tenant: what a ProcessQuery
// constructs to reach tenant administration.
const tenantTypeID = "{268004ae-ef6b-4e9b-8425-127220d84719}"

// csomNamespace is the client object model's request namespace.
const csomNamespace = "http://schemas.microsoft.com/sharepoint/clientquery/2009"

// quotaSettings is what set-site-quota was asked to set, read and checked.
type quotaSettings struct {
	gb             float64
	warningPercent float64
	maxMB          int64
	warningMB      int64
}

// readQuota reads and checks the quota a task resolved. The values arrive as the text
// their FEEL value resolved to, so "50" and "50.0" both read as fifty gigabytes; what
// is not a number, not positive, or past SharePoint's ceiling is refused here rather
// than sent, because SharePoint would refuse it with a message about megabytes nobody
// authored.
func readQuota(op string, req Request) (quotaSettings, error) {
	raw := strings.TrimSpace(req.QuotaGB)
	if raw == "" {
		return quotaSettings{}, fmt.Errorf("sharepoint: %s has no quotaGB", op)
	}
	gb, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(gb) || math.IsInf(gb, 0) {
		return quotaSettings{}, fmt.Errorf("sharepoint: %s: quotaGB %q is not a number of gigabytes", op, raw)
	}
	if gb <= 0 || gb > MaxQuotaGB {
		return quotaSettings{}, fmt.Errorf("sharepoint: %s: quotaGB %s is outside 0 to %d, the most SharePoint gives one site", op, raw, MaxQuotaGB)
	}
	pct := float64(DefaultWarningPercent)
	if w := strings.TrimSpace(req.WarningPercent); w != "" {
		pct, err = strconv.ParseFloat(w, 64)
		if err != nil || math.IsNaN(pct) || pct <= 0 || pct > 100 {
			return quotaSettings{}, fmt.Errorf("sharepoint: %s: warningPercent %q is not a share between 1 and 100", op, w)
		}
	}
	maxMB := int64(math.Round(gb * 1024))
	if maxMB < 1 {
		return quotaSettings{}, fmt.Errorf("sharepoint: %s: quotaGB %s is less than one megabyte", op, raw)
	}
	return quotaSettings{
		gb:             gb,
		warningPercent: pct,
		maxMB:          maxMB,
		warningMB:      int64(math.Floor(float64(maxMB) * pct / 100)),
	}, nil
}

// answer is what set-site-quota answers with: the site, and the quota as it was set,
// in the units a person ordered it in and in the megabytes SharePoint stores.
func (q quotaSettings) answer(siteURL string, complete bool) map[string]any {
	return map[string]any{
		"site":                  siteURL,
		"quotaGB":               json.Number(strconv.FormatFloat(q.gb, 'f', -1, 64)),
		"warningPercent":        json.Number(strconv.FormatFloat(q.warningPercent, 'f', -1, 64)),
		"storageMaximumLevelMB": json.Number(strconv.FormatInt(q.maxMB, 10)),
		"storageWarningLevelMB": json.Number(strconv.FormatInt(q.warningMB, 10)),
		"complete":              complete,
	}
}

// siteURLOf answers the absolute URL of a site reference without asking anybody, when
// the reference carries one: a URL, a host and path, or a bare host. A Graph site id
// does not, and answers "".
func siteURLOf(ref string) string {
	host, path, isID := parseSite(strings.TrimSpace(ref))
	if isID || host == "" {
		return ""
	}
	return "https://" + host + escapeSegments(path)
}

// adminBaseFor derives the tenant's admin host from one of its sites:
// contoso.sharepoint.com → https://contoso-admin.sharepoint.com, and the same in the
// national clouds (contoso.sharepoint.us). A site under a custom domain, or one that is
// already the admin host, does not say where administration lives; the Worker's
// credential names it then (adminUrl).
func adminBaseFor(siteURL string) (string, bool) {
	u, err := url.Parse(siteURL)
	if err != nil || u.Host == "" {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	label, rest, ok := strings.Cut(host, ".")
	if !ok || !strings.HasPrefix(rest, "sharepoint.") || label == "" || strings.HasSuffix(label, "-admin") {
		return "", false
	}
	// contoso-my.sharepoint.com is the tenant's OneDrive host; its admin host is the
	// tenant's own.
	label = strings.TrimSuffix(label, "-my")
	return "https://" + label + "-admin." + rest, true
}

// setSiteQuotaBody is the ProcessQuery request that sets one site's quota.
func setSiteQuotaBody(siteURL string, q quotaSettings) []byte {
	var esc bytes.Buffer
	_ = xml.EscapeText(&esc, []byte(siteURL))
	return []byte(`<Request AddExpandoFieldTypeSuffix="true" SchemaVersion="15.0.0.0" LibraryVersion="16.0.0.0" ApplicationName="Atlas" xmlns="` + csomNamespace + `">` +
		`<Actions>` +
		`<ObjectPath Id="2" ObjectPathId="1"/>` +
		`<ObjectPath Id="4" ObjectPathId="3"/>` +
		`<SetProperty Id="5" ObjectPathId="3" Name="StorageMaximumLevel"><Parameter Type="Int64">` + strconv.FormatInt(q.maxMB, 10) + `</Parameter></SetProperty>` +
		`<SetProperty Id="6" ObjectPathId="3" Name="StorageWarningLevel"><Parameter Type="Int64">` + strconv.FormatInt(q.warningMB, 10) + `</Parameter></SetProperty>` +
		`<ObjectPath Id="8" ObjectPathId="7"/>` +
		`<Query Id="9" ObjectPathId="7"><Query SelectAllProperties="false"><Properties><Property Name="IsComplete" ScalarProperty="true"/></Properties></Query></Query>` +
		`</Actions>` +
		`<ObjectPaths>` +
		`<Constructor Id="1" TypeId="` + tenantTypeID + `"/>` +
		`<Method Id="3" ParentId="1" Name="GetSitePropertiesByUrl"><Parameters><Parameter Type="String">` + esc.String() + `</Parameter><Parameter Type="Boolean">false</Parameter></Parameters></Method>` +
		`<Method Id="7" ParentId="3" Name="Update"/>` +
		`</ObjectPaths>` +
		`</Request>`)
}

// csomError is the ErrorInfo the client object model answers a refused request with.
type csomError struct {
	ErrorMessage  string `json:"ErrorMessage"`
	ErrorTypeName string `json:"ErrorTypeName"`
	ErrorCode     int64  `json:"ErrorCode"`
}

// readProcessQuery reads a ProcessQuery answer: a JSON array whose first element says
// whether the request failed (ErrorInfo), followed by the queried objects. It answers
// the refusal, if there was one, and whether the update reported itself complete.
func readProcessQuery(body []byte) (*csomError, bool, error) {
	var parts []json.RawMessage
	if err := json.Unmarshal(body, &parts); err != nil || len(parts) == 0 {
		return nil, false, fmt.Errorf("the answer is not a client object model response")
	}
	var head struct {
		ErrorInfo *csomError `json:"ErrorInfo"`
	}
	if err := json.Unmarshal(parts[0], &head); err != nil {
		return nil, false, fmt.Errorf("the answer is not a client object model response")
	}
	if head.ErrorInfo != nil {
		return head.ErrorInfo, false, nil
	}
	complete := true // an Update that answers nothing about itself has nothing pending
	for _, p := range parts[1:] {
		var op struct {
			IsComplete *bool `json:"IsComplete"`
		}
		if json.Unmarshal(p, &op) == nil && op.IsComplete != nil {
			complete = *op.IsComplete
		}
	}
	return nil, complete, nil
}

// setSiteQuota sets a site's storage quota through SharePoint's tenant administration.
//
// The refusals a tenant gives for good are [Refusal]s, so a process with a path for them
// hands the quota to a person at once: a credential the admin API does not take (a
// client secret), an app the admin API turns away (401/403, or the object model's
// UnauthorizedAccessException), and a quota the tenant refuses (any other error the
// object model reports — automatic storage management, a quota past what the tenant has
// left). A site the tenant does not know yet, a transport fault, throttling and a 5xx
// stay failures with their retries: each can change.
func (c *GraphClient) setSiteQuota(ctx context.Context, op string, req Request) (any, error) {
	q, err := readQuota(op, req)
	if err != nil {
		return nil, err
	}
	// Asked first, before anything is sent: a credential that cannot reach tenant
	// administration is a refusal whatever the site is.
	if c.admin == nil {
		return nil, refused(CodeAdminNotPermitted, op, "this Worker's credential cannot reach SharePoint's tenant administration; "+
			"it takes an app only with a certificate, so give the bundle a certificate or certificateThumbprint in place of the client secret")
	}
	siteURL := siteURLOf(req.Site)
	if siteURL == "" {
		// A Graph site id names no address, and tenant administration addresses a site
		// by its URL: Graph says which URL that is.
		site, err := c.getSite(ctx, op, req.Site)
		if err != nil {
			return nil, err
		}
		m, _ := site.(map[string]any)
		if exists, _ := m["exists"].(bool); !exists {
			return nil, fmt.Errorf("sharepoint: %s: there is no site %q", op, strings.TrimSpace(req.Site))
		}
		siteURL, _ = m["webUrl"].(string)
		if siteURL == "" {
			return nil, fmt.Errorf("sharepoint: %s: the site %q answered with no webUrl", op, strings.TrimSpace(req.Site))
		}
	}
	base := strings.TrimRight(strings.TrimSpace(c.admin.url), "/")
	if base == "" {
		derived, ok := adminBaseFor(siteURL)
		if !ok {
			return nil, fmt.Errorf("sharepoint: %s: the site %s does not say where the tenant's administration lives; "+
				"set adminUrl in the Worker's credential bundle (https://<tenant>-admin.sharepoint.com)", op, siteURL)
		}
		base = derived
	}
	tokens, err := c.admin.tokensFor(base)
	if err != nil {
		return nil, err
	}
	tok, err := tokens.Token(ctx)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/_vti_bin/client.svc/ProcessQuery", bytes.NewReader(setSiteQuotaBody(siteURL, q)))
	if err != nil {
		return nil, fmt.Errorf("sharepoint: build %s request: %w", op, err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+tok)
	httpReq.Header.Set("Content-Type", "text/xml")
	httpReq.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("sharepoint: %s: %w", op, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limits.Default().Variable+1))
	if err != nil {
		return nil, fmt.Errorf("sharepoint: read %s response: %w", op, err)
	}
	r := graphResponse{status: resp.StatusCode, header: resp.Header, body: raw}
	switch {
	case r.status == http.StatusUnauthorized || r.status == http.StatusForbidden:
		return nil, refused(CodeAdminNotPermitted, op, fmt.Sprintf("SharePoint's tenant administration at %s answered %d%s; "+
			"the app needs the SharePoint application permission Sites.FullControl.All with admin consent, and a certificate credential", base, r.status, quoteBody(r.body)))
	case !r.ok():
		return nil, fmt.Errorf("%w (SharePoint tenant administration)", graphFailure(op, http.MethodPost, r))
	}
	refusal, complete, err := readProcessQuery(r.body)
	if err != nil {
		return nil, fmt.Errorf("sharepoint: %s: %v%s", op, err, quoteBody(r.body))
	}
	if refusal != nil {
		msg := strings.TrimSpace(refusal.ErrorMessage)
		switch {
		case strings.Contains(refusal.ErrorTypeName, "UnauthorizedAccess"):
			return nil, refused(CodeAdminNotPermitted, op, "SharePoint's tenant administration refused the app: "+msg+
				"; the app needs the SharePoint application permission Sites.FullControl.All with admin consent")
		case strings.Contains(refusal.ErrorTypeName, "NotFound") || strings.Contains(refusal.ErrorTypeName, "NoSite") ||
			refusal.ErrorCode == -2147024894:
			// A site Microsoft provisions after its group may not be known to tenant
			// administration yet; that changes, so it keeps its retries.
			return nil, fmt.Errorf("sharepoint: %s: tenant administration does not know the site %s yet: %s", op, siteURL, msg)
		default:
			return nil, refused(CodeQuotaRefused, op, "SharePoint refused the quota for "+siteURL+": "+msg)
		}
	}
	return q.answer(siteURL, complete), nil
}

// quoteBody renders the start of an answer's body for an error message, or nothing.
func quoteBody(body []byte) string {
	quote := bytes.TrimSpace(body)
	if len(quote) == 0 {
		return ""
	}
	if int64(len(quote)) > limits.Default().ErrorBody {
		quote = quote[:limits.Default().ErrorBody]
	}
	return ": " + string(quote)
}
