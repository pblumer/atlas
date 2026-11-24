package api_test

import (
	"net/http"
	"strings"
	"testing"
)

// TestABodyPastItsBudgetIsRefusedNotRead: nineteen handlers read their request body
// with no ceiling at all, among them the one that places an order, which any
// signed-in account may call. A body of any size was read into memory before a
// single field of it was looked at, so one request could spend the memory of the
// one process that serves everybody. Each now reads against the budget of what it
// carries, and a body past it is answered 413 and not parsed.
//
// The bodies here are valid JSON of the shape each route expects, so that what
// refuses them is their size and nothing else.
func TestABodyPastItsBudgetIsRefusedNotRead(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "s3cret-pass")
	admin := newClient(t)
	if code := login(t, admin, ts, "admin", "s3cret-pass"); code != http.StatusOK {
		t.Fatalf("login: %d", code)
	}
	// Past the largest budget any of these routes reads against (ModelUpload, 4 MiB).
	big := strings.Repeat("a", 5<<20)
	for _, tc := range []struct {
		name, method, path, body string
	}{
		{"saving one's preferences", "PUT", "/api/v1/me/preferences", `{"appearance":"` + big + `"}`},
		{"placing an order", "POST", "/api/v1/orders", `{"note":"` + big + `"}`},
		{"deciding approvals", "POST", "/api/v1/approvals/decide", `{"comment":"` + big + `"}`},
		{"claiming a task", "POST", "/api/v1/tasks/1/claim", `{"assignee":"` + big + `"}`},
		{"completing a task", "POST", "/api/v1/tasks/1/complete", `{"variables":{"v":"` + big + `"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, body := cReq(t, admin, ts, tc.method, tc.path, tc.body)
			if code != http.StatusRequestEntityTooLarge {
				t.Errorf("%s %s with a %d MiB body = %d %.120s; want 413", tc.method, tc.path, len(tc.body)>>20, code, body)
			}
		})
	}
	// A body within the budget is still read: the same route answers on its merits.
	if code, body := cReq(t, admin, ts, "PUT", "/api/v1/me/preferences", `{"appearance":"dark"}`); code == http.StatusRequestEntityTooLarge {
		t.Errorf("an ordinary preferences body was refused as too large: %s", body)
	}
}
