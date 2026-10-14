package worker

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/rest"
	"github.com/pblumer/atlas/connector/webscrape"
)

// TestEveryRegisteredHandlerIsItsOwnKindsRunner: TestEveryKindServesItsOwnJobTypes
// proves the job types land in the right arm; this proves what each arm put under
// them. A job that arrives without its resolved detail is refused by the runner it
// reached, and that runner names its kind — so a job type wired to another kind's
// runner, or to a closure that dropped the job, shows here rather than as a task
// failing in a far system.
func TestEveryRegisteredHandlerIsItsOwnKindsRunner(t *testing.T) {
	// The prefix a kind's runner puts in front of its refusals, where it is not the
	// kind's own name: the three SQL products share one runner.
	prefix := map[string]string{"mariadb": "sqldb:", "mssql": "sqldb:", "postgres": "sqldb:"}
	for kind, jobTypes := range connectorJobTypes {
		t.Run(kind, func(t *testing.T) {
			built, err := BuiltinConnectors(envMap(configuredEnvFor(t, kind)), kind)
			if err != nil {
				t.Fatalf("BuiltinConnectors(%q): %v", kind, err)
			}
			want := prefix[kind]
			if want == "" {
				want = kind + ":"
			}
			for _, jobType := range slices.Sorted(slices.Values(jobTypes)) {
				h, ok := built.Handlers[jobType]
				if !ok {
					t.Fatalf("no handler under %s", jobType)
				}
				out, err := h.Run(context.Background(), Job{Type: jobType})
				if err == nil || !strings.HasPrefix(err.Error(), want) {
					t.Errorf("%s handler on a job with no detail = (%v, %v), want a refusal starting %q",
						jobType, out, err, want)
				}
			}
		})
	}
}

// discardingREST answers every call, so a test can see what the worker does with an
// answer the model asked to throw away.
type discardingREST struct{ calls int }

func (c *discardingREST) Do(context.Context, rest.Request) (rest.Response, error) {
	c.calls++
	return rest.Response{Body: map[string]any{"id": 7}}, nil
}

// oneValueScraper answers every scrape with one value.
type oneValueScraper struct{ calls int }

func (s *oneValueScraper) Scrape(context.Context, webscrape.Request) ([]string, error) {
	s.calls++
	return []string{"Headline"}, nil
}

// TestAResultNobodyAskedForCompletesWithNothing: a task naming no result variable
// still does its work — the call is made, the page is fetched — and completes with no
// variables, rather than writing the answer under an empty name.
func TestAResultNobodyAskedForCompletesWithNothing(t *testing.T) {
	client := &discardingREST{}
	out, err := runREST(context.Background(), Job{Connector: &ConnectorPayload{
		Kind: "rest", Fields: map[string]any{"method": "POST", "url": "https://api.example.com/tickets"},
	}}, client, nil)
	if err != nil || out != nil || client.calls != 1 {
		t.Fatalf("runREST = (%v, %v) after %d calls; want the call made and nothing written", out, err, client.calls)
	}

	scraper := &oneValueScraper{}
	out, err = runWebScrape(context.Background(), Job{Connector: &ConnectorPayload{
		Kind: "webscrape", Fields: map[string]any{"url": "https://example.com", "selector": "h1"},
	}}, scraper)
	if err != nil || out != nil || scraper.calls != 1 {
		t.Fatalf("runWebScrape = (%v, %v) after %d calls; want the page fetched and nothing written", out, err, scraper.calls)
	}
}
