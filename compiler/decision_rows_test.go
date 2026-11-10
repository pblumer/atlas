package compiler

import (
	"strings"
	"testing"
)

// rowsBPMN is a business rule task over a file (ADR-0548) with the given rowsFrom
// attributes and, when temis is set, a temis worker as well.
func rowsBPMN(attrs string, temis bool) string {
	extra := ""
	if temis {
		extra = `<temisConnector connector="risk"/>`
	}
	return `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p" isExecutable="true">
    <startEvent id="s"/>
    <businessRuleTask id="check">
      <extensionElements>
        <calledDecision decisionId="QualityRow" resultVariable="pruefung"/>
        <rowsFrom ` + attrs + `/>` + extra + `
      </extensionElements>
    </businessRuleTask>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="check"/>
    <sequenceFlow id="f2" sourceRef="check" targetRef="e"/>
  </process>
</definitions>`
}

func rowsDetail(t *testing.T, cp *CompiledProcess) *BusinessRuleTaskDetail {
	t.Helper()
	for i := range cp.nodes {
		if cp.nodes[i].Type == TypeBusinessRuleTask {
			return cp.BusinessRuleTask(cp.nodes[i].Detail)
		}
	}
	t.Fatal("no business rule task")
	return nil
}

// A business rule task over a file compiles its source, layout and summary settings,
// with the defaults filled in, and stays a local decision.
func TestDecisionRowsCompile(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(rowsBPMN(`source="antrag" delimiter=";" columns="email, group ,license" validOutput="ok" failureSample="50"`, false)))
	if err != nil {
		t.Fatal(err)
	}
	d := rowsDetail(t, cp)
	r := d.Rows
	if r == nil || r.Source != "antrag" || r.Delimiter != ";" || !r.HasHeader || r.ValidOutput != "ok" || r.FailureSample != 50 {
		t.Fatalf("rows = %+v", r)
	}
	if strings.Join(r.Columns, "|") != "email|group|license" {
		t.Fatalf("columns = %v", r.Columns)
	}
	if d.Connector >= 0 || cp.Intern(d.DecisionId) != "QualityRow" {
		t.Fatalf("a decision over a file is local: %+v", d)
	}
	if got := cp.BusinessRuleDecisions(); len(got) != 1 || got[0] != "QualityRow" {
		t.Fatalf("decisions the deploy gate checks = %v", got)
	}

	cp, err = Parse(1, 1, strings.NewReader(rowsBPMN(`source="antrag" hasHeader="false" columns="a,b"`, false)))
	if err != nil {
		t.Fatal(err)
	}
	if r := rowsDetail(t, cp).Rows; r.HasHeader || r.FailureSample != DefaultRowsFailureSample || r.ValidOutput != "" {
		t.Fatalf("defaults = %+v", r)
	}

	// An ordinary business rule task has none.
	plain := strings.Replace(rowsBPMN(`source="x"`, false), `<rowsFrom source="x"/>`, "", 1)
	cp, err = Parse(1, 1, strings.NewReader(plain))
	if err != nil {
		t.Fatal(err)
	}
	if rowsDetail(t, cp).Rows != nil {
		t.Fatal("a task without rowsFrom has rows")
	}
}

// What a run would otherwise find on its first record is refused at deploy.
func TestDecisionRowsRefused(t *testing.T) {
	for _, tc := range []struct {
		attrs string
		temis bool
		want  string
	}{
		{`source=""`, false, "names no source variable"},
		{`source="=antrag.file"`, false, "is not a variable name"},
		{`source="antrag" delimiter=";;"`, false, "not one character"},
		{`source="antrag" hasHeader="maybe"`, false, "neither true nor false"},
		{`source="antrag" hasHeader="false"`, false, "must name its columns"},
		{`source="antrag" columns="a,b,a"`, false, "listed twice"},
		{`source="antrag" failureSample="-1"`, false, "failureSample"},
		{`source="antrag" failureSample="10001"`, false, "failureSample"},
		{`source="antrag" failureSample="many"`, false, "failureSample"},
		{`source="antrag"`, true, "cannot also name a temis worker"},
		{`source="antrag" resultless="true"`, false, "needs a resultVariable"},
	} {
		text := rowsBPMN(tc.attrs, tc.temis)
		if strings.Contains(tc.attrs, "resultless") {
			text = strings.Replace(text, ` resultVariable="pruefung"`, "", 1)
		}
		_, err := Parse(1, 1, strings.NewReader(text))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s temis=%v: err = %v, want %q", tc.attrs, tc.temis, err, tc.want)
		}
	}
}
