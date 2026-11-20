package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/service/credential"
)

func credentialID(r *http.Request) (domain.CredentialID, error) {
	return idParam(r, "id", domain.CredentialID.Validate)
}

type credentialRequest struct {
	Name string         `json:"name"`
	Type string         `json:"type"`
	Data map[string]any `json:"data"`
}

func (a *API) listCredentials(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	if err := allowParams(q, "limit", "cursor"); err != nil {
		return err
	}
	page, err := a.pageOf(q)
	if err != nil {
		return err
	}
	res, err := a.d.Credentials.List(r.Context(), page)
	if err != nil {
		return fmt.Errorf("listing credentials: %w", err)
	}
	return a.writeJSON(w, r, http.StatusOK, listOf(res, toCredentialSummary))
}

func (a *API) createCredential(w http.ResponseWriter, r *http.Request) error {
	var in credentialRequest
	if err := decodeJSON(r, &in, false); err != nil {
		return err
	}
	v, err := a.d.Credentials.Create(r.Context(), credential.Input{Name: in.Name, Type: in.Type, Data: in.Data})
	if err != nil {
		return fmt.Errorf("creating credential: %w", err)
	}
	w.Header().Set("Location", v1("/credentials/"+string(v.ID)))
	return a.writeJSON(w, r, http.StatusCreated, toCredentialDTO(v))
}

func (a *API) getCredential(w http.ResponseWriter, r *http.Request) error {
	id, err := credentialID(r)
	if err != nil {
		return err
	}
	v, err := a.d.Credentials.Get(r.Context(), id)
	if err != nil {
		return fmt.Errorf("getting credential: %w", err)
	}
	return a.writeJSON(w, r, http.StatusOK, toCredentialDTO(v))
}

func (a *API) updateCredential(w http.ResponseWriter, r *http.Request) error {
	id, err := credentialID(r)
	if err != nil {
		return err
	}
	var in credentialRequest
	if err := decodeJSON(r, &in, false); err != nil {
		return err
	}
	v, err := a.d.Credentials.Update(r.Context(), id, credential.Input{Name: in.Name, Type: in.Type, Data: in.Data})
	if err != nil {
		return fmt.Errorf("updating credential: %w", err)
	}
	return a.writeJSON(w, r, http.StatusOK, toCredentialDTO(v))
}

func (a *API) deleteCredential(w http.ResponseWriter, r *http.Request) error {
	id, err := credentialID(r)
	if err != nil {
		return err
	}
	if err := a.d.Credentials.Delete(r.Context(), id); err != nil {
		return fmt.Errorf("deleting credential: %w", err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

type testResultDTO struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

// Messages of a failed credential test. They are fixed on purpose: the
// underlying error can contain the request URL, which may carry a secret.
const (
	msgTestFailed  = "the test request failed; check the credential values"
	msgNeedsReauth = "the credential must be connected again"
)

// testCredential runs the credential's test. A failing test is an answer
// (success false), not an error of this API call.
func (a *API) testCredential(w http.ResponseWriter, r *http.Request) error {
	id, err := credentialID(r)
	if err != nil {
		return err
	}
	err = a.d.Credentials.Test(r.Context(), id)
	switch {
	case err == nil:
		return a.writeJSON(w, r, http.StatusOK, testResultDTO{Success: true})
	case errors.Is(err, domain.ErrNotFound), errors.Is(err, domain.ErrNoTest), isValidation(err):
		return fmt.Errorf("testing credential: %w", err)
	case errors.Is(err, domain.ErrNeedsReauth):
		return a.writeJSON(w, r, http.StatusOK, testResultDTO{Message: msgNeedsReauth})
	case r.Context().Err() != nil:
		return fmt.Errorf("testing credential: %w", r.Context().Err())
	}
	return a.writeJSON(w, r, http.StatusOK, testResultDTO{Message: msgTestFailed})
}

func isValidation(err error) bool {
	var v *domain.ValidationError
	return errors.As(err, &v)
}

type authorizeDTO struct {
	URL string `json:"url"`
}

// authorizeCredential starts the OAuth2 authorization-code flow and returns the
// provider URL the UI sends the user to.
func (a *API) authorizeCredential(w http.ResponseWriter, r *http.Request) error {
	id, err := credentialID(r)
	if err != nil {
		return err
	}
	u, err := a.d.Credentials.BeginAuthorization(r.Context(), id)
	if err != nil {
		return fmt.Errorf("starting authorization: %w", err)
	}
	return a.writeJSON(w, r, http.StatusOK, authorizeDTO{URL: u})
}

type authorizedDTO struct {
	CredentialID domain.CredentialID `json:"credentialId"`
}

// oauth2Callback is where the provider sends the browser back. The sealed
// state authenticates the request, so the route needs no session. Provider
// error parameters are reported without being echoed into the page.
func (a *API) oauth2Callback(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	if err := allowParams(q, "state", "code", "error", "error_description", "error_uri", "iss", "session_state"); err != nil {
		return err
	}
	if q.Get("error") != "" {
		return badRequest("the provider denied the authorization")
	}
	id, err := a.d.Credentials.CompleteAuthorization(r.Context(), q.Get("state"), q.Get("code"))
	if err != nil {
		return fmt.Errorf("completing authorization: %w", err)
	}
	if target := a.d.Server.OAuth2CompleteRedirect; target != "" {
		u, perr := url.Parse(target)
		if perr == nil {
			qq := u.Query()
			qq.Set("credentialId", string(id))
			u.RawQuery = qq.Encode()
			http.Redirect(w, r, u.String(), http.StatusSeeOther)
			return nil
		}
	}
	return a.writeJSON(w, r, http.StatusOK, authorizedDTO{CredentialID: id})
}
