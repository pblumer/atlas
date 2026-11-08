"""Regression guards: a candidate must never disappear or turn into an unearned pass."""
from __future__ import annotations

from copy import deepcopy
import json
from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parent))
import conformance_registry as registry


class ConformanceRegistryTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.data = json.loads(registry.REGISTRY.read_text(encoding="utf-8"))

    def assert_invalid(self, mutated, reason):
        with self.assertRaisesRegex(registry.RegistryError, reason):
            registry.validate(mutated)

    def test_frozen_inventory_is_valid(self):
        registry.validate(deepcopy(self.data))

    def test_report_has_not_drifted(self):
        self.assertEqual(registry.REPORT.read_text(encoding="utf-8"),
                         registry.render(self.data))

    def test_missing_registered_candidate_fails(self):
        altered = deepcopy(self.data)
        altered["requirements"] = [x for x in altered["requirements"] if x["id"] != "FLOW-10"]
        self.assert_invalid(altered, "CON0 IDs changed")

    def test_duplicate_id_fails(self):
        altered = deepcopy(self.data)
        altered["requirements"][1]["id"] = altered["requirements"][0]["id"]
        self.assert_invalid(altered, "duplicate requirement")

    def test_known_unsupported_cannot_become_implemented(self):
        altered = deepcopy(self.data)
        next(x for x in altered["requirements"] if x["id"] == "FLOW-10")["implementation"] = "implemented"
        self.assert_invalid(altered, "known gap")

    def test_refuse_unapproved_compliance_claim(self):
        altered = deepcopy(self.data)
        altered["denominatorApproved"] = True
        self.assert_invalid(altered, "owner sign-off")

    def test_reference_to_missing_code_is_rejected(self):
        altered = deepcopy(self.data)
        altered["requirements"][0]["sourceRefs"] = ["engine/not-a-file.go"]
        self.assert_invalid(altered, "missing/outside repo")

    def test_reference_to_missing_test_is_rejected(self):
        altered = deepcopy(self.data)
        item = next(x for x in altered["requirements"] if x["id"] == "FLOW-10")
        item["testRefs"] = ["compiler/parse_test.go:TestNotReallyInSource"]
        self.assert_invalid(altered, "missing test")

    def test_sample_cannot_count_without_scenario(self):
        altered = deepcopy(self.data)
        item = next(x for x in altered["requirements"] if x["id"] == "ACT-02")
        item["scenarioIds"] = []
        self.assert_invalid(altered, "sample evidence missing")

    def test_illegal_event_combination_is_not_missing_support(self):
        altered = deepcopy(self.data)
        item = next(x for x in altered["eventPositions"] if x["family"] == "Error"
                    and x["position"] == "top-start")
        item["implementation"] = "unsupported"
        self.assert_invalid(altered, "illegal construct counted as gap")

    def test_valid_event_cannot_disappear(self):
        altered = deepcopy(self.data)
        altered["eventPositions"].pop()
        self.assert_invalid(altered, "13x8 cells")

    def test_event_legality_is_locked_to_omg(self):
        altered = deepcopy(self.data)
        item = next(x for x in altered["eventPositions"] if x["family"] == "Link"
                    and x["position"] == "intermediate-throw")
        item["legal"] = False
        self.assert_invalid(altered, "contradicts OMG table")

    def test_wcp_inventory_cannot_shrink(self):
        altered = deepcopy(self.data)
        altered["wcp"] = altered["wcp"][1:]
        self.assert_invalid(altered, "43 patterns")

    def test_wcp_registration_is_not_semantics_proof(self):
        altered = deepcopy(self.data)
        next(x for x in altered["wcp"] if x["id"] == "WCP-1")["verification"] = "sample-tested"
        self.assert_invalid(altered, "registration is not proof")

    def test_separate_modeling_class_must_remain_excluded(self):
        altered = deepcopy(self.data)
        next(x for x in altered["requirements"] if x["id"] == "MODEL-07")["applicability"] = "required"
        self.assert_invalid(altered, "separate conformance class counted in execution")


if __name__ == "__main__":
    unittest.main()
