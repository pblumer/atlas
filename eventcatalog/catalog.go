// Package eventcatalog is the one list of the events Atlas emits (ADR-0435): under
// which name, meaning what, carrying what, through which channel, since when and how
// stable. The Console's Events page, the Modeler's signal picker, the handbook's
// chapter and the runtime contract's table all read it, and tests hold it equal to
// what the code emits, in both directions, the way logging's catalogue is held.
//
// It imports nothing from the engine, the server or the state store, so every one of
// them may import it (eventcatalog's own test holds that).
package eventcatalog

import "strings"

// Kind says who produces an event.
type Kind string

const (
	// Domain is a fact of a system process, emitted by its model as a signal or a
	// message, at a moment where something waits for a person or an outcome is final.
	Domain Kind = "domain"
	// Platform is a fact of the engine or the server, derived from a durable record and
	// delivered through the feed or the log — never thrown as a BPMN signal.
	Platform Kind = "platform"
)

// Channel is how an event leaves its producer.
type Channel string

const (
	// Signal is a BPMN signal inside the engine: zero to many listeners, not buffered.
	Signal Channel = "signal"
	// Message is a BPMN message inside the engine, correlated to one receiver.
	Message Channel = "message"
	// Feed is the CloudEvents feed at GET /api/v1/events and its push delivery:
	// durable, at least once, ordered from a cursor.
	Feed Channel = "feed"
	// Log is a structured log event, named in logging's catalogue.
	Log Channel = "log"
)

// Stability says what a receiver may rely on across versions.
type Stability string

const (
	// Stable entries only ever gain payload fields; a removal fails a test.
	Stable Stability = "stable"
	// Experimental entries may change, and a change is called out in the changelog.
	Experimental Stability = "experimental"
)

// Unreleased is the Since of an event that arrived after the newest release.
const Unreleased = "Unreleased"

// Text is a sentence in both of the handbook's languages.
type Text struct {
	EN string `json:"en"`
	DE string `json:"de"`
}

// Moment is where an event is emitted. A domain fact names the places — a system
// process and its element — that throw it, or, for a message the server publishes,
// the element that receives it, and Producer says who sends it. One fact may be
// thrown in several processes: the shop's three approval processes each announce the
// same request. A platform fact names the record or the component it is derived from,
// and no place.
type Moment struct {
	Places   []Place `json:"places,omitempty"`
	Producer string  `json:"producer"`
}

// Place is one element of one system process.
type Place struct {
	Process string `json:"process"`
	Element string `json:"element"`
}

// At is the place of an element in a system process.
func At(process, element string) Place { return Place{Process: process, Element: element} }

// Field is one field of a payload a receiver may rely on.
type Field struct {
	Name string `json:"name"`
	// Type is the JSON type a receiver reads: string, number, boolean, object.
	Type string `json:"type"`
	// Always is false for a field that is present only sometimes.
	Always bool `json:"always"`
	// Data says whether the field is personal data. Every field says so: there is
	// no default.
	Data    Data `json:"data"`
	Meaning Text `json:"meaning"`
}

// Data says whether a payload field is personal data (ADR-0314). It has no default
// (ADR-0435 §5): the access rule reads it, so the zero value is no answer, and a test
// refuses a field that gives none rather than letting it pass as not personal.
type Data string

const (
	// PersonalData is somebody's data: a receiver that keeps it keeps that person's
	// data, and a listener on a signal carrying it is deployed by an administrator.
	PersonalData Data = "personal"
	// NotPersonal names nobody.
	NotPersonal Data = "not-personal"
)

// Personal reports whether the field is marked personal data.
func (f Field) Personal() bool { return f.Data == PersonalData }

// Entry is one event, as the catalogue states it (ADR-0435 §1).
type Entry struct {
	// Type is the name used verbatim as the signal, the message or the CloudEvents
	// type: atlas.<subject>.<fact in the past tense>. An entry that describes a shape
	// rather than one name — a product action's outcome, named by its product's
	// author — writes the shape here and sets Shaped.
	Type   string `json:"type"`
	Shaped bool   `json:"shaped,omitempty"`
	Kind   Kind   `json:"kind"`
	// Meaning is one sentence: what has happened when this is emitted.
	Meaning  Text      `json:"meaning"`
	Moment   Moment    `json:"moment"`
	Channels []Channel `json:"channels"`
	// LogEvent names the event in logging's catalogue, for an entry with the Log
	// channel.
	LogEvent string  `json:"logEvent,omitempty"`
	Payload  []Field `json:"payload"`
	// NeverSecret names the test that holds the payload free of secrets. Every entry
	// has one: an event never carries a secret.
	NeverSecret string    `json:"neverSecret"`
	Since       string    `json:"since"`
	Stability   Stability `json:"stability"`
	// Access says who may receive it, per channel.
	Access map[Channel]string `json:"access"`
	// Listenable says a model of an installation may listen to it, so the Modeler
	// offers its name. A message that drives a system process is not: a second
	// receiver would take it from the process it drives.
	Listenable bool `json:"listenable,omitempty"`
}

// Has reports whether the entry uses a channel.
func (e Entry) Has(c Channel) bool {
	for _, have := range e.Channels {
		if have == c {
			return true
		}
	}
	return false
}

// PersonalFields names the payload fields marked personal data.
func (e Entry) PersonalFields() []string {
	var out []string
	for _, f := range e.Payload {
		if f.Personal() {
			out = append(out, f.Name)
		}
	}
	return out
}

// Lookup finds the entry of an event type. A product action's outcome type is the
// product author's and is not found here by name.
func Lookup(eventType string) (Entry, bool) {
	for _, e := range Entries {
		if !e.Shaped && e.Type == eventType {
			return e, true
		}
	}
	return Entry{}, false
}

// IsAtlasName reports whether a name is in Atlas's own namespace, atlas.*. A listener
// on such a name receives a fact of Atlas's, which is what the catalogue governs.
func IsAtlasName(name string) bool {
	return strings.HasPrefix(strings.TrimSpace(name), "atlas.")
}

// The names the code emits, as constants the producers use, so a name in the code
// and its entry here are the same string by construction.
const (
	OrderPlaced        = "atlas.order.placed"
	OrderAdvanced      = "atlas.order.advanced"
	EntitlementGranted = "atlas.entitlement.granted"
	EntitlementRevoked = "atlas.entitlement.revoked"
	UserRequested      = "atlas.user.requested"
	ApprovalRequested  = "atlas.approval.requested"
	// ActionOutcomePrefix is the type of an action's outcome whose product declares
	// none and whose action names no message: atlas.action.<outcome>.
	ActionOutcomePrefix = "atlas.action."
)

// Access rules, as the entries state them.
const (
	accessFeed    = "the feedreader role or an events token; an events token narrowed by reach receives only the events of products whose home catalogue it reaches (ADR-0430, ADR-0432)"
	accessSignal  = "any model deployed on this server with a signal start or catch on the name (ADR-0431); when the payload carries personal data, only an administrator may deploy such a model (ADR-0435)"
	accessMessage = "the system process it drives; a model of an installation does not receive it"
)

// Entries is the catalogue. A planned event lands here with the change that emits
// it, never before: the drift tests refuse an entry nothing produces.
var Entries = []Entry{
	{
		Type: OrderPlaced, Kind: Domain,
		Meaning: Text{
			EN: "An order was placed, so its fulfilment can begin.",
			DE: "Eine Bestellung wurde aufgegeben; ihre Erfüllung kann beginnen.",
		},
		Moment:   Moment{Places: []Place{At("atlas-auftrag-erfuellung", "Start")}, Producer: "the order service, when an order is placed"},
		Channels: []Channel{Message},
		Payload: []Field{
			{Name: "orderId", Type: "string", Always: true, Data: NotPersonal, Meaning: Text{EN: "The order.", DE: "Die Bestellung."}},
			{Name: "orderer", Type: "string", Always: true, Data: NotPersonal, Meaning: Text{EN: "Who placed it, by principal id.", DE: "Wer bestellt hat, als Principal-ID."}},
			{Name: "recipient", Type: "string", Always: true, Data: NotPersonal, Meaning: Text{EN: "Who it is for, by principal id.", DE: "Für wen bestellt wurde, als Principal-ID."}},
			{Name: "portalBaseUrl", Type: "string", Always: true, Data: NotPersonal, Meaning: Text{EN: "Where the shop is reached.", DE: "Wo der Shop erreichbar ist."}},
			{Name: "atlasApiBase", Type: "string", Always: true, Data: NotPersonal, Meaning: Text{EN: "Where the API is reached.", DE: "Wo die API erreichbar ist."}},
		},
		NeverSecret: "TestTheOrderMessagesCarryNoSecret",
		Since:       "0.7.0", Stability: Stable,
		Access: map[Channel]string{Message: accessMessage},
	},
	{
		Type: OrderAdvanced, Kind: Domain,
		Meaning: Text{
			EN: "A position of an order moved on, so the fulfilment asks again what may start.",
			DE: "Eine Position einer Bestellung ist weitergekommen; die Erfüllung fragt erneut, was starten darf.",
		},
		Moment:      Moment{Places: []Place{At("atlas-auftrag-erfuellung", "Warten")}, Producer: "the order service, when a position is reported, cancelled or repaired; correlated on the order id, with no variables"},
		Channels:    []Channel{Message},
		Payload:     []Field{},
		NeverSecret: "TestTheOrderMessagesCarryNoSecret",
		Since:       "0.7.0", Stability: Stable,
		Access: map[Channel]string{Message: accessMessage},
	},
	{
		Type: EntitlementGranted, Kind: Platform,
		Meaning: Text{
			EN: "A right was granted to a person: a position was provisioned, or a right was recorded by hand or by a load.",
			DE: "Einer Person wurde ein Recht erteilt: Eine Position wurde bereitgestellt, oder ein Recht wurde von Hand oder durch einen Abgleich erfasst.",
		},
		Moment:   Moment{Producer: "the entitlement record (IntentEntitlementGranted), folded into the feed by applyToState"},
		Channels: []Channel{Feed},
		Payload: []Field{
			{Name: "principal", Type: "string", Always: true, Data: NotPersonal, Meaning: Text{EN: "Who holds the right, by principal id.", DE: "Wer das Recht hält, als Principal-ID."}},
			{Name: "itemId", Type: "string", Always: true, Data: NotPersonal, Meaning: Text{EN: "The product.", DE: "Das Produkt."}},
			{Name: "orderId", Type: "string", Always: true, Data: NotPersonal, Meaning: Text{EN: "The order it came from, empty for a right recorded otherwise.", DE: "Die Bestellung, aus der es stammt; leer bei einem anders erfassten Recht."}},
			{Name: "since", Type: "string", Always: true, Data: NotPersonal, Meaning: Text{EN: "When it was granted, RFC 3339.", DE: "Seit wann es gilt, RFC 3339."}},
			{Name: "origin", Type: "string", Always: true, Data: NotPersonal, Meaning: Text{EN: "How it came about.", DE: "Wie es zustande kam."}},
			{Name: "variantId", Type: "string", Data: NotPersonal, Meaning: Text{EN: "The variant, for a product ordered in a shape.", DE: "Die Variante, bei einem Produkt mit Varianten."}},
			{Name: "until", Type: "string", Data: NotPersonal, Meaning: Text{EN: "When it ends, for a right with a time limit.", DE: "Wann es endet, bei einem befristeten Recht."}},
			{Name: "homeCatalog", Type: "string", Data: NotPersonal, Meaning: Text{EN: "The catalogue that maintains the product.", DE: "Der Katalog, der das Produkt pflegt."}},
		},
		NeverSecret: "TestTheFeedCarriesNoSecret",
		Since:       Unreleased, Stability: Stable,
		Access: map[Channel]string{Feed: accessFeed},
	},
	{
		Type: EntitlementRevoked, Kind: Platform,
		Meaning: Text{
			EN: "A right a person held ended: it was returned, expired, or taken away.",
			DE: "Ein Recht, das eine Person hielt, endete: Es wurde zurückgegeben, ist abgelaufen oder wurde entzogen.",
		},
		Moment:   Moment{Producer: "the entitlement record (IntentEntitlementRevoked), folded into the feed by applyToState"},
		Channels: []Channel{Feed},
		Payload: []Field{
			{Name: "principal", Type: "string", Always: true, Data: NotPersonal, Meaning: Text{EN: "Who held the right, by principal id.", DE: "Wer das Recht hielt, als Principal-ID."}},
			{Name: "itemId", Type: "string", Always: true, Data: NotPersonal, Meaning: Text{EN: "The product.", DE: "Das Produkt."}},
			{Name: "orderId", Type: "string", Always: true, Data: NotPersonal, Meaning: Text{EN: "The order it came from.", DE: "Die Bestellung, aus der es stammte."}},
			{Name: "since", Type: "string", Always: true, Data: NotPersonal, Meaning: Text{EN: "When it had been granted.", DE: "Seit wann es galt."}},
			{Name: "endedAt", Type: "string", Always: true, Data: NotPersonal, Meaning: Text{EN: "When it ended.", DE: "Wann es endete."}},
			{Name: "reason", Type: "string", Always: true, Data: NotPersonal, Meaning: Text{EN: "Why it ended.", DE: "Warum es endete."}},
			{Name: "endedBy", Type: "string", Always: true, Data: NotPersonal, Meaning: Text{EN: "Who or what ended it.", DE: "Wer oder was es beendet hat."}},
			{Name: "variantId", Type: "string", Data: NotPersonal, Meaning: Text{EN: "The variant, for a product ordered in a shape.", DE: "Die Variante, bei einem Produkt mit Varianten."}},
			{Name: "homeCatalog", Type: "string", Data: NotPersonal, Meaning: Text{EN: "The catalogue that maintains the product.", DE: "Der Katalog, der das Produkt pflegt."}},
		},
		NeverSecret: "TestTheFeedCarriesNoSecret",
		Since:       Unreleased, Stability: Stable,
		Access: map[Channel]string{Feed: accessFeed},
	},
	{
		Type: "<message>.<outcome>", Shaped: true, Kind: Domain,
		Meaning: Text{
			EN: "An action asked of a held position ended. Its type is the product author's: the action's declared event type, by default its message and the outcome; atlas.action.<outcome> where the action names neither.",
			DE: "Eine Aktion an einer gehaltenen Position ist abgeschlossen. Den Typ bestimmt der Produktautor: der deklarierte Ereignistyp der Aktion, sonst ihre Nachricht und das Ergebnis; atlas.action.<outcome>, wo die Aktion keines von beiden nennt.",
		},
		Moment:   Moment{Producer: "the action outcome record (VTActionOutcome), written by the outcome route or a shop task and folded into the feed by applyToState"},
		Channels: []Channel{Feed},
		Payload: []Field{
			{Name: "orderId", Type: "string", Always: true, Data: NotPersonal, Meaning: Text{EN: "The order.", DE: "Die Bestellung."}},
			{Name: "position", Type: "string", Always: true, Data: NotPersonal, Meaning: Text{EN: "The position.", DE: "Die Position."}},
			{Name: "commandId", Type: "string", Always: true, Data: NotPersonal, Meaning: Text{EN: "The command the outcome answers.", DE: "Der Auftrag, den das Ergebnis beantwortet."}},
			{Name: "action", Type: "string", Always: true, Data: NotPersonal, Meaning: Text{EN: "The action's key.", DE: "Der Schlüssel der Aktion."}},
			{Name: "effect", Type: "string", Always: true, Data: NotPersonal, Meaning: Text{EN: "What the action does to the right.", DE: "Was die Aktion am Recht bewirkt."}},
			{Name: "outcome", Type: "string", Always: true, Data: NotPersonal, Meaning: Text{EN: "How it ended.", DE: "Wie sie endete."}},
			{Name: "source", Type: "string", Always: true, Data: NotPersonal, Meaning: Text{EN: "Where the outcome was reported.", DE: "Wo das Ergebnis gemeldet wurde."}},
			{Name: "principal", Type: "string", Always: true, Data: NotPersonal, Meaning: Text{EN: "Who asked, by principal id.", DE: "Wer die Aktion ausgelöst hat, als Principal-ID."}},
			{Name: "itemId", Type: "string", Always: true, Data: NotPersonal, Meaning: Text{EN: "The product.", DE: "Das Produkt."}},
			{Name: "at", Type: "string", Always: true, Data: NotPersonal, Meaning: Text{EN: "When it ended.", DE: "Wann sie endete."}},
			{Name: "variantId", Type: "string", Data: NotPersonal, Meaning: Text{EN: "The variant.", DE: "Die Variante."}},
			{Name: "instanceKey", Type: "number", Data: NotPersonal, Meaning: Text{EN: "The instance that carried it out.", DE: "Die Instanz, die sie ausgeführt hat."}},
			{Name: "result", Type: "object", Data: NotPersonal, Meaning: Text{EN: "What the process reported back, as the product declares it.", DE: "Was der Prozess zurückmeldet, wie das Produkt es deklariert."}},
			{Name: "homeCatalog", Type: "string", Data: NotPersonal, Meaning: Text{EN: "The catalogue that maintains the product.", DE: "Der Katalog, der das Produkt pflegt."}},
		},
		NeverSecret: "TestTheFeedCarriesNoSecret",
		Since:       Unreleased, Stability: Stable,
		Access: map[Channel]string{Feed: accessFeed},
	},
	{
		Type: UserRequested, Kind: Domain,
		Meaning: Text{
			EN: "Somebody asked for an account, and the request now waits for an administrator to approve it.",
			DE: "Jemand hat ein Konto beantragt; der Antrag wartet nun auf die Freigabe durch die Administration.",
		},
		Moment:   Moment{Places: []Place{At("proc_benutzer_aufnahme", "beantragt_melden")}, Producer: "the intake process, before its approval waits"},
		Channels: []Channel{Signal},
		Payload: []Field{
			{Name: "atlasInstance", Type: "string", Always: true, Data: NotPersonal, Meaning: Text{EN: "The intake instance that emitted it, so a receiver can point back at the request.", DE: "Die Aufnahme-Instanz, die es ausgesendet hat, damit ein Empfänger auf den Antrag verweisen kann."}},
			{Name: "vorname", Type: "string", Always: true, Data: PersonalData, Meaning: Text{EN: "First name.", DE: "Vorname."}},
			{Name: "nachname", Type: "string", Always: true, Data: PersonalData, Meaning: Text{EN: "Last name.", DE: "Nachname."}},
			{Name: "email", Type: "string", Always: true, Data: PersonalData, Meaning: Text{EN: "E-mail address.", DE: "E-Mail-Adresse."}},
			{Name: "benutzername", Type: "string", Always: true, Data: PersonalData, Meaning: Text{EN: "The proposed user name.", DE: "Der vorgeschlagene Benutzername."}},
			{Name: "abteilung", Type: "string", Data: NotPersonal, Meaning: Text{EN: "Department or team.", DE: "Abteilung oder Team."}},
			{Name: "begruendung", Type: "string", Data: PersonalData, Meaning: Text{EN: "Why the account is needed, in the requester's words.", DE: "Begründung, in den Worten der antragstellenden Person."}},
		},
		NeverSecret: "TestSystemIntakeAnnouncesTheRequestAsASignal",
		Since:       Unreleased, Stability: Experimental,
		Access:     map[Channel]string{Signal: accessSignal},
		Listenable: true,
	},
	{
		Type: ApprovalRequested, Kind: Domain,
		Meaning: Text{
			EN: "A position of an order needs an approval, and the approval task now waits for whoever decides it.",
			DE: "Eine Bestellposition braucht eine Genehmigung; die Genehmigungsaufgabe wartet nun auf die Person oder Gruppe, die entscheidet.",
		},
		Moment: Moment{
			Places: []Place{
				At("atlas-genehmigung-fix", "Angefragt"),
				At("atlas-genehmigung-rolle", "Angefragt"),
				At("atlas-genehmigung-vorgesetzter", "Angefragt"),
			},
			Producer: "the shop's three approval processes, before their approval task waits; a server whose service catalogue is switched off runs none of them",
		},
		Channels: []Channel{Signal},
		Payload: []Field{
			{Name: "atlasInstance", Type: "string", Always: true, Data: NotPersonal, Meaning: Text{EN: "The approval instance that emitted it, so a receiver can point back at the request.", DE: "Die Genehmigungsinstanz, die es ausgesendet hat, damit ein Empfänger auf die Anfrage verweisen kann."}},
			{Name: "approvalKind", Type: "string", Always: true, Data: NotPersonal, Meaning: Text{EN: "The product's approval rule: fixed (a person named on the product), role (a group) or superior (the orderer's line manager).", DE: "Die Genehmigungsregel des Produkts: fixed (eine am Produkt genannte Person), role (eine Gruppe) oder superior (die vorgesetzte Person der bestellenden)."}},
			{Name: "approver", Type: "string", Always: true, Data: NotPersonal, Meaning: Text{EN: "Who decides: a principal id for fixed and superior, the group for role.", DE: "Wer entscheidet: eine Principal-ID bei fixed und superior, die Gruppe bei role."}},
			{Name: "orderId", Type: "string", Always: true, Data: NotPersonal, Meaning: Text{EN: "The order.", DE: "Die Bestellung."}},
			{Name: "positionId", Type: "string", Always: true, Data: NotPersonal, Meaning: Text{EN: "The position of the order that waits.", DE: "Die Position der Bestellung, die wartet."}},
			{Name: "itemId", Type: "string", Always: true, Data: NotPersonal, Meaning: Text{EN: "The product.", DE: "Das Produkt."}},
			{Name: "variantId", Type: "string", Always: true, Data: NotPersonal, Meaning: Text{EN: "The variant; empty where the product has none.", DE: "Die Variante; leer, wo das Produkt keine hat."}},
			{Name: "orderer", Type: "string", Always: true, Data: NotPersonal, Meaning: Text{EN: "Who placed the order, by principal id.", DE: "Wer bestellt hat, als Principal-ID."}},
			{Name: "recipient", Type: "string", Always: true, Data: NotPersonal, Meaning: Text{EN: "Who it is for, by principal id.", DE: "Für wen bestellt wurde, als Principal-ID."}},
			{Name: "approvalRef", Type: "string", Always: true, Data: NotPersonal, Meaning: Text{EN: "The approval rule's reference as the product states it: the person for fixed, the group for role.", DE: "Die Referenz der Genehmigungsregel, wie das Produkt sie nennt: die Person bei fixed, die Gruppe bei role."}},
			{Name: "provisionProcess", Type: "string", Always: true, Data: NotPersonal, Meaning: Text{EN: "The process that provisions the position once it is approved.", DE: "Der Prozess, der die Position nach der Genehmigung bereitstellt."}},
			{Name: "atlasApiBase", Type: "string", Always: true, Data: NotPersonal, Meaning: Text{EN: "This server's own API address.", DE: "Die eigene API-Adresse dieses Servers."}},
			{Name: "portalBaseUrl", Type: "string", Always: true, Data: NotPersonal, Meaning: Text{EN: "The external address of the portal; empty where none is set.", DE: "Die externe Adresse des Portals; leer, wo keine gesetzt ist."}},
			{Name: "vorgesetzter", Type: "string", Data: NotPersonal, Meaning: Text{EN: "For superior only: the line manager found in the directory, the same as approver.", DE: "Nur bei superior: die im Verzeichnis gefundene vorgesetzte Person, dieselbe wie approver."}},
		},
		NeverSecret: "TestTheApprovalProcessesAnnounceTheRequestAsASignal",
		Since:       Unreleased, Stability: Experimental,
		Access:     map[Channel]string{Signal: accessSignal},
		Listenable: true,
	},
}
