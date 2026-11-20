package nodedocs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Marker ids that are not node types.
const (
	// IndexID is the marker id of the node table in the index page.
	IndexID = "index"
	// CredentialTypesID is the marker id of the credential-type reference.
	CredentialTypesID = "credential-types" //nolint:gosec // a marker name, not a secret
)

var (
	beginRe   = regexp.MustCompile(`(?m)^<!-- BEGIN GENERATED: (\S+) -->[ \t]*$`)
	headingRe = regexp.MustCompile(`(?m)^## (.+?)\s*$`)
)

func endMarker(id string) string { return "<!-- END GENERATED: " + id + " -->" }

type block struct {
	id         string
	file       string
	start, end int // byte range of the content between the markers
	heading    string
}

type page struct {
	name   string
	text   string
	blocks []block
}

// Sync computes the new content of every page in dir. It returns only the
// pages whose content changes. It fails when a node has no marker, a marker
// names no node, a marker is duplicated or unterminated.
func Sync(dir string, descs []nodesdk.Descriptor, creds []nodesdk.CredentialType) (map[string]string, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		return nil, fmt.Errorf("listing pages: %w", err)
	}
	sort.Strings(files)
	var pages []*page
	for _, f := range files {
		raw, err := os.ReadFile(f) //nolint:gosec // f comes from globbing the docs directory
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", f, err)
		}
		p, err := parsePage(filepath.Base(f), string(raw))
		if err != nil {
			return nil, err
		}
		pages = append(pages, p)
	}

	byType := map[string][]nodesdk.Descriptor{}
	var order []string
	for _, d := range descs {
		if _, ok := byType[d.Type]; !ok {
			order = append(order, d.Type)
		}
		byType[d.Type] = append(byType[d.Type], d)
	}
	content := map[string]string{CredentialTypesID: RenderCredentialTypes(creds)}
	for t, v := range byType {
		content[t] = RenderNode(v)
	}

	seen := map[string]string{}
	var errs []error
	var located []located
	for _, p := range pages {
		for _, b := range p.blocks {
			if prev, dup := seen[b.id]; dup {
				errs = append(errs, fmt.Errorf("marker %q appears in both %s and %s", b.id, prev, p.name))
			}
			seen[b.id] = p.name
			if _, isNode := byType[b.id]; isNode {
				located = append(located, locatedNode(b, p.name, byType[b.id][0]))
			}
			if _, ok := content[b.id]; !ok && b.id != IndexID {
				errs = append(errs, fmt.Errorf("%s: marker %q matches no registered node type", p.name, b.id))
			}
		}
	}
	for _, t := range order {
		if _, ok := seen[t]; !ok {
			errs = append(errs, fmt.Errorf("node %q has no documentation section: add a <!-- BEGIN GENERATED: %s --> / %s block to a page in %s", t, t, endMarker(t), dir))
		}
	}
	if _, ok := seen[CredentialTypesID]; !ok {
		errs = append(errs, fmt.Errorf("no %q marker found in %s", CredentialTypesID, dir))
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	content[IndexID] = renderIndex(located)

	out := map[string]string{}
	for _, p := range pages {
		next := p.text
		for i := len(p.blocks) - 1; i >= 0; i-- {
			b := p.blocks[i]
			next = next[:b.start] + "\n" + strings.TrimRight(content[b.id], "\n") + "\n" + next[b.end:]
		}
		if next != p.text {
			out[filepath.Join(dir, p.name)] = next
		}
	}
	return out, nil
}

func parsePage(name, text string) (*page, error) {
	p := &page{name: name, text: text}
	headings := headingRe.FindAllStringSubmatchIndex(text, -1)
	for _, m := range beginRe.FindAllStringSubmatchIndex(text, -1) {
		id := text[m[2]:m[3]]
		start := m[1]
		rel := strings.Index(text[start:], endMarker(id))
		if rel < 0 {
			return nil, fmt.Errorf("%s: marker %q has no %s", name, id, endMarker(id))
		}
		b := block{id: id, file: name, start: start, end: start + rel}
		for _, h := range headings {
			if h[0] < m[0] {
				b.heading = text[h[2]:h[3]]
			}
		}
		p.blocks = append(p.blocks, b)
	}
	return p, nil
}

type located struct {
	heading, file string
	d             nodesdk.Descriptor
}

func locatedNode(b block, file string, d nodesdk.Descriptor) located {
	return located{heading: b.heading, file: file, d: d}
}

func renderIndex(nodes []located) string {
	var sb strings.Builder
	sb.WriteString("| Node | Type | Group | Purpose |\n|---|---|---|---|\n")
	for _, n := range nodes {
		link := n.d.DisplayName
		if n.heading != "" {
			link = fmt.Sprintf("[%s](%s#%s)", cell(n.heading), n.file, slug(n.heading))
		}
		fmt.Fprintf(&sb, "| %s | `%s` | `%s` | %s |\n", link, n.d.Type, n.d.Group, cellOrDash(firstSentence(n.d.Description)))
	}
	return sb.String()
}

func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i >= 0 {
		return s[:i+1]
	}
	return s
}

// slug mimics the GitHub heading anchor: lower case, punctuation dropped,
// spaces to hyphens.
func slug(h string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(h) {
		switch {
		case r == ' ' || r == '-':
			b.WriteRune('-')
		case r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r > 127:
			b.WriteRune(r)
		}
	}
	return b.String()
}
