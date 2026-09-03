package mcp

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
)

// The information-model tools (ADR-0230).
//
// An agent that authors BPMN through these tools runs into exactly the gap the
// information model exists to close: it can write `itemSubjectRef="Order"` on a data
// object and nothing anywhere says what an Order is. So the same surface a person
// gets is a tool surface — read the vocabulary before modeling against it, and
// author it when it does not exist yet.
//
// The subset tool matters more here than in the browser. A canvas greys out an
// illegal connection; an agent has nothing to grey out, so it needs the rules and
// the reasons stated before it writes, or it will produce a model the server
// refuses and learn why one refusal at a time.
// idArg is the single-string-id input schema these tools share — the opaque model
// id, where the runtime tools take a numeric entity key.
func idArg(desc string) map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"id": stringProp(desc)},
		"required":   []any{"id"},
	}
}

// arrayProp and integerProp complete the property helpers for the shapes this file
// needs: a whole list of classes or associations, and a revision.
func arrayProp(desc string) map[string]any {
	return map[string]any{"type": "array", "description": desc}
}

func integerProp(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}

func infomodelTools() []Tool {
	return []Tool{
		{
			Name: "atlas_infomodel_subset",
			Description: "Read the information model's authoring subset: the class kinds (business object, " +
				"value type, enumeration) with what each means, the association kinds, the primitive " +
				"attribute types, the multiplicities, the matrix of which association may run between " +
				"which kinds of class, and what this build deliberately does not author. Read it before " +
				"writing a model — the server enforces this table and refuses anything outside it.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			Handler: func(c *Client, _ map[string]any) (string, error) {
				return asText(c.get("/api/v1/infomodel/subset"))
			},
		},
		{
			Name: "atlas_list_information_models",
			Description: "List the information models — the UML class-diagram documents that give a BPMN " +
				"data object's itemSubjectRef a type to resolve against. Each row carries its id, owning " +
				"application, and how many classes and associations it holds. Filter by applicationId.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"applicationId": stringProp("Optional process application id to list the models of."),
				},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				path := "/api/v1/infomodel/models"
				if app := optString(args, "applicationId"); app != "" {
					path += "?applicationId=" + url.QueryEscape(app)
				}
				return asText(c.get(path))
			},
		},
		{
			Name: "atlas_get_information_model",
			Description: "Read one information model whole: its classes with their attributes, business " +
				"keys and documentation, its associations, and the validation verdict on all of it. This " +
				"is the vocabulary a BPMN model's itemSubjectRef names — read it before writing a data " +
				"object, so the type you declare is one that exists.",
			InputSchema: idArg("The information model id (from atlas_list_information_models)."),
			Handler: func(c *Client, args map[string]any) (string, error) {
				id, err := argString(args, "id")
				if err != nil {
					return "", err
				}
				return asText(c.get("/api/v1/infomodel/models/" + url.PathEscape(id)))
			},
		},
		{
			Name: "atlas_create_information_model",
			Description: "Start an empty information model for a process application. It inherits the " +
				"application's sharing scope, which is what lets every process in that application share " +
				"one vocabulary for its data. Fill it with atlas_save_information_model.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"applicationId": stringProp("The process application that owns the model."),
					"name":          stringProp("A name for the model, e.g. \"Sales data\"."),
					"documentation": stringProp("Optional: what part of the business these classes describe."),
				},
				"required": []any{"applicationId", "name"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				appID, err := argString(args, "applicationId")
				if err != nil {
					return "", err
				}
				name, err := argString(args, "name")
				if err != nil {
					return "", err
				}
				payload := map[string]any{"applicationId": appID, "name": name}
				if d := optString(args, "documentation"); d != "" {
					payload["documentation"] = d
				}
				body, _ := json.Marshal(payload)
				return asText(c.post("/api/v1/infomodel/models", "application/json", body))
			},
		},
		{
			Name: "atlas_import_information_model",
			Description: "Import a UML class diagram as a NEW information model of an application. Two " +
				"documents are read: Atlas's own JSON (what atlas_get_information_model returns, so a " +
				"model moves between applications and installations), and the XMI 2.5.1 a UML tool " +
				"exports. The format is detected from the document unless you state it. Reading a " +
				"foreign notation into a declared subset is LOSSY: everything outside the subset is " +
				"dropped or adjusted, and the answer's notes name every element it happened to — read " +
				"them, they are the substance of the answer. Set dryRun to get those notes and the " +
				"model it would create without storing anything.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"applicationId": stringProp("The process application that will own the imported model."),
					"document":      stringProp("The whole source document: Atlas JSON, or UML XMI."),
					"format":        stringProp("Optional: \"json\" or \"xmi\". Detected from the document when omitted."),
					"name":          stringProp("Optional: a name for the model, overriding the one the document carries."),
					"documentation": stringProp("Optional: what part of the business these classes describe."),
					"dryRun": map[string]any{"type": "boolean",
						"description": "Report what the import would do — the notes and the model it would create — and store nothing."},
				},
				"required": []any{"applicationId", "document"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				appID, err := argString(args, "applicationId")
				if err != nil {
					return "", err
				}
				document, err := argString(args, "document")
				if err != nil {
					return "", err
				}
				payload := map[string]any{"applicationId": appID, "document": document}
				for _, field := range []string{"format", "name", "documentation"} {
					if v := optString(args, field); v != "" {
						payload[field] = v
					}
				}
				if dry, ok := args["dryRun"].(bool); ok && dry {
					payload["dryRun"] = true
				}
				body, _ := json.Marshal(payload)
				return asText(c.post("/api/v1/infomodel/import", "application/json", body))
			},
		},
		{
			Name: "atlas_save_information_model",
			Description: "Replace an information model's classes and associations. The whole document is " +
				"sent, so read it first and send it back changed. A class is {id, name, stereotype, " +
				"attributes:[{name,type,multiplicity}], identity:[attribute names], literals:[…] for an " +
				"enumeration, x, y}; an association is {id, kind, name, from:{classId,role,multiplicity}, " +
				"to:{…}}. Leave an id empty for something new and the server mints it, rewriting any " +
				"association end that referred to your placeholder. A model that does not validate is " +
				"REFUSED with its findings and nothing is stored — read atlas_infomodel_subset first, " +
				"because the refusals distinguish what UML forbids from what this build does not author.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":            stringProp("The information model id to write."),
					"name":          stringProp("Optional new name."),
					"documentation": stringProp("Optional new documentation."),
					"classes":       arrayProp("The classes, whole. Omit to leave them unchanged."),
					"associations":  arrayProp("The associations, whole. Omit to leave them unchanged."),
					"revision":      integerProp("The revision you read. A stale one is refused as a conflict; omit to write regardless."),
				},
				"required": []any{"id"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				id, err := argString(args, "id")
				if err != nil {
					return "", err
				}
				payload := map[string]any{}
				for _, field := range []string{"name", "documentation"} {
					if v := optString(args, field); v != "" {
						payload[field] = v
					}
				}
				for _, field := range []string{"classes", "associations"} {
					if v, ok := args[field]; ok {
						payload[field] = v
					}
				}
				if rev, ok, err := optPositiveUint(args, "revision"); err != nil {
					return "", err
				} else if ok {
					payload["revision"] = rev
				}
				body, _ := json.Marshal(payload)
				return asText(c.put("/api/v1/infomodel/models/"+url.PathEscape(id), "application/json", body))
			},
		},
		{
			Name:        "atlas_delete_information_model",
			Description: "Delete an information model. Its classes and associations go with it.",
			InputSchema: idArg("The information model id to delete."),
			Handler: func(c *Client, args map[string]any) (string, error) {
				id, err := argString(args, "id")
				if err != nil {
					return "", err
				}
				if _, err := c.del("/api/v1/infomodel/models/" + url.PathEscape(id)); err != nil {
					return "", err
				}
				return "deleted information model " + id, nil
			},
		},
		{
			Name: "atlas_information_model_schema",
			Description: "Project one class to a JSON Schema — the contract a *value* of that class is " +
				"checked against — together with what the projection could not carry. A JSON document is " +
				"a tree and a class model is a graph, so only composition becomes containment; " +
				"associations, the flattened generalization and the business key are reported as loss " +
				"rather than silently dropped. Derived, never authored.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":    stringProp("The information model id."),
					"class": stringProp("The class name to project, e.g. \"Order\"."),
				},
				"required": []any{"id", "class"},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				id, err := argString(args, "id")
				if err != nil {
					return "", err
				}
				class, err := argString(args, "class")
				if err != nil {
					return "", err
				}
				return asText(c.get("/api/v1/infomodel/models/" + url.PathEscape(id) +
					"/schema?class=" + url.QueryEscape(class)))
			},
		},
		{
			Name: "atlas_instance_object_graph",
			Description: "Derive one process instance's object diagram: its data objects as UML object " +
				"nodes with their attributes and business keys, and the lines between them. Two things " +
				"become a line — a part living inside a whole's value (composition), and one object " +
				"holding another's business key. A reference matching nothing here is reported under " +
				"unresolved rather than dropped: that is the edge of what one instance can see, not a " +
				"fault. Where the owning application models nothing the graph says so (degraded) and " +
				"still lists what the instance holds.",
			InputSchema: keyArg("The instance key (from atlas_list_instances) to draw."),
			Handler: func(c *Client, args map[string]any) (string, error) {
				key, err := argUint(args, "key")
				if err != nil {
					return "", err
				}
				return asText(c.get("/api/v1/instances/" + strconv.FormatUint(key, 10) + "/object-graph"))
			},
		},
		{
			Name: "atlas_data_objects",
			Description: "The data-centric index: which instances carry which data, newest first — the " +
				"landscape read from the data's side rather than the process's. Each row names its " +
				"instance and process, the type the model declared (itemSubjectRef), the data state, the " +
				"value, and the business key when the class declares an identity. Filter by class for " +
				"\"which processes are handling an Order\", and by key for the question BPMN cannot " +
				"express: which instances, across which processes, are carrying THIS order. Running " +
				"instances only unless history is true, which also sweeps finished ones. The answer says " +
				"how many instances it examined and whether a bound stopped it — treat a truncated " +
				"answer as a page, not as the whole truth.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"class":   stringProp("Optional declared type name to filter by, e.g. \"Order\"."),
					"key":     stringProp("Optional business key to filter by, e.g. \"ORD-1\". Only rows whose class declares an identity can match."),
					"history": map[string]any{"type": "boolean", "description": "Also sweep finished instances. Costs a longer walk; their data is retained until purged."},
				},
			},
			Handler: func(c *Client, args map[string]any) (string, error) {
				params := url.Values{}
				if class := strings.TrimSpace(optString(args, "class")); class != "" {
					params.Set("class", class)
				}
				if key := strings.TrimSpace(optString(args, "key")); key != "" {
					params.Set("key", key)
				}
				if h, ok := args["history"].(bool); ok && h {
					params.Set("history", "true")
				}
				path := "/api/v1/data-objects"
				if len(params) > 0 {
					path += "?" + params.Encode()
				}
				return asText(c.get(path))
			},
		},
	}
}
