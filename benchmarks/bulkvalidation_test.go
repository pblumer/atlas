package benchmarks

// Slice 0 of ADR-0548: the
// measurements that decide between one validation job per file and one multi-instance
// iteration per row (ADR-0084). Nothing here is a feature; it answers the record's open
// question with numbers instead of arithmetic. The write-up of a run is
// results/bulk-validation-*.md.
//
//	ATLAS_BENCH_BULK=full go test -run=^$ -bench=BulkValidation -benchmem -benchtime=1x -count=3 -timeout=120m ./benchmarks/
//
// The decision benchmarks take a normal -benchtime; the file and multi-instance ones do
// one whole file per op, so give them -benchtime=1x (or 3x). Without ATLAS_BENCH_BULK=full
// each runs one size only (a 50,000-row file, a 1,000-row instance): CI smoke-runs every
// benchmark in this package once, and the full multi-instance series takes minutes.
// ATLAS_BENCH_ROWS picks the multi-instance sizes by hand.

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/dmn"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// qualityDMN is a rule closer to what a quality check holds than the example's single
// rule: an e-mail pattern, twenty groups, and a licence allowed per group. Its inputs
// are typed and connected to the decision, and the verdict has its own type. Hit policy
// FIRST, so a row is matched by scanning the rules in order, and the valid rows spread
// over all twenty groups make the scan length realistic rather than one rule deep.
var qualityDMN = func() string {
	var rules strings.Builder
	rules.WriteString(`<rule id="r_email"><inputEntry><text>false</text></inputEntry><inputEntry><text>-</text></inputEntry><inputEntry><text>-</text></inputEntry>
        <outputEntry><text>false</text></outputEntry><outputEntry><text>"email"</text></outputEntry></rule>`)
	for g := 1; g <= 20; g++ {
		licences := `"PRO","BASIC"`
		if g > 10 {
			licences = `"PRO"`
		}
		fmt.Fprintf(&rules, `<rule id="r_g%02d"><inputEntry><text>true</text></inputEntry><inputEntry><text>"g%02d"</text></inputEntry><inputEntry><text>%s</text></inputEntry>
        <outputEntry><text>true</text></outputEntry><outputEntry><text>""</text></outputEntry></rule>`, g, g, licences)
	}
	rules.WriteString(`<rule id="r_rest"><inputEntry><text>-</text></inputEntry><inputEntry><text>-</text></inputEntry><inputEntry><text>-</text></inputEntry>
        <outputEntry><text>false</text></outputEntry><outputEntry><text>"group or licence"</text></outputEntry></rule>`)
	return `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="defs_quality" name="quality" namespace="http://atlas/bench">
  <itemDefinition id="tQualityVerdict" name="tQualityVerdict">
    <itemComponent id="tQualityVerdict_valid" name="valid"><typeRef>boolean</typeRef></itemComponent>
    <itemComponent id="tQualityVerdict_reason" name="reason"><typeRef>string</typeRef></itemComponent>
  </itemDefinition>
  <inputData id="id_email" name="email"><variable id="var_email" name="email" typeRef="string"/></inputData>
  <inputData id="id_group" name="group"><variable id="var_group" name="group" typeRef="string"/></inputData>
  <inputData id="id_license" name="license"><variable id="var_license" name="license" typeRef="string"/></inputData>
  <decision id="QualityRow" name="QualityRow">
    <variable id="var_QualityRow" name="QualityRow" typeRef="tQualityVerdict"/>
    <informationRequirement id="ir_email"><requiredInput href="#id_email"/></informationRequirement>
    <informationRequirement id="ir_group"><requiredInput href="#id_group"/></informationRequirement>
    <informationRequirement id="ir_license"><requiredInput href="#id_license"/></informationRequirement>
    <decisionTable id="dt" hitPolicy="FIRST">
      <input id="in_e" label="emailOk"><inputExpression typeRef="boolean"><text>matches(email, "^[^@ ]+@[^@ ]+\.[a-z]{2,}$")</text></inputExpression></input>
      <input id="in_g" label="group"><inputExpression typeRef="string"><text>group</text></inputExpression></input>
      <input id="in_l" label="license"><inputExpression typeRef="string"><text>license</text></inputExpression></input>
      <output id="o_v" name="valid" typeRef="boolean"/>
      <output id="o_r" name="reason" typeRef="string"/>
      ` + rules.String() + `
    </decisionTable>
  </decision>
</definitions>`
}()

// benchRow is row i of a synthetic file: mostly valid, spread over the twenty groups,
// with one row in thirty-three failing its e-mail and one in fifty its licence — the
// order of failure rates a real export has, not a worst case.
func benchRow(i int) map[string]any {
	email := fmt.Sprintf("user%06d@example.ch", i)
	if i%33 == 0 {
		email = fmt.Sprintf("user%06d.example.ch", i)
	}
	g := i%20 + 1
	licence := "PRO"
	if g <= 10 && i%2 == 0 {
		licence = "BASIC"
	}
	if i%50 == 0 {
		licence = "NONE"
	}
	return map[string]any{"email": email, "group": fmt.Sprintf("g%02d", g), "license": licence}
}

// exampleRow is row i for the ADR-0084 example decision RowValid, every row valid so the
// multi-instance run completes without parking on a correction task.
func exampleRow(i int) map[string]any {
	licence := "PRO"
	if i%2 == 0 {
		licence = "BASIC"
	}
	return map[string]any{"email": fmt.Sprintf("user%06d@example.ch", i), "group": "users", "license": licence}
}

func exampleDMN(b *testing.B) []byte {
	b.Helper()
	data, err := os.ReadFile(filepath.Join("..", "examples", "pruefe-datensaetze.dmn"))
	if err != nil {
		b.Fatalf("read example DMN: %v", err)
	}
	return data
}

// BenchmarkBulkValidationDecisionPerRow is the cost of one decision evaluation — the
// unit both designs pay once per row. "traced" is what a business rule task pays
// (ADR-0066 keeps the trace); "plain" is what one validation job per file would pay.
func BenchmarkBulkValidationDecisionPerRow(b *testing.B) {
	for _, tc := range []struct {
		name, decision string
		model          func(*testing.B) []byte
		row            func(int) map[string]any
	}{
		{"example", "RowValid", exampleDMN, exampleRow},
		{"quality", "QualityRow", func(*testing.B) []byte { return []byte(qualityDMN) }, benchRow},
	} {
		reg := dmn.NewRegistry()
		if err := reg.Deploy(1, tc.model(b)); err != nil {
			b.Fatalf("deploy %s: %v", tc.name, err)
		}
		rows := make([]map[string]any, 1000)
		for i := range rows {
			rows[i] = tc.row(i + 1)
		}
		ctx := context.Background()
		b.Run(tc.name+"/plain", func(b *testing.B) {
			b.ReportAllocs()
			for i := range b.N {
				if _, err := reg.Evaluate(ctx, 1, tc.decision, rows[i%len(rows)]); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(tc.name+"/traced", func(b *testing.B) {
			b.ReportAllocs()
			var traceBytes int
			for i := range b.N {
				_, trace, err := reg.EvaluateTraced(ctx, 1, tc.decision, rows[i%len(rows)])
				if err != nil {
					b.Fatal(err)
				}
				traceBytes += len(trace)
			}
			b.ReportMetric(float64(traceBytes)/float64(b.N), "traceB/op")
		})
	}
}

// benchCSV renders n synthetic rows as the CSV a quality manager would upload.
func benchCSV(n int) []byte {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"email", "group", "license"})
	for i := 1; i <= n; i++ {
		r := benchRow(i)
		_ = w.Write([]string{r["email"].(string), r["group"].(string), r["license"].(string)})
	}
	w.Flush()
	return buf.Bytes()
}

// validateFile is the core of the proposed validate-rows operation, without the job
// around it: read the CSV a row at a time, evaluate the decision, write the result row,
// drop the input. It returns the counts the summary would carry.
func validateFile(ctx context.Context, reg *dmn.Registry, src io.Reader, dst io.Writer) (total, invalid int, err error) {
	r := csv.NewReader(src)
	r.ReuseRecord = true
	first, err := r.Read()
	if err != nil {
		return 0, 0, err
	}
	head := append([]string(nil), first...) // ReuseRecord overwrites first on the next Read
	w := csv.NewWriter(dst)
	if err := w.Write(append(append([]string{}, head...), "_row", "_valid", "_reason")); err != nil {
		return 0, 0, err
	}
	in := make(map[string]any, len(head))
	out := make([]string, 0, len(head)+3)
	for line := 2; ; line++ {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return total, invalid, err
		}
		for i, h := range head {
			in[h] = rec[i]
		}
		res, err := reg.Evaluate(ctx, 1, "QualityRow", in)
		if err != nil {
			return total, invalid, err
		}
		total++
		// A decision with more than one output answers a context under its own name.
		verdict, _ := res["QualityRow"].(map[string]any)
		valid, _ := verdict["valid"].(bool)
		if !valid {
			invalid++
		}
		reason, _ := verdict["reason"].(string)
		out = append(append(out[:0], rec...), strconv.Itoa(line), strconv.FormatBool(valid), reason)
		if err := w.Write(out); err != nil {
			return total, invalid, err
		}
	}
	w.Flush()
	return total, invalid, w.Error()
}

// BenchmarkBulkValidationStreamedFile is one whole file through validateFile: the time
// a validate-rows job would hold its lease for, minus the document store's read and
// write. One op is one file.
func BenchmarkBulkValidationStreamedFile(b *testing.B) {
	reg := dmn.NewRegistry()
	if err := reg.Deploy(1, []byte(qualityDMN)); err != nil {
		b.Fatal(err)
	}
	sizes := []int{50_000}
	if os.Getenv("ATLAS_BENCH_BULK") == "full" {
		sizes = []int{10_000, 50_000, 200_000}
	}
	for _, n := range sizes {
		src := benchCSV(n)
		b.Run(fmt.Sprintf("rows=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			var resultBytes, invalid int
			var peak uint64
			for range b.N {
				var dst bytes.Buffer
				_, inv, err := validateFile(context.Background(), reg, bytes.NewReader(src), &dst)
				if err != nil {
					b.Fatal(err)
				}
				resultBytes, invalid = dst.Len(), inv
				var ms runtime.MemStats
				runtime.ReadMemStats(&ms)
				peak = max(peak, ms.HeapInuse)
			}
			b.ReportMetric(float64(len(src)), "srcB")
			b.ReportMetric(float64(resultBytes), "resultB")
			b.ReportMetric(float64(invalid), "invalid")
			b.ReportMetric(float64(b.Elapsed().Microseconds())/float64(b.N)/float64(n), "µs/row")
			b.ReportMetric(float64(peak), "heapB")
		})
	}
}

// miValidationBPMN is the ADR-0084 shape without the upload and parse steps in front:
// rows arrive as a start variable, and a multi-instance embedded subprocess runs the
// business rule task per row, branches on the verdict, and would park an invalid row on
// a correction task. Every benchmark row is valid, so none parks.
const miValidationBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
             id="defs_bench_mi" targetNamespace="http://atlas/bench">
  <process id="bench_mi_validation" isExecutable="true">
    <startEvent id="start"/>
    <subProcess id="pruefe_datensatz">
      <multiInstanceLoopCharacteristics>
        <extensionElements>
          <zeebe:loopCharacteristics inputCollection="=rows" inputElement="row"
            outputCollection="verdicts" outputElement="=verdict"/>
        </extensionElements>
      </multiInstanceLoopCharacteristics>
      <startEvent id="row_start"/>
      <businessRuleTask id="regelpruefung">
        <extensionElements>
          <zeebe:calledDecision decisionId="RowValid" resultVariable="verdict" retries="3"/>
          <zeebe:ioMapping>
            <zeebe:input source="=row.email" target="email"/>
            <zeebe:input source="=row.group" target="group"/>
            <zeebe:input source="=row.license" target="license"/>
          </zeebe:ioMapping>
        </extensionElements>
      </businessRuleTask>
      <exclusiveGateway id="pruef_split" default="rf_korrigieren"/>
      <userTask id="korrigiere"/>
      <endEvent id="row_end"/>
      <endEvent id="row_fix_end"/>
      <sequenceFlow id="rf_start" sourceRef="row_start" targetRef="regelpruefung"/>
      <sequenceFlow id="rf_check" sourceRef="regelpruefung" targetRef="pruef_split"/>
      <sequenceFlow id="rf_ok" sourceRef="pruef_split" targetRef="row_end">
        <conditionExpression>=verdict.valid</conditionExpression>
      </sequenceFlow>
      <sequenceFlow id="rf_korrigieren" sourceRef="pruef_split" targetRef="korrigiere"/>
      <sequenceFlow id="rf_fixed" sourceRef="korrigiere" targetRef="row_fix_end"/>
    </subProcess>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="pruefe_datensatz"/>
    <sequenceFlow id="f2" sourceRef="pruefe_datensatz" targetRef="end"/>
  </process>
</definitions>`

// dirBytes sums every file under dir.
func dirBytes(b *testing.B, dir string) int64 {
	b.Helper()
	var total int64
	err := filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	if err != nil {
		b.Fatal(err)
	}
	return total
}

// do serves one in-process request and returns its status and body.
func do(h http.Handler, method, path, body string) (int, []byte) {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

// BenchmarkBulkValidationMultiInstance is the ADR-0084 design, measured: one instance
// per op over n rows, through the real server with its in-process DMN worker, on the
// durable profile. It reports what the log, the state and the evaluation records grow
// by for one file, and how long the file takes from start to completion.
func BenchmarkBulkValidationMultiInstance(b *testing.B) {
	sizes := []int{1_000}
	if os.Getenv("ATLAS_BENCH_BULK") == "full" {
		sizes = []int{1_000, 2_000, 5_000, 10_000}
	}
	for _, n := range benchRowCounts(sizes) {
		b.Run(fmt.Sprintf("rows=%d", n), func(b *testing.B) {
			dir := b.TempDir()
			walDir := filepath.Join(dir, "wal")
			log, err := wal.Open(wal.Options{Dir: walDir})
			if err != nil {
				b.Fatal(err)
			}
			store, err := state.Open(filepath.Join(dir, "state"))
			if err != nil {
				b.Fatal(err)
			}
			proc := engine.New(1, log, store, nil)
			if err := proc.Recover(); err != nil {
				b.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(dir, "dmn-models"), 0o755); err != nil {
				b.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "dmn-models", "rowvalid.dmn"), exampleDMN(b), 0o644); err != nil {
				b.Fatal(err)
			}
			srv, err := api.New(proc, store, dir)
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { srv.Close(); _ = store.Close(); _ = log.Close() })
			h := srv.Handler()
			// Every process and decision reference belongs to an application (ADR-0544).
			code, out := do(h, http.MethodPost, "/api/v1/applications", `{"name":"Bench"}`)
			var app struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(out, &app) != nil || app.ID == "" {
				b.Fatalf("application: %d %s", code, out)
			}
			if code, out := do(h, http.MethodPost, "/api/v1/dmnrefs", `{"name":"RowValid","modelRef":"rowvalid","projectId":"`+app.ID+`"}`); code != http.StatusOK {
				b.Fatalf("dmnref: %d %s", code, out)
			}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/deployments?projectId="+app.ID, strings.NewReader(miValidationBPMN))
			req.Header.Set("Content-Type", "application/xml")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			var dep struct {
				Key uint64 `json:"key"`
			}
			if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &dep) != nil || dep.Key == 0 {
				b.Fatalf("deploy: %d %s", rec.Code, rec.Body.String())
			}
			rows := make([]map[string]any, n)
			for i := range rows {
				rows[i] = exampleRow(i + 1)
			}
			body, _ := json.Marshal(map[string]any{"variables": map[string]any{"rows": rows}})

			startPos := appliedPosition(b, store)
			startWAL := walDirBytes(b, walDir)
			startState := dirBytes(b, filepath.Join(dir, "state"))
			var ms0, ms1 runtime.MemStats
			runtime.ReadMemStats(&ms0)
			b.ResetTimer()
			for range b.N {
				code, out := do(h, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), string(body))
				if code != http.StatusOK {
					b.Fatalf("start: %d %s", code, out)
				}
				var started struct {
					InstanceKey uint64 `json:"instanceKey"`
				}
				_ = json.Unmarshal(out, &started)
				waitCompleted(b, store, started.InstanceKey, 30*time.Minute)
			}
			b.StopTimer()
			runtime.ReadMemStats(&ms1)
			per := float64(b.N)
			endPos, _ := store.LastAppliedPosition()
			evals := 0
			_ = store.EachDecisionEvaluation(func(uint64, int64, *model.DecisionEvaluationValue) error { evals++; return nil })
			b.ReportMetric(float64(endPos-startPos)/per, "events/op")
			b.ReportMetric(float64(walDirBytes(b, walDir)-startWAL)/per, "walB/op")
			b.ReportMetric(float64(dirBytes(b, filepath.Join(dir, "state"))-startState)/per, "stateB/op")
			b.ReportMetric(float64(evals)/per, "evals/op")
			b.ReportMetric(float64(ms1.TotalAlloc-ms0.TotalAlloc)/per, "allocB/op")
			b.ReportMetric(float64(b.Elapsed().Microseconds())/per/float64(n), "µs/row")
		})
	}
}

// benchRowCounts is the row counts to run, or ATLAS_BENCH_ROWS (comma-separated) when
// set, so one size can be run or a smaller one tried first.
func benchRowCounts(def []int) []int {
	env := os.Getenv("ATLAS_BENCH_ROWS")
	if env == "" {
		return def
	}
	var out []int
	for _, f := range strings.Split(env, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(f)); err == nil && n > 0 {
			out = append(out, n)
		}
	}
	return out
}

// waitCompleted polls the state store until no instance is active any more, then
// checks the one it waited for is among the completed ones. Reading the store is safe
// beside the server's writer, and it keeps the poll off the HTTP path being measured.
func waitCompleted(b *testing.B, store *state.Store, key uint64, limit time.Duration) {
	b.Helper()
	deadline := time.Now().Add(limit)
	for {
		active, err := store.ActiveProcessInstanceCount()
		if err != nil {
			b.Fatal(err)
		}
		if active == 0 {
			break
		}
		if time.Now().After(deadline) {
			b.Fatalf("instance %d still active after %s", key, limit)
		}
		time.Sleep(20 * time.Millisecond)
	}
	found := false
	_ = store.CompletedProcessInstances(func(k uint64, _ *model.ProcessInstanceValue) error {
		found = found || k == key
		return nil
	})
	if !found {
		b.Fatalf("instance %d is neither active nor completed", key)
	}
}
