package api

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/engi-hq/engi/internal/adapter/jsoncodec"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/service/credential"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// The DTOs below are the wire format. The domain carries no JSON tags; this is
// the only place that decides field names. Workflow documents and run data use
// the shared document format of the jsoncodec package (docs/03).

type userDTO struct {
	ID        domain.UserID `json:"id"`
	Email     string        `json:"email"`
	Name      string        `json:"name"`
	Role      domain.Role   `json:"role"`
	Disabled  bool          `json:"disabled"`
	CreatedAt time.Time     `json:"createdAt"`
	UpdatedAt time.Time     `json:"updatedAt"`
}

func toUserDTO(u domain.User) userDTO {
	return userDTO{ID: u.ID, Email: u.Email, Name: u.Name, Role: u.Role, Disabled: u.Disabled, CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt}
}

type apiKeyDTO struct {
	ID         domain.APIKeyID `json:"id"`
	Name       string          `json:"name"`
	Prefix     string          `json:"prefix"`
	Scopes     []domain.Scope  `json:"scopes"`
	ExpiresAt  *time.Time      `json:"expiresAt,omitempty"`
	LastUsedAt *time.Time      `json:"lastUsedAt,omitempty"`
	RevokedAt  *time.Time      `json:"revokedAt,omitempty"`
	CreatedAt  time.Time       `json:"createdAt"`
}

func toAPIKeyDTO(k domain.APIKey) apiKeyDTO {
	return apiKeyDTO{ID: k.ID, Name: k.Name, Prefix: k.Prefix, Scopes: k.Scopes, ExpiresAt: k.ExpiresAt,
		LastUsedAt: k.LastUsedAt, RevokedAt: k.RevokedAt, CreatedAt: k.CreatedAt}
}

// workflowSummaryDTO is a workflow in a list: no graph.
type workflowSummaryDTO struct {
	ID        domain.WorkflowID `json:"id"`
	Name      string            `json:"name"`
	Active    bool              `json:"active"`
	Version   int               `json:"version"`
	Tags      []string          `json:"tags"`
	NodeCount int               `json:"nodeCount"`
}

func toWorkflowSummary(w domain.Workflow) workflowSummaryDTO {
	tags := w.Tags
	if tags == nil {
		tags = []string{}
	}
	return workflowSummaryDTO{ID: w.ID, Name: w.Name, Active: w.Active, Version: w.Version, Tags: tags, NodeCount: len(w.Nodes)}
}

// workflowDoc renders a workflow as its document (a json.RawMessage, so the
// handler layer never re-encodes it).
func workflowDoc(w domain.Workflow) (json.RawMessage, error) {
	b, err := jsoncodec.EncodeWorkflow(w)
	if err != nil {
		return nil, fmt.Errorf("encoding workflow: %w", err)
	}
	return b, nil
}

type versionSummaryDTO struct {
	Version   int       `json:"version"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
}

type versionDTO struct {
	Version   int             `json:"version"`
	CreatedAt time.Time       `json:"createdAt"`
	Workflow  json.RawMessage `json:"workflow"`
}

type errorInfoDTO struct {
	Kind        domain.ErrorKind `json:"kind"`
	Node        string           `json:"node,omitempty"`
	Message     string           `json:"message"`
	Description string           `json:"description,omitempty"`
	HTTPStatus  int              `json:"httpStatus,omitempty"`
	ItemIndex   *int             `json:"itemIndex,omitempty"`
}

func toErrorInfoDTO(e *domain.ErrorInfo) *errorInfoDTO {
	if e == nil {
		return nil
	}
	return &errorInfoDTO{Kind: e.Kind, Node: e.Node, Message: e.Message, Description: e.Description, HTTPStatus: e.HTTPStatus, ItemIndex: e.ItemIndex}
}

// executionDTO is an execution; RunData is present only on the detail view.
type executionDTO struct {
	ID                domain.ExecutionID     `json:"id"`
	WorkflowID        domain.WorkflowID      `json:"workflowId"`
	WorkflowVersion   int                    `json:"workflowVersion"`
	Mode              domain.ExecutionMode   `json:"mode"`
	Status            domain.ExecutionStatus `json:"status"`
	StartedAt         time.Time              `json:"startedAt"`
	FinishedAt        *time.Time             `json:"finishedAt,omitempty"`
	WaitUntil         *time.Time             `json:"waitUntil,omitempty"`
	ParentExecutionID *domain.ExecutionID    `json:"parentExecutionId,omitempty"`
	// Attempts counts how many times a worker started the run (1 for a run
	// that went through once). LastRecoveredAt is set when a dead worker lost it.
	Attempts        int             `json:"attempts"`
	LastRecoveredAt *time.Time      `json:"lastRecoveredAt,omitempty"`
	Error           *errorInfoDTO   `json:"error,omitempty"`
	RunData         json.RawMessage `json:"runData,omitempty"`
}

func toExecutionDTO(e domain.Execution) executionDTO {
	return executionDTO{ID: e.ID, WorkflowID: e.WorkflowID, WorkflowVersion: e.WorkflowVersion, Mode: e.Mode, Status: e.Status,
		StartedAt: e.StartedAt, FinishedAt: e.FinishedAt, WaitUntil: e.WaitUntil, ParentExecutionID: e.ParentExecutionID,
		Attempts: e.Attempts, LastRecoveredAt: e.LastRecoveredAt, Error: toErrorInfoDTO(e.Error)}
}

func toExecutionDetail(e domain.Execution) (executionDTO, error) {
	dto := toExecutionDTO(e)
	runs, err := jsoncodec.EncodeRunData(e.RunData)
	if err != nil {
		return executionDTO{}, fmt.Errorf("encoding run data: %w", err)
	}
	dto.RunData = runs
	return dto, nil
}

type credentialSummaryDTO struct {
	ID        domain.CredentialID `json:"id"`
	Name      string              `json:"name"`
	Type      string              `json:"type"`
	CreatedAt time.Time           `json:"createdAt"`
	UpdatedAt time.Time           `json:"updatedAt"`
}

func toCredentialSummary(c domain.Credential) credentialSummaryDTO {
	return credentialSummaryDTO{ID: c.ID, Name: c.Name, Type: c.Type, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
}

// credentialDTO is a credential with its secrets already replaced by the
// service's sentinel.
type credentialDTO struct {
	ID          domain.CredentialID `json:"id"`
	Name        string              `json:"name"`
	Type        string              `json:"type"`
	Data        map[string]any      `json:"data"`
	NeedsReauth bool                `json:"needsReauth"`
	CreatedAt   time.Time           `json:"createdAt"`
	UpdatedAt   time.Time           `json:"updatedAt"`
}

func toCredentialDTO(v credential.View) credentialDTO {
	return credentialDTO{ID: v.ID, Name: v.Name, Type: v.Type, Data: v.Data, NeedsReauth: v.NeedsReauth, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
}

type optionDTO struct {
	Name        string `json:"name"`
	Value       any    `json:"value"`
	Description string `json:"description,omitempty"`
}

type displayOptionsDTO struct {
	Show map[string][]any `json:"show,omitempty"`
	Hide map[string][]any `json:"hide,omitempty"`
}

type propertyDTO struct {
	Name           string             `json:"name"`
	DisplayName    string             `json:"displayName,omitempty"`
	Type           string             `json:"type"`
	Default        any                `json:"default,omitempty"`
	Required       bool               `json:"required,omitempty"`
	Placeholder    string             `json:"placeholder,omitempty"`
	Description    string             `json:"description,omitempty"`
	Options        []optionDTO        `json:"options,omitempty"`
	Children       []propertyDTO      `json:"children,omitempty"`
	DisplayOptions *displayOptionsDTO `json:"displayOptions,omitempty"`
}

func toPropertyDTO(p nodesdk.Property) propertyDTO {
	dto := propertyDTO{Name: p.Name, DisplayName: p.DisplayName, Type: string(p.Type), Default: p.Default,
		Required: p.Required, Placeholder: p.Placeholder, Description: p.Description}
	for _, o := range p.Options {
		dto.Options = append(dto.Options, optionDTO{Name: o.Name, Value: o.Value, Description: o.Description})
	}
	for _, c := range p.Children {
		dto.Children = append(dto.Children, toPropertyDTO(c))
	}
	if d := p.DisplayOptions; d != nil {
		dto.DisplayOptions = &displayOptionsDTO{Show: d.Show, Hide: d.Hide}
	}
	return dto
}

type portDTO struct {
	Name        string `json:"name,omitempty"`
	Kind        string `json:"kind"`
	DisplayName string `json:"displayName,omitempty"`
	Required    bool   `json:"required,omitempty"`
	// MaxConnections caps sub-port input connections; absent means unlimited.
	MaxConnections int `json:"maxConnections,omitempty"`
}

type credentialRefDTO struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required,omitempty"`
}

type nodeTypeDTO struct {
	Type        string             `json:"type"`
	Version     int                `json:"version"`
	DisplayName string             `json:"displayName"`
	Description string             `json:"description,omitempty"`
	Group       string             `json:"group"`
	Icon        string             `json:"icon,omitempty"`
	Inputs      []portDTO          `json:"inputs"`
	Outputs     []portDTO          `json:"outputs"`
	Properties  []propertyDTO      `json:"properties"`
	Credentials []credentialRefDTO `json:"credentials"`
	MergePolicy string             `json:"mergePolicy,omitempty"`
}

func toPortDTOs(ports []nodesdk.PortSpec) []portDTO {
	out := make([]portDTO, len(ports))
	for i, p := range ports {
		kind := string(p.Kind)
		if kind == "" {
			kind = string(nodesdk.PortMain)
		}
		out[i] = portDTO{Name: p.Name, Kind: kind, DisplayName: p.DisplayName, Required: p.Required, MaxConnections: p.MaxConnections}
	}
	return out
}

func toNodeTypeDTO(d nodesdk.Descriptor) nodeTypeDTO {
	dto := nodeTypeDTO{Type: d.Type, Version: d.Version, DisplayName: d.DisplayName, Description: d.Description,
		Group: string(d.Group), Icon: d.Icon, Inputs: toPortDTOs(d.Inputs), Outputs: toPortDTOs(d.Outputs),
		Properties: make([]propertyDTO, len(d.Properties)), Credentials: make([]credentialRefDTO, len(d.Credentials)),
		MergePolicy: string(d.MergePolicy)}
	for i, p := range d.Properties {
		dto.Properties[i] = toPropertyDTO(p)
	}
	for i, c := range d.Credentials {
		dto.Credentials[i] = credentialRefDTO{Name: c.Name, Type: c.Type, Required: c.Required}
	}
	return dto
}

type credentialFieldDTO struct {
	propertyDTO
	Secret  bool `json:"secret,omitempty"`
	Managed bool `json:"managed,omitempty"`
}

type credentialTypeDTO struct {
	Name        string               `json:"name"`
	DisplayName string               `json:"displayName"`
	Icon        string               `json:"icon,omitempty"`
	Fields      []credentialFieldDTO `json:"fields"`
	Testable    bool                 `json:"testable"`
	OAuth2      bool                 `json:"oauth2"`
	// Authenticates is true when HTTP nodes can apply the credential.
	Authenticates bool `json:"authenticates"`
}

func toCredentialTypeDTO(t nodesdk.CredentialType) credentialTypeDTO {
	dto := credentialTypeDTO{Name: t.Name, DisplayName: t.DisplayName, Icon: t.Icon, Fields: make([]credentialFieldDTO, 0, len(t.Fields)),
		Testable: t.Test != nil || t.OAuth2 != nil, OAuth2: t.OAuth2 != nil, Authenticates: t.Authenticate != nil}
	for _, f := range t.Fields {
		if f.Managed {
			continue // system-owned fields are not part of the form
		}
		dto.Fields = append(dto.Fields, credentialFieldDTO{propertyDTO: toPropertyDTO(f.Property), Secret: f.Secret})
	}
	return dto
}
