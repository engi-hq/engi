package porttest

import (
	"crypto/rand"
	"sync"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/id"
	"github.com/engi-hq/engi/internal/port"
)

// FakeClock is a manually advanced port.Clock, safe for concurrent use.
type FakeClock struct {
	mu  sync.Mutex
	now time.Time
}

var _ port.Clock = (*FakeClock)(nil)

// NewFakeClock starts at a fixed instant.
func NewFakeClock() *FakeClock {
	return &FakeClock{now: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
}

// Now implements port.Clock.
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward by d.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// fixtures builds domain values with fresh, time-ordered IDs.
type fixtures struct {
	t   *testing.T
	gen *id.Generator
	clk *FakeClock
}

func newFixtures(t *testing.T, clk *FakeClock) *fixtures {
	t.Helper()
	return &fixtures{t: t, clk: clk, gen: id.NewGenerator(clk.Now, rand.Reader)}
}

func (f *fixtures) workflowID() domain.WorkflowID {
	f.t.Helper()
	v, err := f.gen.WorkflowID()
	if err != nil {
		f.t.Fatalf("generating workflow id: %v", err)
	}
	return v
}

func (f *fixtures) executionID() domain.ExecutionID {
	f.t.Helper()
	v, err := f.gen.ExecutionID()
	if err != nil {
		f.t.Fatalf("generating execution id: %v", err)
	}
	return v
}

func (f *fixtures) tagID() domain.TagID {
	f.t.Helper()
	v, err := f.gen.TagID()
	if err != nil {
		f.t.Fatalf("generating tag id: %v", err)
	}
	return v
}

func (f *fixtures) variableID() domain.VariableID {
	f.t.Helper()
	v, err := f.gen.VariableID()
	if err != nil {
		f.t.Fatalf("generating variable id: %v", err)
	}
	return v
}

func (f *fixtures) credentialID() domain.CredentialID {
	f.t.Helper()
	v, err := f.gen.CredentialID()
	if err != nil {
		f.t.Fatalf("generating credential id: %v", err)
	}
	return v
}

func (f *fixtures) jobID() domain.JobID {
	f.t.Helper()
	v, err := f.gen.New(domain.PrefixJob)
	if err != nil {
		f.t.Fatalf("generating job id: %v", err)
	}
	return domain.JobID(v)
}

// workflow returns a small two-node workflow with the given name and tags.
func (f *fixtures) workflow(name string, tags ...string) domain.Workflow {
	return domain.Workflow{
		ID:   f.workflowID(),
		Name: name,
		Nodes: []domain.Node{
			{ID: "nd_a", Name: "Start", Type: "engi.core.manual", TypeVersion: 1, Position: domain.Position{X: 1, Y: 2}},
			{ID: "nd_b", Name: "Set", Type: "engi.core.set", TypeVersion: 1, Position: domain.Position{X: 3, Y: 4},
				Parameters: map[string]any{"value": "={{ $json.a }}", "n": float64(3)}},
		},
		Connections: []domain.Connection{{
			From: domain.Endpoint{Node: "Start", Port: domain.PortMain},
			To:   domain.Endpoint{Node: "Set", Port: domain.PortMain},
		}},
		Tags: tags,
	}
}

// execution returns a queued execution of version v of workflow w, started now.
func (f *fixtures) execution(w domain.WorkflowID, v int) domain.Execution {
	return domain.Execution{
		ID: f.executionID(), WorkflowID: w, WorkflowVersion: v,
		Mode: domain.ModeManual, Status: domain.StatusQueued, StartedAt: f.clk.Now(),
	}
}
