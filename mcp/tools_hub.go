package mcp

import (
	"errors"
	"net/url"
	"strings"
)

// hubTools are atlas hub (ADR-0499): the signed registry this server's operator
// pinned, read and installed through the same routes the Console's atlas hub dialog
// uses. The registry's URL and key are the operator's and never an argument here,
// so an agent can only ever reach the one source the server trusts.
func hubTools() []Tool {
	release := func() map[string]any {
		return map[string]any{
			"type": "object",
			"properties": map[string]any{
				"blueprintId": stringProp("The release's blueprintId, from atlas_hub_list_releases."),
				"version":     stringProp("The release's version, from atlas_hub_list_releases."),
			},
			"required": []any{"blueprintId", "version"},
		}
	}
	releasePath := func(args map[string]any, tail string) (string, error) {
		id, _ := args["blueprintId"].(string)
		version, _ := args["version"].(string)
		if strings.TrimSpace(id) == "" || strings.TrimSpace(version) == "" {
			return "", errors.New("blueprintId and version are required")
		}
		return "/api/v1/blueprints/" + url.PathEscape(id) + "/releases/" + url.PathEscape(version) + tail, nil
	}
	importSchema := release()
	props := importSchema["properties"].(map[string]any)
	props["digest"] = stringProp("The digest atlas_hub_preview_release reported for this release; the import is refused if the registry now serves other bytes.")
	props["dryRun"] = map[string]any{"type": "boolean",
		"description": "Answer the plan and write nothing. Defaults to true: pass false only once the person has seen the plan."}
	props["overwrite"] = map[string]any{"type": "array", "items": map[string]any{"type": "string"},
		"description": "Refs of changed artifacts to replace with the release's (process:<id>, form:<id>, decision:<id>, model:<handle>, constant:<NAME>, or *) — only as the person decided"}
	props["keep"] = map[string]any{"type": "array", "items": map[string]any{"type": "string"},
		"description": "Refs of changed artifacts to leave as this server has them (or *)"}
	props["adopt"] = map[string]any{"type": "array", "items": map[string]any{"type": "string"},
		"description": "Refs of artifacts the plan lists as elsewhere — held by another existing application — to move into the release's application, one by one (no *), and only as the person decided. Needs the editor role on the application each leaves. Also refs of gaps (capability:<key>, valuestream:<key>) whose proposal the person decided to file in the capability map."}
	props["map"] = map[string]any{"type": "array", "items": map[string]any{"type": "string"},
		"description": "Gaps the person decided to file under a capability the map already has, as ref=key (capability:<key>=<existing key>)"}
	props["omit"] = map[string]any{"type": "array", "items": map[string]any{"type": "string"},
		"description": "Refs of gaps the person decided to leave out (capability:<key>, valuestream:<key>, or *)"}
	importSchema["required"] = []any{"blueprintId", "version", "digest"}

	architectureSchema := release()
	archProps := architectureSchema["properties"].(map[string]any)
	archProps["digest"] = stringProp("The digest atlas_hub_preview_release reported for this release; the sketch is refused if the registry now serves other bytes.")
	archProps["layers"] = stringProp("Comma-separated layers to show: strategy, business, application, data, technology. A package records application, business and data. Default application,technology.")
	architectureSchema["required"] = []any{"blueprintId", "version", "digest"}

	return []Tool{
		{
			Name: "atlas_hub_list_releases",
			Description: "List the releases of the atlas hub registry this server's operator pinned (ADR-0499): " +
				"{registryId, releases}, each release with blueprintId, version, publisherId, title, " +
				"description, license, tags, atlas (the versions it is offered to), needs (Worker Types " +
				"and server constants it declares — not verified) and artifact (path, digest, size). " +
				"The index is verified against the operator's pinned Ed25519 key before anything is " +
				"answered; an unconfigured registry answers 503, an unreachable or wrongly signed one 502 " +
				"naming the station the way ended at and its cause — \"(stopped at tls: unknown_authority)\", " +
				"for example; say that to the person, it tells a firewall from a wrong key. " +
				"Filter by tags yourself. Nothing is installed.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			Handler: func(c *Client, _ map[string]any) (string, error) {
				return asText(c.get("/api/v1/blueprints"))
			},
		},
		{
			Name: "atlas_hub_preview_release",
			Description: "Preview one atlas hub release before installing it: the server downloads it from the " +
				"pinned registry, checks it against the signed digest and inspects the archive in memory, " +
				"and answers the application it installs (applicationKey, applicationName), its files, the " +
				"catalogues and products it offers, its declared needs and its digest. Pass that digest to " +
				"atlas_hub_import_release. When the registry cannot be read, or the package differs from " +
				"the signed index, it answers 502 naming the station it stopped at and its cause. Nothing " +
				"is written.",
			InputSchema: release(),
			Handler: func(c *Client, args map[string]any) (string, error) {
				path, err := releasePath(args, "/preview")
				if err != nil {
					return "", err
				}
				return asText(c.get(path))
			},
		},
		{
			Name: "atlas_hub_release_architecture",
			Description: "The architecture sketch of an atlas hub release's application before it is installed " +
				"(ADR-0539), as data: the server derives it from the process " +
				"models of the verified package and from nothing else — the messages its processes receive " +
				"(entryPoints), the workers, job types, decisions and calls they name (dependencies), the data " +
				"objects and personal data they declare (data), the roles their user tasks name (business) and " +
				"the names they throw and catch (coupling). It is marked packaged: it has no physical part, no " +
				"strategy layer, no catalogue products and no applications at the other end of a name, because " +
				"those belong to an installation, and a release published after installing records its own " +
				"snapshot (atlas_release_architecture), which can differ. Pass the digest " +
				"atlas_hub_preview_release reported; layers as on atlas_release_architecture (strategy, " +
				"business, application, data, technology; default application,technology). Nothing is written.",
			InputSchema: architectureSchema,
			Handler: func(c *Client, args map[string]any) (string, error) {
				path, err := releasePath(args, "/architecture")
				if err != nil {
					return "", err
				}
				digest, _ := args["digest"].(string)
				if strings.TrimSpace(digest) == "" {
					return "", errors.New("digest is required: preview the release with atlas_hub_preview_release first")
				}
				q := url.Values{"digest": {digest}}
				if layers, _ := args["layers"].(string); strings.TrimSpace(layers) != "" {
					q.Set("layers", layers)
				}
				return asText(c.get(path + "?" + q.Encode()))
			},
		},
		{
			Name: "atlas_hub_import_release",
			Description: "Install the application of an atlas hub release through the ordinary source import " +
				"(ADR-0467), exactly as the Console's atlas hub dialog does: the server fetches the release " +
				"from its pinned registry and refuses it unless its digest is the one the preview reported. " +
				"Runs as a DRY RUN unless dryRun is false: it answers the plan — every artifact with its " +
				"status (new, unchanged, behind, ahead, changed) and outcome — and the needs (server " +
				"constants, application constants, workers) this installation does or does not meet. Show " +
				"the plan to the person. Work done on this server is never replaced unasked: an artifact " +
				"that is changed here is written only when named in overwrite, left when named in keep, " +
				"and one in neither stops the import with 409, undecided and the plan — ask the person " +
				"which changes to take, never decide for them. A process, form or decision another " +
				"existing application holds is planned as elsewhere, with heldBy (the application, when " +
				"you may see it) and compared (unchanged or changed): it stops the import until the person " +
				"decides to take it, named in adopt, which moves it into the release's application — and, " +
				"if compared is changed, also needs overwrite or keep for its content. A decision model " +
				"other applications' decisions use too is listed with sharedWith: it is replaced only when " +
				"named in overwrite (never by *), which changes those decisions as well. A capability or value stream the " +
				"release needs and this server's capability map lacks is planned as gap (proposed says the release " +
				"offers one): it stops the import until the person decides — adopt files the release's proposal in " +
				"the map, map files it under an existing key, omit leaves it out; never decide that for them. A " +
				"proposal for a key the map has is linked and never written over the map's record. " +
				"It creates the application when this server has " +
				"never seen its key, else needs the editor role on it; it does not publish (use " +
				"atlas_publish_application) and it does not import the package's catalogues, which it " +
				"names under cataloguesNotImported: their questions ask for this installation's groups and " +
				"people, which a person answers in the Console. Importing the same release again changes " +
				"nothing.",
			InputSchema: importSchema,
			Handler: func(c *Client, args map[string]any) (string, error) {
				path, err := releasePath(args, "/import")
				if err != nil {
					return "", err
				}
				digest, _ := args["digest"].(string)
				if strings.TrimSpace(digest) == "" {
					return "", errors.New("digest is required: preview the release with atlas_hub_preview_release first")
				}
				q := url.Values{"digest": {digest}}
				if dry, given := args["dryRun"].(bool); !given || dry {
					q.Set("dryRun", "true")
				}
				for _, list := range []string{"overwrite", "keep", "adopt", "map", "omit"} {
					if refs := stringList(args[list]); len(refs) > 0 {
						q.Set(list, strings.Join(refs, ","))
					}
				}
				return asText(c.post(path+"?"+q.Encode(), "application/json", nil))
			},
		},
	}
}
