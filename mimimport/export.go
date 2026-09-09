package mimimport

import "strings"

// A MIM workflow is normally extracted with Export-FIMConfig, which does not hand
// out the XOML on its own: it exports the WorkflowDefinition *resource*, and the
// XOML is one attribute on it. The others are not decoration —
//
//	DisplayName        the name the MIM administrator sees, and the only
//	                   human name the workflow has; the XOML root carries a
//	                   class name like "MimWorkflow" or nothing at all
//	Description        what the workflow is for, in the author's words
//	RequestPhase       Authentication, Authorization or Action — when in a
//	                   request's life the workflow runs, which is most of what
//	                   makes two otherwise similar workflows different
//	RunOnPolicyUpdate  whether a policy change replays it over existing objects
//	ObjectID           the resource's identity, stable across exports
//
// — and all of them used to be dropped, along with every workflow after the
// first, because the importer took the first XOML it could find and ignored the
// resource around it.
//
// FIMAutomation writes an attribute in more than one shape, and this file accepts
// each: the attribute name as an attribute of the entry, as a child element of
// it, or as the entry's own element name.

// SourceInfo is the WorkflowDefinition resource an imported workflow came from.
// Every field is empty when the input was raw XOML rather than an export.
type SourceInfo struct {
	DisplayName       string `json:"displayName,omitempty"`
	Description       string `json:"description,omitempty"`
	RequestPhase      string `json:"requestPhase,omitempty"`
	RunOnPolicyUpdate string `json:"runOnPolicyUpdate,omitempty"`
	ObjectID          string `json:"objectId,omitempty"`
}

// describe renders the resource's fields as one sentence for the process
// documentation, or "" when there is nothing to say.
func (s SourceInfo) describe() string {
	var parts []string
	for _, f := range []struct{ label, value string }{
		{"Beschreibung", s.Description},
		{"Anforderungsphase", s.RequestPhase},
		{"Bei Richtlinienänderung ausführen", s.RunOnPolicyUpdate},
		{"MIM-ObjectID", s.ObjectID},
	} {
		if strings.TrimSpace(f.value) != "" {
			parts = append(parts, f.label+": "+strings.TrimSpace(f.value))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ". ") + "."
}

// exportedWorkflow is one WorkflowDefinition found in an export wrapper.
type exportedWorkflow struct {
	source SourceInfo
	xoml   string
}

// exportedWorkflows walks a resource graph and returns every WorkflowDefinition
// it carries, in document order. A resource is recognised by carrying an
// attribute named XOML rather than by its element names, because the wrapper's
// own vocabulary differs between the shapes FIMAutomation writes.
func exportedWorkflows(n xnode) []exportedWorkflow {
	var out []exportedWorkflow
	collectWorkflows(n, &out)
	return out
}

func collectWorkflows(n xnode, out *[]exportedWorkflow) {
	// An attribute the wrapper wrote on the element itself, rather than as a child.
	for _, a := range n.Attrs {
		if strings.EqualFold(a.Name.Local, "XOML") && strings.TrimSpace(a.Value) != "" {
			*out = append(*out, exportedWorkflow{source: sourceOf(attributesOf(n)), xoml: cleanEmbedded(a.Value)})
			return
		}
	}
	attrs := attributesOf(n)
	if x := strings.TrimSpace(attrs["xoml"]); x != "" {
		*out = append(*out, exportedWorkflow{source: sourceOf(attrs), xoml: cleanEmbedded(x)})
		return // the resource's own children are its attributes, not more resources
	}
	// The element may be the XOML attribute entry itself rather than the resource
	// around it — a bare <XOML>…</XOML>, or an entry naming the attribute with
	// nothing beside it. There is no resource here, so no fields to read off one.
	if name, value, ok := attributeEntry(n); ok && strings.EqualFold(name, "XOML") && strings.TrimSpace(value) != "" {
		*out = append(*out, exportedWorkflow{xoml: cleanEmbedded(value)})
		return
	}
	for _, k := range n.Kids {
		collectWorkflows(k, out)
	}
}

// attributesOf reads an element's children as resource attributes, keyed by
// lower-cased name. A child that names no attribute contributes nothing.
func attributesOf(n xnode) map[string]string {
	attrs := map[string]string{}
	for _, k := range n.Kids {
		name, value, ok := attributeEntry(k)
		if !ok {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(name))
		if _, seen := attrs[key]; !seen {
			attrs[key] = value // a repeated (multi-valued) attribute keeps its first value
		}
	}
	return attrs
}

// attributeEntry reads one attribute entry of a resource, in any of the shapes
// FIMAutomation writes:
//
//	<AttributeType AttributeName="XOML"><Value>…</Value></AttributeType>
//	<ResourceManagementAttribute><AttributeName>XOML</AttributeName><Value>…</Value></…>
//	<XOML>…</XOML>
func attributeEntry(k xnode) (name, value string, ok bool) {
	if v, has := k.attr("AttributeName"); has && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v), valueText(k), true
	}
	for _, kk := range k.Kids {
		if strings.EqualFold(kk.local(), "AttributeName") {
			if s := strings.TrimSpace(kk.Text); s != "" {
				return s, valueText(k), true
			}
		}
	}
	if v := valueText(k); v != "" {
		return k.local(), v, true
	}
	return "", "", false
}

// sourceOf picks the WorkflowDefinition fields out of a resource's attributes.
func sourceOf(attrs map[string]string) SourceInfo {
	return SourceInfo{
		DisplayName:       strings.TrimSpace(attrs["displayname"]),
		Description:       strings.TrimSpace(attrs["description"]),
		RequestPhase:      strings.TrimSpace(attrs["requestphase"]),
		RunOnPolicyUpdate: strings.TrimSpace(attrs["runonpolicyupdate"]),
		ObjectID:          strings.TrimSpace(attrs["objectid"]),
	}
}
