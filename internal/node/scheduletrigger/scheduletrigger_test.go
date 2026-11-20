package scheduletrigger

import (
	"strings"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

func TestSchedule(t *testing.T) {
	n := Node{defaultTimezone: "UTC"}
	tests := []struct {
		name   string
		params map[string]any
		want   nodesdk.ScheduleSpec
		err    string
	}{
		{"defaults to one minute", nil, nodesdk.ScheduleSpec{Interval: time.Minute, Timezone: "UTC"}, ""},
		{"hours", map[string]any{"interval": 2.0, "unit": "hours"}, nodesdk.ScheduleSpec{Interval: 2 * time.Hour, Timezone: "UTC"}, ""},
		{"weeks", map[string]any{"interval": 1.0, "unit": "weeks"}, nodesdk.ScheduleSpec{Interval: 7 * 24 * time.Hour, Timezone: "UTC"}, ""},
		{"cron with zone", map[string]any{"rule": "cron", "cronExpression": " 0 9 * * MON-FRI ", "timezone": "Europe/Berlin"},
			nodesdk.ScheduleSpec{Cron: "0 9 * * MON-FRI", Timezone: "Europe/Berlin"}, ""},
		{"cron missing", map[string]any{"rule": "cron"}, nodesdk.ScheduleSpec{}, "cronExpression is required"},
		{"cron invalid", map[string]any{"rule": "cron", "cronExpression": "61 * * * *"}, nodesdk.ScheduleSpec{}, "outside"},
		{"bad zone", map[string]any{"timezone": "Mars/Base"}, nodesdk.ScheduleSpec{}, "timezone"},
		{"bad unit", map[string]any{"unit": "eons"}, nodesdk.ScheduleSpec{}, "unknown unit"},
		{"too small", map[string]any{"interval": 0.1, "unit": "seconds"}, nodesdk.ScheduleSpec{}, "at least one second"},
		{"bad rule", map[string]any{"rule": "lunar"}, nodesdk.ScheduleSpec{}, "unknown rule"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := n.Schedule(tt.params)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("err = %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("got %+v, %v; want %+v", got, err, tt.want)
			}
		})
	}
}

func TestDescribeAndExecute(t *testing.T) {
	n := New("UTC")
	if err := n.Describe().Validate(); err != nil {
		t.Fatal(err)
	}
	out := nodetest.Run(t, n, nodetest.New(n.Describe(), nodetest.Items(map[string]any{"timestamp": "x"}), nil))
	if len(out.Ports[0]) != 1 || out.Ports[0][0].JSON["timestamp"] != "x" {
		t.Fatalf("out = %+v", out)
	}
}
