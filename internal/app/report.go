package app

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/service/engine"
)

// Report is the JSON summary `engi run` prints: the execution outcome, a
// per-node run summary and the output of the node that finished last.
type Report struct {
	ExecutionID string                 `json:"executionId"`
	Status      string                 `json:"status"`
	Error       *ReportError           `json:"error,omitempty"`
	Pending     *ReportPending         `json:"pending,omitempty"`
	StartedAt   time.Time              `json:"startedAt"`
	FinishedAt  *time.Time             `json:"finishedAt,omitempty"`
	RunData     map[string][]RunReport `json:"runData"`
	Output      *OutputReport          `json:"output,omitempty"`
}

// ReportError describes why an execution failed.
type ReportError struct {
	Kind        string `json:"kind"`
	Node        string `json:"node,omitempty"`
	Message     string `json:"message"`
	Description string `json:"description,omitempty"`
	HTTPStatus  int    `json:"httpStatus,omitempty"`
}

// ReportPending says what a waiting execution waits for.
type ReportPending struct {
	Node        string     `json:"node"`
	Until       *time.Time `json:"until,omitempty"`
	ResumeToken string     `json:"resumeToken,omitempty"`
}

// RunReport summarises one run of one node.
type RunReport struct {
	RunIndex   int          `json:"runIndex"`
	Status     string       `json:"status"`
	DurationMs int64        `json:"durationMs"`
	Attempts   int          `json:"attempts,omitempty"`
	Items      []int        `json:"items"`
	Error      *ReportError `json:"error,omitempty"`
}

// OutputReport holds the output ports of the node that finished last.
type OutputReport struct {
	Node  string         `json:"node"`
	Ports [][]ReportItem `json:"ports"`
}

// ReportItem is an item as printed.
type ReportItem struct {
	JSON   map[string]any `json:"json"`
	Binary map[string]any `json:"binary,omitempty"`
	Error  *ReportError   `json:"error,omitempty"`
}

// BuildReport summarises an execution state.
func BuildReport(s *engine.State) Report {
	r := Report{
		ExecutionID: string(s.ExecutionID), Status: string(s.Status), StartedAt: s.StartedAt,
		Error: reportError(s.Error), RunData: map[string][]RunReport{},
	}
	if !s.FinishedAt.IsZero() {
		r.FinishedAt = &s.FinishedAt
	}
	if p := s.Pending; p != nil {
		r.Pending = &ReportPending{Node: p.Node, ResumeToken: p.ResumeToken}
		if !p.Until.IsZero() {
			r.Pending.Until = &p.Until
		}
	}
	names := make([]string, 0, len(s.RunData))
	for n := range s.RunData {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		for _, run := range s.RunData[n] {
			rr := RunReport{
				RunIndex: run.RunIndex, Status: string(run.Status), DurationMs: run.Duration.Milliseconds(),
				Attempts: run.Attempts, Error: reportError(run.Error),
			}
			for _, port := range run.Output[domain.PortMain] {
				rr.Items = append(rr.Items, len(port))
			}
			r.RunData[n] = append(r.RunData[n], rr)
		}
	}
	if len(s.Order) > 0 {
		last := s.Order[len(s.Order)-1]
		if runs := s.RunData[last.Node]; last.RunIndex < len(runs) {
			out := &OutputReport{Node: last.Node}
			for _, port := range runs[last.RunIndex].Output[domain.PortMain] {
				items := make([]ReportItem, len(port))
				for i, it := range port {
					items[i] = reportItem(it)
				}
				out.Ports = append(out.Ports, items)
			}
			r.Output = out
		}
	}
	return r
}

func reportItem(it domain.Item) ReportItem {
	ri := ReportItem{JSON: it.JSON, Error: reportError(it.Error)}
	if ri.JSON == nil {
		ri.JSON = map[string]any{}
	}
	if len(it.Binary) > 0 {
		ri.Binary = map[string]any{}
		for k, b := range it.Binary {
			ri.Binary[k] = map[string]any{"ref": b.Ref, "mime": b.MIME, "name": b.Name, "size": b.Size}
		}
	}
	return ri
}

func reportError(e *domain.ErrorInfo) *ReportError {
	if e == nil {
		return nil
	}
	return &ReportError{Kind: string(e.Kind), Node: e.Node, Message: e.Message, Description: e.Description, HTTPStatus: e.HTTPStatus}
}

// Encode renders the report as indented JSON.
func (r Report) Encode() ([]byte, error) {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding report: %w", err)
	}
	return b, nil
}
