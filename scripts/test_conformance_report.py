"""CON5 guards: the public report and the documented counts cannot drift or overclaim."""
from __future__ import annotations

from copy import deepcopy
import json
import tempfile
from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parent))
import conformance_registry as registry  # noqa: E402
import conformance_report as report  # noqa: E402


class ConformanceReportTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.data = json.loads(registry.REGISTRY.read_text(encoding="utf-8"))
        cls.manifest = json.loads(report.MANIFEST.read_text(encoding="utf-8"))
        cls.facts = report.suite_facts()
        cls.tmp = tempfile.TemporaryDirectory()

    @classmethod
    def tearDownClass(cls):
        cls.tmp.cleanup()

    def test_report_has_not_drifted(self):
        self.assertEqual(report.REPORT.read_text(encoding="utf-8"),
                         report.render(self.data, self.manifest, self.facts))

    def test_documented_counts_have_not_drifted(self):
        values = report.counts(self.data, self.facts)
        for rel in report.MARKED_DOCS:
            text = (report.ROOT / rel).read_text(encoding="utf-8")
            self.assertEqual(text, report.rewrite_markers(text, values, rel), rel)

    def test_every_marked_doc_carries_a_marker(self):
        for rel in report.MARKED_DOCS:
            self.assertRegex((report.ROOT / rel).read_text(encoding="utf-8"), report.MARKER, rel)

    def test_stale_count_is_rewritten(self):
        values = report.counts(self.data, self.facts)
        stale = "registers <!-- con:features -->31<!-- /con --> features"
        self.assertEqual(report.rewrite_markers(stale, values, "t"),
                         f"registers <!-- con:features -->{values['features']}<!-- /con --> features")

    def test_unknown_marker_fails(self):
        with self.assertRaisesRegex(report.ReportError, "unknown count marker"):
            report.rewrite_markers("<!-- con:coverage -->95<!-- /con -->", {}, "t")

    def test_no_percentage_without_an_approved_denominator(self):
        with self.assertRaisesRegex(report.ReportError, "percentage"):
            report.check_no_percentage("95% supported", {"denominatorApproved": False})
        report.check_no_percentage("95% supported", {"denominatorApproved": True})

    def test_current_report_states_no_percentage(self):
        report.check_no_percentage(report.render(self.data, self.manifest, self.facts), self.data)

    def test_deviation_must_name_a_requirement(self):
        altered = deepcopy(self.data)
        altered["deviations"][0]["requirement"] = "FLOW-99"
        with self.assertRaisesRegex(report.ReportError, "names no registered requirement"):
            report.deviation_check(altered)

    def test_deviation_must_cite_its_decision(self):
        altered = deepcopy(self.data)
        altered["deviations"][0]["adr"] = []
        with self.assertRaisesRegex(report.ReportError, "cite the decision"):
            report.deviation_check(altered)

    def test_deviation_ids_are_unique(self):
        altered = deepcopy(self.data)
        altered["deviations"][1]["id"] = altered["deviations"][0]["id"]
        with self.assertRaisesRegex(report.ReportError, "duplicate"):
            report.deviation_check(altered)

    def test_catalog_mirror_of_the_differential_subset_is_checked(self):
        original = report.CATALOG_MIRROR
        stale = Path(self.tmp.name) / "catalog_test.go"
        text = original.read_text(encoding="utf-8").replace('\t"linear-independent":   true,\n', "", 1)
        self.assertNotEqual(text, original.read_text(encoding="utf-8"), "fixture no longer finds the entry")
        stale.write_text(text, encoding="utf-8")
        report.CATALOG_MIRROR = stale
        try:
            with self.assertRaisesRegex(report.ReportError, "differentialSubset differs"):
                report.suite_facts()
        finally:
            report.CATALOG_MIRROR = original

    def test_unsupported_rows_are_listed(self):
        text = report.render(self.data, self.manifest, self.facts)
        for ident in registry.UNSUPPORTED:
            self.assertIn(f"| {ident} |", text)


if __name__ == "__main__":
    unittest.main()
