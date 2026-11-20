// Package jsoncodec maps the domain and engine types to and from their JSON
// wire/file format (docs/03). The domain carries no JSON tags; the format lives
// here so it can evolve independently.
package jsoncodec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/engi-hq/engi/internal/domain"
)

type workflowDTO struct {
	ID          string               `json:"id,omitempty"`
	Name        string               `json:"name"`
	Active      bool                 `json:"active,omitempty"`
	Version     int                  `json:"version,omitempty"`
	Settings    settingsDTO          `json:"settings,omitzero"`
	Nodes       []nodeDTO            `json:"nodes"`
	Connections []connectionDTO      `json:"connections"`
	PinData     map[string][]itemDTO `json:"pinData,omitempty"`
	Tags        []string             `json:"tags,omitempty"`
	Notes       []noteDTO            `json:"notes,omitempty"`
}

type noteDTO struct {
	ID       string     `json:"id"`
	Text     string     `json:"text,omitempty"`
	Position [2]float64 `json:"position"`
	Size     sizeDTO    `json:"size"`
	Color    string     `json:"color,omitempty"`
}

type sizeDTO struct {
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type settingsDTO struct {
	TimeoutSeconds  float64 `json:"timeoutSeconds,omitempty"`
	ErrorWorkflowID string  `json:"errorWorkflowId,omitempty"`
	SaveSuccessData bool    `json:"saveSuccessData,omitempty"`
}

type nodeDTO struct {
	ID          string            `json:"id,omitempty"`
	Name        string            `json:"name"`
	Type        string            `json:"type"`
	TypeVersion int               `json:"typeVersion,omitempty"`
	Position    [2]float64        `json:"position"`
	Parameters  map[string]any    `json:"parameters,omitempty"`
	Credentials map[string]string `json:"credentials,omitempty"`
	Disabled    bool              `json:"disabled,omitempty"`
	Settings    nodeSettingsDTO   `json:"settings,omitzero"`
	Notes       string            `json:"notes,omitempty"`
}

type nodeSettingsDTO struct {
	Retry            retryDTO `json:"retry,omitzero"`
	OnError          string   `json:"onError,omitempty"`
	AlwaysOutputData bool     `json:"alwaysOutputData,omitempty"`
	ExecuteOnce      bool     `json:"executeOnce,omitempty"`
	TimeoutMs        int64    `json:"timeoutMs,omitempty"`
}

type retryDTO struct {
	MaxAttempts    int    `json:"maxAttempts,omitempty"`
	Backoff        string `json:"backoff,omitempty"`
	InitialDelayMs int64  `json:"initialDelayMs,omitempty"`
}

type endpointDTO struct {
	Node  string `json:"node"`
	Port  string `json:"port,omitempty"`
	Index int    `json:"index,omitempty"`
}

type connectionDTO struct {
	From endpointDTO `json:"from"`
	To   endpointDTO `json:"to"`
}

// DecodeWorkflow parses a workflow document. Unknown fields are rejected. An
// omitted connection port means "main". The result is not validated; call
// Workflow.Validate.
func DecodeWorkflow(data []byte) (domain.Workflow, error) {
	var dto workflowDTO
	if err := decodeStrict(data, &dto); err != nil {
		return domain.Workflow{}, fmt.Errorf("decoding workflow: %w", err)
	}
	return dto.toDomain(), nil
}

// EncodeWorkflow renders a workflow document.
func EncodeWorkflow(w domain.Workflow) ([]byte, error) {
	out, err := json.MarshalIndent(workflowToDTO(w), "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding workflow: %w", err)
	}
	return out, nil
}

func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("parsing json: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("parsing json: unexpected data after the document")
	}
	return nil
}

func (d workflowDTO) toDomain() domain.Workflow {
	w := domain.Workflow{
		ID: domain.WorkflowID(d.ID), Name: d.Name, Active: d.Active, Version: d.Version, Tags: d.Tags,
		Settings: domain.WorkflowSettings{
			Timeout:         time.Duration(d.Settings.TimeoutSeconds * float64(time.Second)),
			ErrorWorkflowID: domain.WorkflowID(d.Settings.ErrorWorkflowID),
			SaveSuccessData: d.Settings.SaveSuccessData,
		},
	}
	for _, n := range d.Nodes {
		dn := domain.Node{
			ID: domain.NodeID(n.ID), Name: n.Name, Type: n.Type, TypeVersion: n.TypeVersion,
			Position: domain.Position{X: n.Position[0], Y: n.Position[1]}, Parameters: n.Parameters,
			Disabled: n.Disabled, Notes: n.Notes,
			Settings: domain.NodeSettings{
				Retry: domain.RetryPolicy{
					MaxAttempts: n.Settings.Retry.MaxAttempts, Backoff: domain.Backoff(n.Settings.Retry.Backoff),
					InitialDelay: time.Duration(n.Settings.Retry.InitialDelayMs) * time.Millisecond,
				},
				OnError:          domain.OnError(n.Settings.OnError),
				AlwaysOutputData: n.Settings.AlwaysOutputData,
				ExecuteOnce:      n.Settings.ExecuteOnce,
				Timeout:          time.Duration(n.Settings.TimeoutMs) * time.Millisecond,
			},
		}
		if len(n.Credentials) > 0 {
			dn.Credentials = make(map[string]domain.CredentialID, len(n.Credentials))
			for k, v := range n.Credentials {
				dn.Credentials[k] = domain.CredentialID(v)
			}
		}
		w.Nodes = append(w.Nodes, dn)
	}
	for _, c := range d.Connections {
		w.Connections = append(w.Connections, domain.Connection{From: c.From.toDomain(), To: c.To.toDomain()})
	}
	if len(d.PinData) > 0 {
		w.PinData = make(map[string][]domain.Item, len(d.PinData))
		for k, items := range d.PinData {
			w.PinData[k] = itemsToDomain(items)
		}
	}
	for _, n := range d.Notes {
		w.Notes = append(w.Notes, domain.Note{
			ID: domain.NoteID(n.ID), Text: n.Text, Color: domain.NoteColor(n.Color),
			Position: domain.Position{X: n.Position[0], Y: n.Position[1]},
			Size:     domain.Size{Width: n.Size.Width, Height: n.Size.Height},
		})
	}
	return w
}

func (e endpointDTO) toDomain() domain.Endpoint {
	port := domain.PortKind(e.Port)
	if port == "" {
		port = domain.PortMain
	}
	return domain.Endpoint{Node: e.Node, Port: port, Index: e.Index}
}

func workflowToDTO(w domain.Workflow) workflowDTO {
	d := workflowDTO{
		ID: string(w.ID), Name: w.Name, Active: w.Active, Version: w.Version, Tags: w.Tags,
		Settings: settingsDTO{
			TimeoutSeconds:  w.Settings.Timeout.Seconds(),
			ErrorWorkflowID: string(w.Settings.ErrorWorkflowID),
			SaveSuccessData: w.Settings.SaveSuccessData,
		},
		Nodes:       make([]nodeDTO, 0, len(w.Nodes)),
		Connections: make([]connectionDTO, 0, len(w.Connections)),
	}
	for _, n := range w.Nodes {
		dn := nodeDTO{
			ID: string(n.ID), Name: n.Name, Type: n.Type, TypeVersion: n.TypeVersion,
			Position: [2]float64{n.Position.X, n.Position.Y}, Parameters: n.Parameters,
			Disabled: n.Disabled, Notes: n.Notes,
			Settings: nodeSettingsDTO{
				Retry: retryDTO{
					MaxAttempts: n.Settings.Retry.MaxAttempts, Backoff: string(n.Settings.Retry.Backoff),
					InitialDelayMs: n.Settings.Retry.InitialDelay.Milliseconds(),
				},
				OnError:          string(n.Settings.OnError),
				AlwaysOutputData: n.Settings.AlwaysOutputData,
				ExecuteOnce:      n.Settings.ExecuteOnce,
				TimeoutMs:        n.Settings.Timeout.Milliseconds(),
			},
		}
		if len(n.Credentials) > 0 {
			dn.Credentials = make(map[string]string, len(n.Credentials))
			for k, v := range n.Credentials {
				dn.Credentials[k] = string(v)
			}
		}
		d.Nodes = append(d.Nodes, dn)
	}
	for _, c := range w.Connections {
		d.Connections = append(d.Connections, connectionDTO{
			From: endpointDTO{c.From.Node, string(c.From.Port), c.From.Index},
			To:   endpointDTO{c.To.Node, string(c.To.Port), c.To.Index},
		})
	}
	if len(w.PinData) > 0 {
		d.PinData = make(map[string][]itemDTO, len(w.PinData))
		for k, items := range w.PinData {
			d.PinData[k] = itemsToDTO(items)
		}
	}
	for _, n := range w.Notes {
		d.Notes = append(d.Notes, noteDTO{
			ID: string(n.ID), Text: n.Text, Color: string(n.Color),
			Position: [2]float64{n.Position.X, n.Position.Y},
			Size:     sizeDTO{Width: n.Size.Width, Height: n.Size.Height},
		})
	}
	return d
}
