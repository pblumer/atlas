"""Tests for the deterministic programme registry contract (no network)."""

import contextlib
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock

import programs


class ProgrammeIndexTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.folder = self.root / "docs/programs/sample"
        self.folder.mkdir(parents=True)
        (self.root / "docs/planning").mkdir(parents=True)
        (self.root / "docs/planning/sample.md").write_text("# Programme\n")
        (self.root / "ROADMAP.md").write_text("# Roadmap\n")
        self.manifest = self.folder / "program.json"
        self.data = {
            "id": "sample",
            "name": "Sample programme",
            "summary": "Explain what will be delivered.",
            "status": "active",
            "owner": "maintainer",
            "last_reviewed": "2026-10-25",
            "plan": "../../planning/sample.md",
            "milestone": "../../../ROADMAP.md",
            "next_action": "Review the first slice.",
            "slices": [
                {"id": "S0", "title": "First vertical slice", "status": "active",
                 "owner": "maintainer", "depends_on": [], "issues": [12],
                 "prs": [34], "note": "Reviews pending."},
                {"id": "S1", "title": "Second slice", "status": "planned",
                 "owner": "unassigned", "depends_on": ["S0"], "issues": [],
                 "prs": [], "note": "Wait for S0."},
            ],
        }
        self.write()

    def write(self):
        self.manifest.write_text(json.dumps(self.data, indent=2) + "\n")

    def assert_invalid(self, message):
        with self.assertRaisesRegex(programs.ProgramError, message):
            programs.collect(self.root)

    def test_index_generation_and_check_is_deterministic(self):
        self.assertEqual(programs.main([], root=self.root), 0)
        output = (self.root / "docs/programs/README.md").read_text()
        self.assertIn("[Sample programme](../planning/sample.md)", output)
        self.assertIn("0/2", output)
        self.assertIn("PR #34", output)
        self.assertIn("Issue #12", output)
        self.assertIn("S0", output)
        self.assertEqual(programs.main(["--check"], root=self.root), 0)
        self.assertEqual(programs.render(self.root, programs.collect(self.root)), output)

    def test_check_detects_stale_readme(self):
        self.assertEqual(programs.main([], root=self.root), 0)
        (self.root / "docs/programs/README.md").write_text("stale\n")
        with contextlib.redirect_stderr(io.StringIO()) as captured:
            self.assertEqual(programs.main(["--check"], root=self.root), 1)
        self.assertIn("Stale index", captured.getvalue())

    def test_generation_is_offline(self):
        with mock.patch.object(programs.urllib.request, "urlopen",
                               side_effect=AssertionError("unexpected network")):
            self.assertEqual(programs.main([], root=self.root), 0)

    def test_duplicate_slice_ids_are_rejected(self):
        self.data["slices"][1]["id"] = "S0"
        self.write()
        self.assert_invalid("duplicate/invalid slice id")

    def test_missing_dependency_is_rejected(self):
        self.data["slices"][1]["depends_on"] = ["S42"]
        self.write()
        self.assert_invalid("unknown dependency")

    def test_dependency_cycles_are_rejected(self):
        self.data["slices"][0]["depends_on"] = ["S1"]
        self.write()
        self.assert_invalid("dependency cycle")

    def test_completion_requires_local_evidence(self):
        self.data["slices"][0]["status"] = "complete"
        self.write()
        self.assert_invalid("completion requires evidence")

    def test_completed_programme_cannot_have_open_slices(self):
        self.data["status"] = "complete"
        self.write()
        self.assert_invalid("complete programme still has open slices")

    def test_completion_requires_accountable_owner(self):
        self.data["slices"][0]["status"] = "complete"
        self.data["slices"][0]["owner"] = "unassigned"
        self.data["slices"][0]["evidence"] = ["../../planning/sample.md"]
        self.write()
        self.assert_invalid("completed slice requires an assigned owner")

    def test_invalid_github_references_are_rejected(self):
        self.data["slices"][0]["prs"] = ["34"]
        self.write()
        self.assert_invalid("must be positive numbers")

    def test_missing_document_is_rejected(self):
        self.data["plan"] = "../../planning/absent.md"
        self.write()
        self.assert_invalid("missing file")

    def test_local_reference_cannot_escape_repository(self):
        self.data["milestone"] = "../../../../../etc/passwd"
        self.write()
        self.assert_invalid("path escapes repository")

    def test_invalid_review_date_is_rejected(self):
        self.data["last_reviewed"] = "not-a-date"
        self.write()
        self.assert_invalid("invalid last_reviewed")

    def test_completion_evidence_must_be_in_repository(self):
        self.data["slices"][0]["status"] = "complete"
        self.data["slices"][0]["evidence"] = ["../../planning/sample.md"]
        self.write()
        self.assertEqual(len(programs.collect(self.root)), 1)

    def test_current_repository_manifest_is_valid(self):
        # Keep the pilot contract under the same validation as future programmes.
        found = programs.collect(programs.ROOT)
        self.assertIn("cda", [p["id"] for _, p in found])


if __name__ == "__main__":
    unittest.main()
