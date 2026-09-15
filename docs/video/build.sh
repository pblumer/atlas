#!/usr/bin/env bash
# Builds the introduction video for the Atlas Console, start to finish.
#
#   ./build.sh                    against a server already running on 127.0.0.1:8080
#   ./build.sh --with-server      builds Atlas, starts it on a throwaway data
#                                 directory, seeds demo data, cleans up afterwards
#   ./build.sh --skip-tts         skip speech synthesis (leave the narration as it is)
#
# The order is the point: sound first, then picture. tts.py measures how long each
# scene takes to say; record.mjs cuts the recording to exactly those durations.
# Editing the narration moves the picture with it — nothing else to keep in step.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$HERE/../.." && pwd)"
BUILD="${BUILD_DIR:-$HERE/build}"
SCRIPT="${SCRIPT_FILE:-$HERE/console-intro.de.md}"
OUT="${OUT_FILE:-$BUILD/atlas-console-intro.de.mp4}"
export ATLAS_BASE="${ATLAS_BASE:-http://127.0.0.1:8080}"
export ATLAS_USER="${ATLAS_USER:-admin}"
export ATLAS_PASS="${ATLAS_PASS:-atlas-demo-2026}"
WITH_SERVER=0; SKIP_TTS=0
for a in "$@"; do case "$a" in
  --with-server) WITH_SERVER=1 ;; --skip-tts) SKIP_TTS=1 ;;
  *) echo "unknown option: $a"; exit 2 ;;
esac; done

for tool in ffmpeg node python3; do
  command -v "$tool" >/dev/null || { echo "$tool missing"; exit 1; }
done
python3 -c "import piper" 2>/dev/null || { echo "piper-tts missing: pip install piper-tts"; exit 1; }
mkdir -p "$BUILD"

# --- the voice --------------------------------------------------------------------
# Piper voices normally live on Hugging Face; the German male ones are also a GitHub
# release asset, which is the reachable route from a network that does not allow it.
VOICE=$(sed -n 's/^voice: *//p' "$SCRIPT" | head -1)
VOICES="${VOICE_DIR:-$BUILD/voices}"
if [ ! -f "$VOICES/$VOICE/de-$VOICE.onnx" ]; then
  echo "==> fetching voice $VOICE"
  mkdir -p "$VOICES/$VOICE"
  curl -sSL --max-time 600 -o "$BUILD/voice.tar.gz" \
    "https://github.com/rhasspy/piper/releases/download/v0.0.2/voice-de-$VOICE.tar.gz"
  tar xzf "$BUILD/voice.tar.gz" -C "$VOICES/$VOICE" && rm -f "$BUILD/voice.tar.gz"
fi
export VOICE_DIR="$VOICES"

# --- Playwright -------------------------------------------------------------------
[ -d "$REPO/e2e/node_modules/@playwright" ] || (cd "$REPO/e2e" && npm ci)

# --- the server -------------------------------------------------------------------
SRV_PID=""
if [ "$WITH_SERVER" = 1 ]; then
  echo "==> building Atlas"
  go -C "$REPO" build -o "$BUILD/atlas" ./cmd/atlas
  DATA="$BUILD/server-data"; rm -rf "$DATA"
  echo "==> starting the server"
  ATLAS_ADMIN_USERNAME="$ATLAS_USER" ATLAS_ADMIN_PASSWORD="$ATLAS_PASS" \
    "$BUILD/atlas" serve --data-dir "$DATA" --addr 127.0.0.1:8080 > "$BUILD/server.log" 2>&1 &
  SRV_PID=$!
  trap '[ -n "$SRV_PID" ] && kill "$SRV_PID" 2>/dev/null || true' EXIT
  for _ in $(seq 40); do
    curl -sf "$ATLAS_BASE/healthz" >/dev/null 2>&1 && break; sleep 0.5
  done
  bash "$HERE/seed.sh"
fi
curl -sf "$ATLAS_BASE/healthz" >/dev/null || { echo "no server on $ATLAS_BASE"; exit 1; }

# --- sound, then picture, then both -----------------------------------------------
if [ "$SKIP_TTS" = 0 ]; then
  echo "==> speech synthesis"
  SCENE_GAP="${SCENE_GAP:-0.4}" python3 "$HERE/tts.py" "$SCRIPT" "$BUILD"
fi
echo "==> screen recording"
rm -rf "$BUILD/raw"
(cd "$REPO/e2e" && node "$HERE/record.mjs" "$BUILD")
echo "==> joining sound and picture"
bash "$HERE/mux.sh" "$BUILD" "$OUT"
