// The training nuggets: what each one shows, in order, and what to point at.
//
// This file is the source. `capture.mjs` reads it, drives a throwaway Atlas
// instance to take every shot in SHOTS, measures each named target out of the
// live page, and writes both the images and the #nug-data block in
// api/web/handbuch.html. Nothing here is a coordinate — a scene names a target
// ("the Deploy button") and the capture resolves where that actually is, so a
// UI that moves is re-measured rather than re-guessed.
//
// Adding a scene: give it an `img` that exists in SHOTS, a bilingual `cap`, and
// optionally a `focus` naming one of that shot's `targets`.

/** Shots to take: a route, what to do once there, and the targets to measure. */
export const SHOTS = [
  { id: "console", route: "#/console" },

  // The app switcher is a drawer, not a popup — which is exactly the kind of
  // thing a drawn stage got wrong before these were captures.
  { id: "apps", route: "#/console",
    act: async (p) => { await p.locator("button[aria-label*='app' i]").first().click(); await p.waitForTimeout(800); },
    targets: { list: "#drawer-apps", tasks: "#drawer-apps a >> text=Tasks" } },

  { id: "modeler-home", route: "#/modeler" },
  { id: "modeler-diagram", route: "#/modeler", wait: 3500,
    act: async (p) => { await p.locator("a", { hasText: /Order-to-Cash/ }).first().click(); await p.waitForTimeout(3500); },
    targets: { deploy: "button:has-text('Deploy')", tabs: "button:has-text('Playground')",
               canvas: ".djs-container, .bjs-container" } },
  { id: "modeler-playground", route: "#/modeler", wait: 3500,
    act: async (p) => {
      await p.locator("a", { hasText: /Order-to-Cash/ }).first().click(); await p.waitForTimeout(3200);
      await p.locator("button", { hasText: "Playground" }).first().click(); await p.waitForTimeout(2600);
    } },

  { id: "tasks-inbox", route: "#/tasks",
    targets: { list: "text=All tasks", first: "text=Antrag freigeben" } },
  { id: "tasks-form", route: "#/tasks",
    act: async (p) => {
      await p.locator("*", { hasText: /^Antrag freigeben$/ }).last().click({ timeout: 4000 }).catch(() => {});
      await p.waitForTimeout(2400);
    },
    targets: { claim: "button:has-text('Claim')", complete: "button:has-text('Complete task')",
               detail: "text=Candidate groups" } },
  { id: "tasks-start", route: "#/tasks/start" },

  { id: "ops-instances", route: "#/operations",
    targets: { search: "input[placeholder*='variable' i]",
               row: "text=Order-to-Cash (Warenkorb bis Zahlung)",
               open: "a:has-text('Open'), button:has-text('Open')" } },
  // Open the Order-to-Cash row specifically. Taking whichever row came first
  // once produced a shot of a different process than the caption described.
  { id: "ops-process", route: "#/operations", wait: 3500,
    act: async (p) => {
      await p.locator("tr", { hasText: "Order-to-Cash" }).first()
        .locator("a,button").filter({ hasText: /^Open$/ }).first().click();
      await p.waitForTimeout(3500);
    },
    targets: { canvas: ".djs-container, .bjs-container" } },
  { id: "ops-incidents", route: "#/operations/incidents" },
  { id: "ops-workers", route: "#/operations/workers" },
  { id: "ops-decisions", route: "#/operations/decisions" },

  { id: "panorama", route: "#/panorama/landscape", wait: 3600 },
  { id: "data-model", route: "#/data" },

  { id: "console-org", route: "#/console/org" },
  { id: "console-workers", route: "#/console/workers" },
  { id: "console-ai", route: "#/console/ai-access" },
  { id: "console-audit", route: "#/console/audit" },
  { id: "console-engine", route: "#/console/engine" },
];

const de = (s) => s, en = (s) => s; // readability only

export const NUGGETS = [
{
  id: "roundhouse",
  title: { de: "Roundhouse Kick &ndash; Atlas in drei Minuten", en: "Roundhouse Kick &ndash; Atlas in three minutes" },
  lead: { de: "Der ganze Überblick am Stück, an der echten Oberfläche: was Atlas ist, was es kann und wofür man es nimmt.",
          en: "The whole picture in one go, on the real interface: what Atlas is, what it does and what it is for." },
  scenes: [
    { t: 9000, img: "console",
      cap: { de: "<b>Atlas</b> ist eine Workflow-Engine für BPMN 2.x in einer einzigen Datei. Das hier ist die <b>Console</b> &ndash; der Startpunkt, der sagt, was auf dieser Installation läuft.",
             en: "<b>Atlas</b> is a workflow engine for BPMN 2.x in a single file. This is the <b>Console</b> &ndash; the starting point that says what is running on this installation." } },
    { t: 8500, img: "apps", focus: "list",
      cap: { de: "Oben links öffnet sich die App-Schublade. <b>Sechs Apps</b>, und jede Person sieht nur, was ihre Rolle erreicht.",
             en: "The app drawer opens at the top left. <b>Six apps</b>, and each person sees only what their role reaches." } },
    { t: 9000, img: "modeler-home",
      cap: { de: "Im <b>Modeler</b> liegen die Modelle. Jedes ist ein Prozess, den die Engine ausführen kann &ndash; kein Dokument daneben, das mit der Wirklichkeit auseinanderläuft.",
             en: "The <b>Modeler</b> holds the models. Each one is a process the engine can execute &ndash; no document alongside it drifting away from reality." } },
    { t: 11000, img: "modeler-diagram", focus: "canvas",
      cap: { de: "Das ist ein echter Prozess: Bestellung prüfen, rechnen, und beim Gateway <b>«Summe &gt; 100 EUR?»</b> teilt sich der Weg &ndash; <b>ja</b> zur Freigabe durch einen Menschen, <b>nein</b> direkt weiter. Danach laufen Kommissionierung, Versand und Rechnung <b>parallel</b>.",
             en: "This is a real process: check the order, do the arithmetic, and at the gateway <b>\"total &gt; 100 EUR?\"</b> the path splits &ndash; <b>yes</b> to a human approval, <b>no</b> straight on. After that, picking, shipping and invoicing run <b>in parallel</b>." } },
    { t: 8500, img: "modeler-diagram", focus: "deploy", tap: true,
      cap: { de: "<b>Deploy</b> kompiliert das Modell, prüft es und nimmt es an. Ab jetzt kann es laufen &ndash; und laufende Instanzen bleiben auf ihrer bisherigen Version.",
             en: "<b>Deploy</b> compiles the model, validates it and accepts it. From now on it can run &ndash; and instances already running stay on the version they started with." } },
    { t: 9000, img: "ops-instances", focus: "row",
      cap: { de: "In <b>Operations</b> steht eine Zeile je deploytem Prozess: wie viele Instanzen laufen, wie viele klemmen, wann zuletzt etwas passiert ist.",
             en: "In <b>Operations</b> there is one row per deployed process: how many instances are running, how many are stuck, when something last happened." } },
    { t: 11000, img: "ops-process", focus: "canvas",
      cap: { de: "Ein Klick auf <b>Open</b> zeigt alle Instanzen <i>auf dem Diagramm</i>. Die Zahlen an den Elementen sind Token &ndash; hier warten vier Fälle bei «Bestellung freigeben». Rechts stehen die echten Variablen jeder Instanz.",
             en: "<b>Open</b> shows every instance <i>on the diagram</i>. The numbers on the elements are tokens &ndash; four cases are waiting at \"Bestellung freigeben\" here. On the right are each instance's actual variables." } },
    { t: 9000, img: "tasks-inbox", focus: "list",
      cap: { de: "Wo ein Mensch entscheiden muss, entsteht eine <b>Aufgabe</b>. Sie landet in <b>Tasks</b>, im Posteingang derer, die zuständig sind &ndash; nicht in einer Mail.",
             en: "Where a person has to decide, a <b>task</b> appears. It lands in <b>Tasks</b>, in the inbox of whoever is responsible &ndash; not in a mail." } },
    { t: 10000, img: "tasks-form", focus: "detail",
      cap: { de: "Die Aufgabe zeigt, woher sie kommt &ndash; Prozess, Element, Instanz &ndash; und darunter ihr <b>Formular</b>. Was hier eingetragen wird, sind die Daten, mit denen der Prozess weiterrechnet.",
             en: "The task shows where it came from &ndash; process, element, instance &ndash; and below that its <b>form</b>. What is entered here is the data the process carries on with." } },
    { t: 9000, img: "console-workers",
      cap: { de: "Andere Schritte laufen von selbst: Ein <b>Worker</b> erledigt sie. Mail, REST, Datenbanken, SharePoint, Active Directory, Jira &ndash; eingerichtet wird das einmal in der Console.",
             en: "Other steps run by themselves: a <b>worker</b> does them. Mail, REST, databases, SharePoint, Active Directory, Jira &ndash; configured once in the Console." } },
    { t: 9000, img: "ops-decisions",
      cap: { de: "Regeln gehören nicht ins Diagramm, sondern in eine <b>Entscheidungstabelle</b>. Operations zeigt jede Auswertung mit Eingaben und Ergebnis &ndash; so ist nachlesbar, warum ein Fall so entschieden wurde.",
             en: "Rules do not belong in the diagram but in a <b>decision table</b>. Operations shows every evaluation with its inputs and result &ndash; so why a case was decided that way stays readable." } },
    { t: 9500, img: "ops-incidents",
      cap: { de: "Klemmt etwas, verschwindet der Fall nicht: Es entsteht ein <b>Incident</b>, der sagt, woran es liegt. Hier ist gerade keiner offen &ndash; ist einer da, steht er mit Ursache und Instanz in dieser Liste.",
             en: "When something jams, the case does not vanish: an <b>incident</b> is raised that says what is wrong. None is open here &ndash; when there is one, it stands in this list with its cause and instance." } },
    { t: 10000, img: "modeler-playground",
      cap: { de: "Vor dem Go-live rechnet der <b>Playground</b> den Entwurf mit echten Daten auf der echten Engine durch &ndash; in einer Wegwerf-Sandbox, ohne Deploy und ohne Nebenwirkungen.",
             en: "Before go-live the <b>Playground</b> runs the draft with real data on the real engine &ndash; in a throwaway sandbox, with no deploy and no side effects." } },
    { t: 9000, img: "panorama",
      cap: { de: "Sind es viele Prozesse, zeigt <b>Panorama</b> die Landschaft: was es gibt und was woran hängt &ndash; berechnet aus dem, was deployt ist, nicht gezeichnet.",
             en: "Once there are many processes, <b>Panorama</b> shows the landscape: what exists and what hangs off what &ndash; computed from what is deployed, not drawn." } },
    { t: 9000, img: "data-model",
      cap: { de: "Und <b>Data</b> hält fest, was die Daten <i>sind</i>: ein Klassendiagramm über allen Prozessen einer Applikation, mit dem Business Key, der sagt, dass zwei Aufträge derselbe sind.",
             en: "And <b>Data</b> records what the data <i>is</i>: a class diagram above every process of an application, with the business key that says two orders are the same one." } },
    { t: 9500, img: "console-engine",
      cap: { de: "Betrieben wird das als <b>eine Datei</b>: keine Datenbank, kein Broker, kein Cluster als Voraussetzung. Der schnellste Einstieg steht gleich darunter &ndash; oder der Pfad für deine Rolle.",
             en: "It runs as <b>one file</b>: no database, no broker, no cluster as a prerequisite. The fastest way in is right below &ndash; or the path for your own role." } },
  ],
},
{
  id: "user", role: "user",
  title: { de: "Für <code>user</code>: eine Aufgabe erledigen", en: "For <code>user</code>: doing a task" },
  lead: { de: "Der kürzeste Weg: Aufgabe finden, übernehmen, ausfüllen, abschliessen.",
          en: "The shortest path: find a task, claim it, fill it in, complete it." },
  scenes: [
    { t: 8000, img: "apps", focus: "tasks", tap: true,
      cap: { de: "Deine Rolle ist <code>user</code>: Du erledigst, was ein Prozess an Menschen gibt. Dafür brauchst du eine App &ndash; <b>Tasks</b>.",
             en: "Your role is <code>user</code>: you do what a process hands to people. For that you need one app &ndash; <b>Tasks</b>." } },
    { t: 9000, img: "tasks-inbox", focus: "list",
      cap: { de: "Links filterst du: <b>alle</b> Aufgaben, <b>dir zugewiesene</b>, <b>freie</b> oder die deiner <b>Gruppe</b>. Die Zahl dahinter sagt, wie viele es sind.",
             en: "On the left you filter: <b>all</b> tasks, <b>assigned to me</b>, <b>unassigned</b>, or your <b>group's</b>. The number beside each says how many." } },
    { t: 9000, img: "tasks-form", focus: "detail",
      cap: { de: "Eine Aufgabe anklicken öffnet sie rechts: woher sie stammt, wer sie bearbeiten darf, und welche Instanz dahinter steht.",
             en: "Clicking a task opens it on the right: where it came from, who may work on it, and which instance is behind it." } },
    { t: 9000, img: "tasks-form", focus: "claim", tap: true,
      cap: { de: "<b>Claim</b> heisst: Die Aufgabe gehört jetzt dir, und alle anderen sehen das. So arbeiten nicht zwei am selben Fall.",
             en: "<b>Claim</b> means the task is yours now, and everyone else can see it. So two people do not work the same case." } },
    { t: 9000, img: "tasks-form", focus: "complete", tap: true,
      cap: { de: "<b>Complete task</b> gibt den Fall an den Prozess zurück. Der Token wandert weiter, und der nächste Schritt beginnt &ndash; automatisch oder bei der nächsten Person.",
             en: "<b>Complete task</b> hands the case back to the process. The token moves on and the next step begins &ndash; automatically, or at the next person." } },
    { t: 8000, img: "tasks-start",
      cap: { de: "Unter <b>Start</b> stehen die Prozesse, die du selbst anstossen darfst &ndash; mit dem Formular, das sie dafür brauchen.",
             en: "Under <b>Start</b> are the processes you may kick off yourself &ndash; with the form they need for it." } },
  ],
},
{
  id: "modeler", role: "modeler",
  title: { de: "Für <code>modeler</code>: vom Diagramm zum Deploy", en: "For <code>modeler</code>: from diagram to deploy" },
  lead: { de: "Zeichnen, verdrahten, im Playground durchrechnen, deployen.",
          en: "Draw it, wire it, run it through the Playground, deploy it." },
  scenes: [
    { t: 8500, img: "modeler-home",
      cap: { de: "Deine Rolle ist <code>modeler</code>: Du baust die Prozesse &ndash; und du darfst sie <b>deployen</b>. Deployen ist Codeausführung, deshalb ist es genau diese Rolle.",
             en: "Your role is <code>modeler</code>: you build the processes &ndash; and you may <b>deploy</b> them. Deploying is code execution, which is why it takes exactly this role." } },
    { t: 11000, img: "modeler-diagram", focus: "canvas",
      cap: { de: "Auf der Zeichenfläche entsteht der Ablauf. Ein <b>Gateway</b> verzweigt immer in mindestens zwei Wege &ndash; hier «ja» zur Freigabe und «nein» daran vorbei. Ein Gateway mit nur einem Ausgang wäre keins.",
             en: "The flow takes shape on the canvas. A <b>gateway</b> always splits into at least two paths &ndash; here \"yes\" to approval and \"no\" past it. A gateway with a single exit would not be one." } },
    { t: 9000, img: "modeler-diagram", focus: "tabs", tap: true,
      cap: { de: "Oben wechselst du zwischen <b>Design</b> (was passiert), <b>Implement</b> (womit) und <b>Playground</b> (was dabei herauskommt).",
             en: "At the top you switch between <b>Design</b> (what happens), <b>Implement</b> (with what) and <b>Playground</b> (what comes out of it)." } },
    { t: 10000, img: "modeler-playground",
      cap: { de: "Der <b>Playground</b> lässt den Entwurf mit echten Daten auf der echten Engine laufen &ndash; in einer Sandbox, die danach verworfen wird. Kein Deploy, keine Nebenwirkungen.",
             en: "The <b>Playground</b> runs the draft with real data on the real engine &ndash; in a sandbox discarded afterwards. No deploy, no side effects." } },
    { t: 9000, img: "modeler-diagram", focus: "deploy", tap: true,
      cap: { de: "<b>Deploy</b> kompiliert, validiert und versioniert. Laufende Instanzen bleiben auf ihrer Version &ndash; ein Deploy reisst niemandem den Boden weg.",
             en: "<b>Deploy</b> compiles, validates and versions. Running instances stay on their own version &ndash; a deploy pulls the rug from under nobody." } },
    { t: 9000, img: "data-model",
      cap: { de: "Was ein «Auftrag» überhaupt ist, sagst du einmal in <b>Data</b> &ndash; als Klassendiagramm, das für alle Prozesse einer Applikation gilt.",
             en: "What an \"order\" even is you state once in <b>Data</b> &ndash; as a class diagram that holds for every process of an application." } },
  ],
},
{
  id: "operator", role: "operator",
  title: { de: "Für <code>operator</code>: beobachten und reparieren", en: "For <code>operator</code>: watching and repairing" },
  lead: { de: "Instanz finden, auf dem Diagramm lesen, Incident auflösen, Worker prüfen.",
          en: "Find an instance, read it on the diagram, resolve an incident, check the worker." },
  scenes: [
    { t: 8500, img: "ops-instances", focus: "row",
      cap: { de: "Deine Rolle ist <code>operator</code>: Du betreibst, was deployt ist. Starten, beobachten, reparieren &ndash; aber nicht modellieren.",
             en: "Your role is <code>operator</code>: you run what is deployed. Start, watch, repair &ndash; but not model." } },
    { t: 10000, img: "ops-instances", focus: "search", tap: true,
      cap: { de: "Eine Version kann Hunderttausende Instanzen tragen, deshalb suchst du: nach Instanzschlüssel oder nach einer <b>Variablen</b> &ndash; <code>kdnr=MT-1008</code> findet genau diesen Kunden, nicht MT-10081.",
             en: "A version can carry hundreds of thousands of instances, so you search: by instance key or by a <b>variable</b> &ndash; <code>kdnr=MT-1008</code> finds exactly that customer, not MT-10081." } },
    { t: 11000, img: "ops-process", focus: "canvas",
      cap: { de: "Die Prozessansicht zeigt alle Instanzen zugleich auf dem Diagramm: wo Token stehen, was schon durchlaufen ist, und rechts die Variablen jedes einzelnen Falls. Die Legende unten erklärt jede Markierung.",
             en: "The process view shows every instance at once on the diagram: where tokens stand, what has already run, and on the right each case's variables. The legend at the bottom explains every marking." } },
    { t: 9500, img: "ops-incidents",
      cap: { de: "Ein <b>Incident</b> ist ein Fall, der nicht weiterkann &ndash; und er sagt, woran es liegt. Ursache beheben, auflösen, und die Instanz läuft weiter, wo sie stand. Kein Fall geht verloren.",
             en: "An <b>incident</b> is a case that cannot proceed &ndash; and it says why. Fix the cause, resolve it, and the instance carries on where it stopped. No case is lost." } },
    { t: 9000, img: "ops-workers",
      cap: { de: "Wiederholt sich derselbe Incident, liegt es selten am Fall: <b>Workers</b> zeigt, ob der Worker überhaupt läuft und Aufgaben abholt.",
             en: "If the same incident keeps returning it is rarely the case at fault: <b>Workers</b> shows whether the worker is running and leasing jobs at all." } },
    { t: 9000, img: "ops-decisions",
      cap: { de: "Und unter <b>Decisions</b> steht jede ausgewertete Entscheidung mit Eingaben und Ergebnis &ndash; die Antwort auf «warum wurde das so entschieden?».",
             en: "And under <b>Decisions</b> stands every evaluated decision with inputs and result &ndash; the answer to \"why was it decided that way?\"." } },
  ],
},
{
  id: "admin", role: "admin",
  title: { de: "Für <code>admin</code>: Konten, Rollen, Zugangsdaten", en: "For <code>admin</code>: accounts, roles, credentials" },
  lead: { de: "Rollen bewusst vergeben, Worker einrichten, KI-Zugang, Protokoll und Sicherung.",
          en: "Grant roles deliberately, configure workers, AI access, the audit log and backup." },
  scenes: [
    { t: 9000, img: "console-org",
      cap: { de: "Deine Rolle ist <code>admin</code>: alles, was die Installation selbst betrifft &ndash; Konten, Gruppen, Anmeldung. <code>admin</code> erreicht alles.",
             en: "Your role is <code>admin</code>: everything about the installation itself &ndash; accounts, groups, sign-in. <code>admin</code> reaches everything." } },
    { t: 9500, img: "console-org",
      cap: { de: "Vier Rollen, und sie sind eine <b>Liste, keine Rangfolge</b>: <code>modeler</code> enthält <code>operator</code> nicht. Ein neues Konto bekommt <code>user</code> &ndash; alles Weitere vergibst du bewusst.",
             en: "Four roles, and they are a <b>list, not a ladder</b>: <code>modeler</code> does not contain <code>operator</code>. A new account gets <code>user</code> &ndash; everything beyond that you grant deliberately." } },
    { t: 9500, img: "console-workers",
      cap: { de: "Ein <b>Worker</b> ist die Stelle, an der Atlas nach draussen greift. Die Zugangsdaten liegen im Tresor, nie im Prozessmodell &ndash; und hier siehst du, welche Art wo läuft.",
             en: "A <b>worker</b> is where Atlas reaches outside. Credentials live in the vault, never in the process model &ndash; and here you see which kind runs where." } },
    { t: 9500, img: "console-ai",
      cap: { de: "Soll ein KI-Assistent Atlas bedienen, richtest du das unter <b>AI access</b> ein. Die Seite prüft vorher, ob die veröffentlichten Adressen überhaupt brauchbar sind &ndash; sonst endet es als «der Connector geht einfach nicht».",
             en: "To let an AI assistant drive Atlas you set that up under <b>AI access</b>. The page checks first whether the published addresses are usable at all &ndash; otherwise it ends as \"the connector just doesn't work\"." } },
    { t: 9000, img: "console-audit",
      cap: { de: "Das <b>Revisionsprotokoll</b> hält jede Freigabe, jeden Entzug und jeden Eigentümerwechsel fest. Ohne eingeschaltete Authentisierung wird nichts aufgezeichnet &ndash; ohne angemeldete Person gibt es keinen Handelnden.",
             en: "The <b>audit log</b> records every share, every revocation and every change of owner. With authentication off nothing is recorded &ndash; with no principal there is no actor." } },
    { t: 9000, img: "console-engine",
      cap: { de: "Und unter <b>Engine</b> steht, wie es der Installation geht. Ein letzter Punkt, der zählt, wenn es darauf ankommt: Ohne den Tresor-Schlüssel ist ein Datenverzeichnis nicht wiederherstellbar. Sichere ihn getrennt.",
             en: "And under <b>Engine</b> stands how the installation is doing. One last point that matters when it matters: without its vault key a data directory is not recoverable. Back it up separately." } },
  ],
},
];
