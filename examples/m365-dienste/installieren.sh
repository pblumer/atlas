#!/bin/sh
# Installiert das Paket «Beispiel: Microsoft-365-Dienste» in eine Atlas-Instanz.
#
#   ATLAS_TOKEN=atlasat_… ./installieren.sh https://atlas.intern.example.ch antworten.json
#
# Schritte, in dieser Reihenfolge:
#   1. die fünf Entscheidungsmodelle unter ihren Handles hochladen (die Applikation trägt
#      nur Referenzen auf sie, und das Veröffentlichen prüft, dass es sie gibt);
#   2. die Applikation als Quellbaum importieren (Prozesse, Formulare, Entscheidungsreferenzen);
#   3. die Applikation veröffentlichen;
#   4. die Platzhalter im Katalogdokument mit den Gruppen der Instanz ausfüllen und das
#      Dokument importieren und veröffentlichen.
#
# Das Token braucht die Rolle modeler. Den Katalog importiert nur, wer productmanager ist,
# und ein API-Token trägt diese Rolle nie: Wird Schritt 4 abgelehnt, liegt das ausgefüllte
# Dokument als katalog-zum-import.json bereit und wird in der Console unter Catalogue ->
# Import hochgeladen (mit «veröffentlichen»).
#
# Braucht curl, tar und python3. Die Applikation gehört danach dem Token, das sie angelegt hat:
# Ein zweiter Lauf mit demselben Token aktualisiert sie, ein anderes Token sieht sie nicht.
set -eu

SERVER=${1:?Aufruf: installieren.sh SERVER [ANTWORTEN.json]}
ANTWORTEN=${2:-antworten.json}
TOKEN=${ATLAS_TOKEN:?ATLAS_TOKEN ist nicht gesetzt}
HERE=$(cd "$(dirname "$0")" && pwd)
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
AUTH="Authorization: Bearer $TOKEN"

echo "1. Entscheidungsmodelle"
for m in m365-lizenzprofil m365-namensregel m365-postfachzugriff m365-telefonie m365-verteiler-zuordnung; do
  curl -fsS -X POST "$SERVER/api/v1/dmn-models?handle=$m" -H "$AUTH" \
    -H 'Content-Type: application/xml' --data-binary "@$HERE/$m.dmn" > /dev/null
  echo "   $m"
done

echo "2. Applikation importieren"
FILES=$(python3 -I -c 'import json,sys; m=json.load(open(sys.argv[1])); print(" ".join(["atlas.json"]+[p["path"] for p in m["processes"]]+[f["path"] for f in m["forms"]]))' "$HERE/atlas.json")
# shellcheck disable=SC2086
tar -czf "$WORK/source.tar.gz" -C "$HERE" $FILES
curl -fsS -X POST "$SERVER/api/v1/applications/source" -H "$AUTH" \
  -H 'Content-Type: application/gzip' --data-binary "@$WORK/source.tar.gz" > "$WORK/source.json"
APP=$(python3 -I -c 'import json,sys; print(json.load(open(sys.argv[1]))["applicationId"])' "$WORK/source.json")
echo "   Applikation $APP"

echo "3. Applikation veröffentlichen"
curl -fsS -X POST "$SERVER/api/v1/applications/$APP/publish" -H "$AUTH" \
  -H 'Content-Type: application/json' -d '{"note":"installieren.sh"}' > "$WORK/publish.json"
python3 -I -c 'import json,sys; d=json.load(open(sys.argv[1])); print("   Release", d.get("release",{}).get("version")); [print("   Hinweis:", w) for w in d.get("warnings",[])]' "$WORK/publish.json"

echo "4. Katalog"
curl -fsS "$SERVER/api/v1/principals" -H "$AUTH" > "$WORK/principals.json"
python3 -I - "$HERE/katalog.json" "$HERE/fragen.json" "$ANTWORTEN" "$WORK/principals.json" "$HERE/katalog-zum-import.json" <<'PY'
import json, re, sys
doc, fragen, antworten, principals, out = sys.argv[1:]
raw = open(doc, encoding="utf-8").read()
fragen = json.load(open(fragen, encoding="utf-8"))
antworten = json.load(open(antworten, encoding="utf-8"))
dirs = json.load(open(principals, encoding="utf-8"))
fehler = []
def aufloesen(name):
    q = fragen.get(name)
    if q is None:
        fehler.append(f"{{{{{name}}}}} wird in fragen.json nicht gefragt")
        return name
    wert = antworten.get(name)
    if not wert:
        fehler.append(f"{name} ist nicht beantwortet: {q['label']['de']}")
        return name
    art = q["kind"]
    treffer = [p["id"] for p in dirs if p.get("type") == art and (p["id"] == wert or p.get("name") == wert)]
    if len(treffer) != 1:
        fehler.append(f"{name}: «{wert}» ist {'unbekannt' if not treffer else 'mehrdeutig'} ({art})")
        return name
    return treffer[0]
gefuellt = re.sub(r"\{\{([a-z0-9-]+)\}\}", lambda m: aufloesen(m.group(1)), raw)
if fehler:
    sys.exit("Katalog nicht ausgefüllt:\n  " + "\n  ".join(fehler))
open(out, "w", encoding="utf-8").write(gefuellt)
PY
CODE=$(curl -sS -o "$WORK/katalog.json" -w '%{http_code}' -X POST "$SERVER/api/v1/catalogs/import" -H "$AUTH" \
  -H 'Content-Type: application/json' --data-binary "@$HERE/katalog-zum-import.json")
if [ "$CODE" = "200" ] || [ "$CODE" = "201" ]; then
  echo "   Katalog importiert und veröffentlicht"
  rm -f "$HERE/katalog-zum-import.json"
else
  echo "   Der Server hat den Katalogimport abgelehnt (HTTP $CODE):"
  sed 's/^/     /' "$WORK/katalog.json"; echo
  echo "   Das ausgefüllte Dokument liegt unter $HERE/katalog-zum-import.json."
  echo "   In der Console: Catalogue -> Import -> Datei wählen, «veröffentlichen» ankreuzen."
fi

echo
echo "Danach: Applikationskonstanten MAIL_DOMAIN, USAGE_LOCATION, PARTNER_UPN_DOMAIN und"
echo "ATLAS_API_BASE setzen, die Worker ucc_entra, ucc_exchange und ucc_teams einrichten"
echo "und die öffentlichen Links erzeugen (README.md)."
