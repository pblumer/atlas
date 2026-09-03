# Jira-Demo: Ticket-Eingang 📥

Die Gegenrichtung zum [Zugangsantrag](../jira-zugangsantrag/): dort schreibt Atlas
nach Jira, hier **hört Atlas auf Jira**. Ein neuer Vorgang startet eine
Prozessinstanz ([ADR-0214](../../docs/adr/0214-jira-inbound-issue-watch.md)), die
den Eingang quittiert, das Konto des Melders nachschlägt
([ADR-0223](../../docs/adr/0223-jira-account-lookup.md)) und ihm den Vorgang
zuweist.

```
Neuer Jira-Vorgang
  → (Nachricht jira.ticket.created)
  → Eingang quittieren        add-comment
  → Melder nachschlagen       search-users     → konten
  → Konto gefunden?           (X) Default: ohne Zuweisung
       gefunden   → Ticket zuweisen    assign-issue  =konten[1].accountId
       kein Konto → Zuweisung offen    add-comment
  → Eingang bearbeitet
```

Es gibt **kein Startformular**. Die Variablen kommen aus dem Ereignis.

## Voraussetzungen

Ein Worker namens `jira` unter *Console → Workers* — Einrichtung und
API-Token wie im [Zugangsantrag](../jira-zugangsantrag/README.md#einrichtung-jira-cloud)
beschrieben. Wenn dort der Verbindungstest durchläuft, stimmt alles Nötige.

Zusätzlich für dieses Modell: das Atlassian-Konto des Workers braucht die globale
Berechtigung **Benutzer und Gruppen durchsuchen**. Ohne sie findet `search-users`
niemanden — und zwar ohne Fehler, siehe unten.

## Die Überwachung anlegen

*Console → Workers* → in der Zeile des Jira-Workers auf **Events**:

| Feld | Wert |
|---|---|
| JQL | `project = OPS AND issuetype = Bug` |
| Watch | **new issues** |
| Message name | `jira.ticket.created` |
| Correlation key | *(leer lassen)* |

Zwei Regeln für die JQL: sie muss etwas einschränken — Jira Cloud weist eine
unbegrenzte Abfrage mit HTTP 400 ab — und sie darf **kein `ORDER BY`** tragen. Die
Überwachung sortiert selbst nach dem Cursor-Feld; täte sie es nicht, hätte ihre
Fortsetzungsposition keine Bedeutung.

Der **Message name** ist die ganze Kopplung zwischen der Überwachung und dem
Modell. Ist er hier und im Startereignis auch nur um ein Zeichen verschieden,
laufen beide Hälften fehlerfrei und treffen sich nie. Der Modeler schreibt deshalb
unter den Nachrichtennamen, welche Überwachungen ihn veröffentlichen — steht dort
„No inbound event watch … publishes", ist genau das passiert.

## Was die Instanz sieht

Die Überwachung setzt diese Variablen auf der gestarteten Instanz:

| Variable | Inhalt |
|---|---|
| `issueKey` | `OPS-42` |
| `issueId` | die numerische Id |
| `projectKey` | `OPS` |
| `issueType` | `Bug` |
| `summary` | die Titelzeile |
| `status` | `To Do` |
| `reporter` | der **Anzeigename** des Melders |
| `created`, `updated` | die Zeitstempel |
| `eventType` | `jira.issue.created` |
| `issue` | der ganze Vorgang, für alles Übrige |

`reporter` ist ein Name, keine `accountId` — und genau deshalb steht `search-users`
im Modell. Jira übergibt einen Vorgang an eine `accountId`, der Prozess kennt einen
Namen; die Suche ist der Schritt dazwischen.

## Ausprobieren

1. Modell in ein Projekt deployen.
2. Überwachung wie oben anlegen.
3. In Jira ein Ticket im überwachten Projekt anlegen.
4. **Etwa zwei Minuten warten**, dann *Operations → Instanzen*.
5. Im Jira-Vorgang stehen der Quittungskommentar und die Zuweisung.

## Drei Dinge, die sonst wie ein Defekt aussehen

**Es dauert rund zwei Minuten.** Der Cursor wird absichtlich hinter dem neuesten
gesehenen Vorgang gehalten, weil Jiras Suche aus einem Index bedient wird, der dem
Schreiben nachläuft — ein spät indizierter Vorgang liegt so noch im Fenster der
nächsten Abfrage. Ihn erneut zu lesen kostet nichts: die Idempotenz-Marke gehört
dem Vorgang selbst, der zweite Fund wird verworfen. Braucht deine Site länger, ist
`lagSeconds` an der Überwachung der Regler dafür.

**Eine neue Überwachung ist vorwärtsgerichtet.** Bestehende Vorgänge werden
übersprungen — du kannst sie auf ein Projekt mit jahrelanger Historie richten, ohne
pro Alt-Ticket eine Instanz zu starten. Zum Testen heisst das: erst die
Überwachung, dann das Ticket.

**Ein leeres `konten` ist mehrdeutig.** Dasselbe leere Array bedeutet „niemand
passt", „das Konto ist über seine Profil-Sichtbarkeit nicht auffindbar" und „dem
Worker fehlt *Benutzer und Gruppen durchsuchen*" — Jira unterscheidet die drei
nicht, also kann der Worker es auch nicht. Deshalb ist der Default des Gateways
der Zweig **ohne** Zuweisung, und deshalb schreibt er den Grund in den Vorgang,
statt still zu enden.

## Die FEEL-Falle im Gateway

Die Bedingung lautet `count(konten) > 0` und ist hier richtig — aber nicht aus dem
Grund, den man vermutet:

```
count([])    = 0     leere Liste
count(null)  = 1     ← ein einzelner Wert zählt als einelementige Liste
```

`search-users` schreibt **immer** eine Liste, auch eine leere, deshalb greift die
Bedingung. Wäre `konten` nicht gesetzt — etwa weil die `resultVariable` umbenannt
wurde —, nähme das Gateway den Zweig „gefunden", und der nächste Task zöge eine
`accountId` aus `null`. Wer die Bedingung kopiert, sollte wissen, worauf sie sich
verlässt.
