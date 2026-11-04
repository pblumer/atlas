package sharepoint

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// The paths of set-site-quota that end before the admin host answers: the site Graph
// does not know or names without an address, an admin host that cannot be derived, a
// token that cannot be had, and an admin host that cannot be reached.
func TestSetSiteQuotaStopsBeforeTheAdminHostAnswers(t *testing.T) {
	graphSite := func(body string, status int) func(http.ResponseWriter, *http.Request, []byte) {
		return func(w http.ResponseWriter, r *http.Request, _ []byte) {
			if strings.HasSuffix(r.URL.Path, "/drive") {
				answer(w, http.StatusOK, `{"quota":{"total":1}}`)
				return
			}
			answer(w, status, body)
		}
	}
	t.Run("a Graph site id Graph does not know", func(t *testing.T) {
		_, c, _ := quotaClient(t, graphSite(`{"error":{"code":"itemNotFound","message":"x"}}`, http.StatusNotFound))
		_, err := c.Do(context.Background(), Request{Operation: OpSetSiteQuota, Site: siteID, QuotaGB: "2"})
		if err == nil || !strings.Contains(err.Error(), "there is no site") {
			t.Fatalf("err = %v, want it to say there is no such site", err)
		}
	})
	t.Run("a Graph site without a webUrl", func(t *testing.T) {
		_, c, _ := quotaClient(t, graphSite(`{"id":"`+siteID+`"}`, http.StatusOK))
		_, err := c.Do(context.Background(), Request{Operation: OpSetSiteQuota, Site: siteID, QuotaGB: "2"})
		if err == nil || !strings.Contains(err.Error(), "no webUrl") {
			t.Fatalf("err = %v, want it to name the missing webUrl", err)
		}
	})
	t.Run("a Graph lookup that fails", func(t *testing.T) {
		_, c, _ := quotaClient(t, graphSite(``, http.StatusInternalServerError))
		if _, err := c.Do(context.Background(), Request{Operation: OpSetSiteQuota, Site: siteID, QuotaGB: "2"}); err == nil {
			t.Fatal("completed although Graph failed")
		}
	})
	t.Run("an admin host derived from the site", func(t *testing.T) {
		_, c, scopes := quotaClient(t, graphSite(``, http.StatusOK))
		c.admin.url = ""
		boom := errors.New("no token for you")
		c.admin.newTokens = func(scope string) (TokenSource, error) {
			*scopes = append(*scopes, scope)
			return nil, boom
		}
		_, err := c.Do(context.Background(), Request{Operation: OpSetSiteQuota, Site: "contoso.sharepoint.com:/sites/x", QuotaGB: "2"})
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v, want the token source's error", err)
		}
		if len(*scopes) != 1 || !strings.Contains((*scopes)[0], "contoso-admin.sharepoint.com") {
			t.Errorf("scopes = %v, want the derived admin host", *scopes)
		}
	})
	t.Run("a custom domain without adminUrl", func(t *testing.T) {
		_, c, _ := quotaClient(t, graphSite(``, http.StatusOK))
		c.admin.url = ""
		_, err := c.Do(context.Background(), Request{Operation: OpSetSiteQuota, Site: "intranet.example.com:/sites/x", QuotaGB: "2"})
		if err == nil || !strings.Contains(err.Error(), "adminUrl") {
			t.Fatalf("err = %v, want it to ask for adminUrl", err)
		}
	})
	t.Run("a token that cannot be had", func(t *testing.T) {
		_, c, _ := quotaClient(t, graphSite(``, http.StatusOK))
		boom := errors.New("token endpoint down")
		c.admin.newTokens = func(string) (TokenSource, error) { return stubTokens{err: boom}, nil }
		_, err := c.Do(context.Background(), Request{Operation: OpSetSiteQuota, Site: "contoso.sharepoint.com:/sites/x", QuotaGB: "2"})
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v, want the token error", err)
		}
	})
	t.Run("an admin host that cannot be reached", func(t *testing.T) {
		fg, c, _ := quotaClient(t, graphSite(``, http.StatusOK))
		fg.srv.Close()
		_, err := c.Do(context.Background(), Request{Operation: OpSetSiteQuota, Site: "contoso.sharepoint.com:/sites/x", QuotaGB: "2"})
		if err == nil || !strings.Contains(err.Error(), OpSetSiteQuota) {
			t.Fatalf("err = %v, want a failure naming the operation", err)
		}
		var r *Refusal
		if errors.As(err, &r) {
			t.Errorf("err = refusal %s, want an ordinary failure that keeps its retries", r.Code)
		}
	})
}
