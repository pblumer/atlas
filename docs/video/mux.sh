#!/usr/bin/env bash
# Assemble the narration track and join it with the screen recording into one MP4.
#   $1 = build directory (holds timing.json, audio/, raw/)
#   $2 = output file (.mp4)
set -euo pipefail
BUILD="${1:-./build}"
OUT="${2:-$BUILD/atlas-console-intro.de.mp4}"
LEAD="${LEAD:-3.0}"          # lead-in that record.mjs captures before the first scene

command -v ffmpeg >/dev/null || { echo "ffmpeg missing"; exit 1; }
TOTAL=$(python3 -c "import json;print(json.load(open('$BUILD/timing.json'))['total'])")
GAP=$(python3 -c "import json;print(json.load(open('$BUILD/timing.json'))['gap'])")
RAW=$(ls -t "$BUILD/raw"/*.webm | head -1)
echo "picture: $RAW"
echo "length per narration script: ${TOTAL}s"

# --- the narration: every scene plus its breath, in script order -----------------
PAD="$BUILD/audio-padded"; rm -rf "$PAD"; mkdir -p "$PAD"
LIST="$BUILD/audio.txt"; : > "$LIST"
python3 - "$BUILD/timing.json" > "$BUILD/scenes.txt" <<'PY'
import json,sys
for s in json.load(open(sys.argv[1]))["scenes"]: print(s["audio"])
PY
i=0
while read -r wav; do
  out=$(printf "%s/%02d.wav" "$PAD" "$i")
  # -nostdin, or ffmpeg eats the lines this loop is still reading.
  ffmpeg -nostdin -y -loglevel error -i "$wav" -af "apad=pad_dur=$GAP" -ar 48000 -ac 1 "$out"
  echo "file '$out'" >> "$LIST"
  i=$((i+1))
done < "$BUILD/scenes.txt"

# One pass of conditioning: high-pass against rumble, loudness to broadcast level.
ffmpeg -y -loglevel error -f concat -safe 0 -i "$LIST" \
  -af "highpass=f=75,loudnorm=I=-16:TP=-1.5:LRA=11,aresample=48000" \
  "$BUILD/voice.wav"

# --- picture and sound ------------------------------------------------------------
# -ss goes BEFORE the picture input: it drops the lead-in from the recording, not
# from the narration (as an output option it would shorten both tracks at the head).
ffmpeg -y -loglevel error -ss "$LEAD" -i "$RAW" -i "$BUILD/voice.wav" \
  -t "$TOTAL" \
  -map 0:v:0 -map 1:a:0 \
  -vf "fps=30,format=yuv420p" \
  -c:v libx264 -preset slow -crf 20 -profile:v high -level 4.0 \
  -c:a aac -b:a 160k -ar 48000 -ac 2 \
  -movflags +faststart -shortest "$OUT"

echo
ffprobe -v error -show_entries format=duration,size -show_entries stream=codec_name,width,height,r_frame_rate \
  -of default=nw=1 "$OUT"
echo "done: $OUT"
