// Package wait implements the Wait node, which pauses an execution for a time
// interval or until a date and time.
package wait

import (
	"context"
	"fmt"
	"time"

	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.flow.wait"

// Resume modes.
const (
	resumeInterval = "interval"
	resumeAt       = "specificTime"
	resumeWebhook  = "webhook"
)

// Interval units.
const (
	unitSeconds = "seconds"
	unitMinutes = "minutes"
	unitHours   = "hours"
	unitDays    = "days"
)

// maxWait keeps duration arithmetic far from overflow (about 100 years).
const maxWait = 100 * 365 * 24 * time.Hour

// Clock tells the current time. The node takes it by injection so tests and
// the engine agree on "now".
type Clock interface {
	Now() time.Time
}

// Node is the Wait node.
type Node struct {
	clock Clock
}

// New returns a Wait node reading time from clock. A webhook wait takes its
// resume secret from the execution (nodesdk.ResumeSecreter), so every Wait node
// of one execution shares the same resume URL.
func New(clock Clock) nodesdk.Node { return Node{clock: clock} }

// Describe implements nodesdk.Node.
func (Node) Describe() nodesdk.Descriptor {
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "Wait", Group: nodesdk.GroupFlow, Icon: "clock",
		Description: "Pause the execution, then continue with the same items.",
		Inputs:      []nodesdk.PortSpec{nodekit.Main("main", "")},
		Outputs:     []nodesdk.PortSpec{nodekit.Main("main", "")},
		Properties: []nodesdk.Property{
			{Name: "resume", DisplayName: "Resume", Type: nodesdk.PropOptions, Default: resumeInterval, Options: []nodesdk.Option{
				{Name: "After a time interval", Value: resumeInterval},
				{Name: "At a specific date and time", Value: resumeAt},
				{Name: "On webhook call", Value: resumeWebhook}}},
			{Name: "amount", DisplayName: "Amount", Type: nodesdk.PropNumber, Default: float64(1), DisplayOptions: nodekit.Show("resume", resumeInterval)},
			{Name: "unit", DisplayName: "Unit", Type: nodesdk.PropOptions, Default: unitSeconds, DisplayOptions: nodekit.Show("resume", resumeInterval), Options: []nodesdk.Option{
				{Name: "Seconds", Value: unitSeconds}, {Name: "Minutes", Value: unitMinutes},
				{Name: "Hours", Value: unitHours}, {Name: "Days", Value: unitDays}}},
			{Name: "dateTime", DisplayName: "Date and time", Type: nodesdk.PropDateTime, DisplayOptions: nodekit.Show("resume", resumeAt),
				Description: "ISO-8601; without an offset it is read as UTC."},
		},
	}
}

// Execute implements nodesdk.Node. A wait whose time has already passed
// continues at once.
func (n Node) Execute(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	items := in.Items(0)
	out := make([]nodesdk.Item, len(items))
	for i, it := range items {
		out[i] = nodekit.Paired(it, i)
	}
	res := nodesdk.Output{Ports: [][]nodesdk.Item{out}}

	mode, err := nodesdk.ParamString(ctx, in, "resume", 0)
	if err != nil {
		return nodesdk.Output{}, fmt.Errorf("reading resume: %w", err)
	}
	switch mode {
	case resumeInterval, "":
		d, err := interval(ctx, in)
		if err != nil {
			return nodesdk.Output{}, err
		}
		if d > 0 {
			res.Wait = &nodesdk.Wait{Until: n.clock.Now().Add(d)}
		}
	case resumeAt:
		raw, err := in.Param(ctx, "dateTime", 0)
		if err != nil {
			return nodesdk.Output{}, fmt.Errorf("reading dateTime: %w", err)
		}
		if raw == nil {
			return nodesdk.Output{}, fmt.Errorf("dateTime is required")
		}
		at, err := nodekit.ToTime(raw)
		if err != nil {
			return nodesdk.Output{}, fmt.Errorf("dateTime: %w", err)
		}
		if at.After(n.clock.Now()) {
			res.Wait = &nodesdk.Wait{Until: at}
		}
	case resumeWebhook:
		// The token is "<executionID>.<execution secret>"; the resume route
		// (/hook-wait/<token>) accepts exactly this token, compared in constant time.
		secreter, ok := in.(nodesdk.ResumeSecreter)
		if !ok || secreter.ResumeSecret() == "" {
			return nodesdk.Output{}, fmt.Errorf("waiting for a webhook: the execution has no resume secret")
		}
		res.Wait = &nodesdk.Wait{ResumeToken: nodesdk.NewResumeToken(in.ExecutionInfo().ExecutionID, secreter.ResumeSecret())}
	default:
		return nodesdk.Output{}, fmt.Errorf("unknown resume mode %q", mode)
	}
	return res, nil
}

func interval(ctx context.Context, in nodesdk.Input) (time.Duration, error) {
	amount, err := nodekit.ParamFloat(ctx, in, "amount", 0)
	if err != nil {
		return 0, err //nolint:wrapcheck // already descriptive
	}
	if amount < 0 {
		return 0, fmt.Errorf("amount must not be negative, got %v", amount)
	}
	unit, err := nodesdk.ParamString(ctx, in, "unit", 0)
	if err != nil {
		return 0, fmt.Errorf("reading unit: %w", err)
	}
	var base time.Duration
	switch unit {
	case unitSeconds, "":
		base = time.Second
	case unitMinutes:
		base = time.Minute
	case unitHours:
		base = time.Hour
	case unitDays:
		base = 24 * time.Hour
	default:
		return 0, fmt.Errorf("unknown unit %q", unit)
	}
	total := amount * float64(base)
	if total > float64(maxWait) {
		return 0, fmt.Errorf("wait of %v %s is longer than the supported maximum", amount, unit)
	}
	return time.Duration(total), nil
}
