// Package scheduletrigger implements the Schedule trigger node. The trigger
// service fires it on an interval or a cron expression and hands it an item
// describing the fire time.
package scheduletrigger

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/engi-hq/engi/internal/cronexpr"
	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.trigger.schedule"

// Rules.
const (
	ruleInterval = "interval"
	ruleCron     = "cron"
)

// Interval units.
const (
	unitSeconds = "seconds"
	unitMinutes = "minutes"
	unitHours   = "hours"
	unitDays    = "days"
	unitWeeks   = "weeks"
)

var unitDurations = map[string]time.Duration{
	unitSeconds: time.Second, unitMinutes: time.Minute, unitHours: time.Hour,
	unitDays: 24 * time.Hour, unitWeeks: 7 * 24 * time.Hour,
}

// Node is the Schedule trigger.
type Node struct {
	defaultTimezone string
}

var _ nodesdk.ScheduleTrigger = Node{}

// New returns a Schedule trigger whose timezone parameter defaults to
// defaultTimezone (schedule.defaultTimezone).
func New(defaultTimezone string) nodesdk.Node { return Node{defaultTimezone: defaultTimezone} }

// Describe implements nodesdk.Node.
func (n Node) Describe() nodesdk.Descriptor {
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "Schedule Trigger", Group: nodesdk.GroupTrigger, Icon: "clock",
		Description: "Starts the workflow on an interval or a cron expression.",
		Outputs:     []nodesdk.PortSpec{nodekit.Main("main", "")},
		Properties: []nodesdk.Property{
			{Name: "rule", DisplayName: "Rule", Type: nodesdk.PropOptions, Default: ruleInterval, Options: []nodesdk.Option{
				{Name: "Every interval", Value: ruleInterval}, {Name: "Cron expression", Value: ruleCron}}},
			{Name: "interval", DisplayName: "Every", Type: nodesdk.PropNumber, Default: float64(1), DisplayOptions: nodekit.Show("rule", ruleInterval)},
			{Name: "unit", DisplayName: "Unit", Type: nodesdk.PropOptions, Default: unitMinutes, DisplayOptions: nodekit.Show("rule", ruleInterval), Options: []nodesdk.Option{
				{Name: "Seconds", Value: unitSeconds}, {Name: "Minutes", Value: unitMinutes}, {Name: "Hours", Value: unitHours},
				{Name: "Days", Value: unitDays}, {Name: "Weeks", Value: unitWeeks}}},
			{Name: "cronExpression", DisplayName: "Cron expression", Type: nodesdk.PropString, Placeholder: "0 9 * * MON-FRI",
				Description:    "Five fields (minute hour day month weekday) or six with leading seconds.",
				DisplayOptions: nodekit.Show("rule", ruleCron)},
			{Name: "timezone", DisplayName: "Timezone", Type: nodesdk.PropString, Default: n.defaultTimezone,
				Description: "IANA zone, for example Europe/Berlin. Cron expressions are evaluated in it."},
		},
	}
}

// Execute implements nodesdk.Node. The service supplies the fire-time item.
func (Node) Execute(_ context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	items := in.Items(0)
	out := make([]nodesdk.Item, len(items))
	for i, it := range items {
		out[i] = nodekit.Paired(it, i)
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
}

// Schedule implements nodesdk.ScheduleTrigger. params are the parameters as saved.
func (n Node) Schedule(params map[string]any) (nodesdk.ScheduleSpec, error) {
	tz := n.defaultTimezone
	if s, ok := params["timezone"].(string); ok && strings.TrimSpace(s) != "" {
		tz = strings.TrimSpace(s)
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return nodesdk.ScheduleSpec{}, fmt.Errorf("timezone %q: %w", tz, err)
	}
	rule := ruleInterval
	if s, ok := params["rule"].(string); ok && s != "" {
		rule = s
	}
	switch rule {
	case ruleCron:
		expr, _ := params["cronExpression"].(string)
		expr = strings.TrimSpace(expr)
		if expr == "" {
			return nodesdk.ScheduleSpec{}, fmt.Errorf("cronExpression is required")
		}
		if _, err := cronexpr.Parse(expr); err != nil {
			return nodesdk.ScheduleSpec{}, err //nolint:wrapcheck // already descriptive
		}
		return nodesdk.ScheduleSpec{Cron: expr, Timezone: tz}, nil
	case ruleInterval:
		amount := 1.0
		if v, ok := params["interval"]; ok && v != nil && v != "" {
			f, err := nodekit.ToFloat(v, true)
			if err != nil {
				return nodesdk.ScheduleSpec{}, fmt.Errorf("interval: %w", err)
			}
			amount = f
		}
		unit := unitMinutes
		if s, ok := params["unit"].(string); ok && s != "" {
			unit = s
		}
		base, ok := unitDurations[unit]
		if !ok {
			return nodesdk.ScheduleSpec{}, fmt.Errorf("unknown unit %q", unit)
		}
		d := time.Duration(amount * float64(base))
		if amount <= 0 || d < time.Second {
			return nodesdk.ScheduleSpec{}, fmt.Errorf("interval must be at least one second, got %v %s", amount, unit)
		}
		return nodesdk.ScheduleSpec{Interval: d, Timezone: tz}, nil
	}
	return nodesdk.ScheduleSpec{}, fmt.Errorf("unknown rule %q", rule)
}
