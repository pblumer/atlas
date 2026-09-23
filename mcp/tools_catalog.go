package mcp

import (
	"encoding/json"
	"net/url"
)

// The portal catalogue tools: what a product manager maintains
// (ADR-0312, ADR-0315) — the products and services on offer, the catalogues that
// offer them, and the release that freezes a version of both so an order has
// something immutable to name.
//
// These are the tools ADR-0376 decided to expose
// after the surface they adapt stopped moving. Each is one HTTP operation and
// nothing more (ADR-0016): the adapter interprets no catalogue, resolves no edge
// and decides no publish — [catalog.Publish] does all of that behind the route,
// so a refusal an agent reads here is the same refusal a person reads in the
// Console.
//
// # Three things the descriptions have to carry, because an agent has no screen
//
// **Saving a product replaces it.** A field left out is a field cleared, which is
// harmless for a form that rendered every field and is the likeliest way for an
// agent to destroy work. So every write tool says to read first, and the product
// tool takes the revision it read.
//
// **Nothing here deletes.** A catalogue has no delete at all and a product is
// withdrawn rather than removed, because an order placed years ago and an
// entitlement still held both resolve through the product. Withdrawal is a state
// on the ordinary save, so there is no tool to look for and not find.
//
// **Nothing is orderable until it is published.** Editing a catalogue changes
// what the *next* release will contain and changes nothing the portal shows.
//
// # Who may call them
//
// The catalogue routes require the `productmanager` role plus editor or owner on
// the catalogue itself, and both are the caller's — the adapter holds no
// credential of its own (ADR-0196). Over the HTTP transport the caller's own
// credential is forwarded, so a signed-in product manager reaches exactly their
// own catalogues. Over stdio the adapter authenticates with an API token, and no
// API token can carry `productmanager` today: tokenRoles gives an admin minter
// the legacy set, which ADR-0315 deliberately kept the new role out of. So the
// read tools work there and the write tools answer 403, which is the honest
// outcome rather than a hole to widen.

// catalogIDArg is the JSON Schema for a tool whose only argument is a catalogue id.
func catalogIDArg(desc string) map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"id": stringProp(desc)},
		"required":   []any{"id"},
	}
}

// catalogRecordProps are the writable fields of a catalogue, shared by create and
// update so the two cannot drift into describing different records.
//
// Every list here is **the complete list**, never an addition: the handler
// replaces the field it is given. That is stated on each one rather than once at
// the top, because a model reading a single property description is the case
// these have to survive.
func catalogRecordProps() map[string]any {
	return map[string]any{
		"texts": objectProp("The catalogue's name per language tag, e.g. " +
			"{\"de\": \"Standardarbeitsplatz\", \"fr\": \"Poste de travail standard\"}."),
		"languages": arrayProp("The language tags this catalogue is offered in, e.g. [\"de\", \"fr\"]. " +
			"Publishing refuses a product missing a text for any of them, so adding a language " +
			"is a commitment every product has to meet. THE COMPLETE LIST — it replaces what is there."),
		"rank": integerProp("Which catalogue a person gets when their groups reach several: the " +
			"HIGHEST rank wins, and a person sees exactly one. Ranks must be unique across " +
			"catalogues and publishing refuses a collision."),
		"groups": arrayProp("The audience: the group ids whose members may see this catalogue. " +
			"EMPTY MEANS NOBODY, not everybody — a catalogue somebody is still filling must not " +
			"be a shop that is already open. THE COMPLETE LIST."),
		"items": arrayProp("The product ids this catalogue OFFERS. A product exists whether or " +
			"not a catalogue offers it, so creating one and forgetting this is the likeliest way " +
			"to lose work: it is stored, and nothing carries it. THE COMPLETE LIST."),
		"edges": arrayProp("How the offered products relate: [{from, to, kind}] with kind one of " +
			"\"composition\" (the part is integral to the whole and always ordered with it), " +
			"\"aggregation\" (the part is optional and separately orderable), " +
			"\"requires\" (from cannot be provisioned before to — the only kind that means \"after\"), " +
			"\"excludes\" (the two must never be held by the same person; symmetric). " +
			"Structure and precedence must both be acyclic or publishing refuses. THE COMPLETE LIST."),
		"members": arrayProp("Who else maintains this catalogue: [{ref: {type: \"user\"|\"group\", id}, " +
			"role: \"viewer\"|\"editor\"}]. Only the OWNER may change this; an editor changing the " +
			"catalogue and its member list would be a grant that amplifies itself. THE COMPLETE LIST."),
		"revision": integerProp("THE REVISION YOU READ, as a precondition. Because `items`, `edges` " +
			"and `members` are replaced whole, the list you send is normally one you read and " +
			"changed — and without this, a change somebody made in between is erased by yours " +
			"with nothing to say it happened. When set, the write is refused as a conflict " +
			"unless the catalogue is still on that revision. Omitting it changes unconditionally, " +
			"which is only right when you are sending values that came from nowhere but you."),
	}
}

// catalogItemProps are the writable fields of a product. The whole record is sent
// on every save, so this list is also the list of fields an incomplete write
// destroys — which is why it is one place and not two.
func catalogItemProps() map[string]any {
	return map[string]any{
		"id": stringProp("The identity, chosen and not minted: a model, an import and a " +
			"catalogue's item list all refer to a product by it. It cannot be renamed later."),
		"homeCatalog": stringProp("The catalogue whose editors may change this product. A product " +
			"is REFERENCED by several catalogues and EDITED through exactly one, so this is not " +
			"\"the catalogue it is in\" — it is who is responsible for it. Moving it needs editor " +
			"on both the old home and the new one."),
		"state": stringProp("\"draft\" (being written, cannot be published), \"active\" (a release " +
			"may carry it), or \"withdrawn\" (no longer orderable, still resolvable forever). " +
			"THERE IS NO DELETE: an order placed years ago and an entitlement still held both " +
			"resolve through this product, so retiring one means setting \"withdrawn\" here."),
		"texts": objectProp("The product's name per language tag. Publishing refuses a product " +
			"missing a text for any language its catalogue declares."),
		"descriptions": objectProp("What the product IS, per language tag, beside the name in " +
			"texts — the sentence somebody reads when the name was not enough. Optional as a " +
			"whole: most products need none, and one with none publishes. Once there is one, " +
			"publishing demands it in EVERY language the catalogue declares, because a product " +
			"explained to one audience and not another leaves the other an empty panel. " +
			"Whitespace does not count as one."),
		"approval": objectProp("How an order line for this product is approved: {kind, ref}. " +
			"kind is \"none\" (ordered without approval), \"fixed\" (ref names one principal), " +
			"\"role\" (ref names a group; any member may approve), \"superior\" (the RECIPIENT's " +
			"line manager, resolved from the directory), or the name of a registered approval " +
			"process. Publishing refuses a kind that needs a ref and has none."),
		"provisionProcess": stringProp("The BPMN process id that grants this product. It must " +
			"already be deployed — choosing a process is not deploying one, and a product manager " +
			"is deliberately not a modeller. REQUIRED to publish."),
		"deprovisionProcess": stringProp("The BPMN process id that revokes it. REQUIRED to " +
			"publish, and the requirement is the point: a catalogue that can only grant is not a " +
			"lifecycle, and the day somebody must revoke at scale is the wrong day to find out " +
			"the process was never written."),
		"lifecycle": objectProp("The window in which it may be ordered: {from, until} as Unix " +
			"nanoseconds. Zero on a side means unbounded there, which is the ordinary case. " +
			"BOTH ENDS ARE INCLUSIVE AND IT IS ENFORCED: an order placed outside the window is " +
			"refused with 403, naming the product and the date. It is authored as days — the " +
			"Console stores the END of the last one, so a product orderable \"until the 31st\" " +
			"is orderable on the 31st, and a value you compute yourself should do the same. It " +
			"governs ordering only: a right already held when the window closes keeps running, " +
			"and when a right ends is `maxDays`."),
		"variants": arrayProp("The orderable shapes of this product: [{id, texts}] — a laptop's " +
			"size, a licence tier. UNORDERED on purpose: \"higher\" is meaningful for a tier and " +
			"meaningless for Windows against Linux, so the basket asks the orderer."),
		"multipleAllowed": map[string]any{"type": "boolean",
			"description": "Whether one person may hold this more than once — two licences, two " +
				"mailboxes. Where false, the basket marks it as already held and skips it."},
		"targets": arrayProp("What this product is called in the systems that actually hold it: " +
			"[{system, ref}] — {\"system\": \"ad\", \"ref\": \"CN=VPN-Users,...\"}. It is what lets a " +
			"commissioning load attribute a right it FOUND to this product, so it is a claim to be " +
			"checked, not the provisioning process. Compared literally, case and all. The same ref " +
			"on two products is refused at publish: an observation matching both cannot be attributed."),
		"eligible": arrayProp("Group ids whose members may RECEIVE this product, narrowing the " +
			"catalogue's audience for this one product. Empty is the ordinary case and narrows " +
			"nothing — it never opens anything, because the catalogue's audience stays the gate. " +
			"The RECIPIENT is checked, never the orderer: a manager ordering for a new hire must " +
			"keep working."),
		"maxDays": integerProp("How long a right this product grants may last, in days, or 0 for " +
			"one that does not end. A CEILING DECLARED AS POLICY — \"nobody holds this for more " +
			"than ninety days\" — not a date somebody chose. It never applies to a right found by " +
			"a commissioning load."),
		"keywords": arrayProp("Words somebody might search for that are NOT the product's name: " +
			"synonyms, the vendor's term, the abbreviation everybody uses, the thing it replaced. " +
			"One flat list and not one per language, because a synonym list is for finding and a " +
			"searcher's language is not the catalogue's."),
		"configForm": stringProp("The id of an Atlas form the orderer fills in for this product — " +
			"a cost centre, a site — whose answers travel with the order line. The catalogue names " +
			"the id and never resolves it, so an id that exists is the caller's to verify."),
		"price": stringProp("What it costs, written as the catalogue wants it read: \"CHF 1'200.–\", " +
			"\"49.– / Monat\", \"im Grundpaket enthalten\". DISPLAYED AND NEVER COMPUTED — nothing " +
			"adds these up. It is frozen into the release and copied onto the order line, so an " +
			"approver's figure stays the figure they decided on."),
		"category": stringProp("The heading the portal groups it under — \"Arbeitsplatz\", " +
			"\"Kommunikation\". THE KEY AND NOT THE WORDING: the portal groups by this value and " +
			"renders `categoryTexts` beside it, so two spellings are two categories. Reuse a " +
			"heading already in the catalogue rather than inventing a second spelling of it. A " +
			"heading and nothing else: no ordering and no entity behind it. It is read off the " +
			"products NOTHING CONTAINS: on a product that is a part of another one the portal " +
			"never reads it, so set it on the offering and not on the services behind it."),
		"categoryTexts": objectProp("The heading per language tag, where `category` above is what " +
			"the portal groups by: {\"de\": \"Arbeitsplatz\", \"fr\": \"Poste de travail\"}. " +
			"Leave it out and the key renders in every language, which is the ordinary state for " +
			"a catalogue declaring one. Optional as a whole and ALL-OR-NOTHING once there is one: " +
			"publishing refuses a heading translated into one declared language and not another, " +
			"because the fallback renders and one audience silently reads somebody else's column " +
			"head. Translations without a `category` are refused too — nothing would read them."),
		"productGroup": stringProp("The group one level below the heading: the portal's cascade " +
			"reads Kategorie > Produktgruppe > Produkt > Services. The key, exactly as `category` " +
			"above is, with `productGroupTexts` for the wording, and read off the same products — " +
			"the group has no record and therefore no category of its own, so the chain is " +
			"assembled per product and a group whose products sit in two categories appears " +
			"under both."),
		"productGroupTexts": objectProp("The product group per language tag, exactly as " +
			"`categoryTexts` is the heading's — same key-and-wording split, same all-or-nothing " +
			"rule at publication. See that property."),
		"createdAt": integerProp("When the product was first stored. Send back what you read: the " +
			"write is a replace, so omitting it resets the creation date to now."),
		"revision": integerProp("THE REVISION YOU READ, as a precondition. When set, the write is " +
			"refused with a conflict unless the stored product is still on it — which is what " +
			"makes a read-modify-write safe against a second maintainer, who would otherwise be " +
			"overwritten with nothing to say they existed. Omitting it replaces unconditionally, " +
			"which is only right when you are creating the product. Note that this is the " +
			"`revision` and NOT the `updatedAt` beside it: that one is Unix nanoseconds, a number " +
			"too large for a JSON client to hand back unchanged."),
	}
}

// catalogBody marshals exactly the declared properties a caller supplied, so a
// tool forwards the fields its schema advertises and nothing a caller invented.
func catalogBody(args map[string]any, props map[string]any) []byte {
	body, _ := json.Marshal(recordFromArgs(args, props))
	return body
}

// withID returns the catalogue-scoped path for a tool that names one, escaping
// the id so a caller cannot reach a sibling route by supplying a path.
func withID(id, suffix string) string {
	return "/api/v1/catalogs/" + url.PathEscape(id) + suffix
}

func catalogTools() []Tool {
	return []Tool{
		{
			Name: "atlas_list_catalogs",
			Description: "List the product catalogues you maintain, lowest rank first. This is the " +
				"MAINTENANCE list and not the portal one: being the audience for a catalogue puts " +
				"nothing in it, so a customer never learns which other customers exist. Read this " +
				"before changing anything — a catalogue's id is what every other catalogue tool names.",
			InputSchema: noArgs(),
			Handler: func(c *Client, _ map[string]any) (string, error) {
				return asText(c.get("/api/v1/catalogs"))
			},
		},
		{
			Name: "atlas_get_catalog",
			Description: "Read one product catalogue whole: its texts, languages, rank, audience " +
				"groups, the product ids it offers, the edges between them, and who maintains it. " +
				"A catalogue you may not see reads as absent, so a 404 does not tell you whether " +
				"it exists.",
			InputSchema: catalogIDArg("The catalogue id, as atlas_list_catalogs returns it."),
			Handler: func(c *Client, args map[string]any) (string, error) {
				id, err := argString(args, "id")
				if err != nil {
					return "", err
				}
				return asText(c.get(withID(id, "")))
			},
		},
		{
			Name: "atlas_create_catalog",
			Description: "Create a product catalogue. You become its owner. NOTHING IS ORDERABLE " +
				"UNTIL IT IS PUBLISHED — this creates the thing that is edited, and " +
				"atlas_publish_catalog creates the thing that is ordered from. Note two defaults " +
				"that are the opposite of the friendly guess: an empty `groups` means NOBODY may " +
				"see it, and a product is offered only once its id is in `items`. A catalogue " +
				"cannot be deleted afterwards, so create one when you mean to.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": catalogRecordProps(),
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				return asText(c.post("/api/v1/catalogs", "application/json",
					catalogBody(args, catalogRecordProps())))
			},
		},
		{
			Name: "atlas_update_catalog",
			Description: "Change a catalogue: what it offers, the edges between its products, its " +
				"languages, rank, audience and maintainers. Only the fields you send are changed, " +
				"but each one you do send REPLACES that field whole — sending `items` with one id " +
				"drops every other product the catalogue offered. Read it with atlas_get_catalog " +
				"first, send the full list back, and pass the `revision` it answered with — the " +
				"write is then refused rather than erasing a change somebody made in between. Changing it changes nothing the portal shows " +
				"until you publish. Requires editor on the catalogue; only its owner may change " +
				"`members`.",
			InputSchema: func() map[string]any {
				props := catalogRecordProps()
				props["id"] = stringProp("The catalogue to change.")
				return map[string]any{"type": "object", "properties": props, "required": []any{"id"}}
			}(),
			Handler: func(c *Client, args map[string]any) (string, error) {
				id, err := argString(args, "id")
				if err != nil {
					return "", err
				}
				return asText(c.patch(withID(id, ""), "application/json",
					catalogBody(args, catalogRecordProps())))
			},
		},
		{
			Name: "atlas_list_catalog_products",
			Description: "List the products and services whose home catalogue you maintain, with " +
				"every field: texts, state, approval rule, process bindings, targets, eligibility, " +
				"price, the two headings and their wording per language, keywords, and the " +
				"`revision` each is on. READ THIS " +
				"BEFORE EVERY CHANGE — atlas_save_catalog_product replaces the whole record, so " +
				"this is where the fields you are not changing come from.",
			InputSchema: noArgs(),
			Handler: func(c *Client, _ map[string]any) (string, error) {
				return asText(c.get("/api/v1/catalog-products"))
			},
		},
		{
			Name: "atlas_catalog_approver_report",
			Description: "Which of the products you maintain name an approver that reaches " +
				"nobody. An approval rule's reference becomes a task's assignee (a named " +
				"person, matched by username) or its candidate groups (a group, matched by id " +
				"or name); neither is checked when the rule is written, and neither failure is " +
				"reported when it fires — the approval is created, lands in no inbox, and the " +
				"order waits. Run this before publishing: a rule that reaches nobody is not a " +
				"publish error, so nothing else will tell you. It stays silent about a rule " +
				"that still reaches somebody, about a kind that names an approval process " +
				"directly, and about a leftover reference beside a kind that needs none.",
			InputSchema: noArgs(),
			Handler: func(c *Client, _ map[string]any) (string, error) {
				return asText(c.get("/api/v1/catalog-products/approver-report"))
			},
		},
		{
			Name: "atlas_catalog_fulfilment_report",
			Description: "Which of the services you offer cannot be fulfilled on this " +
				"installation. A catalogue binds a product to processes by name, and nothing " +
				"checks those names — not when the binding is written, not when the catalogue " +
				"is published, not when an order reaches one. Two ways it fails: the process " +
				"was never deployed, or it is deployed and waits on a job type nothing works. " +
				"THE SECOND RAISES NO INCIDENT AT ALL: a parked token is work waiting, not " +
				"work failed, so no retry is spent, nothing turns red, and the order simply " +
				"stands at waiting. This is the only place it is said. It walks the whole path " +
				"an order takes — the fulfilment orchestration, the approval process where the " +
				"rule needs one, then provisioning and the return — because any of them stops " +
				"it, and it reads the newest release of each catalogue you maintain, since " +
				"that is what can be ordered today. A problem on the shared orchestration is " +
				"reported once and names no product: it is one fact about the installation and " +
				"stops every order alike. Run it beside atlas_catalog_approver_report, which " +
				"asks the other half — that one whether the rule reaches a person, this one " +
				"whether the work reaches a worker.",
			InputSchema: noArgs(),
			Handler: func(c *Client, _ map[string]any) (string, error) {
				return asText(c.get("/api/v1/catalog-products/fulfilment-report"))
			},
		},
		{
			Name: "atlas_catalog_translation_gaps",
			Description: "Where the catalogues you maintain are written in one of their " +
				"declared languages and not another: the name, the description and the two " +
				"headings, per product and per language. Publishing used to refuse these and " +
				"does not any more — the portal falls back to the language the catalogue has, " +
				"so the refusal protected no reader and instead held a usable catalogue back " +
				"until the last translation arrived. What the refusal did do is make the gap " +
				"impossible to ignore, and this is that half kept. READ THIS BEFORE TELLING " +
				"SOMEBODY A CATALOGUE IS FINISHED: a gap here is invisible in the portal, " +
				"because a reader is shown the language that exists and nothing says it was " +
				"not the one they asked for. It reads the catalogues AS THEY STAND rather " +
				"than their releases, unlike the two reports beside it, because it is a list " +
				"of work to do — computed from the same input a publish is, so it says " +
				"exactly what the next publish would have to live with. A product named in no " +
				"language at all is not here: that one is still refused at publish.",
			InputSchema: noArgs(),
			Handler: func(c *Client, _ map[string]any) (string, error) {
				return asText(c.get("/api/v1/catalog-products/translation-gaps"))
			},
		},
		{
			Name: "atlas_save_catalog_product",
			Description: "Create or change one product or service. THIS IS A FULL REPLACE: every " +
				"field you leave out is CLEARED, including translations, variants, keywords, " +
				"eligibility, the product group and the deprovisioning binding. To change a " +
				"product, call " +
				"atlas_list_catalog_products, take the record, change what you mean to change, " +
				"send the whole thing back, and pass the `revision` you read — the write is then " +
				"refused with 409 if somebody else changed it meanwhile, instead of silently " +
				"overwriting them. Omit `revision` only when creating. " +
				"TO RETIRE A PRODUCT, SAVE IT WITH state=\"withdrawn\": there is no delete, because " +
				"an order placed years ago and an entitlement still held both resolve through it. " +
				"A product is stored whether or not any catalogue offers it, so add its id to the " +
				"home catalogue's `items` with atlas_update_catalog, and publish before anybody " +
				"can order it.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": catalogItemProps(),
				"required":   []any{"id", "homeCatalog"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				if _, err := argString(args, "id"); err != nil {
					return "", err
				}
				if _, err := argString(args, "homeCatalog"); err != nil {
					return "", err
				}
				return asText(c.post("/api/v1/catalog-products", "application/json",
					catalogBody(args, catalogItemProps())))
			},
		},
		{
			Name: "atlas_publish_catalog",
			Description: "Publish a catalogue: validate it and freeze a release, which is the " +
				"immutable thing an order names. Until a catalogue has one, the portal shows it to " +
				"nobody; after one, editing the catalogue changes the NEXT release and not what is " +
				"being ordered today. A refused publish WRITES NOTHING and answers with every " +
				"problem at once (422) — each naming the catalogue or the product it belongs to — " +
				"so fix them together rather than one per attempt. It refuses, among others: a " +
				"product still in draft, a missing text for a declared language, an unresolved or " +
				"absent provisioning or deprovisioning binding, an approval rule needing a ref and " +
				"having none, a cycle in the structure or precedence edges, two catalogues at the " +
				"same rank, and the same target ref on two products.",
			InputSchema: catalogIDArg("The catalogue to publish."),
			Handler: func(c *Client, args map[string]any) (string, error) {
				id, err := argString(args, "id")
				if err != nil {
					return "", err
				}
				// No body: a publish is the catalogue as it stands, not an argument.
				return asText(c.post(withID(id, "/releases"), "", nil))
			},
		},
		{
			Name: "atlas_catalog_releases",
			Description: "List a catalogue's releases, newest first. A release is frozen: it " +
				"carries the products, edges, approval rules, ceilings and prices as they stood " +
				"when it was published, which is what lets an order placed last month stay " +
				"answerable after this month's edits. Read this to see what the portal is actually " +
				"offering, as opposed to what the catalogue currently says.",
			InputSchema: catalogIDArg("The catalogue whose releases to list."),
			Handler: func(c *Client, args map[string]any) (string, error) {
				id, err := argString(args, "id")
				if err != nil {
					return "", err
				}
				return asText(c.get(withID(id, "/releases")))
			},
		},
		{
			Name: "atlas_catalog_unpublished",
			Description: "What publishing this catalogue would change for the people ordering: " +
				"products it would ADD, products the portal is STILL OFFERING that it would take " +
				"away, and products EDITED since the release being served. Ask it before and " +
				"after editing a catalogue. The second group is the one worth the call: a " +
				"product dropped from a catalogue is gone from every listing at once and the " +
				"release goes on offering it, so nothing else you can read says it is still on " +
				"the portal. \"Edited\" is decided on the record's revision and not on a " +
				"comparison of fields, so a save that changed nothing still counts — the remedy " +
				"is atlas_publish_catalog, which loses nothing. A product offered here but homed " +
				"in a catalogue you do not maintain is never reported as edited, because you " +
				"cannot read it to compare.",
			InputSchema: catalogIDArg("The catalogue to compare against its newest release."),
			Handler: func(c *Client, args map[string]any) (string, error) {
				id, err := argString(args, "id")
				if err != nil {
					return "", err
				}
				return asText(c.get(withID(id, "/unpublished")))
			},
		},
		{
			Name: "atlas_import_catalog_archimate",
			Description: "Derive catalogue drafts from an ArchiMate Open Exchange model: Products " +
				"and Business Services become products, compositions become integral parts and " +
				"aggregations optional ones. Everything arrives as a DRAFT and nothing becomes " +
				"orderable — the bindings, approval rules and texts still have to be filled in " +
				"with atlas_save_catalog_product. A product already stored is LEFT AS IT IS and " +
				"reported as skipped: the model is where a catalogue starts, not something it " +
				"follows, so a second import cannot undo work somebody did after the first.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":  stringProp("The catalogue to import into. It must already exist."),
					"xml": stringProp("The full ArchiMate Open Exchange XML document."),
				},
				"required": []any{"id", "xml"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				id, err := argString(args, "id")
				if err != nil {
					return "", err
				}
				model, err := argString(args, "xml")
				if err != nil {
					return "", err
				}
				return asText(c.post(withID(id, "/import"), "application/xml", []byte(model)))
			},
		},
	}
}
