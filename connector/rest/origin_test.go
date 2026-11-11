package rest

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// What a REST task's breaker is keyed by: the host it calls, not the route — a host's
// outage is one outage, whichever paths the models use on it.
func TestOriginIsTheHostNotTheRoute(t *testing.T) {
	for raw, want := range map[string]string{
		"https://API.example.com/kunden?x=1":    "https://api.example.com",
		"http://127.0.0.1:8080/api/v1/orders/1": "http://127.0.0.1:8080",
		"http://h/a#1/start":                    "http://h",
		"  https://h:8443  ":                    "https://h:8443",
		"/relative/path":                        "",
		"":                                      "",
		"::not a url":                           "",
	} {
		if got := originOf(raw); got != want {
			t.Errorf("originOf(%q) = %q, want %q", raw, got, want)
		}
	}
	if got := Origin(nil, &compiler.ConnectorTaskDetail{Url: compiler.RestExpr{Literal: "https://a.example/x"}}, nil, 0); got != "https://a.example" {
		t.Errorf("Origin of a literal url = %q, want its host, read without touching the store", got)
	}
	if got := Origin(nil, nil, nil, 0); got != "" {
		t.Errorf("Origin with no detail = %q, want none", got)
	}
}
