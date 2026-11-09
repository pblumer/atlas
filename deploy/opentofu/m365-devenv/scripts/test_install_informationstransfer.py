"""Offline checks of install-informationstransfer.py: the constants and the rewrite of their fallbacks.

    python3 -m unittest discover -s deploy/opentofu/m365-devenv/scripts -p 'test_install_informationstransfer.py' -v
"""
import os
import re
import shutil
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
it = __import__("install-informationstransfer")
EXAMPLE = os.path.join(HERE, "..", "..", "..", "..", "examples", "informationstransfer")
TEST_OU = "OU=AtlasTest,DC=atlastest,DC=internal"


class Rewrite(unittest.TestCase):
    def setUp(self):
        self.work = tempfile.mkdtemp()
        self.pkg = os.path.join(self.work, "informationstransfer")
        self.constants = it.constants_for(TEST_OU, "exchange-server", "atlas-test-x")
        it.adapt(EXAMPLE, self.pkg, self.constants)

    def tearDown(self):
        shutil.rmtree(self.work, ignore_errors=True)

    def test_no_contoso_dn_is_left(self):
        _, files = it.uploaded_files(self.pkg)
        for f in files:
            self.assertNotRegex(open(os.path.join(self.pkg, f), encoding="utf-8").read(), r"(?i)DC=contoso", f)

    def test_the_entra_transfer_stays_off(self):
        self.assertEqual(self.constants["ENTRA_TRANSFER"], "aus")
        s = open(os.path.join(self.pkg, "entra.bpmn"), encoding="utf-8").read()
        self.assertIn('<atlas:constant name="ENTRA_TRANSFER" type="string" source="application" value="aus"/>', s)

    def test_every_declared_constant_is_set_and_followed(self):
        c = self.constants
        self.assertEqual(c["AD_GRUPPEN_OU"], "OU=Gruppen,OU=Atlas," + TEST_OU)
        declared = set()
        for name in os.listdir(self.pkg):
            if name.endswith(".bpmn"):
                s = open(os.path.join(self.pkg, name), encoding="utf-8").read()
                for k, v in re.findall(r'<atlas:constant name="([A-Z_]+)"[^>]*?\bvalue="([^"]*)"', s):
                    declared.add(k)
                    self.assertEqual(v, c[k], f"{name}: {k}")
        self.assertEqual(declared, set(c))

    def test_an_unknown_mail_system_is_refused(self):
        with self.assertRaises(SystemExit):
            it.constants_for(TEST_OU, "notes", "")


if __name__ == "__main__":
    unittest.main()
