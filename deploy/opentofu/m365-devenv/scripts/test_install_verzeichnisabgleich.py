"""Offline checks of install-verzeichnisabgleich.py: the environment copy of the directory mirroring.

    python3 -m unittest discover -s deploy/opentofu/m365-devenv/scripts -p 'test_install_verzeichnisabgleich.py' -v
"""
import os
import re
import sys
import unittest
import xml.etree.ElementTree as ET

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
va = __import__("install-verzeichnisabgleich")
ENV = "atlas-test-58o4"


def source():
    return open(va.EXAMPLE, encoding="utf-8").read()


class Copy(unittest.TestCase):
    def setUp(self):
        self.xml = va.adapt(source(), ENV, "ucc_entra")

    def test_both_deltas_keep_only_this_environment(self):
        root = ET.fromstring(self.xml.encode("utf-8"))
        keeps = [e.get("keep") for e in root.iter("{http://atlas/schema/1.0}entraConnector")]
        self.assertEqual(keeps, [
            f"department eq '{ENV}'",
            f"startswith(displayName,'{ENV}-')",
        ])

    def test_the_users_select_carries_what_the_keep_reads(self):
        self.assertIn('select="id,userPrincipalName,displayName,mail,accountEnabled,manager,department"', self.xml)

    def test_the_entra_worker_is_this_environments(self):
        self.assertEqual(self.xml.count('connector="ucc_entra"'), 2)
        self.assertNotIn("contoso", re.sub(r"<!--.*?-->", "", self.xml, flags=re.S))

    def test_atlas_is_called_as_rest_connector_tasks(self):
        self.assertNotIn('type="rest"', self.xml)
        self.assertEqual(self.xml.count('authSecret="verzeichnisabgleich"'), 2)
        self.assertEqual(self.xml.count('url="http://127.0.0.1:8080/api/v1/directory-sync"'), 2)

    def test_the_dry_run_is_the_default(self):
        self.assertIn('<zeebe:input source="=false" target="apply" />', self.xml)
        applied = va.adapt(source(), ENV, "ucc_entra", apply=True)
        self.assertIn('<zeebe:input source="=true" target="apply" />', applied)

    def test_the_copy_is_its_own_process(self):
        self.assertIn(f'id="{va.COPY_ID}"', self.xml)
        self.assertNotRegex(self.xml, va.PROCESS_ID + r'(?!_devenv)')

    def test_interval(self):
        self.assertIn("<bpmn:timeCycle>R/PT15M</bpmn:timeCycle>", va.adapt(source(), ENV, "e", interval="PT15M"))
        with self.assertRaises(SystemExit):
            va.adapt(source(), ENV, "e", interval="15m")

    def test_an_environment_id_that_would_change_the_predicate_is_refused(self):
        with self.assertRaises(SystemExit):
            va.keeps("x' or department eq 'y")

    def test_one_entra_worker(self):
        self.assertEqual(va.entra_worker_of({"ucc_entra": "entra", "ucc_mail": "mail"}), "ucc_entra")
        with self.assertRaises(SystemExit):
            va.entra_worker_of({"ucc_mail": "mail"})


if __name__ == "__main__":
    unittest.main()
