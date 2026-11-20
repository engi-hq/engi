package app

import (
	"context"
	"fmt"

	"github.com/engi-hq/engi/internal/adapter/http/webhook"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/service/execution"
	"github.com/engi-hq/engi/internal/service/trigger"
	"github.com/engi-hq/engi/internal/service/waiter"
)

// buildTriggers wires the activation service and the webhook handler over the
// runtime's stores, bus and services.
func (a *App) buildTriggers(store storeSet, lease port.LeaseProvider, bus port.Bus, execs *execution.Service, wt *waiter.Service, queue port.QueueStats) (*trigger.Service, *webhook.Handler, error) {
	svc, err := trigger.New(a.Config.Trigger, trigger.Deps{
		Workflows: store.Workflows, Webhooks: store.Webhooks, TestWebhooks: store.TestHooks, States: store.Triggers, Activations: store.Activations, Credentials: a.credResolver,
		Nodes: a.Registry, Executions: execs, Blobs: a.Blobs, IDs: a.ids, Lease: lease, Queue: queue, Bus: bus, Clock: a.Clock, Sleeper: a.Sleeper, Metrics: a.obs.metrics,
		Logger: a.Logger.With("component", "trigger"),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("building trigger service: %w", err)
	}
	h, err := webhook.New(a.Config.Trigger.Webhook, webhook.Deps{
		Router: svc, Executions: execs, Resumer: wt, Credentials: a.credResolver, Bus: bus, Blobs: a.Blobs, IDs: a.ids,
		Logger: a.Logger.With("component", "webhook"),
		Chat:   &webhook.ChatDeps{Config: a.Config.Trigger.Chat, Router: svc},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("building webhook handler: %w", err)
	}
	return svc, h, nil
}

// StartTriggers begins campaigning for leadership (the standalone process
// leads at once and runs schedules) and registers the webhooks and schedules of
// every active workflow. Call it after Start, from `engi serve`; the CLI's
// one-shot persisted run does not start triggers. Shutdown stops them.
func (r *Runtime) StartTriggers(ctx context.Context) error {
	if err := r.Triggers.Start(ctx); err != nil {
		return fmt.Errorf("starting triggers: %w", err)
	}
	if err := r.Triggers.ActivateAll(ctx); err != nil {
		return fmt.Errorf("activating workflows: %w", err)
	}
	r.Retention.Start(ctx)
	return nil
}
