package contentsource

import (
	"reflect"
	"testing"

	"github.com/pblumer/atlas/api/contentsource/wikifake"
)

// The adapter's reads that the gather and runner tests do not reach, against
// the fake source.

func TestAncestorsWalkFromThePageToTheRoot(t *testing.T) {
	f := wiki(t)
	ad := adapter(t, f)
	p, err := ad.Page(ctx(t), "it_c1")
	if err != nil {
		t.Fatal(err)
	}
	got, err := ad.Ancestors(ctx(t), p)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, a := range got {
		ids = append(ids, a.ID)
	}
	if want := []string{"it_de", "it_root"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("ancestors of it_c1 = %v, want %v", ids, want)
	}
}

func TestTheHeadIsTheFeedsPosition(t *testing.T) {
	f := wiki(t)
	ad := adapter(t, f)
	if head, err := ad.Head(ctx(t)); err != nil || head != 0 {
		t.Fatalf("head of an empty feed = %d, %v", head, err)
	}
	f.TouchAccount()
	if head, err := ad.Head(ctx(t)); err != nil || head != 1 {
		t.Fatalf("head after one entry = %d, %v", head, err)
	}
	f.Set(func(s *wikifake.Server) { s.Down = true })
	if _, err := ad.Head(ctx(t)); err == nil {
		t.Fatal("a source in an outage reported a head")
	}
}

// TestTheAdapterRefusesAnAddressOrTokenItCannotUseSafely: the token travels with
// every request, so the adapter is never built for an address that would expose
// it, or without one.
func TestTheAdapterRefusesAnAddressOrTokenItCannotUseSafely(t *testing.T) {
	for _, base := range []string{"", "http://wiki.example", "https://u:p@wiki.example", "https://wiki.example/?a=1"} {
		if _, err := NewWikiWikiBangBang(base, wikifake.Token, nil); err == nil {
			t.Errorf("built for base URL %q", base)
		}
	}
	if _, err := NewWikiWikiBangBang("https://wiki.example", "  ", nil); err == nil {
		t.Error("built without a token")
	}
}
