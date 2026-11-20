package domain_test

import (
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/domain"
)

func TestExecutionLastOutput(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	run := func(idx int, start time.Duration, status domain.NodeRunStatus, v float64) domain.NodeRun {
		return domain.NodeRun{RunIndex: idx, StartedAt: t0.Add(start), Duration: time.Second, Status: status,
			Output: domain.PortData{domain.PortMain: {{{JSON: map[string]any{"v": v}}}}}}
	}
	ex := domain.Execution{RunData: map[string][]domain.NodeRun{
		"A": {run(0, 0, domain.NodeRunSuccess, 1)},
		"B": {run(0, 2*time.Second, domain.NodeRunSuccess, 2), run(1, 4*time.Second, domain.NodeRunSuccess, 3)},
		"C": {run(0, 9*time.Second, domain.NodeRunError, 4)},
		"D": {run(0, 9*time.Second, domain.NodeRunDisabled, 5)},
	}}
	if got := ex.LastOutput(); len(got) != 1 || got[0].JSON["v"] != 3.0 {
		t.Fatalf("last output = %v", got)
	}
	if got := (domain.Execution{}).LastOutput(); got != nil {
		t.Fatalf("empty execution = %v", got)
	}
}
