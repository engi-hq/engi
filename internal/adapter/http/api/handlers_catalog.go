package api

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/engi-hq/engi/internal/domain"
)

// catalogDTO is the unpaged list of a catalog (the registries are small and
// static, so they are not cursor-paged).
type catalogDTO[T any] struct {
	Data []T `json:"data"`
}

func (a *API) listNodeTypes(w http.ResponseWriter, r *http.Request) error {
	if err := allowParams(r.URL.Query()); err != nil {
		return err
	}
	ds := a.d.Nodes.Descriptors()
	out := catalogDTO[nodeTypeDTO]{Data: make([]nodeTypeDTO, len(ds))}
	for i, d := range ds {
		out.Data[i] = toNodeTypeDTO(d)
	}
	return a.writeJSON(w, r, http.StatusOK, out)
}

// getNodeType returns one descriptor; without ?version it returns the newest.
func (a *API) getNodeType(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	if err := allowParams(q, "version"); err != nil {
		return err
	}
	t := r.PathValue("type")
	version := 0
	if s := q.Get("version"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			return badRequest("version must be a positive integer")
		}
		version = n
	} else {
		for _, d := range a.d.Nodes.Descriptors() {
			if d.Type == t {
				version = max(version, d.Version)
			}
		}
	}
	d, err := a.d.Nodes.Descriptor(t, version)
	if err != nil {
		return fmt.Errorf("node type %q: %w", t, domain.ErrNotFound)
	}
	return a.writeJSON(w, r, http.StatusOK, toNodeTypeDTO(d))
}

func (a *API) listCredentialTypes(w http.ResponseWriter, r *http.Request) error {
	if err := allowParams(r.URL.Query()); err != nil {
		return err
	}
	ts := a.d.CredentialTypes.List()
	out := catalogDTO[credentialTypeDTO]{Data: make([]credentialTypeDTO, len(ts))}
	for i, t := range ts {
		out.Data[i] = toCredentialTypeDTO(t)
	}
	return a.writeJSON(w, r, http.StatusOK, out)
}

func (a *API) getCredentialType(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("name")
	t, ok := a.d.CredentialTypes.Get(name)
	if !ok {
		return fmt.Errorf("credential type %q: %w", name, domain.ErrNotFound)
	}
	return a.writeJSON(w, r, http.StatusOK, toCredentialTypeDTO(t))
}

// pluginDTO is the wire form of domain.PluginStatus.
type pluginDTO struct {
	Name      string   `json:"name"`
	Version   string   `json:"version"`
	Transport string   `json:"transport,omitempty"`
	Nodes     []string `json:"nodes"`
	State     string   `json:"state"`
	Error     string   `json:"error,omitempty"`
}

func (a *API) listPlugins(w http.ResponseWriter, r *http.Request) error {
	if err := allowParams(r.URL.Query()); err != nil {
		return err
	}
	out := catalogDTO[pluginDTO]{Data: []pluginDTO{}}
	if a.d.Plugins != nil {
		for _, p := range a.d.Plugins.Plugins() {
			nodes := p.Nodes
			if nodes == nil {
				nodes = []string{}
			}
			out.Data = append(out.Data, pluginDTO{Name: p.Name, Version: p.Version, Transport: p.Transport, Nodes: nodes, State: string(p.State), Error: p.Error})
		}
	}
	return a.writeJSON(w, r, http.StatusOK, out)
}
