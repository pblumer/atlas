package mcp

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

// The shop's customer side (ADR-0550): what a person does in
// the portal — read the catalogues they may order from, order, follow their orders,
// decide the approvals they hold, withdraw, correct, return and change what they
// ordered.
//
// # Whose act it is
//
// Every one of these acts is the caller's own. The adapter holds no credential
// (ADR-0196): over the /mcp transport it forwards the credential the person
// connected with — their OAuth grant (ADR-0200) or their session — and over stdio
// the API token the adapter was started with. The handler then decides exactly as
// it decides for the portal, and the order, the decision or the withdrawal records
// that person's name. So an agent can only do in the shop what the person who
// connected it could do there themselves, and the record says it was them.
//
// # The switch
//
// It is an opt-out: a server started with --mcp-shop=false refuses these routes to a
// request that came through MCP, with 403 and a sentence naming the setting, and
// keeps serving the portal. The server holds that line, not this adapter, so the
// tools stay listed and their descriptions say a server may have switched them off.
//
// # What the descriptions carry, because an agent has no screen
//
// What the body needs, that ordering, returning and changing reach target systems,
// which acts take an idempotency key and which do not, and what a refusal means.

// shopNote ends every description in this file: whose act it is and the switch.
const shopNote = " Acts as YOU — the person whose credential this MCP connection carries — with " +
	"exactly your rights in the shop, and is recorded under your name. A server may have " +
	"switched the shop off for agents (--mcp-shop=false): it then answers 403 naming that " +
	"setting, and the person can still use the shop in a browser."

// orderPath is the route of one order, with an optional suffix.
func orderPath(id, suffix string) string {
	return "/api/v1/orders/" + url.PathEscape(id) + suffix
}

// orderLinePath is the route of one position of an order, with an optional suffix.
func orderLinePath(id, item, suffix string) string {
	return orderPath(id, "/lines/"+url.PathEscape(item)+suffix)
}

// orderAndItem reads the two arguments every position tool takes.
func orderAndItem(args map[string]any) (string, string, error) {
	id, err := argString(args, "orderId")
	if err != nil {
		return "", "", err
	}
	item, err := argString(args, "item")
	if err != nil {
		return "", "", err
	}
	return id, item, nil
}

// jsonPost posts body as JSON.
func jsonPost(c *Client, path string, body map[string]any) (string, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	return asText(c.post(path, "application/json", raw))
}

// withReason adds the optional reason argument to a body.
func withReason(body, args map[string]any) map[string]any {
	if reason := optString(args, "reason"); reason != "" {
		body["reason"] = reason
	}
	return body
}

var (
	orderIDProp  = stringProp("The order id, as atlas_list_orders or atlas_place_order answered it.")
	positionProp = stringProp("The position: the product (item) id, or itemId#variantId where one " +
		"order carries the product in two shapes.")
	reasonProp = stringProp("Why, in the person's words; it is kept with the order.")
)

// positionArgs is the schema of a tool that takes an order and a position, plus extra.
func positionArgs(extra map[string]any, required ...string) map[string]any {
	props := map[string]any{"orderId": orderIDProp, "item": positionProp}
	for k, v := range extra {
		props[k] = v
	}
	req := []any{"orderId", "item"}
	for _, r := range required {
		req = append(req, r)
	}
	return map[string]any{"type": "object", "properties": props, "required": req}
}

func shopTools() []Tool {
	return markCatalogue([]Tool{
		{
			Name: "atlas_shop_catalogs",
			Description: "The catalogues YOU may order from, highest rank first (ADR-0522), " +
				"answered from your own groups; an empty list means you are the audience of " +
				"none. To see what is on offer, read each catalogue's newest release with " +
				"atlas_catalog_releases {id, limit: 1}: the release's id is the releaseId an " +
				"order names and its items are what can be ordered — the catalogue itself is " +
				"what the next release will contain. A product in several catalogues is offered " +
				"from the highest-ranked, and one order names one release, so a basket from two " +
				"catalogues is placed as two orders. READ-ONLY." + shopNote,
			InputSchema: noArgs(),
			Handler: func(c *Client, _ map[string]any) (string, error) {
				return asText(c.get("/api/v1/shop/catalogs"))
			},
		},
		{
			Name: "atlas_shop_catalog",
			Description: "The one catalogue assigned to YOU: the highest-ranked your groups " +
				"reach (404 when none is). atlas_shop_catalogs answers all of them and is " +
				"what the shop reads. READ-ONLY." + shopNote,
			InputSchema: noArgs(),
			Handler: func(c *Client, _ map[string]any) (string, error) {
				return asText(c.get("/api/v1/shop/catalog"))
			},
		},
		{
			Name: "atlas_shop_form_query",
			Description: "Search the directory behind one field of a product's order form — " +
				"a person picker, a value picker — while you fill in an order (ADR-0473): one " +
				"bounded page of options from the Worker the form names. Use it to find the " +
				"value a field expects before atlas_place_order: a person chosen in such a " +
				"field is looked up again when the order is placed or corrected, so a value " +
				"this search would not have offered is refused there. `value` resolves one " +
				"known value instead of searching; `filters` carries the values of the form " +
				"fields the source is bound to; pass the `cursor` an answer gave for the next " +
				"page. READ-ONLY, though it asks a target system." + shopNote,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"releaseId": stringProp("The release you are ordering from."),
					"item":      stringProp("The product whose order form carries the field."),
					"field":     stringProp("The form field's key."),
					"search":    stringProp("What to search for."),
					"filters":   map[string]any{"type": "object", "description": "Values of the form fields the source is bound to, by field key."},
					"cursor":    stringProp("The cursor of the next page, from a previous answer."),
					"value":     stringProp("One value to resolve instead of searching."),
					"limit":     map[string]any{"type": "integer", "description": "Page size."},
				},
				"required": []any{"releaseId", "item", "field"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				release, err := argString(args, "releaseId")
				if err != nil {
					return "", err
				}
				item, err := argString(args, "item")
				if err != nil {
					return "", err
				}
				field, err := argString(args, "field")
				if err != nil {
					return "", err
				}
				body := map[string]any{}
				for _, k := range []string{"search", "cursor", "value"} {
					if v := optString(args, k); v != "" {
						body[k] = v
					}
				}
				if f, ok := args["filters"].(map[string]any); ok && len(f) > 0 {
					body["filters"] = f
				}
				if n, ok, err := optPositiveUint(args, "limit"); err != nil {
					return "", err
				} else if ok {
					body["limit"] = n
				}
				return jsonPost(c, "/api/v1/shop/releases/"+url.PathEscape(release)+"/items/"+
					url.PathEscape(item)+"/form-data/"+url.PathEscape(field)+"/query", body)
			},
		},
		{
			Name: "atlas_place_order",
			Description: "PLACE AN ORDER against one catalogue release, as the shop's basket does. " +
				"THIS IS REAL: the order starts its approvals and then its provisioning, which " +
				"reaches TARGET SYSTEMS (accounts, licences, devices). There is NO idempotency " +
				"key — every call places a NEW order, so after an error or a timeout read " +
				"atlas_list_orders before you try again. `releaseId` is the newest release's id " +
				"(atlas_catalog_releases {id, limit: 1}); `items` the product ids chosen, and " +
				"everything they are made of comes with them. A product that comes in more than " +
				"one shape needs `variants`: {itemId: [variantId, ...]}, one entry per position, " +
				"including where it arrived as an integral part and was never named in items; " +
				"two shapes order it twice, refused unless the catalogue allows holding it more " +
				"than once. `config` carries the answers to each product's order form: " +
				"{itemId: {fieldKey: value}} — strings. `recipient` orders for somebody else, " +
				"which needs the operator role; it defaults to you. Answers the placed order " +
				"(201). A refusal says why in the shop's own words: 403 you may not order from " +
				"that catalogue or for that person, 404 no such release, 400/409 the basket " +
				"breaks a rule of the release (a missing variant, a product already held, an " +
				"answer the form or its directory does not accept)." + shopNote,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"releaseId": stringProp("The release to order from."),
					"items": map[string]any{
						"type": "array", "items": map[string]any{"type": "string"},
						"description": "The product ids chosen.",
					},
					"variants": map[string]any{
						"type":        "object",
						"description": "Shapes per product with more than one: {itemId: [variantId, ...]}.",
					},
					"config": map[string]any{
						"type":        "object",
						"description": "Order-form answers per product: {itemId: {fieldKey: value}}.",
					},
					"recipient": stringProp("Whom the order is for (needs the operator role); defaults to you."),
				},
				"required": []any{"releaseId", "items"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				release, err := argString(args, "releaseId")
				if err != nil {
					return "", err
				}
				items := stringList(args["items"])
				if len(items) == 0 {
					return "", fmt.Errorf("argument \"items\" must name at least one product id")
				}
				body := map[string]any{"releaseId": release, "items": items}
				if r := optString(args, "recipient"); r != "" {
					body["recipient"] = r
				}
				for _, k := range []string{"variants", "config"} {
					if v, ok := args[k].(map[string]any); ok && len(v) > 0 {
						body[k] = v
					}
				}
				return jsonPost(c, "/api/v1/orders", body)
			},
		},
		{
			Name: "atlas_list_orders",
			Description: "YOUR orders — those you placed and those placed for you — newest " +
				"first, each with the status of every line (pending, blocked, running, done — held " +
				"—, skipped, failed, rejected, cancelled, returning, returned …). Read it after atlas_place_order fails " +
				"or times out, before placing again. READ-ONLY." + shopNote,
			InputSchema: noArgs(),
			Handler: func(c *Client, _ map[string]any) (string, error) {
				return asText(c.get("/api/v1/orders"))
			},
		},
		{
			Name: "atlas_get_order",
			Description: "One of YOUR orders, with the status of every line. An order that is " +
				"neither yours nor placed for you answers 404, never 403: whose orders exist is " +
				"not something the shop confirms. READ-ONLY." + shopNote,
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"orderId": orderIDProp},
				"required":   []any{"orderId"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				id, err := argString(args, "orderId")
				if err != nil {
					return "", err
				}
				return asText(c.get(orderPath(id, "")))
			},
		},
		{
			Name: "atlas_order_ready_lines",
			Description: "Which of an order's lines may be started now — those still waiting " +
				"whose preconditions are all provisioned — each with the process that " +
				"provisions it and the variant chosen, without the order-form answers. This is " +
				"the fulfilment orchestrator's read and needs the OPERATOR role; a customer " +
				"follows an order with atlas_get_order and atlas_order_line_progress. READ-ONLY." +
				shopNote,
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"orderId": orderIDProp},
				"required":   []any{"orderId"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				id, err := argString(args, "orderId")
				if err != nil {
					return "", err
				}
				return asText(c.get(orderPath(id, "/next")))
			},
		},
		{
			Name: "atlas_shop_tasks",
			Description: "The open tasks of YOUR orders and of the orders you hold a task in " +
				"(ADR-0416) — an approval waiting for your manager, a step waiting for a group " +
				"in IT, or an approval YOU are to decide: each with the order and position it " +
				"works, its name and form, whom it waits for and whether you may answer it. " +
				"The orders you hold a task in are under `orders`, without their form answers; " +
				"`truncated` says the walk of open tasks was cut short. To answer a task you " +
				"hold, use atlas_get_task and atlas_complete_task with its key, or — for " +
				"approvals — atlas_decide_approvals. READ-ONLY." + shopNote,
			InputSchema: noArgs(),
			Handler: func(c *Client, _ map[string]any) (string, error) {
				return asText(c.get("/api/v1/shop/tasks"))
			},
		},
		{
			Name: "atlas_order_line_progress",
			Description: "Where one position of YOUR order stands: the steps the process working " +
				"on it is sitting on right now, by the names its model gives them, and no " +
				"process variable. state \"none\" means nothing is running for it. Somebody " +
				"else's order answers 404. READ-ONLY." + shopNote,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"orderId":  orderIDProp,
					"position": positionProp,
				},
				"required": []any{"orderId", "position"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				id, err := argString(args, "orderId")
				if err != nil {
					return "", err
				}
				pos, err := argString(args, "position")
				if err != nil {
					return "", err
				}
				return asText(c.get("/api/v1/shop/orders/" + url.PathEscape(id) + "/lines/" +
					url.PathEscape(pos) + "/progress"))
			},
		},
		{
			Name: "atlas_cancel_order",
			Description: "WITHDRAW an order: everything in it that has not happened yet. Yours " +
				"for an order you placed (not one merely placed for you), an operator's for any. " +
				"Approvals still open are cancelled with their lines, and so is every process " +
				"working a withdrawn line — what such a process already did in a target system " +
				"is NOT undone. A line already running or finished keeps its outcome: the " +
				"answer names what was withdrawn (`cancelled`) and what was not (`kept`) — tell " +
				"the person about `kept`. Undoing a provisioned line is atlas_return_order_line. " +
				"Repeating it withdraws nothing more." + shopNote,
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"orderId": orderIDProp, "reason": reasonProp},
				"required":   []any{"orderId"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				id, err := argString(args, "orderId")
				if err != nil {
					return "", err
				}
				return jsonPost(c, orderPath(id, "/cancel"), withReason(map[string]any{}, args))
			},
		},
		{
			Name: "atlas_cancel_order_line",
			Description: "WITHDRAW one position of an order rather than the whole order; who may " +
				"is who may withdraw the order. 409 when the position is already running or " +
				"finished, or when its whole always carries it — the refusal names what to " +
				"withdraw instead. Its open approval and processes are cancelled with it, " +
				"without undoing what they already did." + shopNote,
			InputSchema: positionArgs(map[string]any{"reason": reasonProp}),
			Handler: func(c *Client, args map[string]any) (string, error) {
				id, item, err := orderAndItem(args)
				if err != nil {
					return "", err
				}
				return jsonPost(c, orderLinePath(id, item, "/cancel"), withReason(map[string]any{}, args))
			},
		},
		{
			Name: "atlas_amend_order_line",
			Description: "CORRECT the order-form answers given for one position — a wrong cost " +
				"centre, a misspelt name. What is HELD is never changed in place: another product " +
				"is a return and a new order. A position not yet attempted is simply corrected; " +
				"one already held keeps the correction beside the old answers with who and " +
				"when, and the target system is NOT changed by it; one being provisioned now " +
				"is refused (409) until its process has finished. `config` is {fieldKey: value} " +
				"for this one product, strings, held to the form as a placed order is (a person " +
				"field is looked up again). Who may is who may withdraw the order." + shopNote,
			InputSchema: positionArgs(map[string]any{
				"config": map[string]any{"type": "object", "description": "The corrected answers: {fieldKey: value}."},
				"reason": reasonProp,
			}, "config"),
			Handler: func(c *Client, args map[string]any) (string, error) {
				id, item, err := orderAndItem(args)
				if err != nil {
					return "", err
				}
				cfg, ok := args["config"].(map[string]any)
				if !ok || len(cfg) == 0 {
					return "", fmt.Errorf("missing required argument: config")
				}
				return jsonPost(c, orderLinePath(id, item, "/details"), withReason(map[string]any{"config": cfg}, args))
			},
		},
		{
			Name: "atlas_return_order_line",
			Description: "GIVE BACK one provisioned position: starts the deprovisioning the order " +
				"froze when it was placed, which REVOKES THE ACCESS IN THE TARGET SYSTEM — an " +
				"account somebody is using, a licence, a mailbox. Only call it when the person " +
				"asked to give this position back. Who may: the triggers of the product's return " +
				"— normally whoever placed the order or holds it, or an operator. 409 while " +
				"something still held requires it, or when the line is not held; 404 for " +
				"somebody else's order. Answers the order and the process now revoking the line; " +
				"follow it with atlas_order_line_progress and atlas_order_line_outcomes." + shopNote,
			InputSchema: positionArgs(nil),
			Handler: func(c *Client, args map[string]any) (string, error) {
				id, item, err := orderAndItem(args)
				if err != nil {
					return "", err
				}
				return asText(c.post(orderLinePath(id, item, "/return"), "application/json", nil))
			},
		},
		{
			Name: "atlas_change_order_line",
			Description: "CHANGE one held position whose product runs one lifecycle instance per " +
				"position (ADR-0428) — a larger mailbox, a new cost centre: the product's change " +
				"is delivered to the instance that carries the right, which ACTS ON THE TARGET " +
				"SYSTEM. It is the action keyed `change`; atlas_order_line_actions says whether " +
				"the position takes it now and which form its `variables` follow. `changeId` is " +
				"required and makes a retry answer with the first delivery instead of changing " +
				"twice: reuse it when you retry, choose a new one only for a new request. 409 " +
				"when the position is not per-position, not held, binds no change, or its " +
				"instance is not waiting for a change now; 403 when the product's change is not " +
				"yours to ask." + shopNote,
			InputSchema: positionArgs(map[string]any{
				"changeId":  stringProp("Idempotency id for this change."),
				"reason":    reasonProp,
				"variables": map[string]any{"type": "object", "description": "What the change needs."},
			}, "changeId"),
			Handler: func(c *Client, args map[string]any) (string, error) {
				id, item, err := orderAndItem(args)
				if err != nil {
					return "", err
				}
				changeID, err := argString(args, "changeId")
				if err != nil {
					return "", err
				}
				body := withReason(map[string]any{"changeId": changeID}, args)
				if vars, ok := args["variables"].(map[string]any); ok && len(vars) > 0 {
					body["variables"] = vars
				}
				return jsonPost(c, orderLinePath(id, item, "/change"), body)
			},
		},
		{
			Name: "atlas_list_approvals",
			Description: "The open approvals addressed to YOU as an approver, newest first: each " +
				"with its task key, the order line it decides, the product as the release froze " +
				"it, the orderer's form answers and whether they were corrected. Paged: pass " +
				"`before` = the `nextCursor` of the previous page. Decide them with " +
				"atlas_decide_approvals. READ-ONLY." + shopNote,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"before": map[string]any{"type": "integer", "description": "The nextCursor of the previous page."},
				},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				path := "/api/v1/approvals"
				if n, ok, err := optPositiveUint(args, "before"); err != nil {
					return "", err
				} else if ok {
					path += "?before=" + strconv.FormatUint(n, 10)
				}
				return asText(c.get(path))
			},
		},
		{
			Name: "atlas_decide_approvals",
			Description: "DECIDE approvals YOU hold, as the approver, with one decision and one " +
				"reason — the shop's Approve / Refuse. `approved` true approves, which lets the " +
				"lines go on to be PROVISIONED IN TARGET SYSTEMS; false refuses and needs a " +
				"`reason`, which the orderer reads. `taskKeys` (from atlas_list_approvals or " +
				"atlas_shop_tasks) must all belong to ONE order, at most 100. Decide only what " +
				"the person decided. There is no transaction across lines: the answer is per " +
				"line, `decided` and `skipped` with why (a task no longer open, not yours) — a " +
				"skipped key was not decided, and repeating a decided one decides nothing again." +
				shopNote,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"approved": map[string]any{"type": "boolean", "description": "true approves, false refuses."},
					"reason":   stringProp("Why; required for a refusal."),
					"taskKeys": map[string]any{
						"type": "array", "items": map[string]any{"type": "integer"},
						"description": "The approval tasks' keys, all of one order.",
					},
				},
				"required": []any{"approved", "taskKeys"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				approved, ok := args["approved"].(bool)
				if !ok {
					return "", fmt.Errorf("missing required argument: approved (true or false)")
				}
				raw, _ := args["taskKeys"].([]any)
				if len(raw) == 0 {
					return "", fmt.Errorf("missing required argument: taskKeys")
				}
				keys := make([]uint64, 0, len(raw))
				for i := range raw {
					k, err := argUint(map[string]any{"taskKeys": raw[i]}, "taskKeys")
					if err != nil {
						return "", err
					}
					keys = append(keys, k)
				}
				return jsonPost(c, "/api/v1/approvals/decide",
					withReason(map[string]any{"approved": approved, "taskKeys": keys}, args))
			},
		},
	})
}
