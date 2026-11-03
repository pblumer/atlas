package entra

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/atlas/compiler"
)

// fastFollow shrinks how create-community follows an accepted operation, so a test
// runs in milliseconds; the package values are put back afterwards.
func fastFollow(t *testing.T, interval, bound time.Duration) {
	t.Helper()
	pi, fb := communityPollInterval, communityFollowBound
	communityPollInterval, communityFollowBound = interval, bound
	t.Cleanup(func() { communityPollInterval, communityFollowBound = pi, fb })
}

// communityGraph stands in for Graph's communities API as its documentation describes
// it: the POST answered 202, empty, with a Location; the operation polled there
// answering the statuses it is given in turn (the last one repeating); and the
// community readable by its id.
type communityGraph struct {
	t        *testing.T
	srv      *httptest.Server
	location func(srvURL string) string
	statuses []string
	detail   string
	// pollFailures answers that many polls 503 before the statuses start.
	pollFailures int

	mu    sync.Mutex
	posts int
	polls int
	reads int
	body  string
}

const communityOp = "/employeeExperience/engagementAsyncOperations('op-1')"

func newCommunityGraph(t *testing.T, statuses ...string) *communityGraph {
	g := &communityGraph{t: t, statuses: statuses,
		location: func(u string) string { return u + communityOp }}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/employeeExperience/communities":
			g.posts++
			raw, _ := io.ReadAll(r.Body)
			g.body = string(raw)
			w.Header().Set("Location", g.location(g.srv.URL))
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodGet && r.URL.Path == "/employeeExperience/engagementAsyncOperations('op-1')":
			g.polls++
			if g.polls <= g.pollFailures {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"error":{"code":"serviceUnavailable","message":"later"}}`))
				return
			}
			i := g.polls - g.pollFailures - 1
			if i >= len(g.statuses) {
				i = len(g.statuses) - 1
			}
			st := g.statuses[i]
			out := `{"id":"op-1","operationType":"createCommunity","status":"` + st + `"`
			if st == "succeeded" {
				out += `,"resourceId":"c-1","resourceLocation":"` + g.srv.URL + `/employeeExperience/communities('c-1')"`
			}
			if st == "failed" {
				out += `,"statusDetail":"` + g.detail + `"`
			}
			_, _ = w.Write([]byte(out + "}"))
		case r.Method == http.MethodGet && r.URL.Path == "/employeeExperience/communities/c-1":
			g.reads++
			_, _ = w.Write([]byte(`{"id":"c-1","displayName":"Lernende","description":"Für alle Lernenden","privacy":"private","groupId":"g-1"}`))
		default:
			g.t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *communityGraph) counts() (posts, polls, reads int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.posts, g.polls, g.reads
}

func communityJob() Job {
	return Job{Connector: "contoso", Operation: "create-community", UserID: "arno@contoso.com", ResultVariable: "community",
		Attributes: map[string]any{"displayName": "Lernende", "description": "Für alle Lernenden", "privacy": "private"}}
}

func runCommunity(ctx context.Context, t *testing.T, g *communityGraph) (map[string]any, error) {
	t.Helper()
	client := NewGraphClient(staticToken{tok: "tok-1"}, g.srv.URL, http.DefaultClient)
	return Run(ctx, communityJob(), regWith("contoso", client))
}

// The body is the authored community with the user bound as its first owner; an owner
// the model bound itself is sent as authored; and the authored map is never written to.
func TestCreateCommunityBindsTheOwnerAndLeavesTheAttributesAlone(t *testing.T) {
	attrs := map[string]any{"displayName": "Lernende", "privacy": "public"}
	c := &recordingClient{res: map[string]any{"id": "c-1"}}
	if _, err := Run(context.Background(), Job{
		Connector: "contoso", Operation: "create-community", UserID: " arno@contoso.com ", ResultVariable: "community", Attributes: attrs,
	}, regWith("contoso", c)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	body, _ := c.body.(map[string]any)
	owners, _ := body["owners@odata.bind"].([]any)
	if len(owners) != 1 || owners[0] != "https://graph.microsoft.com/v1.0/users/arno@contoso.com" {
		t.Errorf("owners@odata.bind = %#v, want the trimmed user under the worker's Graph root", body["owners@odata.bind"])
	}
	if body["displayName"] != "Lernende" || body["privacy"] != "public" {
		t.Errorf("body = %#v, want the authored attributes sent", body)
	}
	if _, has := attrs["owners@odata.bind"]; has || len(attrs) != 2 {
		t.Errorf("the authored attributes were written to: %#v", attrs)
	}

	authored := map[string]any{"displayName": "X", "owners@odata.bind": []any{"https://graph.microsoft.com/v1.0/users/jemand"}}
	got := withOwner(authored, "https://graph.microsoft.com/v1.0", "arno")
	if o, _ := got["owners@odata.bind"].([]any); len(o) != 1 || o[0] != "https://graph.microsoft.com/v1.0/users/jemand" {
		t.Errorf("an authored owners@odata.bind became %#v", got["owners@odata.bind"])
	}
}

// 202 + Location → the operation is polled until it has succeeded → the community it
// created is read by its id and is the answer. An absolute Location and a relative one
// are both followed, and the POST is sent once.
func TestCreateCommunityFollowsTheOperationToTheCommunity(t *testing.T) {
	fastFollow(t, time.Millisecond, 5*time.Second)
	for name, loc := range map[string]func(string) string{
		"absolute": func(u string) string { return u + communityOp },
		"relative": func(string) string { return communityOp },
	} {
		t.Run(name, func(t *testing.T) {
			g := newCommunityGraph(t, "notStarted", "running", "succeeded")
			g.location = loc
			out, err := runCommunity(context.Background(), t, g)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			c, _ := out["community"].(map[string]any)
			if c["id"] != "c-1" || c["groupId"] != "g-1" || c["privacy"] != "private" {
				t.Errorf("community = %#v, want the community read back by its id", out["community"])
			}
			posts, polls, reads := g.counts()
			if posts != 1 || polls != 3 || reads != 1 {
				t.Errorf("posts=%d polls=%d reads=%d, want one POST, three polls and one read", posts, polls, reads)
			}
			if !strings.Contains(g.body, `"owners@odata.bind":["`+g.srv.URL+`/users/arno@contoso.com"]`) {
				t.Errorf("body = %s, want the owner bound", g.body)
			}
		})
	}
}

// A failed operation is a failed job, and the error carries Graph's statusDetail.
func TestCreateCommunityFailedOperationFailsTheJob(t *testing.T) {
	fastFollow(t, time.Millisecond, 5*time.Second)
	g := newCommunityGraph(t, "running", "failed")
	g.detail = "displayName already in use"
	_, err := runCommunity(context.Background(), t, g)
	if err == nil || !strings.Contains(err.Error(), "displayName already in use") {
		t.Fatalf("err = %v, want it to name the statusDetail", err)
	}
	if _, _, reads := g.counts(); reads != 0 {
		t.Errorf("a failed operation's community was read %d times", reads)
	}
}

// Still running when the bound is reached: the job completes with a null id, the last
// status and the operation — and posts nothing a second time, which a failure's retry
// would.
func TestCreateCommunityStillRunningAtTheBoundCompletesWithANullID(t *testing.T) {
	fastFollow(t, time.Millisecond, 30*time.Millisecond)
	g := newCommunityGraph(t, "notStarted", "running")
	out, err := runCommunity(context.Background(), t, g)
	if err != nil {
		t.Fatalf("Run: %v, want the job to complete", err)
	}
	c, ok := out["community"].(map[string]any)
	if !ok {
		t.Fatalf("result = %#v, want an object", out)
	}
	if id, present := c["id"]; !present || id != nil {
		t.Errorf("id = %#v, want a present null a model can route on", c["id"])
	}
	if c["status"] != "running" || c["operation"] != g.srv.URL+communityOp {
		t.Errorf("result = %#v, want the last status and the operation's URL", c)
	}
	posts, polls, _ := g.counts()
	if posts != 1 || polls < 2 {
		t.Errorf("posts=%d polls=%d, want exactly one POST and the operation polled", posts, polls)
	}
}

// A throttled or unavailable answer while following is waited out like a running
// operation, not a failure that would post again.
func TestCreateCommunityWaitsOutATransientPoll(t *testing.T) {
	fastFollow(t, time.Millisecond, 5*time.Second)
	g := newCommunityGraph(t, "succeeded")
	g.pollFailures = 2
	out, err := runCommunity(context.Background(), t, g)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if c, _ := out["community"].(map[string]any); c["id"] != "c-1" {
		t.Errorf("community = %#v", out["community"])
	}
}

// A Location on another host is refused, not followed: following it would hand the
// bearer to whoever wrote the answer.
func TestCreateCommunityRefusesAForeignLocation(t *testing.T) {
	fastFollow(t, time.Millisecond, 5*time.Second)
	g := newCommunityGraph(t, "succeeded")
	g.location = func(string) string { return "https://evil.example/v1.0" + communityOp }
	_, err := runCommunity(context.Background(), t, g)
	if err == nil || !strings.Contains(err.Error(), "evil.example") {
		t.Fatalf("err = %v, want the foreign host refused by name", err)
	}
	if posts, polls, reads := g.counts(); posts != 1 || polls != 0 || reads != 0 {
		t.Errorf("posts=%d polls=%d reads=%d, want nothing after the POST", posts, polls, reads)
	}
}

// A cancelled context stops the follow at once rather than at the bound.
func TestCreateCommunityHonoursCancellation(t *testing.T) {
	fastFollow(t, time.Hour, time.Hour)
	g := newCommunityGraph(t, "running")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	start := time.Now()
	_, err := runCommunity(ctx, t, g)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("the follow took %v to notice the cancellation", time.Since(start))
	}
}

// A 202 that names no operation cannot be followed, but Graph accepted the community:
// the job completes with a null id rather than failing into a second POST.
func TestCreateCommunityWithoutALocationCompletes(t *testing.T) {
	c := &recordingClient{res: nil}
	out, err := Run(context.Background(), communityJob(), regWith("contoso", c))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if m, _ := out["community"].(map[string]any); m == nil || m["id"] != nil || m["status"] != "unknown" {
		t.Errorf("result = %#v, want a null id with status unknown", out)
	}
	// And it needs somewhere to put the id.
	j := communityJob()
	j.ResultVariable = ""
	if _, err := Run(context.Background(), j, regWith("contoso", c)); err == nil || !strings.Contains(err.Error(), "resultVariable") {
		t.Errorf("err = %v, want the missing resultVariable named", err)
	}
	j = communityJob()
	j.UserID = ""
	if _, err := Run(context.Background(), j, regWith("contoso", c)); err == nil || !strings.Contains(err.Error(), "userId") {
		t.Errorf("err = %v, want the missing owner named", err)
	}
}

// The client turns a relative Location into one on its own endpoint without doubling
// the base's version path, and returns nil for a bodiless 2xx without one.
func TestGraphClientResolvesAnAcceptedLocation(t *testing.T) {
	c := NewGraphClient(staticToken{}, "https://graph.microsoft.com/v1.0", nil)
	for loc, want := range map[string]string{
		"/v1.0/employeeExperience/engagementAsyncOperations('a')":                            "https://graph.microsoft.com/v1.0/employeeExperience/engagementAsyncOperations('a')",
		"/employeeExperience/engagementAsyncOperations('a')":                                 "https://graph.microsoft.com/v1.0/employeeExperience/engagementAsyncOperations('a')",
		"employeeExperience/engagementAsyncOperations('a')":                                  "https://graph.microsoft.com/v1.0/employeeExperience/engagementAsyncOperations('a')",
		"https://graph.microsoft.com/v1.0/employeeExperience/engagementAsyncOperations('a')": "https://graph.microsoft.com/v1.0/employeeExperience/engagementAsyncOperations('a')",
	} {
		got, err := c.accepted(loc)
		if err != nil {
			t.Errorf("%s: %v", loc, err)
			continue
		}
		if a, _ := got.(Accepted); a.Location != want {
			t.Errorf("%s resolved to %#v, want %s", loc, got, want)
		}
	}
	if got, err := c.accepted(" "); got != nil || err != nil {
		t.Errorf("an empty Location = %#v, %v; want nil", got, err)
	}
	if _, err := c.accepted("https://graph.microsoft.com.evil.example/v1.0/x"); err == nil {
		t.Error("a Location on another host must be refused")
	}
}

// The compiler takes create-community with an owner, a community body and a result
// variable, and refuses it without any of them — the result variable included, with
// the reason a create needs one.
func TestCompilerTakesCreateCommunity(t *testing.T) {
	for _, attrs := range []string{
		`connector="c" operation="create-community" userId="=antrag.owner" attributesVariable="community" resultVariable="r"`,
		`connector="c" operation="create-community" userId="u" attributes="{&#34;displayName&#34;:&#34;=antrag.name&#34;,&#34;privacy&#34;:&#34;private&#34;}" resultVariable="r"`,
	} {
		if _, err := compiler.Parse(1, 1, strings.NewReader(entraTaskBPMN(attrs))); err != nil {
			t.Errorf("%s: %v", attrs, err)
		}
	}
	for _, tc := range []struct{ attrs, want string }{
		{`connector="c" operation="create-community" attributesVariable="a" resultVariable="r"`, "userId"},
		{`connector="c" operation="create-community" userId="u" resultVariable="r"`, "attributesVariable"},
		{`connector="c" operation="create-community" userId="u" attributesVariable="a"`, "followed up"},
		{`connector="c" operation="create-community" userId="u" attributesVariable="a" resultVariable="r" select="id"`, "select"},
	} {
		_, err := compiler.Parse(1, 1, strings.NewReader(entraTaskBPMN(tc.attrs)))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to mention %q", tc.attrs, err, tc.want)
		}
	}
}

// scriptedClient answers each request with the next scripted reply, so the follow's
// rarer branches can be driven without a server.
type scriptedClient struct {
	replies []scriptedReply
	calls   int
}

type scriptedReply struct {
	res any
	err error
}

func (c *scriptedClient) BaseURL() string { return "https://graph.example/v1.0" }

func (c *scriptedClient) Call(context.Context, Request) (any, error) {
	i := c.calls
	c.calls++
	if i >= len(c.replies) {
		i = len(c.replies) - 1
	}
	return c.replies[i].res, c.replies[i].err
}

func runScripted(t *testing.T, replies ...scriptedReply) (any, error) {
	t.Helper()
	client := &scriptedClient{replies: replies}
	out, err := Run(context.Background(), communityJob(), regWith("contoso", client))
	if err != nil {
		return nil, err
	}
	return out["community"], nil
}

// The answers Microsoft does not describe, each with the outcome that keeps a retry
// from creating a second community or names what went wrong.
func TestCreateCommunityUnusualAnswers(t *testing.T) {
	fastFollow(t, time.Millisecond, 20*time.Millisecond)
	accepted := scriptedReply{res: Accepted{Location: "https://graph.example/v1.0" + communityOp}}
	notFound := &GraphError{Status: http.StatusNotFound, Code: "NotFound"}

	// A body on the POST is the community itself.
	if c, err := runScripted(t, scriptedReply{res: map[string]any{"id": "c-9"}}); err != nil || c.(map[string]any)["id"] != "c-9" {
		t.Errorf("a body on the POST: %#v, %v", c, err)
	}
	// An empty 2xx without a Location is a community not known yet.
	if c, err := runScripted(t, scriptedReply{}); err != nil || c.(map[string]any)["status"] != "unknown" {
		t.Errorf("an empty answer: %#v, %v", c, err)
	}
	// Anything else is not an answer this operation can give.
	if _, err := runScripted(t, scriptedReply{res: "text"}); err == nil {
		t.Error("a text answer completed")
	}
	// A failed operation without a statusDetail says so.
	if _, err := runScripted(t, accepted, scriptedReply{res: map[string]any{"status": "failed"}}); err == nil || !strings.Contains(err.Error(), "no statusDetail") {
		t.Errorf("failed without detail: %v", err)
	}
	// Succeeded without naming the community: completes, pending, never fails.
	if c, err := runScripted(t, accepted, scriptedReply{res: map[string]any{"status": "succeeded"}}); err != nil || c.(map[string]any)["id"] != nil {
		t.Errorf("succeeded without a resourceId: %#v, %v", c, err)
	}
	// An operation that is not an object, and a refusal that is not transient, fail.
	if _, err := runScripted(t, accepted, scriptedReply{res: []any{}}); err == nil {
		t.Error("an operation answered as a list completed")
	}
	if _, err := runScripted(t, accepted, scriptedReply{err: &GraphError{Status: http.StatusForbidden, Code: "Forbidden"}}); err == nil {
		t.Error("a forbidden poll completed")
	}
	// The community is known but cannot be read before the bound: its id is the answer.
	c, err := runScripted(t, accepted, scriptedReply{res: map[string]any{"status": "succeeded", "resourceId": "c-2"}}, scriptedReply{err: notFound})
	if err != nil || c.(map[string]any)["id"] != "c-2" || c.(map[string]any)["status"] != "succeeded" {
		t.Errorf("a community still replicating: %#v, %v", c, err)
	}
	// Read back as something other than an object, or failing outside Graph: an error.
	if _, err := runScripted(t, accepted, scriptedReply{res: map[string]any{"status": "succeeded", "resourceId": "c-3"}}, scriptedReply{res: "x"}); err == nil {
		t.Error("a community read back as text completed")
	}
	if _, err := runScripted(t, accepted, scriptedReply{res: map[string]any{"status": "succeeded", "resourceId": "c-4"}}, scriptedReply{err: errors.New("network")}); err == nil {
		t.Error("a community read that failed outside Graph completed")
	}
}
