#!/usr/bin/env python3
"""Which deployed models does an engine behaviour change touch?

Reads the BPMN of deployed process definitions — from a running Atlas server or from a
directory of .bpmn files — and reports every element whose behaviour an engine change
altered (today: ADR-0508, scope-aware messaging and compensation), with how certain that
is:

  changed  the element behaves differently whenever it runs
  review   it may: that depends on variable names the model does not state
           (a worker's result, a form's fields, a message's payload)
  info     it behaves differently, but nothing a model reads changes

Each rule mirrors one engine function and says which; a rule's reading of the XML is held
to the compiler's by impact_test.go. Python 3.8+, standard library only.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
import urllib.error
import urllib.request
import xml.etree.ElementTree as ET

CONTAINERS = {"subProcess", "transaction", "adHocSubProcess"}
# The elements whose zeebe:ioMapping the compiler wires (compiler/parse.go, wireScopeIO).
# A mapping anywhere else — on an event, on an ad-hoc subprocess itself — is ignored by
# the engine, so it declares nothing. A send task's mapping counts only when it is a job
# rather than a message throw (ADR-0112).
IO_WIRED = {"serviceTask", "scriptTask", "userTask", "callActivity", "receiveTask",
            "subProcess", "transaction"}
# Activities whose result lands in their flow scope when they have no output mapping
# (engine/behavior.go, ioResultScope) under names the model does not state.
UNNAMED_RESULTS = {"serviceTask", "userTask", "scriptTask", "callActivity", "businessRuleTask",
                   "sendTask"}
SEVERITIES = ("changed", "review", "info")
# Each rule and the decision record whose behaviour change it looks for. A rule for a
# later change is added here with its own record.
RULE_ADR = {
    "compensation.waits": "ADR-0508",
    "compensation.event-subprocess": "ADR-0508",
    "receive.output-mappings": "ADR-0508",
    "correlation.scope": "ADR-0508",
    "payload.scope": "ADR-0508",
    "event-subprocess.runs": "ADR-0508",
}

# --- FEEL names --------------------------------------------------------------------------

# FEEL words that are not variables. Built-in function names are recognised by the
# opening parenthesis that follows them; these are the ones that can stand alone.
_KEYWORDS = {"and", "or", "not", "if", "then", "else", "for", "in", "return", "some",
             "every", "satisfies", "between", "instance", "of", "null", "true", "false",
             "function", "is"}
# Built-ins whose name has spaces, so the parenthesis follows only the last word.
_MULTIWORD_CALLS = re.compile(
    r"\b(date and time|years and months duration|days and time duration|string length|"
    r"upper case|lower case|substring before|substring after|starts with|ends with|"
    r"list contains|insert before|index of|distinct values|get value|get entries|"
    r"context put|context merge|day of week|day of year|month of year|week of year|"
    r"is defined|get or else|string join|is empty|round half up|round half down|"
    r"round up|round down)\s*\(")
_STRING = re.compile(r'"(?:[^"\\]|\\.)*"')
_TOKEN = re.compile("(?<![\\w@$])[A-Za-z_\u00C0-\u024F][\\w\u00C0-\u024F]*|[{}()\\[\\],]")


def feel_names(expression: str | None) -> list[str]:
    """The variables a FEEL expression reads: the first segment of each path, without
    keywords, function names, the keys of a context it builds (a later entry may read an
    earlier one), names an iteration binds, or anything inside a filter (whose names are
    the filtered item's fields). A heuristic: impact_test.go compares it with the FEEL
    compiler's own list over every expression in every model of the repository, so a case
    it gets wrong there fails the build."""
    if not expression:
        return []
    s = expression.strip()
    if s.startswith("="):
        s = s[1:]
    s = _STRING.sub(" ", s)
    s = _MULTIWORD_CALLS.sub(" (", s)
    s = s.replace("..", " .. ")  # a range, 1..n: n is a name, not a path segment
    s = _blank_filters(s)
    bound = set(re.findall(r"\b(?:for|some|every)\s+([A-Za-z_]\w*)\s+in\b", s))
    bound |= {n for params in re.findall(r"\bfunction\s*\(([^)]*)\)", s)
              for n in re.findall(r"[A-Za-z_]\w*", params)}
    out = set()
    depth = 0
    contexts = []  # per open context: [its depth, the entry's key, keys of finished entries]
    for m in _TOKEN.finditer(s):
        tok, pos = m.group(0), m.start()
        if tok in "([":
            depth += 1
        elif tok in ")]":
            depth -= 1
        elif tok == "{":
            depth += 1
            contexts.append([depth, None, set()])
        elif tok == "}":
            depth -= 1
            if contexts:
                contexts.pop()
        elif tok == ",":
            if contexts and contexts[-1][0] == depth and contexts[-1][1]:
                contexts[-1][2].add(contexts[-1][1])
                contexts[-1][1] = None
        elif pos > 0 and s[pos - 1] == "." and (pos < 2 or s[pos - 2] != "."):
            continue  # a path segment, a.b
        elif contexts and contexts[-1][0] == depth and re.match(r"\s*:", s[m.end():]):
            contexts[-1][1] = tok  # a context key, {status: ...}
        elif tok in _KEYWORDS or tok in bound or re.match(r"\s*\(", s[m.end():]):
            continue  # a keyword, a bound name, a function call
        elif any(tok in c[2] for c in contexts):
            continue  # an earlier entry of an enclosing context, not a variable
        else:
            out.add(tok)
    return sorted(out)


def _blank_filters(s: str) -> str:
    """s with the inside of every filter, xs[...], blanked. A bracket opens a filter when
    it follows an operand — a name that is not a keyword, a closing bracket or a closing
    parenthesis — and a list literal otherwise."""
    out, stack = [], []  # stack: True for a filter bracket
    for i, ch in enumerate(s):
        if ch == "[":
            j = i - 1
            while j >= 0 and s[j].isspace():
                j -= 1
            word = re.search(r"(\w+)$", s[:j + 1]) if j >= 0 else None
            is_filter = j >= 0 and (s[j] in ")]}" or (word is not None and word.group(1) not in _KEYWORDS))
            inside = any(stack)
            stack.append(is_filter)
            out.append(" " if inside else ch)
            continue
        if ch == "]" and stack:
            stack.pop()
            out.append(" " if any(stack) else ch)
            continue
        out.append(" " if any(stack) else ch)
    return "".join(out)


# --- Reading a model ---------------------------------------------------------------------

def _ln(tag: str) -> str:
    return tag.rsplit("}", 1)[-1]


def _child(el, name):
    for c in el:
        if _ln(c.tag) == name:
            return c
    return None


def _ext(el, name):
    e = _child(el, "extensionElements")
    if e is None:
        return []
    return [c for c in e if _ln(c.tag) == name]


def _mappings(el):
    ins, outs = [], []
    for io in _ext(el, "ioMapping"):
        for m in io:
            pair = {"source": m.get("source", ""), "target": m.get("target", "")}
            if _ln(m.tag) == "input":
                ins.append(pair)
            elif _ln(m.tag) == "output":
                outs.append(pair)
    return ins, outs


def _loop(el):
    mi = _child(el, "multiInstanceLoopCharacteristics")
    if mi is None:
        return None
    out = {}
    e = _child(mi, "extensionElements")
    if e is not None:
        for c in e:
            if _ln(c.tag) == "loopCharacteristics":
                for k in ("inputCollection", "inputElement", "outputCollection", "outputElement"):
                    if c.get(k):
                        out[k] = c.get(k)
    return out


_SKIP = {"sequenceFlow", "extensionElements", "laneSet", "documentation", "dataObject",
         "dataObjectReference", "dataStoreReference", "textAnnotation", "association",
         "ioSpecification", "property", "multiInstanceLoopCharacteristics",
         "standardLoopCharacteristics", "incoming", "outgoing", "group", "category",
         "dataInputAssociation", "dataOutputAssociation"}


def read_model(xml_text: str) -> list[dict]:
    """Every process in a model, as the facts the rules read."""
    root = ET.fromstring(xml_text)
    messages, signals = {}, {}
    for m in root:
        if _ln(m.tag) == "message":
            sub = _ext(m, "subscription")
            key = sub[0].get("correlationKey") if sub else None
            messages[m.get("id")] = {"name": m.get("name"), "correlationKey": key,
                                     "keyNames": feel_names(key)}
        elif _ln(m.tag) == "signal":
            signals[m.get("id")] = m.get("name")
    return [_read_process(p, messages, signals) for p in root if _ln(p.tag) == "process"]


def _read_process(proc, messages, signals) -> dict:
    nodes = {}

    def walk(container, scope):
        for el in container:
            tag = _ln(el.tag)
            eid = el.get("id")
            if tag in _SKIP or not eid:
                continue
            defs = {_ln(c.tag): c for c in el if _ln(c.tag).endswith("EventDefinition")}
            ins, outs = _mappings(el)
            message_ref = el.get("messageRef")
            if "messageEventDefinition" in defs:
                message_ref = defs["messageEventDefinition"].get("messageRef")
            wired = tag in IO_WIRED or (tag == "sendTask" and not message_ref)
            script = _ext(el, "script")
            called = _ext(el, "calledDecision")
            n = {
                "id": eid, "tag": tag, "name": el.get("name"), "scope": scope,
                "eventSubprocess": tag == "subProcess" and el.get("triggeredByEvent") == "true",
                "inputs": ins if wired else [], "outputs": outs if wired else [],
                "loop": _loop(el),
                "messageRef": message_ref if ("messageEventDefinition" in defs or tag in ("receiveTask", "sendTask")) else None,
                "signalRef": defs["signalEventDefinition"].get("signalRef") if "signalEventDefinition" in defs else None,
                "compensate": "compensateEventDefinition" in defs,
                "attachedTo": el.get("attachedToRef"),
                "interrupting": (el.get("isInterrupting") or "true") != "false",
                "resultVariable": (script[0].get("resultVariable") if script else None)
                or (called[0].get("resultVariable") if called else None),
            }
            if n["compensate"]:
                cd = defs["compensateEventDefinition"]
                n["waitForCompletion"] = cd.get("waitForCompletion")
                n["activityRef"] = cd.get("activityRef")
            nodes[eid] = n
            if tag in CONTAINERS:
                walk(el, eid)

    walk(proc, None)
    outgoing = {}
    expressions = []
    for el in proc.iter():
        t = _ln(el.tag)
        if t == "sequenceFlow":
            outgoing.setdefault(el.get("sourceRef"), []).append(el.get("targetRef"))
        for k, v in el.attrib.items():
            if isinstance(v, str) and v.strip().startswith("="):
                expressions.append({"element": _owner(proc, el), "source": v.strip()})
        if t in ("conditionExpression", "completionCondition", "condition") and (el.text or "").strip():
            expressions.append({"element": _owner(proc, el), "source": el.text.strip()})
    for e in expressions:
        e["names"] = feel_names(e["source"])
    for n in nodes.values():
        n["outgoing"] = outgoing.get(n["id"], [])
    return {"processId": proc.get("id"), "name": proc.get("name"), "nodes": nodes,
            "messages": messages, "signals": signals, "expressions": expressions}


_parents_cache: dict = {}


def _owner(proc, el) -> str:
    """The id of the nearest element with an id at or above el."""
    parents = _parents_cache.get(id(proc))
    if parents is None or parents[0] is not proc:
        parents = (proc, {c: p for p in proc.iter() for c in p})
        _parents_cache[id(proc)] = parents
    cur = el
    while cur is not None:
        if cur.get("id"):
            return cur.get("id")
        cur = parents[1].get(cur)
    return ""


# --- The rules ---------------------------------------------------------------------------

def _compensable(p) -> dict:
    """Activity id → its compensation boundary, for every compensable activity."""
    return {n["attachedTo"]: n["id"] for n in p["nodes"].values()
            if n["tag"] == "boundaryEvent" and n["compensate"]}


def _in_scope(p, scope, ids):
    return [i for i in ids if i in p["nodes"] and p["nodes"][i]["scope"] == scope]


def _scope_label(scope):
    return scope if scope else "the process"


def _locals(p, scope):
    """What a container scope holds that the model names, and the activities that put
    results there under names it does not (ADR-0068, ADR-0074)."""
    c = p["nodes"][scope]
    named = {m["target"]: f"input mapping of {scope}" for m in c["inputs"]}
    if c["loop"] and c["loop"].get("inputElement"):
        named[c["loop"]["inputElement"]] = f"inputElement of {scope}"
    unnamed = []
    for n in p["nodes"].values():
        if n["scope"] != scope:
            continue
        for m in n["outputs"]:
            named.setdefault(m["target"], f"output mapping of {n['id']}")
        if n["outputs"]:
            continue
        if n["resultVariable"]:
            named.setdefault(n["resultVariable"], f"result of {n['id']}")
        elif n["tag"] in UNNAMED_RESULTS and not (n["tag"] == "sendTask" and n["messageRef"]):
            unnamed.append(n["id"])
    if c["eventSubprocess"]:
        unnamed.append(_TRIGGER)
    return named, unnamed


def _chain(p, n):
    """The scopes an element's correlation key resolves through, innermost first, the
    root left out (engine/behavior.go, evalCorrelationKey over bindInputsChain)."""
    chain = []
    own = {m["target"]: f"input mapping of {n['id']}" for m in n["inputs"]}
    if n["loop"] and n["loop"].get("inputElement"):
        own[n["loop"]["inputElement"]] = f"inputElement of {n['id']}"
    host = p["nodes"].get(n["attachedTo"] or "")
    if n["tag"] == "boundaryEvent" and host:
        own.update({m["target"]: f"input mapping of {host['id']}" for m in host["inputs"]})
        if host["loop"] and host["loop"].get("inputElement"):
            own[host["loop"]["inputElement"]] = f"inputElement of {host['id']}"
    if own:
        chain.append((n["id"], own, []))
    scope = n["scope"]
    if _is_trigger(p, n):
        scope = p["nodes"][scope]["scope"]  # a trigger waits in the scope it handles
    while scope:
        named, unnamed = _locals(p, scope)
        chain.append((scope, named, unnamed))
        scope = p["nodes"][scope]["scope"]
    return chain


def _is_trigger(p, n):
    return n["tag"] == "startEvent" and bool(n["scope"]) and p["nodes"][n["scope"]]["eventSubprocess"]


def _receives(p, n):
    if _is_trigger(p, n):
        return bool(n["messageRef"] or n["signalRef"])
    if n["tag"] == "receiveTask":
        return True
    return n["tag"] in ("intermediateCatchEvent", "boundaryEvent") and bool(n["messageRef"] or n["signalRef"])


def _throws_message(n):
    return bool(n["messageRef"]) and n["tag"] in ("intermediateThrowEvent", "endEvent", "sendTask")


_TRIGGER = "@trigger"


def _unnamed_text(unnamed):
    """'tx (results of a, b)' for each scope whose contents the model does not name."""
    parts = []
    for scope, u in unnamed:
        what = []
        tasks = [x for x in u if x != _TRIGGER]
        if tasks:
            what.append("results of " + ", ".join(tasks))
        if _TRIGGER in u:
            what.append("the payload of the message that starts it")
        parts.append(f"{scope} ({'; '.join(what)})")
    return ", ".join(parts)


def rule_compensation(p):
    """engine/behavior.go: triggerCompensation, compensationScope, releaseCompensationWaiters."""
    out = []
    comp = _compensable(p)
    for n in p["nodes"].values():
        if not n["compensate"] or n["tag"] not in ("intermediateThrowEvent", "endEvent"):
            continue
        scope = n["scope"]
        in_es = bool(scope) and p["nodes"][scope]["eventSubprocess"]
        target = p["nodes"][scope]["scope"] if in_es else scope
        targets = _in_scope(p, target, list(comp))
        if n.get("activityRef"):
            targets = [a for a in targets if a == n["activityRef"]]
        waits = (n.get("waitForCompletion") or "").strip() != "false"
        goes_on = n["tag"] == "intermediateThrowEvent" and n["outgoing"]
        wait_text = ""
        if waits and targets and goes_on:
            wait_text = (f" It also waits for them: {', '.join(n['outgoing'])} runs once they have "
                         f"finished; waitForCompletion=\"false\" goes on at once.")
        if in_es:
            own = _in_scope(p, scope, list(comp))
            if own:
                out.append(_f("compensation.event-subprocess", "changed", n,
                              f"It no longer compensates {', '.join(own)} in its own run: a "
                              f"compensation event directly in an event subprocess now compensates "
                              f"{_scope_label(target)}"
                              + (f" ({', '.join(targets)})." if targets else ", which has nothing compensable.")))
            elif targets:
                out.append(_f("compensation.event-subprocess", "changed", n,
                              f"It now compensates {', '.join(targets)} in {_scope_label(target)}; "
                              f"before, it compensated the event subprocess's own run, which holds "
                              f"nothing, so no handler ran." + wait_text))
            else:
                out.append(_f("compensation.event-subprocess", "info", n,
                              f"It now compensates {_scope_label(target)} instead of its own run; "
                              f"neither has a compensable activity, so nothing runs either way."))
            continue
        if not waits or not targets:
            continue
        if goes_on:
            out.append(_f("compensation.waits", "changed", n,
                          f"The step after it ({', '.join(n['outgoing'])}) now runs once the handlers "
                          f"of {', '.join(targets)} have finished, not beside them. "
                          f"waitForCompletion=\"false\" keeps the old behaviour."))
        else:
            out.append(_f("compensation.waits", "info", n,
                          f"It now completes once the handlers of {', '.join(targets)} have finished. "
                          f"Its scope ended after them before too, so only the moment it is shown "
                          f"as completed moves."))
    return out


def rule_receive_outputs(p):
    """engine/behavior.go: writePayload — a receiver with output mappings keeps the payload
    in its own scope, as ioResultScope keeps a worker's result."""
    out = []
    for n in p["nodes"].values():
        if n["tag"] != "receiveTask" or not n["outputs"]:
            continue
        targets = {m["target"] for m in n["outputs"]}
        read = set()
        for m in n["outputs"]:
            read |= set(feel_names(m["source"]))
        raw = sorted(read - targets)
        readers = sorted({e["element"] for e in p["expressions"]
                          if e["element"] != n["id"] and set(e["names"]) & set(raw)})
        msg = p["messages"].get(n["messageRef"] or "", {}).get("name")
        head = (f"The payload of {msg!r} " if msg else "The payload ") + \
               f"now stays in the task's own scope; only its output mappings " \
               f"({', '.join(sorted(targets))}) reach the instance."
        if readers:
            out.append(_f("receive.output-mappings", "changed", n,
                          f"{head} {', '.join(readers)} read{'s' if len(readers) == 1 else ''} "
                          f"{', '.join(raw)} from the payload, which is no longer there."))
        else:
            out.append(_f("receive.output-mappings", "review", n,
                          f"{head} No element of this model reads the rest of the payload; check "
                          f"workers, forms and later messages that might."))
    return out


def rule_correlation(p):
    """engine/behavior.go: evalCorrelationKey — the key resolves up the scope chain from the
    waiting or sending element instead of on the root."""
    out = []
    for n in p["nodes"].values():
        if not ((_receives(p, n) and n["messageRef"]) or _throws_message(n)):
            continue
        msg = p["messages"].get(n["messageRef"], {})
        names = msg.get("keyNames") or []
        if not names:
            continue
        chain = _chain(p, n)
        hits = [(v, scope, named[v]) for scope, named, _ in chain for v in names if v in named]
        if hits:
            v, scope, by = hits[0]
            out.append(_f("correlation.scope", "changed", n,
                          f"Its correlation key {msg.get('correlationKey')} reads {v} from {scope} "
                          f"({by}); before, it read the root's."))
            continue
        unnamed = [(scope, u) for scope, _, u in chain if u]
        if unnamed:
            out.append(_f("correlation.scope", "review", n,
                          f"Its correlation key {msg.get('correlationKey')} reads {', '.join(names)}, "
                          f"now first in {_unnamed_text(unnamed)}; a value of that name there wins "
                          f"over the root's."))
    return out


def rule_payload(p):
    """engine/behavior.go: writePayload, propagationScope — each payload variable updates the
    nearest scope that declares it, from the receiver's flow scope up, else the root."""
    out = []
    for n in p["nodes"].values():
        if not _receives(p, n) or (n["tag"] == "receiveTask" and n["outputs"]):
            continue
        chain = [c for c in _chain(p, n) if c[0] != n["id"]]
        if not chain:
            continue
        named = sorted({v for _, nm, _ in chain for v in nm})
        unnamed = [(scope, u) for scope, _, u in chain if u]
        if not named and not unnamed:
            continue
        if named:
            head = (f"A payload variable named {', '.join(named)} now updates the scope that declares "
                    f"it ({', '.join(sc for sc, nm, _ in chain if nm)}) instead of the root")
            if unnamed:
                head += f", and so does one whose name is already held in {_unnamed_text(unnamed)}"
        else:
            head = (f"A payload variable whose name is already held in {_unnamed_text(unnamed)} "
                    f"now updates it there instead of the root")
        out.append(_f("payload.scope", "review", n,
                      head + ". It is dropped with that scope unless an output mapping carries it "
                      "out; a name no enclosing scope holds still lands on the root."))
    return out


def rule_event_subprocess(p):
    """engine/behavior.go: rearmFiredTrigger, bindTriggerPayload."""
    out = []
    for n in p["nodes"].values():
        if _is_trigger(p, n) and (n["messageRef"] or n["signalRef"]) and not n["interrupting"]:
            out.append(_f("event-subprocess.runs", "info", p["nodes"][n["scope"]],
                          "A second message right behind the first now starts its own run instead "
                          "of finding no subscription, and each run reads the payload of the message "
                          "that started it rather than the root's latest. Its payload counts twice "
                          "against the variable budget (root and run)."))
    return out


RULES = (rule_compensation, rule_receive_outputs, rule_correlation, rule_payload, rule_event_subprocess)


def _f(rule, severity, n, message):
    return {"rule": rule, "adr": RULE_ADR[rule], "severity": severity, "element": n["id"],
            "elementName": n["name"], "message": message}


def findings(process: dict) -> list[dict]:
    out = []
    for r in RULES:
        out.extend(r(process))
    return out


# --- Sources -----------------------------------------------------------------------------

class Server:
    def __init__(self, url: str, token: str | None, timeout: float = 60):
        self.url = url.rstrip("/")
        self.token = token
        self.timeout = timeout

    def get(self, path: str) -> bytes:
        req = urllib.request.Request(self.url + path)
        if self.token:
            req.add_header("Authorization", "Bearer " + self.token)
        with urllib.request.urlopen(req, timeout=self.timeout) as resp:
            return resp.read()

    def summary(self) -> list[dict]:
        return json.loads(self.get("/api/v1/instances/summary"))

    def xml(self, key: int) -> str:
        return self.get(f"/api/v1/processes/{key}/xml").decode("utf-8")


def select(rows: list[dict], all_versions: bool, keys: list[int]) -> list[dict]:
    """The newest version of each process and every version with running instances: the
    definitions a new instance starts on or an instance is still running."""
    if keys:
        return [r for r in rows if r["processDefKey"] in keys]
    if all_versions:
        return list(rows)
    newest = {}
    for r in rows:
        if r["version"] > newest.get(r["processId"], -1):
            newest[r["processId"]] = r["version"]
    return [r for r in rows if r["active"] > 0 or r["version"] == newest[r["processId"]]]


def from_server(args) -> tuple[list[dict], list[dict]]:
    srv = Server(args.server, args.token or os.environ.get("ATLAS_TOKEN"))
    rows = select(srv.summary(), args.all_versions, args.key or [])
    report, errors = [], []
    for r in sorted(rows, key=lambda x: x["processDefKey"]):
        try:
            procs = read_model(srv.xml(r["processDefKey"]))
        except (urllib.error.URLError, ET.ParseError, OSError) as e:
            errors.append({"key": r["processDefKey"], "processId": r["processId"], "error": str(e)})
            continue
        proc = next((p for p in procs if p["processId"] == r["processId"]), None)
        if proc is None:
            errors.append({"key": r["processDefKey"], "processId": r["processId"],
                           "error": "the model holds no process of that id"})
            continue
        report.append({"key": r["processDefKey"], "processId": r["processId"],
                       "version": r["version"], "running": r["active"],
                       "findings": findings(proc)})
    return report, errors


def from_dir(args) -> tuple[list[dict], list[dict]]:
    report, errors = [], []
    for path, shown in _bpmn_files(args.dir):
        try:
            with open(path, encoding="utf-8") as fh:
                procs = read_model(fh.read())
        except (ET.ParseError, OSError, UnicodeDecodeError) as e:
            errors.append({"file": shown, "error": str(e)})
            continue
        for p in procs:
            report.append({"file": shown, "processId": p["processId"], "findings": findings(p)})
    return report, errors


def _bpmn_files(paths):
    """Each .bpmn file under paths, with the name to show for it: relative to the
    directory it was found in, or as given for a file named directly."""
    for root in paths:
        if os.path.isfile(root):
            yield root, root
            continue
        for d, dirs, files in os.walk(root):
            dirs[:] = sorted(x for x in dirs if x not in ("node_modules", ".git"))
            for f in sorted(files):
                if f.endswith(".bpmn"):
                    path = os.path.join(d, f)
                    yield path, os.path.relpath(path, root)


# --- Output ------------------------------------------------------------------------------

def render(report, errors, scope_text) -> str:
    adrs = ", ".join(sorted(set(RULE_ADR.values())))
    lines = [f"Behaviour changes ({adrs}): {_n(len(report), 'definition')} checked ({scope_text})", ""]
    counts = {s: 0 for s in SEVERITIES}
    touched = 0
    for d in report:
        fs = sorted(d["findings"], key=lambda f: (SEVERITIES.index(f["severity"]), f["rule"], f["element"]))
        if not fs:
            continue
        touched += 1
        if "key" in d:
            head = f"{d['processId']} v{d['version']} (key {d['key']}, {d['running']} running)"
        else:
            head = f"{d['processId']} ({d['file']})"
        lines.append(head)
        for f in fs:
            counts[f["severity"]] += 1
            name = f" \"{f['elementName']}\"" if f["elementName"] else ""
            lines.append(f"  {f['severity']:<7} {f['rule']:<30} {f['element']}{name}")
            lines.append(f"          {f['message']}")
        lines.append("")
    for e in errors:
        where = e.get("key", e.get("file"))
        lines.append(f"not checked: {where}: {e['error']}")
    lines.append(f"{counts['changed']} changed, {counts['review']} to review, {counts['info']} info "
                 f"in {_n(touched, 'definition')}; {len(report) - touched} untouched.")
    return "\n".join(lines) + "\n"


def _n(count, noun):
    return f"{count} {noun}{'' if count == 1 else 's'}"


def main(argv=None) -> int:
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    src = ap.add_mutually_exclusive_group(required=True)
    src.add_argument("--server", help="Atlas base URL, e.g. https://atlas.example.org")
    src.add_argument("--dir", nargs="+", help="directories or .bpmn files to read instead of a server")
    ap.add_argument("--token", help="bearer token (or ATLAS_TOKEN); the summary needs the operator role")
    ap.add_argument("--all-versions", action="store_true",
                    help="every deployed version, not only the newest and the running ones")
    ap.add_argument("--key", type=int, action="append", help="only this definition key (repeatable)")
    ap.add_argument("--json", action="store_true", help="the report as JSON")
    ap.add_argument("--facts", action="store_true",
                    help="with --dir: print what the rules read from each model, as JSON (for the tests)")
    ap.add_argument("--fail-on", choices=SEVERITIES[:2],
                    help="exit 1 when a finding of this severity or a higher one is reported")
    args = ap.parse_args(argv)

    if args.facts:
        if not args.dir:
            ap.error("--facts needs --dir")
        out = []
        for path, _ in _bpmn_files(args.dir):
            try:
                with open(path, encoding="utf-8") as fh:
                    out.append({"file": path, "processes": read_model(fh.read())})
            except (ET.ParseError, OSError, UnicodeDecodeError) as e:
                out.append({"file": path, "error": str(e)})
        json.dump(out, sys.stdout, ensure_ascii=False)
        return 0

    try:
        report, errors = from_server(args) if args.server else from_dir(args)
    except (urllib.error.URLError, OSError, ValueError) as e:
        print(f"impact: {e}", file=sys.stderr)
        return 2
    if args.json:
        json.dump({"rules": RULE_ADR, "definitions": report, "notChecked": errors}, sys.stdout,
                  ensure_ascii=False, indent=1)
        sys.stdout.write("\n")
    else:
        scope_text = ("every deployed version" if args.all_versions else
                      "the given keys" if args.key else
                      "newest version of each process and every version with running instances"
                      ) if args.server else "every process in the given files"
        sys.stdout.write(render(report, errors, scope_text))
    if args.fail_on:
        limit = SEVERITIES.index(args.fail_on)
        if any(SEVERITIES.index(f["severity"]) <= limit for d in report for f in d["findings"]):
            return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
