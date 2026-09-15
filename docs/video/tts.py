#!/usr/bin/env python3
"""Narration script -> one WAV per scene, plus timing.json.

Reads the narration script, synthesizes each scene with Piper and measures how long
it actually takes to say. The recording is then cut to exactly those durations, so
picture and sound line up — whoever edits the text changes the length of that scene
too, without a timing to keep in step anywhere else.
"""
import json, os, re, subprocess, sys, wave
from pathlib import Path

HERE = Path(__file__).resolve().parent

def load_rules(path: Path):
    """The pronunciation table: one (compiled pattern, replacement) per line."""
    rules = []
    if not path.exists():
        return rules
    for line in path.read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "\t" not in line:
            continue
        src, dst = line.split("\t", 1)
        rules.append((re.compile(rf"\b{re.escape(src.strip())}\b", re.IGNORECASE), dst.strip()))
    return rules

def parse_script(path: Path):
    """Front matter plus the scenes of the Markdown narration script."""
    raw = path.read_text(encoding="utf-8")
    meta, body = {"voice": "thorsten-low", "tempo": 1.0}, raw
    if raw.startswith("---"):
        fm, _, body = raw[3:].partition("\n---")
        for line in fm.splitlines():
            line = line.strip()
            if not line or line.startswith("#") or ":" not in line:
                continue
            k, v = line.split(":", 1)
            meta[k.strip()] = v.strip()
    meta["tempo"] = float(meta.get("tempo", 1.0))

    scenes, cur = [], None
    for line in body.splitlines():
        if line.startswith("## "):
            if cur:
                scenes.append(cur)
            cur = {"id": line[3:].strip(), "caption": "", "text": []}
            continue
        if cur is None or line.startswith("#"):
            continue
        if line.lower().startswith("caption:"):
            cur["caption"] = line.split(":", 1)[1].strip()
            continue
        if line.strip():
            cur["text"].append(line.strip())
    if cur:
        scenes.append(cur)
    for s in scenes:
        s["text"] = " ".join(s["text"]).strip()
    return meta, [s for s in scenes if s["text"]]

def spoken(text: str, rules) -> str:
    """What the synthesizer is given — never what the viewer is shown."""
    for pattern, repl in rules:
        text = pattern.sub(repl, text)
    return text

def wav_seconds(path: Path) -> float:
    with wave.open(str(path)) as w:
        return w.getnframes() / float(w.getframerate())

def main():
    script = Path(sys.argv[1]) if len(sys.argv) > 1 else HERE / "console-intro.de.md"
    outdir = Path(sys.argv[2]) if len(sys.argv) > 2 else HERE / "build"
    voices = Path(os.environ.get("VOICE_DIR", outdir / "voices"))
    gap = float(os.environ.get("SCENE_GAP", "0.4"))   # breath between scenes

    meta, scenes = parse_script(script)
    locale = "".join(script.suffixes[:-1]).lstrip(".") or "de"
    rules = load_rules(HERE / f"pronunciation.{locale}.tsv")
    model = voices / meta["voice"] / f"de-{meta['voice']}.onnx"
    if not model.exists():
        sys.exit(f"voice missing: {model} — build.sh fetches it.")

    audio_dir = outdir / "audio"
    audio_dir.mkdir(parents=True, exist_ok=True)
    timeline, cursor = [], 0.0
    for i, s in enumerate(scenes):
        wav = audio_dir / f"{i:02d}-{s['id']}.wav"
        subprocess.run(
            [sys.executable, "-m", "piper", "-m", str(model), "-f", str(wav),
             "--length-scale", str(meta["tempo"]), "--sentence-silence", "0.35"],
            input=spoken(s["text"], rules), text=True, check=True,
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        )
        dur = wav_seconds(wav)
        timeline.append({
            "id": s["id"], "caption": s["caption"], "text": s["text"],
            "audio": str(wav), "start": round(cursor, 3),
            "speech": round(dur, 3), "duration": round(dur + gap, 3),
        })
        cursor += dur + gap
        print(f"  {s['id']:<16} {dur:5.1f}s")

    (outdir / "timing.json").write_text(
        json.dumps({"total": round(cursor, 3), "gap": gap, "scenes": timeline},
                   ensure_ascii=False, indent=2), encoding="utf-8")
    print(f"\nTotal: {cursor:.1f}s ({cursor/60:.2f} min) over {len(timeline)} scenes")

if __name__ == "__main__":
    main()
