package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/limits"
)

// A body over its budget, on routes that used to read whatever they were sent.
//
// Each case sends a body that is well-formed and would have been accepted — the size
// sits in a field the route does not read — so before the bound the answer was a
// success and a change of state. The bound has to turn that into a refusal that says
// what is wrong (413, not a parse error on a body that parsed) and leaves everything
// as it was.

// padded returns a JSON object with the given fields plus one unread field that
// brings the whole body just over size bytes.
func padded(fields string, size int64) string {
	return `{` + fields + `,"padding":"` + strings.Repeat("a", int(size)) + `"}`
}

// TestABodyOverItsBudgetIsRefusedAndChangesNothing covers the three areas the routes
// came from: an order, a person's preferences, an approver's decision.
func TestABodyOverItsBudgetIsRefusedAndChangesNothing(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	alice := twoUsers(t, ts, admin, "alice", "mallory")[0]
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/deployments", collectiveBPMN); code != http.StatusOK {
		t.Fatalf("deploy approval model: %d (%s)", code, b)
	}
	release := aCatalogueOffering(t, ts, admin, "laptop")
	budgets := limits.Default()

	ordersOf := func(c *http.Client) int {
		t.Helper()
		code, body := cReq(t, c, ts, "GET", "/api/v1/orders", "")
		var list []json.RawMessage
		if err := json.Unmarshal(body, &list); code != http.StatusOK || err != nil {
			t.Fatalf("list orders: %d %v (%s)", code, err, body)
		}
		return len(list)
	}

	t.Run("an order", func(t *testing.T) {
		code, body := cReq(t, admin, ts, "POST", "/api/v1/orders",
			padded(`"releaseId":"`+release+`","items":["laptop"]`, budgets.Settings))
		if code != http.StatusRequestEntityTooLarge {
			t.Errorf("an order over its budget = %d (%.120s), want 413", code, body)
		}
		if n := ordersOf(admin); n != 0 {
			t.Errorf("%d orders exist after the refusal", n)
		}
		// The same order at an ordinary size is placed as before.
		if code, body := cReq(t, admin, ts, "POST", "/api/v1/orders",
			padded(`"releaseId":"`+release+`","items":["laptop"]`, budgets.Request)); code != http.StatusCreated {
			t.Errorf("an order within its budget = %d (%.120s), want 201", code, body)
		}
	})

	t.Run("a person's preferences", func(t *testing.T) {
		code, body := cReq(t, alice, ts, "PUT", "/api/v1/me/preferences",
			padded(`"language":"de"`, budgets.Request))
		if code != http.StatusRequestEntityTooLarge {
			t.Errorf("preferences over their budget = %d (%.120s), want 413", code, body)
		}
		code, body = cReq(t, alice, ts, "GET", "/api/v1/me/preferences", "")
		var prefs struct {
			Language string `json:"language"`
		}
		if err := json.Unmarshal(body, &prefs); code != http.StatusOK || err != nil {
			t.Fatalf("read preferences: %d %v (%s)", code, err, body)
		}
		if prefs.Language != "" {
			t.Errorf("the refused preferences were stored: language = %q", prefs.Language)
		}
		if code, body := cReq(t, alice, ts, "PUT", "/api/v1/me/preferences", `{"language":"de"}`); code != http.StatusOK {
			t.Errorf("preferences within their budget = %d (%.120s), want 200", code, body)
		}
	})

	t.Run("an approver's decision", func(t *testing.T) {
		aRequestFor(t, ts, admin, release, "laptop")
		held := approvalsOf(t, ts, alice)
		if len(held) != 1 {
			t.Fatalf("alice holds %d approvals, want the one position", len(held))
		}
		fields := fmt.Sprintf(`"approved":true,"reason":"in Ordnung","taskKeys":[%d]`, held[0].Task.Key)
		code, body := cReq(t, alice, ts, "POST", "/api/v1/approvals/decide", padded(fields, budgets.Request))
		if code != http.StatusRequestEntityTooLarge {
			t.Errorf("a decision over its budget = %d (%.120s), want 413", code, body)
		}
		if rest := approvalsOf(t, ts, alice); len(rest) != 1 {
			t.Errorf("alice holds %d approvals after the refusal, want the position still open", len(rest))
		}
		if code, body := cReq(t, alice, ts, "POST", "/api/v1/approvals/decide", `{`+fields+`}`); code != http.StatusOK {
			t.Errorf("a decision within its budget = %d (%.120s), want 200", code, body)
		}
	})
}

// TestACompletionCarriesAWorkersAnswerAndNoMore: completing a job or a task is where a
// Worker's answer and a person's form arrive, so its bound is not the one an ordinary
// command has. A variable as large as a variable may be still completes; a body past
// what a Worker's answer may be is refused, and the work stays open.
func TestACompletionCarriesAWorkersAnswerAndNoMore(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	alice := twoUsers(t, ts, admin, "alice", "mallory")[0]
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/deployments", collectiveBPMN); code != http.StatusOK {
		t.Fatalf("deploy: %d (%s)", code, b)
	}
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/instances", `{"processId":"sammel-genehmigung"}`); code != http.StatusOK {
		t.Fatalf("start: %d (%s)", code, b)
	}
	budgets := limits.Default()

	openTask := func() uint64 {
		t.Helper()
		code, body := cReq(t, alice, ts, "GET", "/api/v1/tasks", "")
		var page struct {
			Items []struct {
				Key uint64 `json:"key"`
			} `json:"items"`
		}
		if err := json.Unmarshal(body, &page); code != http.StatusOK || err != nil {
			t.Fatalf("list tasks: %d %v (%.200s)", code, err, body)
		}
		if len(page.Items) != 1 {
			t.Fatalf("alice has %d open tasks, want one (%.200s)", len(page.Items), body)
		}
		return page.Items[0].Key
	}
	key := openTask()
	path := fmt.Sprintf("/api/v1/tasks/%d/complete", key)

	over := `{"variables":{"note":"` + strings.Repeat("a", int(budgets.Payload+budgets.Request)) + `"}}`
	if code, body := cReq(t, alice, ts, "POST", path, over); code != http.StatusRequestEntityTooLarge {
		t.Errorf("a completion past a Worker's answer = %d (%.120s), want 413", code, body)
	}
	if again := openTask(); again != key {
		t.Errorf("the task changed after the refused completion: %d, was %d", again, key)
	}

	// Far over an ordinary command's budget, and within a variable's: this is what
	// the bound must keep letting through.
	large := `{"variables":{"note":"` + strings.Repeat("a", int(budgets.Variable/2)) + `"}}`
	if code, body := cReq(t, alice, ts, "POST", path, large); code != http.StatusOK {
		t.Errorf("a completion carrying a large variable = %d (%.200s), want 200", code, body)
	}
}
