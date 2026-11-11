package catalog

import (
	"strings"
	"testing"
)

// Every operation is a read that answers a report, and the table says so in the terms
// the manifest declares (ADR-0538): a repeated delivery computes the report again.
func TestEveryOperationIsAReadThatAnswersAReport(t *testing.T) {
	names := Names()
	if strings.Join(names, ",") != "delivery-report,incident-summary,instances-summary,process-statistics" {
		t.Fatalf("operations = %v", names)
	}
	for _, n := range names {
		op, ok := Lookup(n)
		if !ok {
			t.Fatalf("Lookup(%q) found nothing", n)
		}
		if !op.TakesResult() || !op.NeedsResult || op.Result != ResultReport {
			t.Errorf("%s does not answer a report a model must keep", n)
		}
		if op.Idempotency != IdempotencyNatural || op.Effect != EffectReversible {
			t.Errorf("%s declares %s/%s", n, op.Idempotency, op.Effect)
		}
		if !strings.HasPrefix(op.Route, "GET /api/v1/") {
			t.Errorf("%s reads %q, not a read route", n, op.Route)
		}
		for _, k := range op.Required {
			if !strings.Contains(","+strings.Join(op.Inputs, ",")+",", ","+k+",") {
				t.Errorf("%s requires %q, which it does not take", n, k)
			}
		}
	}
	if _, ok := Lookup("kaffee"); ok {
		t.Error("an unknown operation was found")
	}
	if (Op{}).TakesResult() {
		t.Error("an operation without a result takes one")
	}
	if op, _ := Lookup(OpDeliveryReport); !op.Catalogue {
		t.Error("the delivery report is not marked as the catalogue's")
	}
}

func TestInputKeysAreCheckedAgainstTheOperation(t *testing.T) {
	for _, c := range []struct {
		op   string
		keys []string
		want string
	}{
		{"kaffee", nil, "unknown operation"},
		{OpInstancesSummary, []string{"buckets"}, `does not take "buckets"`},
		{OpProcessStatistics, []string{"title"}, `needs "process"`},
		{OpDeliveryReport, []string{"catalog", "from", "to"}, ""},
		{OpProcessStatistics, []string{"process", "buckets", "bucket"}, ""},
	} {
		err := CheckInputKeys(c.op, c.keys)
		if c.want == "" && err != nil || c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
			t.Errorf("CheckInputKeys(%s, %v) = %v, want %q", c.op, c.keys, err, c.want)
		}
	}
}

func TestValuesAreChecked(t *testing.T) {
	for _, b := range Buckets {
		if CheckBucket(b) != nil {
			t.Errorf("bucket %q refused", b)
		}
	}
	if CheckBucket("year") == nil {
		t.Error("bucket year accepted")
	}
	for _, l := range Languages {
		if CheckLanguage(l) != nil {
			t.Errorf("language %q refused", l)
		}
	}
	if CheckLanguage("fr") == nil {
		t.Error("language fr accepted")
	}
	for n, ok := range map[int64]bool{0: false, 1: true, MaxBuckets: true, MaxBuckets + 1: false, -3: false} {
		if (CheckBuckets(n) == nil) != ok {
			t.Errorf("CheckBuckets(%d) = %v", n, CheckBuckets(n))
		}
	}
}
