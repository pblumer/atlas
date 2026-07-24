package compiler

import (
	"strings"
	"testing"
)

// powershellScriptBPMN is a script task authored in PowerShell rather than FEEL:
// it carries an <atlas:jobScript> extension (a distinct local name from
// <zeebe:script>, so the two never collide) naming the language, the result
// variable, and the script body as element text. The compiler turns it into a
// job-based task (ADR-0047), not an inline FEEL script.
const powershellScriptBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="greeting" isExecutable="true">
    <startEvent id="s"/>
    <scriptTask id="greet">
      <extensionElements>
        <jobScript language="powershell" resultVariable="Greeting">Write-Output "Hallo $Vorname"</jobScript>
      </extensionElements>
    </scriptTask>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="greet"/>
    <sequenceFlow id="f2" sourceRef="greet" targetRef="e"/>
  </process>
</definitions>`

// scriptJobTaskDetail returns the single script-job-task detail in cp, or fails.
func scriptJobTaskDetail(t *testing.T, cp *CompiledProcess) (*CompiledNode, *ScriptJobTaskDetail) {
	t.Helper()
	for i := range cp.nodes {
		if cp.nodes[i].Type == TypeScriptJobTask {
			return &cp.nodes[i], cp.ScriptJobTask(cp.nodes[i].Detail)
		}
	}
	t.Fatal("no script-job task compiled")
	return nil, nil
}

// TestParsePowerShellScriptTask proves a PowerShell script task compiles to a
// job-based node carrying the reserved PowerShell job type, with its language,
// source, and result variable interned, and default retries applied.
func TestParsePowerShellScriptTask(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(powershellScriptBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	node, detail := scriptJobTaskDetail(t, cp)
	if node.Type != TypeScriptJobTask {
		t.Fatalf("node type = %v, want ScriptJobTask", node.Type)
	}
	if detail.JobType != PwshJobTypeIndex {
		t.Errorf("job type = %d, want PwshJobTypeIndex %d", detail.JobType, PwshJobTypeIndex)
	}
	if got := cp.Intern(detail.JobType); got != PwshJobType {
		t.Errorf("job type interns to %q, want %q", got, PwshJobType)
	}
	if got := cp.Intern(detail.Language); got != "powershell" {
		t.Errorf("language = %q, want powershell", got)
	}
	if got := cp.Intern(detail.Source); got != `Write-Output "Hallo $Vorname"` {
		t.Errorf("source = %q, want the script body", got)
	}
	if got := cp.Intern(detail.ResultVar); got != "Greeting" {
		t.Errorf("result var = %q, want Greeting", got)
	}
	if detail.Retries != defaultRetries {
		t.Errorf("retries = %d, want default %d", detail.Retries, defaultRetries)
	}
}

// TestParseModelerJobScript locks the modeler round-trip: the exact XML the
// Modeler writes — a namespace-prefixed <atlas:jobScript> whose script body is
// element text and may contain XML-special characters — compiles to the job path.
func TestParseModelerJobScript(t *testing.T) {
	const modelerBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas/schema/1.0">
  <bpmn:process id="greeting" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:scriptTask id="greet">
      <bpmn:extensionElements>
        <atlas:jobScript language="powershell" resultVariable="Greeting">if ($a -lt $b) { "small" } else { "big" }</atlas:jobScript>
      </bpmn:extensionElements>
    </bpmn:scriptTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="greet"/>
    <bpmn:sequenceFlow id="f2" sourceRef="greet" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
	cp, err := Parse(1, 1, strings.NewReader(modelerBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	_, detail := scriptJobTaskDetail(t, cp)
	if detail.JobType != PwshJobTypeIndex {
		t.Errorf("job type = %d, want PwshJobTypeIndex", detail.JobType)
	}
	if got := cp.Intern(detail.Source); got != `if ($a -lt $b) { "small" } else { "big" }` {
		t.Errorf("source = %q, want the script body with its < unescaped", got)
	}
	if got := cp.Intern(detail.ResultVar); got != "Greeting" {
		t.Errorf("result var = %q, want Greeting", got)
	}
}

// TestPwshJobTypeReserved proves the PowerShell job type occupies a fixed global
// index (like DMNJobTypeIndex), so a single in-process worker can serve every
// deployed process, and that the FEEL script task path is unaffected.
func TestPwshJobTypeReserved(t *testing.T) {
	if PwshJobTypeIndex != 2 {
		t.Errorf("PwshJobTypeIndex = %d, want 2 (after DMN=0, user-task=1)", PwshJobTypeIndex)
	}
	// Reserved in every process, even one with no PowerShell task.
	b := NewBuilder(1, "p", 1)
	b.AddStartEvent()
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := cp.Intern(PwshJobTypeIndex); got != PwshJobType {
		t.Errorf("index %d interns to %q, want %q", PwshJobTypeIndex, got, PwshJobType)
	}

	// A FEEL script task still compiles to the inline TypeScriptTask, not the job path.
	const feelScript = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p" isExecutable="true">
    <startEvent id="s"/>
    <scriptTask id="calc">
      <extensionElements><script expression="= 6 * 7" resultVariable="answer"/></extensionElements>
    </scriptTask>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="calc"/>
    <sequenceFlow id="f2" sourceRef="calc" targetRef="e"/>
  </process>
</definitions>`
	cp2, err := Parse(2, 1, strings.NewReader(feelScript))
	if err != nil {
		t.Fatalf("Parse FEEL: %v", err)
	}
	for i := range cp2.nodes {
		if cp2.nodes[i].Type == TypeScriptJobTask {
			t.Fatal("FEEL script task wrongly compiled to the job path")
		}
	}
}

// TestScriptJobTaskValidation rejects, at deploy time, a job script with no
// source, no result variable, or an unsupported language.
func TestScriptJobTaskValidation(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "empty source",
			body: `<jobScript language="powershell" resultVariable="g">   </jobScript>`,
		},
		{
			name: "missing result variable",
			body: `<jobScript language="powershell">Write-Output 1</jobScript>`,
		},
		{
			name: "unsupported language",
			body: `<jobScript language="ruby" resultVariable="g">puts 1</jobScript>`,
		},
		{
			name: "missing language",
			body: `<jobScript resultVariable="g">Write-Output 1</jobScript>`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			xml := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p" isExecutable="true">
    <startEvent id="s"/>
    <scriptTask id="bad"><extensionElements>` + tt.body + `</extensionElements></scriptTask>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="bad"/>
    <sequenceFlow id="f2" sourceRef="bad" targetRef="e"/>
  </process>
</definitions>`
			if _, err := Parse(1, 1, strings.NewReader(xml)); err == nil {
				t.Fatalf("Parse succeeded, want an error for %s", tt.name)
			}
		})
	}
}
