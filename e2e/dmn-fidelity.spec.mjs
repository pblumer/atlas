// A model the editor opens has to be the model the editor saves.
//
// Atlas stores what it is given and serves it back through a text splice, so the Go
// side gives a document back unchanged (dmn/roundtrip_test.go). The editor is the
// other half, and it is the half that rewrites: dmn-js parses the whole document into
// its own object model and writes that object model back out, so anything its
// descriptors do not declare is dropped — without an error, without a diff, in a file
// somebody wrote by hand. Open, save, and it is gone.
//
// This holds the shipped bundle to exactly what it loses today, and to exactly what it
// complains about. Both directions matter: a new entry in either list is a new silent
// loss or a new complaint and fails; an entry that stops appearing means a gap was
// closed and the list must shrink, which also fails. Either way somebody looks.
import { test, expect } from "@playwright/test";
import fs from "node:fs";

const MODEL = fs.readFileSync(
  new URL("../dmn/testdata/roundtrip-kitchen-sink.dmn", import.meta.url), "utf8");

// Nothing. A model carrying the whole surface below goes through the editor and comes
// back whole — every element, every attribute, every value, including the extension
// element in a namespace nobody here declares and the whole of the diagram.
//
// The list is here rather than the assertion being a bare "lose nothing" because the
// day something does start being lost, the honest move is to write it down with its
// reason and its cost, not to soften the test.
const KNOWN_LOSSES = [];

// One warning, and it costs no data — which is the reason to hold it rather than the
// losses alone. useAlternativeInputDataShape is declared on DMNDiagram, as an element,
// where DMN 1.5 puts it on DMNShape as an attribute. So the import calls it unknown,
// and yet it survives verbatim, because an attribute nothing claims is written back as
// it was found. Nothing is lost and nothing reads it either: an input datum an author
// chose to draw as the paper symbol is still drawn as an oval. A warning with no loss
// behind it is exactly the kind that gets dismissed, so it is written down instead.
const KNOWN_WARNINGS = [
  "unknown attribute <useAlternativeInputDataShape>",
];

test("the shipped editor gives back the model it was given", async ({ page }) => {
  await page.goto("/harness.html");
  await page.addScriptTag({ url: "/vendor/dmn/dmn-modeler.js" });

  const result = await page.evaluate(async (xml) => {
    document.body.innerHTML = '<div id="dmn" style="width:1200px;height:700px"></div>';

    const modeler = new window.AtlasDmn.DmnJS({ container: "#dmn", dmnVersion: "1.5" });
    const imported = await modeler.importXML(xml);
    const saved = await modeler.saveXML({ format: true });

    // A document reduced to what it says: one fact per element, one per attribute
    // with its value. Order, indentation and namespace prefixes are how a document is
    // written rather than what it states, so none of them is a fact.
    const factsOf = (text) => {
      const doc = new DOMParser().parseFromString(text, "application/xml");
      const facts = new Map();
      const add = (f) => facts.set(f, (facts.get(f) || 0) + 1);
      const walk = (el) => {
        add("<" + el.localName + ">");
        for (const a of el.attributes) {
          if (a.name === "xmlns" || a.name.startsWith("xmlns:")) continue;
          add(el.localName + "@" + a.localName + "=" + a.value);
        }
        for (const child of el.children) walk(child);
      };
      walk(doc.documentElement);
      return facts;
    };

    const before = factsOf(xml), after = factsOf(saved.xml);
    const lost = [];
    for (const [fact, n] of before) {
      if ((after.get(fact) || 0) < n) lost.push(fact);
    }
    return {
      lost: lost.sort(),
      facts: before.size,
      warnings: imported.warnings.map((w) => w.message.split("\n")[0]).sort(),
      savedIsXML: saved.xml.trim().startsWith("<"),
    };
  }, MODEL);

  expect(result.savedIsXML, "the editor saved a document at all").toBe(true);
  // A fixture that stopped saying anything would let every assertion below pass while
  // proving nothing.
  expect(result.facts, "the fixture still carries a model worth checking").toBeGreaterThan(150);
  expect(result.warnings).toEqual(KNOWN_WARNINGS);
  expect(result.lost).toEqual(KNOWN_LOSSES);
});
