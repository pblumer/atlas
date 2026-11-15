package contentsource

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/limits"
)

// TestASourcePastItsBudgetIsRefusedAndNotStored: creating and replacing a content
// source read the body up to the configured budget, answer 413 past it, and leave
// the configuration as it was.
func TestASourcePastItsBudgetIsRefusedAndNotStored(t *testing.T) {
	f := wiki(t)
	reg := registry(t, f)
	svc := NewService(reg, func() int64 { return 5 })
	svc.Limits = limits.Default()
	svc.Limits.Request = 512

	before, err := reg.Sources()
	if err != nil {
		t.Fatal(err)
	}
	big := `{"id":"neu","padding":"` + strings.Repeat("a", 600) + `"}`

	rec := httptest.NewRecorder()
	svc.HandleCreate(rec, httptest.NewRequest("POST", "/x", strings.NewReader(big)))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("creating a source past its budget = %d (%s), want 413", rec.Code, rec.Body)
	}

	req := httptest.NewRequest("PUT", "/x", strings.NewReader(big))
	req.SetPathValue("id", "neu")
	rec = httptest.NewRecorder()
	svc.HandleUpdate(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("replacing a source past its budget = %d (%s), want 413", rec.Code, rec.Body)
	}

	after, err := reg.Sources()
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Errorf("%d sources are configured after the refusals, were %d", len(after), len(before))
	}
}
