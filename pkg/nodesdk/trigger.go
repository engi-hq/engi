package nodesdk

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// Webhook response modes (WebhookSpec.ResponseMode).
const (
	// ResponseImmediately answers at once; the execution runs in the background.
	ResponseImmediately = "immediately"
	// ResponseLastNode holds the call until the execution ends and answers with
	// the output of the last node.
	ResponseLastNode = "lastNode"
	// ResponseNode holds the call until a Respond to Webhook node runs.
	ResponseNode = "responseNode"
)

// Webhook authentication kinds (WebhookSpec.Auth).
const (
	WebhookAuthNone   = "none"
	WebhookAuthBasic  = "basic"
	WebhookAuthHeader = "header"
)

// WebhookSpec says which HTTP requests a webhook trigger receives and how they
// are answered. The host reads it from the node's parameters when the workflow
// is activated.
type WebhookSpec struct {
	Method string
	// Path is relative to the webhook base path; a segment starting with ":"
	// captures a path parameter. Empty means the host picks one.
	Path         string
	ResponseMode string
	// Auth is one of the WebhookAuth constants; the credential is bound to the
	// node under AuthCredential.
	Auth           string
	AuthCredential string
	// ResponseCode and ResponseBody customize the immediate answer. Zero and
	// empty select the host defaults.
	ResponseCode int
	ResponseBody string
	// ResponseData selects what lastNode answers with: "firstEntryJson",
	// "allEntries" or "noData".
	ResponseData string
	// RawBody delivers the body as text instead of parsing it.
	RawBody bool
}

// WebhookResponse is a custom HTTP reply, produced by a Respond to Webhook
// node (Output.Response).
type WebhookResponse struct {
	Status  int
	Headers http.Header
	Body    []byte
}

// WebhookResponder is implemented by the node that answers the webhook call
// which started an execution (Respond to Webhook). A webhook or chat trigger in
// ResponseNode mode is only valid when such a node is enabled and reachable from
// it, which the host checks when the workflow is activated.
type WebhookResponder interface {
	Node
	// RespondsToWebhook marks the node as a responder. It has no behaviour.
	RespondsToWebhook()
}

// WebhookTrigger starts executions from HTTP calls. The host turns each call
// into the trigger item {headers, params, query, body} itself, so the node
// only declares what it listens to.
type WebhookTrigger interface {
	Node
	Webhook(params map[string]any) (WebhookSpec, error)
}

// PollSpec says how often a polling trigger runs. The host rejects intervals
// below poll.minInterval when the workflow is activated.
type PollSpec struct {
	Interval time.Duration
}

// PollInput is everything the host gives a poller for one poll.
type PollInput struct {
	WorkflowID string
	NodeName   string
	// Params are the node's saved parameters. Expressions are not evaluated:
	// a poller has no input item.
	Params map[string]any
	// Credentials are the decrypted credentials by slot name.
	Credentials map[string]Credential
	Logger      *slog.Logger
	// Blobs is nil when binary storage is unavailable. A poller that stores
	// binary data allocates one execution per item, writes the data through
	// the returned access and sets PolledItem.ExecutionID to the returned id.
	Blobs BlobAllocator
}

// BlobAllocator reserves the execution an item will run in, so binary data can
// be stored under it before the execution exists.
type BlobAllocator interface {
	Allocate() (executionID string, blob BlobAccess, err error)
}

// PolledItem is one unit found by a poll. It starts one execution.
type PolledItem struct {
	Item Item
	// DedupKey identifies the unit at its source (for IMAP uidvalidity:uid).
	// The host scopes it to the workflow and node, so a redelivery after a
	// crash starts no second execution.
	DedupKey string
	// ExecutionID is the id obtained from PollInput.Blobs.Allocate when the
	// item carries binary data stored through it; empty otherwise. The host
	// deletes that data when the item does not start its execution.
	ExecutionID string
}

// PollResult is what one poll found.
type PollResult struct {
	Items []PolledItem
	// Next is the cursor to store. Nil keeps the previous cursor.
	Next []byte
	// Done, when set, runs after every item has been handed to the engine and
	// Next is stored (for example to mark mails as read). Its failure is
	// logged and does not repeat the items: the stored cursor already skips them.
	Done func(ctx context.Context) error
}

// PollTrigger starts executions by polling. The host runs it on the leader
// only: it loads the cursor, calls Poll, starts one execution per item, stores
// the returned cursor and then calls Done. A crash between those steps
// redelivers the items, which the dedup keys absorb (at-least-once).
type PollTrigger interface {
	Node
	// CheckPoll validates the saved parameters when the workflow is activated.
	CheckPoll(params map[string]any) (PollSpec, error)
	// Poll looks for new items. cursor is the opaque state returned by the
	// previous poll (nil on the first one). The first poll after activation
	// should only establish the cursor unless the node asks to emit existing data.
	Poll(ctx context.Context, in PollInput, cursor []byte) (PollResult, error)
}

// ListenEvent is what a listener hands the host for one unit of work: a
// message, or a batch of messages.
type ListenEvent struct {
	// Items become the trigger node's output of one execution.
	Items []Item
	// DedupKey identifies the unit at its source (for Kafka
	// topic:partition:offset). The host scopes it to the workflow and node, so
	// a redelivery after a crash or rebalance starts no second execution.
	DedupKey string
	// AwaitCompletion makes emit return only once the execution has finished
	// (or is waiting), for listeners that must keep their source order.
	AwaitCompletion bool
}

// ListenInput is everything the host gives a listener for one run. The same
// workflow node may be started again after a failure, with fresh credentials.
type ListenInput struct {
	WorkflowID string
	NodeName   string
	// Params are the node's saved parameters. Expressions are not evaluated:
	// a listener has no input item.
	Params map[string]any
	// Credentials are the decrypted credentials by slot name.
	Credentials map[string]Credential
	Logger      *slog.Logger
	// Overloaded reports whether the host asks the listener to stop taking new
	// work because the job queue is deep. It applies hysteresis, so callers may
	// poll it often and act on changes.
	Overloaded func(ctx context.Context) bool
}

// ListenTrigger holds a long-lived connection (Kafka, later NATS ...) and emits
// an event for every message. Group-based listeners are balanced by the source
// itself, so the host runs them on every trigger instance, not only the leader
// (schedules and PollTrigger stay leader-only).
//
// Delivery is at-least-once: Listen must acknowledge a message at the source
// only after emit returned nil. emit treats a repeated DedupKey as success.
type ListenTrigger interface {
	Node
	// CheckListen validates the saved parameters when the workflow is activated.
	CheckListen(params map[string]any) error
	// Listen blocks until ctx is done (returning nil after leaving the source
	// cleanly) or a fatal error occurs; the host then restarts it after a delay.
	Listen(ctx context.Context, in ListenInput, emit func(context.Context, ListenEvent) error) error
}

// ScheduleSpec says when a schedule trigger fires. Exactly one of Cron and
// Interval is set.
type ScheduleSpec struct {
	Cron     string
	Interval time.Duration
	// Timezone is an IANA name; empty means UTC.
	Timezone string
}

// ScheduleTrigger starts executions on a schedule derived from its parameters.
type ScheduleTrigger interface {
	Node
	Schedule(params map[string]any) (ScheduleSpec, error)
}
