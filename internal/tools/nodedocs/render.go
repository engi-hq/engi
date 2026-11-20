// Package nodedocs renders node and credential-type reference tables from the
// descriptors the editor itself uses, and splices them into hand-written
// Markdown pages between generated markers.
package nodedocs

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/engi-hq/engi/pkg/nodesdk"
)

const (
	emptyCell   = "—"
	nestedMark  = "↳ "
	pathJoiner  = "."
	cellBreak   = "<br>"
	defaultPort = nodesdk.PortMain
)

// RenderNode renders the reference block for every registered version of one
// node type. versions must share a Type and be ordered by version.
func RenderNode(versions []nodesdk.Descriptor) string {
	var b strings.Builder
	for i, d := range versions {
		if i > 0 {
			b.WriteString("\n")
		}
		renderDescriptor(&b, d, len(versions) > 1)
	}
	return b.String()
}

func renderDescriptor(b *strings.Builder, d nodesdk.Descriptor, versioned bool) {
	h := ""
	if versioned {
		h = fmt.Sprintf("**Version %d**\n\n", d.Version)
	}
	b.WriteString(h)
	fmt.Fprintf(b, "**Type:** `%s` · **Version:** %d · **Group:** `%s` · **Display name:** %s\n\n", d.Type, d.Version, d.Group, d.DisplayName)
	if d.Description != "" {
		b.WriteString(d.Description + "\n\n")
	}
	if d.MergePolicy != "" {
		fmt.Fprintf(b, "**Merge policy:** `%s`\n\n", d.MergePolicy)
	}
	b.WriteString("**Inputs**\n\n")
	renderPorts(b, d.Inputs, true)
	b.WriteString("\n**Outputs**\n\n")
	renderPorts(b, d.Outputs, false)
	b.WriteString("\n**Credentials**\n\n")
	if len(d.Credentials) == 0 {
		b.WriteString("None.\n")
	} else {
		b.WriteString("| Slot | Accepted credential types | Required |\n|---|---|---|\n")
		for _, c := range d.Credentials {
			accepted := "any (chosen by a parameter)"
			if c.Type != "" {
				accepted = "`" + c.Type + "`"
			}
			fmt.Fprintf(b, "| `%s` | %s | %s |\n", c.Name, accepted, yesNo(c.Required))
		}
	}
	b.WriteString("\n**Parameters**\n\n")
	if len(d.Properties) == 0 {
		b.WriteString("None.\n")
		return
	}
	b.WriteString(propertyTable(d.Properties, true, false))
}

func renderPorts(b *strings.Builder, ports []nodesdk.PortSpec, inputs bool) {
	if len(ports) == 0 {
		b.WriteString("None.\n")
		return
	}
	if inputs {
		b.WriteString("| Index | Kind | Display name | Required |\n|---|---|---|---|\n")
	} else {
		b.WriteString("| Index | Kind | Display name |\n|---|---|---|\n")
	}
	for i, p := range ports {
		kind := p.Kind
		if kind == "" {
			kind = defaultPort
		}
		name := p.DisplayName
		if name == "" {
			name = p.Name
		}
		if name == "" {
			name = emptyCell
		}
		if inputs {
			fmt.Fprintf(b, "| %d | `%s` | %s | %s |\n", i, kind, cell(name), yesNo(p.Required))
		} else {
			fmt.Fprintf(b, "| %d | `%s` | %s |\n", i, kind, cell(name))
		}
	}
}

// propertyTable renders props (and, recursively, their children) as rows of
// one table. Nested rows are named parent.child and prefixed with an arrow.
func propertyTable(props []nodesdk.Property, withRequired, _ bool) string {
	var b strings.Builder
	b.WriteString("| Name | Display name | Type | Default | Required | Options | Shown when | Description |\n|---|---|---|---|---|---|---|---|\n")
	writePropertyRows(&b, props, "", withRequired)
	return b.String()
}

func writePropertyRows(b *strings.Builder, props []nodesdk.Property, parent string, _ bool) {
	for _, p := range props {
		name, label := p.Name, "`"+p.Name+"`"
		if parent != "" {
			name = parent + pathJoiner + p.Name
			label = nestedMark + "`" + name + "`"
		}
		fmt.Fprintf(b, "| %s | %s | `%s` | %s | %s | %s | %s | %s |\n",
			label, cellOrDash(p.DisplayName), p.Type, formatValue(p.Default), yesNo(p.Required),
			formatOptions(p.Options), displayText(p.DisplayOptions), cellOrDash(p.Description))
		writePropertyRows(b, p.Children, name, true)
	}
}

func formatOptions(opts []nodesdk.Option) string {
	if len(opts) == 0 {
		return emptyCell
	}
	parts := make([]string, 0, len(opts))
	for _, o := range opts {
		s := formatValue(o.Value)
		if o.Name != "" && o.Name != fmt.Sprint(o.Value) {
			s += " (" + cell(o.Name) + ")"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, cellBreak)
}

// displayText turns DisplayOptions into prose such as "shown when `mode` = `a` or `b`".
func displayText(d *nodesdk.DisplayOptions) string {
	if d == nil || (len(d.Show) == 0 && len(d.Hide) == 0) {
		return "always"
	}
	var parts []string
	if len(d.Show) > 0 {
		parts = append(parts, "shown when "+conditions(d.Show))
	}
	if len(d.Hide) > 0 {
		parts = append(parts, "hidden when "+conditions(d.Hide))
	}
	return strings.Join(parts, "; ")
}

func conditions(m map[string][]any) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		vals := make([]string, 0, len(m[k]))
		for _, v := range m[k] {
			vals = append(vals, formatValue(v))
		}
		out = append(out, "`"+k+"` = "+strings.Join(vals, " or "))
	}
	return strings.Join(out, " and ")
}

// formatValue renders a default or option value as inline code.
func formatValue(v any) string {
	switch t := v.(type) {
	case nil:
		return emptyCell
	case string:
		if t == "" {
			return "`\"\"`"
		}
		return "`" + escapeCode(t) + "`"
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return "`" + escapeCode(fmt.Sprint(v)) + "`"
	}
	return "`" + escapeCode(string(raw)) + "`"
}

func escapeCode(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	return strings.NewReplacer("\r\n", " ", "\n", " ", "`", "'").Replace(s)
}

func cell(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	return strings.NewReplacer("\r\n", " ", "\n", " ").Replace(s)
}

func cellOrDash(s string) string {
	if s == "" {
		return emptyCell
	}
	return cell(s)
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

// RenderCredentialTypes renders the credential-type reference.
func RenderCredentialTypes(types []nodesdk.CredentialType) string {
	sorted := append([]nodesdk.CredentialType(nil), types...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	var b strings.Builder
	for i, t := range sorted {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "### %s\n\n**Type:** `%s` · **Authentication:** %s\n\n", t.DisplayName, t.Name, authText(t))
		if t.Test != nil {
			fmt.Fprintf(&b, "**Test:** %s\n\n", testText(*t.Test))
		}
		if len(t.Fields) == 0 {
			b.WriteString("No fields.\n")
			continue
		}
		b.WriteString("| Name | Display name | Type | Default | Required | Secret | Managed | Options | Description |\n|---|---|---|---|---|---|---|---|---|\n")
		for _, f := range t.Fields {
			fmt.Fprintf(&b, "| `%s` | %s | `%s` | %s | %s | %s | %s | %s | %s |\n",
				f.Name, cellOrDash(f.DisplayName), f.Type, formatValue(f.Default), yesNo(f.Required),
				yesNo(f.Secret), yesNo(f.Managed), formatOptions(f.Options), cellOrDash(f.Description))
		}
	}
	return b.String()
}

func authText(t nodesdk.CredentialType) string {
	if t.OAuth2 != nil {
		return "OAuth2 (token handled by the credential service)"
	}
	a := t.Authenticate
	if a == nil {
		return "none declared (used directly by the node)"
	}
	switch a.Kind {
	case nodesdk.AuthHeader:
		return "`" + string(a.Kind) + "`: " + target(a, "header")
	case nodesdk.AuthQuery:
		return "`" + string(a.Kind) + "`: " + target(a, "query parameter")
	case nodesdk.AuthBasic:
		return "`basic`: HTTP Basic from `" + a.UserField + "` and `" + a.PasswordField + "`"
	case nodesdk.AuthBearer:
		return "`bearer`: `Authorization: Bearer` from `" + a.TokenField + "`"
	default:
		return "`" + string(a.Kind) + "`"
	}
}

func target(a *nodesdk.AuthSpec, what string) string {
	name := "`" + a.Name + "`"
	if a.NameField != "" {
		name = "name from `" + a.NameField + "`"
	}
	s := what + " " + name + " = `" + a.ValueField + "`"
	if a.Prefix != "" {
		s += " with prefix `" + strings.TrimSpace(a.Prefix) + "`"
	}
	return s
}

func testText(t nodesdk.TestSpec) string {
	method := t.Method
	if method == "" {
		method = "GET"
	}
	url := "`" + t.URL + "`"
	if t.URLField != "" {
		url = "URL from `" + t.URLField + "`"
	}
	status := "any 2xx"
	if t.ExpectStatus != 0 {
		status = fmt.Sprint(t.ExpectStatus)
	}
	return "`" + method + "` " + url + ", expects " + status
}
