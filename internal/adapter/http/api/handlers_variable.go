package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/service/variable"
)

// variableDTO carries a secret variable's value as the service's sentinel.
type variableDTO struct {
	ID        domain.VariableID `json:"id"`
	Key       string            `json:"key"`
	Value     string            `json:"value"`
	Secret    bool              `json:"secret"`
	CreatedAt time.Time         `json:"createdAt"`
	UpdatedAt time.Time         `json:"updatedAt"`
}

func toVariableDTO(v variable.View) variableDTO {
	return variableDTO{ID: v.ID, Key: v.Key, Value: v.Value, Secret: v.Secret, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
}

type variableCreateRequest struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
}

type variablePatchRequest struct {
	Key    *string `json:"key"`
	Value  *string `json:"value"`
	Secret *bool   `json:"secret"`
}

func variableID(r *http.Request) (domain.VariableID, error) {
	return idParam(r, "id", domain.VariableID.Validate)
}

func (a *API) listVariables(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	if err := allowParams(q, "limit", "cursor"); err != nil {
		return err
	}
	page, err := a.pageOf(q)
	if err != nil {
		return err
	}
	res, err := a.d.Variables.List(r.Context(), page)
	if err != nil {
		return fmt.Errorf("listing variables: %w", err)
	}
	return a.writeJSON(w, r, http.StatusOK, listOf(res, toVariableDTO))
}

func (a *API) createVariable(w http.ResponseWriter, r *http.Request) error {
	var in variableCreateRequest
	if err := decodeJSON(r, &in, false); err != nil {
		return err
	}
	v, err := a.d.Variables.Create(r.Context(), variable.Input{Key: in.Key, Value: in.Value, Secret: in.Secret})
	if err != nil {
		return fmt.Errorf("creating variable: %w", err)
	}
	w.Header().Set("Location", v1("/variables/"+string(v.ID)))
	return a.writeJSON(w, r, http.StatusCreated, toVariableDTO(v))
}

func (a *API) updateVariable(w http.ResponseWriter, r *http.Request) error {
	id, err := variableID(r)
	if err != nil {
		return err
	}
	var in variablePatchRequest
	if err := decodeJSON(r, &in, false); err != nil {
		return err
	}
	v, err := a.d.Variables.Update(r.Context(), id, variable.Patch{Key: in.Key, Value: in.Value, Secret: in.Secret})
	if err != nil {
		return fmt.Errorf("updating variable: %w", err)
	}
	return a.writeJSON(w, r, http.StatusOK, toVariableDTO(v))
}

func (a *API) deleteVariable(w http.ResponseWriter, r *http.Request) error {
	id, err := variableID(r)
	if err != nil {
		return err
	}
	if err := a.d.Variables.Delete(r.Context(), id); err != nil {
		return fmt.Errorf("deleting variable: %w", err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
