package api

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The handbook's recipe chapter (web/handbuch.html, #rezepte) promises a reader that
// every recipe can be tried from the page: "▶ Ausprobieren" deploys the model and starts
// it with no variables at all, and the card's "So probierst du es aus" list names each
// further step, most of them with a button that plays the worker, the person, the
// message sender or the operator the recipe waits for.
//
// examples/recipes_test.go proves the models compile. This test proves the promise:
// for every card it does exactly what a reader's clicks do — deploy (the card's DMN
// decision first, when it carries one), start the way the card starts, press every
// step button that is not marked as an alternative, in page order — and then requires
// that the run completed at the end event the card names (data-end), with no incident
// and no variable left null.
//
// The last condition is the one a reader actually ran into: the net-amount recipe read
// a variable `gross` nobody supplied, completed "successfully" and wrote net = null.
// A model that reads input must declare it as a start variable with a default
// (ADR-0455), so that the empty start the button makes is a meaningful one.

var (
	recipeCardRE   = regexp.MustCompile(`(?s)<div class="recipe" id="([^"]+)"([^>]*)>(.*?)\n      </div>\n`)
	recipeAttrRE   = regexp.MustCompile(`data-(proc|start|end)="([^"]*)"`)
	recipeBPMNRE   = regexp.MustCompile(`(?s)<details class="rz-xml">.*?<pre><code>(.*?)</code></pre>`)
	recipeDMNRE    = regexp.MustCompile(`(?s)<pre class="rz-dmn"><code>(.*?)</code></pre>`)
	recipeButtonRE = regexp.MustCompile(`<button class="rz-btn rz-act[^"]*" type="button"([^>]*)>`)
	buttonAttrRE   = regexp.MustCompile(`data-(act|el|msg|key|code|alt)(?:="([^"]*)")?|data-vars='([^']*)'`)
)

type recipeStep struct {
	act, el, msg, key, code, vars string
}

type handbookRecipe struct {
	id, proc, start, end string
	bpmn, dmn            string
	hasRun, hasGuide     bool
	steps                []recipeStep
}

func handbookRecipeCards(t *testing.T) []handbookRecipe {
	t.Helper()
	page, err := webFS.ReadFile("web/handbuch.html")
	if err != nil {
		t.Fatalf("read handbuch.html: %v", err)
	}
	src := string(page)
	i := strings.Index(src, `<section id="rezepte">`)
	if i < 0 {
		t.Fatal(`handbuch.html has no <section id="rezepte">`)
	}
	sec := src[i:]
	sec = sec[:strings.Index(sec, "</section>")]

	var out []handbookRecipe
	for _, m := range recipeCardRE.FindAllStringSubmatch(sec, -1) {
		r := handbookRecipe{id: m[1]}
		for _, a := range recipeAttrRE.FindAllStringSubmatch(m[2], -1) {
			switch a[1] {
			case "proc":
				r.proc = a[2]
			case "start":
				r.start = a[2]
			case "end":
				r.end = a[2]
			}
		}
		body := m[3]
		if b := recipeBPMNRE.FindStringSubmatch(body); b != nil {
			r.bpmn = html.UnescapeString(b[1])
		}
		if d := recipeDMNRE.FindStringSubmatch(body); d != nil {
			r.dmn = html.UnescapeString(d[1])
		}
		r.hasRun = strings.Contains(body, `class="rz-btn rz-run"`)
		r.hasGuide = strings.Contains(body, `<div class="rz-try">`)
		for _, b := range recipeButtonRE.FindAllStringSubmatch(body, -1) {
			var s recipeStep
			alt := false
			for _, a := range buttonAttrRE.FindAllStringSubmatch(b[1], -1) {
				if a[3] != "" {
					s.vars = html.UnescapeString(a[3])
					continue
				}
				v := html.UnescapeString(a[2])
				switch a[1] {
				case "act":
					s.act = v
				case "el":
					s.el = v
				case "msg":
					s.msg = v
				case "key":
					s.key = v
				case "code":
					s.code = v
				case "alt":
					alt = true
				}
			}
			if !alt {
				r.steps = append(r.steps, s)
			}
		}
		out = append(out, r)
	}
	if len(out) < 20 {
		t.Fatalf("found %d recipe cards — the markup changed, so this test stopped testing them", len(out))
	}
	return out
}

// TestHandbookRecipesRunAsTheirGuideSays replays every recipe card as a reader would.
func TestHandbookRecipesRunAsTheirGuideSays(t *testing.T) {
	for _, r := range handbookRecipeCards(t) {
		t.Run(r.id, func(t *testing.T) {
			if !r.hasRun || !r.hasGuide {
				t.Fatalf("card has run button=%v and a «So probierst du es aus» guide=%v; every recipe needs both, "+
					"so no reader is left with a model and no way to see it work", r.hasRun, r.hasGuide)
			}
			if r.end == "" {
				t.Fatal("card has no data-end: name the end event its guided path reaches")
			}
			replayRecipe(t, r)
		})
	}
}

type recipeRun struct {
	t     *testing.T
	srv   *Server
	clk   *testClock
	defs  []uint64
	floor uint64
}

func (rr *recipeRun) do(method, path, body, contentType string) []byte {
	rr.t.Helper()
	code, out := serveInternal(rr.t, rr.srv, method, path, body, contentType)
	if code != http.StatusOK && code != http.StatusCreated && code != http.StatusNoContent && code != http.StatusAccepted {
		rr.t.Fatalf("%s %s: status %d: %s", method, path, code, out)
	}
	return out
}

func (rr *recipeRun) json(method, path string, body any) []byte {
	rr.t.Helper()
	b, _ := json.Marshal(body)
	return rr.do(method, path, string(b), "application/json")
}

type runInstance struct {
	Key       uint64 `json:"key"`
	State     string `json:"state"`
	Variables []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
		Kind  string `json:"kind"`
	} `json:"variables"`
}

// instances are the ones this run started: of the deployed definitions, above the floor.
func (rr *recipeRun) instances() []runInstance {
	rr.t.Helper()
	var out []runInstance
	for _, k := range rr.defs {
		var page struct {
			Items []runInstance `json:"items"`
		}
		if err := json.Unmarshal(rr.do(http.MethodGet, fmt.Sprintf("/api/v1/instances?process=%d&limit=200", k), "", ""), &page); err != nil {
			rr.t.Fatalf("decode instances: %v", err)
		}
		for _, i := range page.Items {
			if i.Key > rr.floor {
				out = append(out, i)
			}
		}
	}
	return out
}

type runJob struct {
	Key       uint64 `json:"key"`
	ElementID string `json:"elementId"`
	JobType   string `json:"jobType"`
}

func (rr *recipeRun) jobsAt(el string) []runJob {
	rr.t.Helper()
	var out []runJob
	for _, i := range rr.instances() {
		if i.State != "active" {
			continue
		}
		var jobs []runJob
		_ = json.Unmarshal(rr.do(http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/jobs", i.Key), "", ""), &jobs)
		for _, j := range jobs {
			if j.ElementID == el {
				out = append(out, j)
			}
		}
	}
	return out
}

// elapse lets a minute pass on the engine's clock and fires what came due. A reader
// waits for a timer in wall time; the test moves the clock, on the run loop.
func (rr *recipeRun) elapse() {
	rr.srv.do(func() {
		rr.clk.t += int64(time.Minute)
		if err := rr.srv.proc.TickTimers(); err != nil {
			rr.t.Fatalf("TickTimers: %v", err)
		}
		if err := rr.srv.jobRunner.Drive(); err != nil {
			rr.t.Fatalf("Drive: %v", err)
		}
	})
}

// waitFor retries cond, letting engine time pass between attempts — the reader's
// "wait 30 seconds, then press the button".
func (rr *recipeRun) waitFor(cond func() bool) bool {
	for range 10 {
		if cond() {
			return true
		}
		rr.elapse()
	}
	return cond()
}

func replayRecipe(t *testing.T, r handbookRecipe) {
	clk := &testClock{t: time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC).UnixNano()}
	rr := &recipeRun{t: t, srv: newServerWithClock(t, clk), clk: clk}

	if r.dmn != "" {
		rr.do(http.MethodPost, "/api/v1/decision-deployments", r.dmn, "application/xml")
	}
	var dep struct {
		Deployments []struct {
			Key       uint64 `json:"key"`
			ProcessID string `json:"processId"`
		} `json:"deployments"`
	}
	if err := json.Unmarshal(rr.do(http.MethodPost, "/api/v1/deployments", r.bpmn, "application/xml"), &dep); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	for _, d := range dep.Deployments {
		if d.ProcessID == r.proc {
			rr.defs = append([]uint64{d.Key}, rr.defs...)
		} else {
			rr.defs = append(rr.defs, d.Key)
		}
	}
	if len(rr.defs) == 0 || len(dep.Deployments) == 0 || dep.Deployments[0].Key == 0 {
		t.Fatalf("deploy returned no definitions")
	}

	switch {
	case r.start == "none":
		// A timer start: the engine starts it; the reader only waits.
	case strings.HasPrefix(r.start, "message:"):
		rr.json(http.MethodPost, "/api/v1/messages", map[string]any{"name": strings.TrimPrefix(r.start, "message:")})
	default:
		var made struct {
			InstanceKey uint64 `json:"instanceKey"`
		}
		_ = json.Unmarshal(rr.json(http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", rr.defs[0]),
			map[string]any{"variables": map[string]any{}}), &made)
		rr.floor = made.InstanceKey - 1
	}

	for n, s := range r.steps {
		switch s.act {
		case "msg":
			body := map[string]any{"name": s.msg}
			if s.key != "" {
				val, ok := "", false
				for _, i := range rr.instances() {
					for _, v := range i.Variables {
						if v.Name == s.key {
							val, ok = v.Value, true
						}
					}
				}
				if !ok {
					t.Fatalf("step %d: no instance carries the correlation variable %q", n+1, s.key)
				}
				body["correlationKey"] = val
			}
			rr.json(http.MethodPost, "/api/v1/messages", body)
		case "resolve":
			var incs []map[string]any
			rr.waitFor(func() bool { incs = rr.incidents(); return len(incs) > 0 })
			if len(incs) == 0 {
				t.Fatalf("step %d: no incident to resolve", n+1)
			}
			for _, inc := range incs {
				rr.json(http.MethodPost, fmt.Sprintf("/api/v1/incidents/%.0f/resolve", inc["elementInstanceKey"]), map[string]any{})
			}
		case "job", "error", "fail":
			var jobs []runJob
			rr.waitFor(func() bool { jobs = rr.jobsAt(s.el); return len(jobs) > 0 })
			if len(jobs) == 0 {
				t.Fatalf("step %d (%s %s): nothing waits at %q — the button would tell the reader so, and the guide would be wrong",
					n+1, s.act, s.el, s.el)
			}
			for k, j := range jobs {
				switch s.act {
				case "job":
					vars := map[string]any{}
					if s.vars != "" {
						if err := json.Unmarshal([]byte(strings.ReplaceAll(s.vars, "{n}", fmt.Sprint(k+1))), &vars); err != nil {
							t.Fatalf("step %d: data-vars is not JSON: %v", n+1, err)
						}
					}
					if j.JobType == "io.atlas.user-task" {
						rr.json(http.MethodPost, fmt.Sprintf("/api/v1/tasks/%d/complete", j.Key), map[string]any{"variables": vars})
					} else {
						rr.json(http.MethodPost, fmt.Sprintf("/api/v1/jobs/%d/complete", j.Key),
							map[string]any{"reason": "handbook recipe test", "variables": vars})
					}
				case "error":
					rr.json(http.MethodPost, fmt.Sprintf("/api/v1/jobs/%d/error", j.Key),
						map[string]any{"errorCode": s.code, "reason": "handbook recipe test"})
				case "fail":
					rr.json(http.MethodPost, fmt.Sprintf("/api/v1/jobs/%d/fail", j.Key),
						map[string]any{"retries": 0, "message": "handbook recipe test"})
				}
			}
		default:
			t.Fatalf("step %d: unknown data-act %q", n+1, s.act)
		}
	}

	done := rr.waitFor(func() bool {
		insts := rr.instances()
		if len(insts) == 0 {
			return false
		}
		for _, i := range insts {
			if i.State != "completed" {
				return false
			}
		}
		return true
	})
	insts := rr.instances()
	if !done {
		states := map[uint64]string{}
		for _, i := range insts {
			states[i.Key] = i.State
		}
		t.Fatalf("after every step of the guide the run is not complete: %v, incidents %v", states, rr.incidents())
	}
	visited := false
	for _, i := range insts {
		var tl struct {
			Steps []struct {
				ElementID string `json:"elementId"`
			} `json:"steps"`
		}
		_ = json.Unmarshal(rr.do(http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/timeline", i.Key), "", ""), &tl)
		for _, s := range tl.Steps {
			if s.ElementID == r.end {
				visited = true
			}
		}
		for _, v := range i.Variables {
			if v.Kind == "null" {
				t.Errorf("variable %q ended null: the recipe reads input the empty start did not give it — "+
					"declare it as a start variable with a default", v.Name)
			}
		}
	}
	if !visited {
		t.Errorf("the run completed, but not at %q, the end event the card names", r.end)
	}
}

func (rr *recipeRun) incidents() []map[string]any {
	rr.t.Helper()
	keys := map[float64]bool{}
	for _, i := range rr.instances() {
		keys[float64(i.Key)] = true
	}
	var page struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal(rr.do(http.MethodGet, "/api/v1/incidents", "", ""), &page)
	var out []map[string]any
	for _, inc := range page.Items {
		if k, ok := inc["processInstanceKey"].(float64); ok && keys[k] {
			out = append(out, inc)
		}
	}
	return out
}
