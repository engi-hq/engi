package domain

import (
	"fmt"
	"strings"
)

// Validate checks the structural integrity of the workflow: names, uniqueness,
// settings and connections. It does not know node types; port counts are
// checked against node descriptors by the engine. The returned error is nil
// or a *ValidationError listing every problem.
func (w *Workflow) Validate() error {
	v := &validator{}
	if strings.TrimSpace(w.Name) == "" {
		v.add("name", "must not be empty")
	}
	if w.ID != "" {
		if err := w.ID.Validate(); err != nil {
			v.add("id", err.Error())
		}
	}
	if w.Settings.Timeout < 0 {
		v.add("settings.timeout", "must not be negative")
	}

	names := make(map[string]struct{}, len(w.Nodes))
	ids := make(map[NodeID]struct{}, len(w.Nodes))
	for i := range w.Nodes {
		w.Nodes[i].validate(v, fmt.Sprintf("nodes[%d]", i))
		n := &w.Nodes[i]
		if n.Name != "" {
			if _, dup := names[n.Name]; dup {
				v.add(fmt.Sprintf("nodes[%d].name", i), fmt.Sprintf("duplicate node name %q", n.Name))
			}
			names[n.Name] = struct{}{}
		}
		if n.ID != "" {
			if _, dup := ids[n.ID]; dup {
				v.add(fmt.Sprintf("nodes[%d].id", i), fmt.Sprintf("duplicate node id %q", n.ID))
			}
			ids[n.ID] = struct{}{}
		}
	}

	seen := make(map[Connection]struct{}, len(w.Connections))
	for i, c := range w.Connections {
		path := fmt.Sprintf("connections[%d]", i)
		validateEndpoint(v, path+".from", c.From, names)
		validateEndpoint(v, path+".to", c.To, names)
		if c.From.Port != c.To.Port {
			v.add(path, fmt.Sprintf("port kind mismatch: %q to %q", c.From.Port, c.To.Port))
		}
		if _, dup := seen[c]; dup {
			v.add(path, "duplicate connection")
		}
		seen[c] = struct{}{}
	}

	for name := range w.PinData {
		if _, ok := names[name]; !ok {
			v.add("pinData", fmt.Sprintf("refers to unknown node %q", name))
		}
	}

	noteIDs := make(map[NoteID]struct{}, len(w.Notes))
	for i := range w.Notes {
		path := fmt.Sprintf("notes[%d]", i)
		w.Notes[i].validate(v, path)
		if id := w.Notes[i].ID; id != "" {
			if _, dup := noteIDs[id]; dup {
				v.add(path+".id", fmt.Sprintf("duplicate note id %q", id))
			}
			noteIDs[id] = struct{}{}
		}
	}
	return v.err()
}

func (n *Node) validate(v *validator, path string) {
	if strings.TrimSpace(n.Name) == "" {
		v.add(path+".name", "must not be empty")
	}
	if n.Type == "" {
		v.add(path+".type", "must not be empty")
	}
	if n.TypeVersion < 0 {
		v.add(path+".typeVersion", "must not be negative")
	}
	if n.ID != "" {
		if err := n.ID.Validate(); err != nil {
			v.add(path+".id", err.Error())
		}
	}
	s := n.Settings
	if s.Retry.MaxAttempts < 0 {
		v.add(path+".settings.retry.maxAttempts", "must not be negative")
	}
	if s.Retry.InitialDelay < 0 {
		v.add(path+".settings.retry.initialDelay", "must not be negative")
	}
	if !s.Retry.Backoff.Valid() {
		v.add(path+".settings.retry.backoff", fmt.Sprintf("unknown backoff %q", s.Retry.Backoff))
	}
	if !s.OnError.Valid() {
		v.add(path+".settings.onError", fmt.Sprintf("unknown onError %q", s.OnError))
	}
	if s.Timeout < 0 {
		v.add(path+".settings.timeout", "must not be negative")
	}
}

func validateEndpoint(v *validator, path string, e Endpoint, names map[string]struct{}) {
	if _, ok := names[e.Node]; !ok {
		v.add(path+".node", fmt.Sprintf("refers to unknown node %q", e.Node))
	}
	if e.Port == "" {
		v.add(path+".port", "must not be empty")
	}
	if e.Index < 0 {
		v.add(path+".index", "must not be negative")
	}
}

type validator struct{ issues []Issue }

func (v *validator) add(path, msg string) {
	v.issues = append(v.issues, Issue{Path: path, Message: msg})
}

func (v *validator) err() error {
	if len(v.issues) == 0 {
		return nil
	}
	return &ValidationError{Issues: v.issues}
}
