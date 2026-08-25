#!/usr/bin/env node
// gen.mjs — builds api/web/whats-new.json for the Console "What's New" section.
//
// CHANGELOG.md is the single source of truth: this reads its version sections and
// Added/Changed/Fixed bullets and derives the *structure* of each entry — a stable
// id, the headline, the version/date, and a link to the ADR or PR it names. It then
// merges a curated overrides file (scripts/whats-new/overrides.json) that supplies
// the layman-friendly, bilingual (DE/EN) summary, tags, optional step-by-step
// tutorial and an optional "Try it" deep link. Entries without an override still
// appear (English CHANGELOG title + first-sentence summary; German falls back to
// English), so a new CHANGELOG entry surfaces automatically and the CHANGELOG never
// has to carry marketing prose.
//
// The output is committed and embedded (api/server.go's //go:embed web), so the web
// UI stays buildless (ADR-0012): this generator runs at authoring time, never at
// runtime. Re-run it after editing CHANGELOG.md or overrides.json:
//
//     node scripts/whats-new/gen.mjs        (or: make whats-new)

import { readFileSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const ROOT = resolve(HERE, "..", "..");
const CHANGELOG = resolve(ROOT, "CHANGELOG.md");
const OVERRIDES = resolve(HERE, "overrides.json");
const OUT = resolve(ROOT, "api", "web", "whats-new.json");

const REPO = "https://github.com/pblumer/atlas";
const BLOB = `${REPO}/blob/main`;
const MAX_ENTRIES = 12;

// slugify turns a headline into a stable, filename-safe id used when a bullet names
// no ADR (ADR-numbered bullets key on the ADR instead, which is more stable).
function slugify(s) {
  return s
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 60);
}

// firstSentence pulls a plain-language lead from the bullet prose: strips markdown
// (links, code, emphasis), then cuts at the first sentence end. It is only a
// fallback — a curated override.summary is preferred.
function firstSentence(prose) {
  const plain = prose
    .replace(/\[([^\]]+)\]\([^)]+\)/g, "$1") // [text](url) -> text
    .replace(/[`*_]/g, "")
    .replace(/\s+/g, " ")
    .trim();
  const m = plain.match(/^(.*?[.!?])(\s|$)/);
  return (m ? m[1] : plain).trim();
}

// Build a lookup ADR-number -> repo-relative path by scanning every markdown ADR
// link in the document, so a bullet that names an ADR only in bare form
// ("(ADR-0158)") still resolves to the file another bullet linked with a path.
function collectAdrPaths(text) {
  const paths = {};
  const re = /\[ADR-(\d+)\]\((docs\/adr\/[^)]+)\)/g;
  let m;
  while ((m = re.exec(text))) paths[m[1]] = m[2];
  return paths;
}

// linkFor resolves the best PR/ADR reference in a bullet to a {label, url}. A PR/
// issue number wins (it is the most user-facing); then an ADR (markdown or bare);
// otherwise a link to the version's CHANGELOG section, so every entry has a target.
function linkFor(block, adrPaths, versionAnchor) {
  const pr = block.match(/\(#(\d+)\)/);
  if (pr) return { label: `PR #${pr[1]}`, url: `${REPO}/pull/${pr[1]}` };
  const adrMd = block.match(/\[ADR-(\d+)\]\((docs\/adr\/[^)]+)\)/);
  if (adrMd) return { label: `ADR-${adrMd[1]}`, url: `${BLOB}/${adrMd[2]}` };
  const adrBare = block.match(/\bADR-(\d+)\b/);
  if (adrBare) {
    const p = adrPaths[adrBare[1]];
    return {
      label: `ADR-${adrBare[1]}`,
      url: p ? `${BLOB}/${p}` : `${BLOB}/docs/adr`,
    };
  }
  return { label: "Changelog", url: `${BLOB}/CHANGELOG.md#${versionAnchor}` };
}

// parseChangelog walks the file top-to-bottom (already newest-first) and yields one
// record per "- **Title**" bullet, carrying its version, date, category and the raw
// block text (first paragraph) for reference/summary extraction.
function parseChangelog(text) {
  const adrPaths = collectAdrPaths(text);
  const lines = text.split("\n");
  const out = [];
  let version = null;
  let date = null;
  let anchor = "";
  let category = null;

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];

    const ver = line.match(/^## \[([^\]]+)\](?:\s+[—-]\s+(\d{4}-\d{2}-\d{2}))?/);
    if (ver) {
      version = ver[1];
      date = ver[2] || null; // Unreleased carries no date
      anchor = slugify(version === "Unreleased" ? "unreleased" : version);
      category = null;
      continue;
    }
    const cat = line.match(/^### (\w+)/);
    if (cat) {
      category = cat[1];
      continue;
    }

    if (!/^-\s+\*\*/.test(line) || !version) continue;

    // Accumulate the bullet's first paragraph: the start line plus following
    // continuation lines, stopping at a blank line or the next bullet/heading.
    const parts = [line];
    let j = i + 1;
    for (; j < lines.length; j++) {
      const l = lines[j];
      if (l.trim() === "" || /^-\s/.test(l) || /^#/.test(l)) break;
      parts.push(l);
    }
    i = j - 1;

    const block = parts.join(" ");
    // Extract the title from the joined block, so a bold headline that wraps across
    // two lines ("**… at send\n  time**") is still captured whole.
    const bullet = block.match(/^-\s+\*\*(.+?)\*\*/);
    if (!bullet) continue;
    const title = bullet[1].replace(/\s+/g, " ").trim();
    // Prose = everything after the bold title (and an optional "(ref):" lead-in).
    // The lead-in regularly *contains* a parenthesis — "([ADR-0163](docs/adr/….md))"
    // — so a naive \([^)]*\) stops at the markdown link's own closing paren and
    // leaves the summary starting "): …". Allow one level of nesting.
    const prose = block
      .replace(/^-\s+\*\*.+?\*\*/, "")
      .replace(/^\s*\((?:[^()]|\([^()]*\))*\)\s*[:—-]?\s*/, "")
      .replace(/^\s*[:—-]\s*/, "")
      .trim();

    out.push({
      // The title slug is the id: unique per bullet (several bullets can share one
      // ADR, so an ADR-keyed id would collide) and stable across edits to prose.
      id: slugify(title),
      version,
      date,
      category,
      title,
      link: linkFor(block, adrPaths, anchor),
      fallbackSummary: firstSentence(prose),
    });
  }
  return out;
}

// bilingual returns {en, de} from an override value that may be a string, a {en,de}
// object, or absent — with `de` falling back to `en`.
function bilingual(val, fallbackEn) {
  if (val && typeof val === "object") {
    const en = val.en != null ? val.en : fallbackEn;
    return { en, de: val.de != null ? val.de : en };
  }
  const en = val != null ? val : fallbackEn;
  return { en, de: en };
}

function bilingualSteps(val) {
  if (!val) return null;
  if (Array.isArray(val)) return { en: val, de: val };
  const en = Array.isArray(val.en) ? val.en : [];
  return { en, de: Array.isArray(val.de) ? val.de : en };
}

function merge(entry, ov) {
  ov = ov || {};
  const out = {
    id: entry.id,
    version: entry.version,
    date: entry.date,
    category: entry.category,
    title: bilingual(ov.title, entry.title),
    summary: bilingual(ov.summary, entry.fallbackSummary),
    link: ov.link || entry.link,
  };
  if (Array.isArray(ov.tags) && ov.tags.length) out.tags = ov.tags;
  const tut = bilingualSteps(ov.tutorial);
  if (tut && tut.en.length) out.tutorial = tut;
  if (ov.try && ov.try.route) {
    out.try = { label: bilingual(ov.try.label, "Try it"), route: ov.try.route };
  }
  return out;
}

function main() {
  const changelog = readFileSync(CHANGELOG, "utf8");
  let overrides = {};
  try {
    overrides = JSON.parse(readFileSync(OVERRIDES, "utf8"));
  } catch (e) {
    console.warn(`whats-new: no usable overrides (${e.message}); using CHANGELOG only`);
  }

  const parsed = parseChangelog(changelog);

  // An override is keyed by the id the generator derives from the bullet's headline,
  // and a key that matches no bullet does nothing at all — the entry still renders,
  // from the CHANGELOG's own wording, with German falling back to English. That is
  // indistinguishable from "no override was written", so a mistyped key ships an
  // untranslated entry and nothing anywhere says so. One did: a key one character
  // short of its 60-character slug put the raw English sentence in both languages.
  // Keys are curated by hand, so an unmatched one is always a mistake — either a typo
  // or a headline that was reworded out from under it. Fail, and name it.
  const known = new Set(parsed.map((e) => e.id));
  const orphans = Object.keys(overrides).filter((k) => !k.startsWith("_") && !known.has(k));
  if (orphans.length) {
    console.error(
      `whats-new: ${orphans.length} override key(s) match no CHANGELOG bullet:\n` +
        orphans.map((k) => `  ${k}`).join("\n") +
        "\nEach does nothing. Fix the key to the bullet's id, or drop it.",
    );
    process.exit(1);
  }

  const entries = [];
  for (const e of parsed) {
    const ov = overrides[e.id];
    if (ov && ov.hidden) continue;
    entries.push(merge(e, ov));
    if (entries.length >= MAX_ENTRIES) break;
  }

  // generatedAt is derived from the CHANGELOG, not the wall clock: the output is a
  // committed file, and CI re-runs this generator to check the commit is current
  // (.github/workflows/ci.yml). A wall-clock stamp would make every such run differ
  // from the commit and fail the check on the date alone. The newest dated release
  // section is what the feed actually reflects.
  const newestDate = changelog.match(/^## \[[^\]]+\]\s+[—-]\s+(\d{4}-\d{2}-\d{2})/m);
  const doc = { generatedAt: newestDate ? newestDate[1] : null, entries };
  writeFileSync(OUT, JSON.stringify(doc, null, 2) + "\n");
  console.log(`whats-new: wrote ${entries.length} entries to ${OUT}`);
}

main();
