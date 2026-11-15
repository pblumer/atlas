package order

import (
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/limits"
)

// TestAnOrderBodyPastItsBudgetIsRefused: placing an order, reporting a line and
// recording a decision each read their body up to a budget and answer 413 past it.
// A basket is held to a form payload's budget, the two commands to an ordinary
// request's; nothing is stored by a refused placement.
func TestAnOrderBodyPastItsBudgetIsRefused(t *testing.T) {
	s := newService(t)
	s.Limits = limits.Default()
	s.Limits.Settings = 1024
	s.Limits.Request = 256
	p := &httpapi.Principal{UserID: "usr_kunde", Username: "kunde"}
	padding := func(n int) string { return `"padding":"` + strings.Repeat("a", n) + `"` }

	if rec := do(t, s.HandlePlace, p, "POST", `{"releaseId":"x",`+padding(1100)+`}`); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("an order past its budget = %d (%s), want 413", rec.Code, rec.Body)
	}
	if all, err := s.store.For(p.UserID); err != nil || len(all) != 0 {
		t.Errorf("after the refusal the store holds %d orders (err %v)", len(all), err)
	}
	// A basket larger than an ordinary command still reaches the order logic: it is
	// refused for what it says, not for its size.
	if rec := do(t, s.HandlePlace, p, "POST", `{"releaseId":"x",`+padding(600)+`}`); rec.Code == http.StatusRequestEntityTooLarge {
		t.Errorf("a basket within a form payload's budget was refused for its size (%s)", rec.Body)
	}

	if rec := do(t, s.HandleReport, p, "POST", `{"status":"done",`+padding(300)+`}`, "id", "ord_x", "item", "laptop"); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("a report past its budget = %d (%s), want 413", rec.Code, rec.Body)
	}
	if rec := do(t, s.HandleDecide, p, "POST", `{"by":"alice",`+padding(300)+`}`, "id", "ord_x", "item", "laptop"); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("a decision past its budget = %d (%s), want 413", rec.Code, rec.Body)
	}
}
