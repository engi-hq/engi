package jsoncodec

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/service/engine"
)

type stateDTO struct {
	ExecutionID string                  `json:"executionId"`
	Status      string                  `json:"status"`
	StartedAt   time.Time               `json:"startedAt,omitzero"`
	FinishedAt  time.Time               `json:"finishedAt,omitzero"`
	Queue       []taskDTO               `json:"queue,omitempty"`
	Waiting     map[string]waitingDTO   `json:"waiting,omitempty"`
	RunData     map[string][]nodeRunDTO `json:"runData"`
	Order       []runRefDTO             `json:"order,omitempty"`
	Pending     *pendingDTO             `json:"pending,omitempty"`
	Error       *errorDTO               `json:"error,omitempty"`
	// ResumeSecret is absent in states saved before execution resume secrets.
	ResumeSecret string `json:"resumeSecret,omitempty"`
}

type runRefDTO struct {
	Node     string `json:"node"`
	RunIndex int    `json:"runIndex"`
}

type sourceDTO struct {
	Node        string `json:"node,omitempty"`
	OutputIndex int    `json:"outputIndex,omitempty"`
	RunIndex    int    `json:"runIndex,omitempty"`
}

type taskDTO struct {
	Node    string      `json:"node"`
	Inputs  [][]itemDTO `json:"inputs"`
	Sources []sourceDTO `json:"sources"`
}

type waitingDTO struct {
	Inputs  [][]itemDTO `json:"inputs"`
	Sources []sourceDTO `json:"sources"`
	Filled  []bool      `json:"filled"`
}

type pendingDTO struct {
	Node        string      `json:"node"`
	RunIndex    int         `json:"runIndex"`
	Until       time.Time   `json:"until,omitzero"`
	ResumeToken string      `json:"resumeToken,omitempty"`
	Output      [][]itemDTO `json:"output"`
	Resumed     bool        `json:"resumed,omitempty"`
	ResumeItems []itemDTO   `json:"resumeItems,omitempty"`
}

type nodeRunDTO struct {
	RunIndex   int                    `json:"runIndex"`
	StartedAt  time.Time              `json:"startedAt,omitzero"`
	DurationMs int64                  `json:"durationMs"`
	Status     string                 `json:"status"`
	Attempts   int                    `json:"attempts,omitempty"`
	Sources    []sourceDTO            `json:"sources,omitempty"`
	Input      map[string][][]itemDTO `json:"input,omitempty"`
	Output     map[string][][]itemDTO `json:"output,omitempty"`
	Error      *errorDTO              `json:"error,omitempty"`
}

// EncodeState serializes an execution state so a waiting execution can be stored.
func EncodeState(s *engine.State) ([]byte, error) {
	d := stateDTO{
		ExecutionID: string(s.ExecutionID), Status: string(s.Status),
		StartedAt: s.StartedAt, FinishedAt: s.FinishedAt,
		RunData: make(map[string][]nodeRunDTO, len(s.RunData)),
		Error:   errorToDTO(s.Error), ResumeSecret: s.ResumeSecret,
	}
	for _, t := range s.Queue {
		d.Queue = append(d.Queue, taskDTO{Node: t.Node, Inputs: portsToDTO(t.Inputs), Sources: sourcesToDTO(t.Sources)})
	}
	if len(s.Waiting) > 0 {
		d.Waiting = make(map[string]waitingDTO, len(s.Waiting))
		for k, w := range s.Waiting {
			d.Waiting[k] = waitingDTO{Inputs: portsToDTO(w.Inputs), Sources: sourcesToDTO(w.Sources), Filled: w.Filled}
		}
	}
	for node, runs := range s.RunData {
		for _, r := range runs {
			d.RunData[node] = append(d.RunData[node], runToDTO(r))
		}
	}
	for _, o := range s.Order {
		d.Order = append(d.Order, runRefDTO(o))
	}
	if p := s.Pending; p != nil {
		d.Pending = &pendingDTO{Node: p.Node, RunIndex: p.RunIndex, Until: p.Until, ResumeToken: p.ResumeToken, Output: portsToDTO(p.Output), Resumed: p.Resumed, ResumeItems: itemsToDTO(p.ResumeItems)}
	}
	out, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding state: %w", err)
	}
	return out, nil
}

// DecodeState is the inverse of EncodeState.
func DecodeState(data []byte) (*engine.State, error) {
	var d stateDTO
	if err := decodeStrict(data, &d); err != nil {
		return nil, fmt.Errorf("decoding state: %w", err)
	}
	s := &engine.State{
		ExecutionID: domain.ExecutionID(d.ExecutionID), Status: domain.ExecutionStatus(d.Status),
		StartedAt: d.StartedAt, FinishedAt: d.FinishedAt,
		Waiting: map[string]engine.WaitingInputs{},
		RunData: make(map[string][]domain.NodeRun, len(d.RunData)),
		Error:   d.Error.toDomain(), ResumeSecret: d.ResumeSecret,
	}
	for _, t := range d.Queue {
		s.Queue = append(s.Queue, engine.Task{Node: t.Node, Inputs: portsToDomain(t.Inputs), Sources: sourcesToDomain(t.Sources)})
	}
	for k, w := range d.Waiting {
		s.Waiting[k] = engine.WaitingInputs{Inputs: portsToDomain(w.Inputs), Sources: sourcesToDomain(w.Sources), Filled: w.Filled}
	}
	for node, runs := range d.RunData {
		for _, r := range runs {
			s.RunData[node] = append(s.RunData[node], runToDomain(r))
		}
	}
	for _, o := range d.Order {
		s.Order = append(s.Order, engine.RunRef(o))
	}
	if p := d.Pending; p != nil {
		s.Pending = &engine.PendingWait{Node: p.Node, RunIndex: p.RunIndex, Until: p.Until, ResumeToken: p.ResumeToken, Output: portsToDomain(p.Output), Resumed: p.Resumed, ResumeItems: itemsToDomain(p.ResumeItems)}
	}
	return s, nil
}

func portsToDTO(ports [][]domain.Item) [][]itemDTO {
	out := make([][]itemDTO, len(ports))
	for i, p := range ports {
		out[i] = itemsToDTO(p)
		if out[i] == nil {
			out[i] = []itemDTO{}
		}
	}
	return out
}

func portsToDomain(ports [][]itemDTO) [][]domain.Item {
	out := make([][]domain.Item, len(ports))
	for i, p := range ports {
		out[i] = itemsToDomain(p)
	}
	return out
}

func sourcesToDTO(src []domain.Source) []sourceDTO {
	out := make([]sourceDTO, len(src))
	for i, s := range src {
		out[i] = sourceDTO(s)
	}
	return out
}

func sourcesToDomain(src []sourceDTO) []domain.Source {
	out := make([]domain.Source, len(src))
	for i, s := range src {
		out[i] = domain.Source(s)
	}
	return out
}

func portDataToDTO(p domain.PortData) map[string][][]itemDTO {
	if p == nil {
		return nil
	}
	out := make(map[string][][]itemDTO, len(p))
	for k, v := range p {
		out[string(k)] = portsToDTO(v)
	}
	return out
}

func portDataToDomain(p map[string][][]itemDTO) domain.PortData {
	if p == nil {
		return nil
	}
	out := make(domain.PortData, len(p))
	for k, v := range p {
		out[domain.PortKind(k)] = portsToDomain(v)
	}
	return out
}

func runToDTO(r domain.NodeRun) nodeRunDTO {
	return nodeRunDTO{
		RunIndex: r.RunIndex, StartedAt: r.StartedAt, DurationMs: r.Duration.Milliseconds(),
		Status: string(r.Status), Attempts: r.Attempts, Sources: sourcesToDTO(r.Sources),
		Input: portDataToDTO(r.Input), Output: portDataToDTO(r.Output), Error: errorToDTO(r.Error),
	}
}

func runToDomain(r nodeRunDTO) domain.NodeRun {
	return domain.NodeRun{
		RunIndex: r.RunIndex, StartedAt: r.StartedAt, Duration: time.Duration(r.DurationMs) * time.Millisecond,
		Status: domain.NodeRunStatus(r.Status), Attempts: r.Attempts, Sources: sourcesToDomain(r.Sources),
		Input: portDataToDomain(r.Input), Output: portDataToDomain(r.Output), Error: r.Error.toDomain(),
	}
}
