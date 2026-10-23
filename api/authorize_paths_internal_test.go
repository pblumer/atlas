package api

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/vault"
)

// The refusals of the authorization and approval routes that the scenarios do not
// reach: malformed requests, answers nobody may give or that would not change
// anything, and a permitted action whose credential cannot be had.

func TestTheAuthorizeRouteRefusesWhatIsNotALease(t *testing.T) {
	r := newPEPRig(t)
	r.govern("service", "enforce", "")
	r.startAsAlice()
	job, lease := r.lease()
	jobKey := strconv.FormatUint(job, 10)
	for name, tc := range map[string]struct {
		key, body string
		want      int
	}{
		"a key that is not a number": {"x", `{"worker":"sharepoint","leaseToken":1}`, http.StatusBadRequest},
		"a body that is not JSON":    {jobKey, `{`, http.StatusBadRequest},
		"no worker":                  {jobKey, `{"leaseToken":1}`, http.StatusBadRequest},
		"no lease token":             {jobKey, `{"worker":"sharepoint"}`, http.StatusBadRequest},
		"a job that does not exist":  {"999999", `{"worker":"sharepoint","leaseToken":1}`, http.StatusNotFound},
		"another worker's lease":     {jobKey, `{"worker":"someone-else","leaseToken":` + strconv.FormatUint(lease, 10) + `}`, http.StatusConflict},
		"a lease token not its own":  {jobKey, `{"worker":"sharepoint","leaseToken":` + strconv.FormatUint(lease+1, 10) + `}`, http.StatusConflict},
	} {
		t.Run(name, func(t *testing.T) {
			if code, raw := r.asWith(r.internal, http.Header{brokerKeyHeader: []string{r.srv.brokerKey}}, r.srv.handleAuthorizeJob,
				http.MethodPost, "/", tc.body, map[string]string{"key": tc.key}); code != tc.want {
				t.Fatalf("%d, want %d: %s", code, tc.want, raw)
			}
		})
	}
}

// A permitted delegated action whose connection has lost its credential is answered
// 502 — the decision stands, recorded, and nothing is released.
func TestAPermitWithoutACredentialReleasesNothing(t *testing.T) {
	r := newPEPRig(t)
	if _, err := r.srv.policies.SaveDraft("hr-documents", []byte(membersMayDelete), "usr_admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.srv.policies.Publish("hr-documents", "usr_admin"); err != nil {
		t.Fatal(err)
	}
	r.linkAlice()
	r.govern("delegated-per-job", "enforce", `,"actAs":"initiator"`)
	r.srv.do(func() {
		if err := r.srv.vault.Delete(vault.ConnectionCredentialName(r.conn.ID)); err != nil {
			t.Fatal(err)
		}
	})
	pi := r.startAsAlice()
	job, lease := r.lease()
	code, raw := r.asWith(r.internal, http.Header{brokerKeyHeader: []string{r.srv.brokerKey}}, r.srv.handleAuthorizeJob,
		http.MethodPost, "/", `{"worker":"sharepoint","leaseToken":`+strconv.FormatUint(lease, 10)+`}`,
		map[string]string{"key": strconv.FormatUint(job, 10)})
	if code != http.StatusBadGateway || strings.Contains(string(raw), "accessToken") {
		t.Fatalf("authorize: %d %s", code, raw)
	}
	ev := r.evidence(pi)
	if len(ev) != 1 || ev[0].Effect != "Permit" {
		t.Fatalf("evidence = %+v", ev)
	}
}

func TestTheApprovalRoutesRefuseWhatTheyCannotAnswer(t *testing.T) {
	r := newPEPRig(t)
	r.linkAlice()
	r.govern("delegated-per-job", "enforce", `,"actAs":"initiator"`)
	pi := r.startAsAlice()
	job, lease := r.lease()
	if code, a := r.authorize(r.internal, job, lease); code != http.StatusOK || a.ApprovalKey == 0 {
		t.Fatalf("first attempt: %d %+v", code, a)
	} else {
		instance, approval := strconv.FormatUint(pi, 10), strconv.FormatUint(a.ApprovalKey, 10)
		answer := func(p *httpapi.Principal, key, ap, body string) int {
			c, _ := r.as(p, r.srv.handleDecideActionApproval, http.MethodPost, "/", body,
				map[string]string{"key": key, "approval": ap})
			return c
		}
		carol := &httpapi.Principal{UserID: "usr_carol", Username: "carol", Roles: []string{RoleUser}, Session: true}
		for name, tc := range map[string]struct {
			p             *httpapi.Principal
			key, ap, body string
			want          int
		}{
			"an instance key that is not a number": {r.bob, "x", approval, `{"outcome":"granted"}`, http.StatusBadRequest},
			"an approval key that is not a number": {r.bob, instance, "x", `{"outcome":"granted"}`, http.StatusBadRequest},
			"a body that is not JSON":              {r.bob, instance, approval, `{`, http.StatusBadRequest},
			"an outcome nobody defined":            {r.bob, instance, approval, `{"outcome":"maybe"}`, http.StatusBadRequest},
			"an approval that does not exist":      {r.bob, instance, "999999", `{"outcome":"granted"}`, http.StatusNotFound},
			"somebody outside the approver group":  {carol, instance, approval, `{"outcome":"granted"}`, http.StatusForbidden},
			"withdrawing an answer never given":    {r.bob, instance, approval, `{"outcome":"withdrawn"}`, http.StatusConflict},
		} {
			if got := answer(tc.p, tc.key, tc.ap, tc.body); got != tc.want {
				t.Errorf("%s: %d, want %d", name, got, tc.want)
			}
		}
		// A rejection is an answer like a grant; it can be withdrawn, and only then
		// answered again.
		if got := answer(r.bob, instance, approval, `{"outcome":"rejected","reason":"not this one"}`); got != http.StatusOK {
			t.Fatalf("reject: %d", got)
		}
		if got := answer(r.bob, instance, approval, `{"outcome":"granted"}`); got != http.StatusConflict {
			t.Fatalf("answering twice: %d", got)
		}
		if got := answer(r.bob, instance, approval, `{"outcome":"withdrawn"}`); got != http.StatusOK {
			t.Fatalf("withdraw: %d", got)
		}
		if got := answer(r.bob, instance, approval, `{"outcome":"granted"}`); got != http.StatusOK {
			t.Fatalf("answer after withdrawing: %d", got)
		}
	}
	// The two listings refuse a key that is not one.
	for name, h := range map[string]http.HandlerFunc{
		"authorizations":   r.srv.handleInstanceAuthorizations,
		"action approvals": r.srv.handleInstanceActionApprovals,
	} {
		if code, _ := r.as(r.admin, h, http.MethodGet, "/", "", map[string]string{"key": "x"}); code != http.StatusBadRequest {
			t.Errorf("%s with a bad key: %d", name, code)
		}
	}
}
