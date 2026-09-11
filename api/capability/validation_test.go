package capability

import (
	"reflect"
	"strings"
	"testing"
)

// TestCapabilityHasNoParent is the one rule of the method that a comment could not
// keep. The record's whole premise is a flat, tagged list: an "end-to-end"
// capability is regularly invoked from inside another one, so any tree drawn over
// them is wrong from some direction, and the argument about which tree is right is
// the one the architecture exists to avoid. A parent field would arrive as a
// convenience and settle that argument by accident, in whichever direction the
// first person to add it happened to need.
//
// So the absence is asserted rather than described. If a hierarchy is ever the right
// answer, it takes a decision record, not a struct field.
func TestCapabilityHasNoParent(t *testing.T) {
	forbidden := []string{"parent", "parentkey", "children", "childkeys", "level", "path"}
	typ := reflect.TypeOf(Capability{})
	for i := 0; i < typ.NumField(); i++ {
		name := strings.ToLower(typ.Field(i).Name)
		for _, bad := range forbidden {
			if name == bad {
				t.Errorf("Capability has a %s field: capabilities are a flat, tagged list, and a "+
					"hierarchy needs a decision record rather than a field", typ.Field(i).Name)
			}
		}
	}
	// Tags are what makes the absence workable rather than merely principled: every
	// view a hierarchy would give is a tag query.
	if _, ok := typ.FieldByName("Tags"); !ok {
		t.Error("Capability has no Tags field, so the flat list has no way to be sorted at all")
	}
}

func TestValidKeyShape(t *testing.T) {
	tests := []struct {
		key  string
		want bool
	}{
		{"loan-underwriting", true},
		{"a", true},
		{"a1", true},
		{"customer-onboarding-2026", true},
		{"", false},
		{"Loan-Underwriting", false}, // upper case: the key is a filename and a URL segment
		{"-leading", false},
		{"trailing-", false},
		{"double--dash", true}, // ugly, not dangerous
		{"under_score", false},
		{"with space", false},
		{"../escape", false},
		{".", false},
		{"..", false},
		{"with/slash", false},
		{strings.Repeat("a", 64), true},
		{strings.Repeat("a", 65), false},
	}
	for _, tt := range tests {
		if got := validKey(tt.key); got != tt.want {
			t.Errorf("validKey(%q) = %v, want %v", tt.key, got, tt.want)
		}
	}
}

func TestValidateCapabilityAcceptsAMinimalRecord(t *testing.T) {
	// The minimum is a key and a name. Everything else is a capability somebody has
	// not got to yet, and refusing it would make the map impossible to start.
	c := Capability{Key: "loan-underwriting", Name: "Loan Underwriting", State: StateProposed}
	if findings := ValidateCapability(c); len(findings) != 0 {
		t.Errorf("minimal capability refused: %v", findings)
	}
}

func TestValidateCapabilityAcceptsNoRealization(t *testing.T) {
	// A capability nobody has automated is the normal state at the start, and it is
	// the most useful row in the list. It must never be a validation error.
	c := Capability{Key: "underwriting", Name: "Underwriting", State: StateActive}
	if findings := ValidateCapability(c); len(findings) != 0 {
		t.Errorf("unrealized capability refused: %v", findings)
	}
}

func TestValidateCapabilityRefusals(t *testing.T) {
	base := func() Capability {
		return Capability{Key: "onboarding", Name: "Customer Onboarding", State: StateActive}
	}
	tests := []struct {
		name string
		mut  func(*Capability)
		want string
	}{
		{"empty key", func(c *Capability) { c.Key = "" }, "key"},
		{"unsafe key", func(c *Capability) { c.Key = "../etc" }, "key"},
		{"empty name", func(c *Capability) { c.Name = "" }, "name"},
		{"unknown state", func(c *Capability) { c.State = "retired" }, "state"},
		{"unknown interface kind", func(c *Capability) {
			c.Inputs = []Interface{{Name: "start", Kind: "carrier-pigeon"}}
		}, "kind"},
		{"unnamed interface", func(c *Capability) {
			c.Inputs = []Interface{{Kind: KindAPI}}
		}, "name"},
		{"unknown resource kind", func(c *Capability) {
			c.Resources = []Resource{{Kind: "budget", Name: "money"}}
		}, "kind"},
		{"unknown realization kind", func(c *Capability) {
			c.Realizations = []Realization{{Kind: "magic"}}
		}, "kind"},
		{"process realization without an application", func(c *Capability) {
			c.Realizations = []Realization{{Kind: RealizationProcess, ProcessID: "p"}}
		}, "applicationKey"},
		{"process realization without a process", func(c *Capability) {
			c.Realizations = []Realization{{Kind: RealizationProcess, ApplicationKey: "app"}}
		}, "processId"},
		{"worker realization without a worker", func(c *Capability) {
			c.Realizations = []Realization{{Kind: RealizationWorker}}
		}, "workerRef"},
		{"system realization without a note", func(c *Capability) {
			c.Realizations = []Realization{{Kind: RealizationSystem}}
		}, "note"},
		{"manual realization without a note", func(c *Capability) {
			c.Realizations = []Realization{{Kind: RealizationManual}}
		}, "note"},
		{"requires itself", func(c *Capability) { c.Requires = []string{"onboarding"} }, "itself"},
		{"requires the same thing twice", func(c *Capability) {
			c.Requires = []string{"identity", "identity"}
		}, "twice"},
		{"requires an unsafe key", func(c *Capability) { c.Requires = []string{"../x"} }, "requires"},
		{"unnamed kpi", func(c *Capability) { c.KPIs = []KPI{{Metric: "cycleTime"}} }, "name"},
		{"kpi without a metric", func(c *Capability) { c.KPIs = []KPI{{Name: "Speed"}} }, "metric"},
		{"unknown kpi direction", func(c *Capability) {
			c.KPIs = []KPI{{Name: "Speed", Metric: "cycleTime", Direction: "sideways"}}
		}, "direction"},
		{"sla without a threshold", func(c *Capability) {
			c.SLAs = []SLA{{Name: "Decision", Metric: "cycleTime", Scope: SLAInternal}}
		}, "threshold"},
		{"unknown sla scope", func(c *Capability) {
			c.SLAs = []SLA{{Name: "D", Metric: "m", Threshold: "5d", Scope: "cosmic"}}
		}, "scope"},
		{"empty tag", func(c *Capability) { c.Tags = []string{"area:lending", ""} }, "tag"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := base()
			tt.mut(&c)
			findings := ValidateCapability(c)
			if len(findings) == 0 {
				t.Fatalf("accepted %s", tt.name)
			}
			joined := strings.ToLower(strings.Join(findings, " | "))
			if !strings.Contains(joined, strings.ToLower(tt.want)) {
				t.Errorf("findings %q do not mention %q", joined, tt.want)
			}
		})
	}
}

func TestValidateCapabilityAcceptsEveryDeclaredVocabulary(t *testing.T) {
	// The vocabularies are small and closed. This is the positive half: each word the
	// record names must actually pass, or a refusal message would be teaching a
	// vocabulary the validator does not accept.
	c := Capability{
		Key: "identity-verification", Name: "Identity Verification", State: StateDeprecated,
		Inputs:  []Interface{{Name: "verify", Kind: KindAPI}, {Name: "retry", Kind: KindMessage}},
		Outputs: []Interface{{Name: "verified", Kind: KindEvent}, {Name: "letter", Kind: KindManual}},
		Resources: []Resource{
			{Kind: ResourceTeam, Name: "KYC"}, {Kind: ResourceSystem, Name: "IdMasters"},
			{Kind: ResourceCapability, Name: "Address Check", Ref: "address-check"},
		},
		Realizations: []Realization{
			{Kind: RealizationProcess, ApplicationKey: "kyc", ProcessID: "identity-verification"},
			{Kind: RealizationWorker, WorkerRef: "idmasters-rest"},
			{Kind: RealizationSystem, Note: "IdMasters SaaS"},
			{Kind: RealizationManual, Note: "Branch clerk checks the passport"},
		},
		Requires: []string{"address-check"},
		KPIs: []KPI{
			{Name: "Speed", Metric: "cycleTime", Goal: "< 10 min", Direction: DirectionDown},
			{Name: "Coverage", Metric: "automationRate", Goal: "> 95%", Direction: DirectionUp},
		},
		SLAs: []SLA{
			{Name: "Decision", Metric: "cycleTime", Threshold: "10 min", Scope: SLAInternal},
			{Name: "Reseller", Metric: "cycleTime", Threshold: "1 d", Scope: SLAExternal, Counterparty: "Acme"},
		},
		Tags: []string{"area:lending", "type:end-to-end"},
	}
	if findings := ValidateCapability(c); len(findings) != 0 {
		t.Errorf("a record using every declared word was refused: %v", findings)
	}
}

func TestValidateValueStream(t *testing.T) {
	tests := []struct {
		name string
		v    ValueStream
		want string // "" means it must be accepted
	}{
		{
			name: "minimal",
			v:    ValueStream{Key: "consumer-loan", Name: "Consumer Loan"},
		},
		{
			name: "ordered stages naming capabilities",
			v: ValueStream{Key: "consumer-loan", Name: "Consumer Loan", Stages: []Stage{
				{Key: "application", Name: "Application submission", Capabilities: []string{"onboarding"}},
				{Key: "underwriting", Name: "Credit evaluation", Capabilities: []string{"underwriting", "scoring"}},
			}},
		},
		{
			// An end-to-end capability is named by every stage it spans. That is the
			// method's accepted inconsistency, and the validator must not "fix" it.
			name: "the same capability in several stages",
			v: ValueStream{Key: "consumer-loan", Name: "Consumer Loan", Stages: []Stage{
				{Key: "a", Name: "A", Capabilities: []string{"loan-application"}},
				{Key: "b", Name: "B", Capabilities: []string{"loan-application"}},
			}},
		},
		{name: "empty key", v: ValueStream{Name: "X"}, want: "key"},
		{name: "empty name", v: ValueStream{Key: "x"}, want: "name"},
		{
			name: "stage without a key",
			v:    ValueStream{Key: "x", Name: "X", Stages: []Stage{{Name: "A"}}},
			want: "key",
		},
		{
			name: "stage without a name",
			v:    ValueStream{Key: "x", Name: "X", Stages: []Stage{{Key: "a"}}},
			want: "name",
		},
		{
			name: "two stages with one key",
			v: ValueStream{Key: "x", Name: "X", Stages: []Stage{
				{Key: "a", Name: "A"}, {Key: "a", Name: "Also A"},
			}},
			want: "twice",
		},
		{
			name: "stage naming the same capability twice",
			v: ValueStream{Key: "x", Name: "X", Stages: []Stage{
				{Key: "a", Name: "A", Capabilities: []string{"c", "c"}},
			}},
			want: "twice",
		},
		{
			name: "stage naming an unsafe capability key",
			v: ValueStream{Key: "x", Name: "X", Stages: []Stage{
				{Key: "a", Name: "A", Capabilities: []string{"../c"}},
			}},
			want: "capabilit",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings := ValidateValueStream(tt.v)
			if tt.want == "" {
				if len(findings) != 0 {
					t.Fatalf("refused: %v", findings)
				}
				return
			}
			if len(findings) == 0 {
				t.Fatalf("accepted %s", tt.name)
			}
			joined := strings.ToLower(strings.Join(findings, " | "))
			if !strings.Contains(joined, strings.ToLower(tt.want)) {
				t.Errorf("findings %q do not mention %q", joined, tt.want)
			}
		})
	}
}

func TestNormalizeTrimsAndDefaults(t *testing.T) {
	c := Capability{Key: "  onboarding  ", Name: "  Customer Onboarding  ", Summary: " s ",
		Requires: []string{" identity ", ""}, Tags: []string{" area:lending ", ""}}
	NormalizeCapability(&c)
	if c.Key != "onboarding" || c.Name != "Customer Onboarding" || c.Summary != "s" {
		t.Errorf("not trimmed: %+v", c)
	}
	if c.State != StateProposed {
		t.Errorf("State = %q, want the default %q — an unstated state is a capability "+
			"somebody has written down but not yet agreed", c.State, StateProposed)
	}
	if len(c.Requires) != 1 || c.Requires[0] != "identity" {
		t.Errorf("Requires = %v, want the blank dropped and the rest trimmed", c.Requires)
	}
	if len(c.Tags) != 1 || c.Tags[0] != "area:lending" {
		t.Errorf("Tags = %v, want the blank dropped and the rest trimmed", c.Tags)
	}
}

func TestNormalizeValueStreamTrims(t *testing.T) {
	v := ValueStream{Key: " consumer-loan ", Name: " Consumer Loan ", Stages: []Stage{
		{Key: " a ", Name: " A ", Capabilities: []string{" onboarding ", ""}},
	}}
	NormalizeValueStream(&v)
	if v.Key != "consumer-loan" || v.Name != "Consumer Loan" {
		t.Errorf("not trimmed: %+v", v)
	}
	if v.Stages[0].Key != "a" || v.Stages[0].Name != "A" {
		t.Errorf("stage not trimmed: %+v", v.Stages[0])
	}
	if len(v.Stages[0].Capabilities) != 1 || v.Stages[0].Capabilities[0] != "onboarding" {
		t.Errorf("stage capabilities = %v", v.Stages[0].Capabilities)
	}
}

func TestSummarize(t *testing.T) {
	c := Capability{
		Key: "k", Name: "N", State: StateActive,
		Realizations: []Realization{{Kind: RealizationManual, Note: "n"}},
		Requires:     []string{"a", "b"},
		KPIs:         []KPI{{Name: "k", Metric: "m"}},
		SLAs:         []SLA{{Name: "s", Metric: "m", Threshold: "t", Scope: SLAInternal}},
	}
	got := summarizeCapability(c, testNow, 12)
	if !got.Realized || got.RealizationCount != 1 || got.RequiresCount != 2 ||
		got.KPICount != 1 || got.SLACount != 1 {
		t.Errorf("summary = %+v", got)
	}

	unrealized := summarizeCapability(Capability{Key: "u", Name: "U"}, testNow, 12)
	if unrealized.Realized {
		t.Error("a capability with no realization summarized as realized")
	}

	v := ValueStream{Key: "v", Name: "V", Stages: []Stage{
		{Key: "a", Name: "A", Capabilities: []string{"x", "y"}},
		{Key: "b", Name: "B", Capabilities: []string{"y"}},
	}}
	vs := summarizeValueStream(v, testNow, 12)
	if vs.StageCount != 2 || vs.CapabilityCount != 2 {
		t.Errorf("value stream summary = %+v, want 2 stages over 2 distinct capabilities", vs)
	}
}

func TestNormalizeTrimsEveryNestedField(t *testing.T) {
	// Every field a person can type is trimmed, because a trailing space in a
	// realization's process id is a realization that resolves to nothing and looks
	// correct on screen.
	c := Capability{
		Key: " k ", Name: " N ", Scope: " s ", State: " " + StateActive + " ",
		Owner:     Owner{Name: " O ", Role: " R ", Contact: " c@x ", Username: " u "},
		Inputs:    []Interface{{Name: " in ", Kind: " " + KindAPI + " ", Description: " d "}},
		Outputs:   []Interface{{Name: " out ", Kind: " " + KindEvent + " ", Description: " d "}},
		Resources: []Resource{{Kind: " " + ResourceTeam + " ", Name: " KYC ", Ref: " r "}},
		Realizations: []Realization{
			{Kind: " " + RealizationProcess + " ", ApplicationKey: " app ", ProcessID: " p "},
			{Kind: " " + RealizationWorker + " ", WorkerRef: " w "},
			{Kind: " " + RealizationManual + " ", Note: " clerk "},
		},
		KPIs: []KPI{{Name: " k ", Metric: " m ", Goal: " g ", Direction: " " + DirectionUp + " ", Note: " n "}},
		SLAs: []SLA{{Name: " s ", Metric: " m ", Threshold: " t ", Window: " w ",
			Scope: " " + SLAInternal + " ", Counterparty: " c ", Note: " n "}},
	}
	NormalizeCapability(&c)
	if findings := ValidateCapability(c); len(findings) != 0 {
		t.Fatalf("a record of padded fields did not normalize into a valid one: %v", findings)
	}
	if c.Owner.Name != "O" || c.Owner.Role != "R" || c.Owner.Contact != "c@x" || c.Owner.Username != "u" {
		t.Errorf("owner = %+v", c.Owner)
	}
	if c.Inputs[0].Name != "in" || c.Inputs[0].Description != "d" ||
		c.Outputs[0].Name != "out" || c.Outputs[0].Description != "d" {
		t.Errorf("interfaces = %+v %+v", c.Inputs, c.Outputs)
	}
	if c.Resources[0].Name != "KYC" || c.Resources[0].Ref != "r" {
		t.Errorf("resources = %+v", c.Resources)
	}
	if c.Realizations[0].ApplicationKey != "app" || c.Realizations[0].ProcessID != "p" ||
		c.Realizations[1].WorkerRef != "w" || c.Realizations[2].Note != "clerk" {
		t.Errorf("realizations = %+v", c.Realizations)
	}
	if c.KPIs[0].Goal != "g" || c.KPIs[0].Note != "n" {
		t.Errorf("kpis = %+v", c.KPIs)
	}
	if c.SLAs[0].Window != "w" || c.SLAs[0].Counterparty != "c" || c.SLAs[0].Note != "n" {
		t.Errorf("slas = %+v", c.SLAs)
	}

	v := ValueStream{Key: " v ", Name: " V ", Description: " d ",
		Owner: Owner{Name: " O "},
		KPIs:  []KPI{{Name: " k ", Metric: " m "}}}
	NormalizeValueStream(&v)
	if v.Description != "d" || v.Owner.Name != "O" || v.KPIs[0].Name != "k" {
		t.Errorf("value stream = %+v", v)
	}
}

func TestValidateRefusesADuplicateTag(t *testing.T) {
	c := Capability{Key: "a", Name: "A", State: StateActive, Tags: []string{"x", "x"}}
	findings := ValidateCapability(c)
	if len(findings) == 0 || !strings.Contains(strings.Join(findings, " "), "twice") {
		t.Errorf("findings = %v, want the repeated tag reported", findings)
	}
}

func TestValidateValueStreamRefusesAnUnsafeStageKeyAndABadKPI(t *testing.T) {
	v := ValueStream{Key: "v", Name: "V",
		Stages: []Stage{{Key: "../x", Name: "X"}},
		KPIs:   []KPI{{Name: "k", Metric: "m", Direction: "sideways"}},
		Tags:   []string{"t", "t"}}
	findings := ValidateValueStream(v)
	joined := strings.Join(findings, " | ")
	for _, want := range []string{"stage 1", "direction", "twice"} {
		if !strings.Contains(joined, want) {
			t.Errorf("findings %q do not mention %q", joined, want)
		}
	}
}
