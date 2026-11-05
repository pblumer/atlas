"""Offline checks of install-ad-dienste.py: the DN rewrite and its agreement with ad-setup.ps1.

    python3 -m unittest discover -s deploy/opentofu/m365-devenv/scripts -p 'test_install_ad_dienste.py' -v
"""
import os
import re
import shutil
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
ad = __import__("install-ad-dienste")
EXAMPLE = os.path.join(HERE, "..", "..", "..", "..", "examples", "ad-dienste")
TEST_OU = "OU=AtlasTest,DC=atlastest,DC=internal"


class Rewrite(unittest.TestCase):
    def setUp(self):
        self.work = tempfile.mkdtemp()
        self.pkg = os.path.join(self.work, "ad-dienste")
        self.constants = ad.constants_for(TEST_OU, "example.onmicrosoft.com")
        self.dns = ad.adapt(EXAMPLE, self.pkg, TEST_OU, self.constants)

    def tearDown(self):
        shutil.rmtree(self.work, ignore_errors=True)

    def test_no_contoso_dn_is_left(self):
        _, files = ad.uploaded_files(self.pkg)
        for f in files:
            self.assertNotRegex(open(os.path.join(self.pkg, f), encoding="utf-8").read(), r"(?i)DC=contoso", f)

    def test_tables_lie_under_the_test_ou(self):
        s = open(os.path.join(self.pkg, "ad-berechtigungen.dmn"), encoding="utf-8").read()
        self.assertIn("CN=SG-FIN-Fileshare-Finanzen-Aendern,OU=Berechtigungen,OU=Gruppen," + TEST_OU, s)
        s = open(os.path.join(self.pkg, "ad-oe.dmn"), encoding="utf-8").read()
        self.assertIn('"OU=FIN,OU=Personen,' + TEST_OU + '"', s)

    def test_constants(self):
        c = self.constants
        self.assertEqual(c["AD_DOMAIN_DN"], "DC=atlastest,DC=internal")
        self.assertEqual(c["AD_PERSONEN_OU"], TEST_OU)
        self.assertEqual(c["AD_GRUPPEN_OU"], "OU=Gruppen,OU=Atlas," + TEST_OU)
        self.assertEqual(c["AD_SCHULUNG_OU"], "OU=Schulungskonten,OU=Atlas," + TEST_OU)
        # Every constant the processes declare is set, and their fallbacks follow it.
        declared = set()
        for name in os.listdir(self.pkg):
            if name.endswith(".bpmn"):
                s = open(os.path.join(self.pkg, name), encoding="utf-8").read()
                for k, v in re.findall(r'<atlas:constant name="([A-Z_]+)"[^>]*?\bvalue="([^"]*)"', s):
                    declared.add(k)
                    self.assertEqual(v, c[k], f"{name}: {k}")
        self.assertEqual(declared, set(c))

    def test_ad_setup_creates_what_the_tables_and_constants_name(self):
        ps1 = open(os.path.join(HERE, "ad-setup.ps1"), encoding="utf-8").read()
        body = ps1[ps1.index("function New-AdDiensteObjects"):]
        names = set(re.findall(r"'([A-Za-z0-9-]+)'", body))
        for dn in self.dns + [v for k, v in self.constants.items() if k.endswith("_OU")]:
            for rdn in dn[: -len(TEST_OU) - 1].split(",") if dn != TEST_OU else []:
                self.assertIn(rdn.split("=", 1)[1], names, f"{dn}: ad-setup.ps1 does not create {rdn}")

    def test_domain_dn_needs_dc_components(self):
        self.assertEqual(ad.domain_dn_of("OU=X,DC=a,DC=b"), "DC=a,DC=b")
        with self.assertRaises(SystemExit):
            ad.domain_dn_of("OU=X")


if __name__ == "__main__":
    unittest.main()
