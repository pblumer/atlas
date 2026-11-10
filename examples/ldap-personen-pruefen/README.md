# LDAP-Personen prüfen

Jede Person eines LDAP-Verzeichnisses an Regeln messen, die in DMN stehen (Plan
[AD-Suche als Dokument](../../docs/planning/ad-search-as-document.md), S3).

| Datei | Inhalt |
|---|---|
| [`ldap-personen-pruefen.bpmn`](ldap-personen-pruefen.bpmn) | LDAP-Suche als Dokument, dann ein Business-Rule-Task über die Datei. |
| [`ldap-personen-pruefen.dmn`](ldap-personen-pruefen.dmn) | `PersonGueltig`: Adresse in beispiel.ch, bekannte Abteilung (`departmentNumber`), Personalnummer mit sechs Ziffern (`employeeNumber`), Personentyp intern oder extern. |
| [`personen.ldif`](personen.ldif) | 200 erfundene Personen unter `ou=Personen,dc=beispiel,dc=ch`, 12 mit Befund. |

Der LDAP-Worker hat **keinen Mockup-Modus**. Das Beispiel braucht einen LDAP-Server mit dem
Suffix `dc=beispiel,dc=ch`, in den `personen.ldif` geladen ist; die Datei beginnt mit dem
Eintrag `ou=Personen` und lässt sich mit `ldapadd` in einen leeren Server laden. Im Task
*Personen lesen* sind Server-URL, Bind-DN und Base-DN anzupassen; das Bind-Passwort liegt als
Secret `LDAP_BIND`. 50 000 Personen erzeugt `go run ./examples/testdaten/generieren -n 50000`.

Getestet in `worker/quality_examples_test.go` gegen ein Verzeichnis im Arbeitsspeicher.
