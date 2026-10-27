package blueprintregistry

import (
	"strings"
	"testing"
)

func TestVersionValidation(t *testing.T) {
	for _, version := range []string{
		"0.2.0",
		"1.0.0",
		"1.0.0-alpha",
		"1.0.0-beta.2",
		"99999999999999999999999999.0.1",
		"0.0.1-1",
	} {
		if !validVersion(version) {
			t.Errorf("valid semantic version rejected: %q", version)
		}
	}
	for _, version := range []string{
		"",
		"1.0",
		"v1.0.0",
		"01.2.3",
		"1.02.3",
		"1.2.03",
		"1.2.3+metadata",
		"1.2.3-01",
		"1.2.3-foo.01",
		"1.2.3-",
		"1.2.3-foo..bar",
		strings.Repeat("9", 65) + ".0.0",
	} {
		if validVersion(version) {
			t.Errorf("invalid semantic version accepted: %q", version)
		}
	}
}

func TestVersionPrecedence(t *testing.T) {
	versions := []string{
		"1.0.0-alpha",
		"1.0.0-alpha.1",
		"1.0.0-alpha.beta",
		"1.0.0-beta",
		"1.0.0-beta.2",
		"1.0.0-beta.11",
		"1.0.0-rc.1",
		"1.0.0",
		"1.0.1",
		"1.1.0",
		"2.0.0",
	}
	for i := 0; i < len(versions); i++ {
		if !validVersion(versions[i]) || compareVersion(versions[i], versions[i]) != 0 {
			t.Fatalf("version must equal itself: %s", versions[i])
		}
		for j := i + 1; j < len(versions); j++ {
			if compareVersion(versions[i], versions[j]) >= 0 {
				t.Errorf("expected %s < %s", versions[i], versions[j])
			}
			if compareVersion(versions[j], versions[i]) <= 0 {
				t.Errorf("expected %s > %s", versions[j], versions[i])
			}
		}
	}
	if compareVersion("999999999999999999999999999.0.0", "9.0.0") <= 0 {
		t.Fatal("very large semantic numeric identifiers must not overflow")
	}
}

func TestVerifySignedIndexRejectsNestedDuplicateAndExcessiveDepth(t *testing.T) {
	raw := testIndex(t)
	duplicate := bytesReplace(t, raw, "\"sizeBytes\": 2048", "\"sizeBytes\": 2048, \"sizeBytes\": 2048")
	if err := verifyTestIndex(t, duplicate); err == nil {
		t.Fatal("nested duplicate JSON key accepted")
	}
	deep := []byte(strings.Repeat("[", 34) + "0" + strings.Repeat("]", 34))
	if err := verifyTestIndex(t, deep); err == nil {
		t.Fatal("excessively nested JSON accepted")
	}
}

func TestVerifySignedIndexRequiresRequirementLists(t *testing.T) {
	for _, key := range []string{"workerTypes", "serverConstants"} {
		raw := mutateTestIndex(t, func(root map[string]any) {
			delete(releaseFields(root)["needs"].(map[string]any), key)
		})
		if err := verifyTestIndex(t, raw); err == nil {
			t.Errorf("missing required list %s accepted", key)
		}
	}
}

func TestVerifySignedIndexAllowsEmptyRegistry(t *testing.T) {
	raw := mutateTestIndex(t, func(root map[string]any) {
		root["releases"] = []any{}
	})
	if err := verifyTestIndex(t, raw); err != nil {
		t.Fatalf("empty but correctly signed public catalog rejected: %v", err)
	}
}

func TestBlueprintCompatibilityMustFitSignedRange(t *testing.T) {
	req := Compatibility{MinVersion: "0.20.0", MaxVersionExclusive: "0.21.0"}
	for _, v := range []string{"0.19.9", "0.20.0-dev", "0.21.0", "v0.20.1", ""} {
		if Compatible(req, v) {
			t.Errorf("accepted atlas %q outside signed range", v)
		}
	}
	for _, v := range []string{"0.20.0", "0.20.1", "0.21.0-rc.1"} {
		if !Compatible(req, v) {
			t.Errorf("rejected compatible atlas %q", v)
		}
	}
	if Compatible(Compatibility{MinVersion: "0.21.0", MaxVersionExclusive: "0.20.0"}, "0.20.0") {
		t.Fatal("accepted inverted signed compatibility range")
	}
	if !Compatible(Compatibility{MinVersion: "0.1.0"}, "0.20.0-dev") {
		t.Fatal("rejected compatible prerelease atlas")
	}
}
