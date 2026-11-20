package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/engi-hq/engi/internal/domain"
)

type tagDTO struct {
	ID        domain.TagID `json:"id"`
	Name      string       `json:"name"`
	CreatedAt time.Time    `json:"createdAt"`
}

func toTagDTO(t domain.Tag) tagDTO { return tagDTO{ID: t.ID, Name: t.Name, CreatedAt: t.CreatedAt} }

type tagRequest struct {
	Name string `json:"name"`
}

func (a *API) listTags(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	if err := allowParams(q, "limit", "cursor"); err != nil {
		return err
	}
	page, err := a.pageOf(q)
	if err != nil {
		return err
	}
	res, err := a.d.Tags.List(r.Context(), page)
	if err != nil {
		return fmt.Errorf("listing tags: %w", err)
	}
	return a.writeJSON(w, r, http.StatusOK, listOf(res, toTagDTO))
}

func (a *API) createTag(w http.ResponseWriter, r *http.Request) error {
	var in tagRequest
	if err := decodeJSON(r, &in, false); err != nil {
		return err
	}
	t, err := a.d.Tags.Create(r.Context(), in.Name)
	if err != nil {
		return fmt.Errorf("creating tag: %w", err)
	}
	w.Header().Set("Location", v1("/tags/"+string(t.ID)))
	return a.writeJSON(w, r, http.StatusCreated, toTagDTO(t))
}

func (a *API) updateTag(w http.ResponseWriter, r *http.Request) error {
	id, err := idParam(r, "id", domain.TagID.Validate)
	if err != nil {
		return err
	}
	var in tagRequest
	if err := decodeJSON(r, &in, false); err != nil {
		return err
	}
	t, err := a.d.Tags.Rename(r.Context(), id, in.Name)
	if err != nil {
		return fmt.Errorf("renaming tag: %w", err)
	}
	return a.writeJSON(w, r, http.StatusOK, toTagDTO(t))
}

func (a *API) deleteTag(w http.ResponseWriter, r *http.Request) error {
	id, err := idParam(r, "id", domain.TagID.Validate)
	if err != nil {
		return err
	}
	if err := a.d.Tags.Delete(r.Context(), id); err != nil {
		return fmt.Errorf("deleting tag: %w", err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
