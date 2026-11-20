// Package pluginnode adapts a plugin transport to the nodesdk.Node contract:
// it turns an Execute call into one pluginproto execute request, serves the
// plugin's callbacks from the node's Input and maps the result back.
package pluginnode

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/engi-hq/engi/pkg/nodesdk"
	"github.com/engi-hq/engi/pkg/pluginproto"
)

// callIDBytes is the entropy of a call id.
const callIDBytes = 16

// Callbacks serves the requests a plugin makes while one call runs. A
// transport without callbacks (HTTP) ignores it.
type Callbacks interface {
	// ReadBlob returns the bytes behind ref.
	ReadBlob(ctx context.Context, ref pluginproto.BinaryRef) ([]byte, error)
	// WriteBlob stores data and returns its reference.
	WriteBlob(ctx context.Context, name, mime string, data []byte) (pluginproto.BinaryRef, error)
	// Eval evaluates an expression in the scope of the given input item.
	Eval(ctx context.Context, expr string, itemIndex int) (any, error)
	// Log records a plugin log line against the call.
	Log(level, msg string, attrs map[string]any)
}

// Caller sends one execute request to a plugin and waits for the result.
type Caller interface {
	Execute(ctx context.Context, p pluginproto.ExecuteParams, cb Callbacks) (pluginproto.ExecuteResult, error)
}

// Options configures a plugin node.
type Options struct {
	// MaxBlobBytes caps the size of a blob a plugin may read.
	MaxBlobBytes int64
	// Rand is the source of call ids; nil means crypto/rand.
	Rand io.Reader
}

type node struct {
	desc   nodesdk.Descriptor
	caller Caller
	opts   Options
}

// New returns the node for descriptor d, executed through c.
func New(d nodesdk.Descriptor, c Caller, o Options) nodesdk.Node {
	if o.Rand == nil {
		o.Rand = rand.Reader
	}
	return &node{desc: d, caller: c, opts: o}
}

// Describe implements nodesdk.Node.
func (n *node) Describe() nodesdk.Descriptor { return n.desc }

// Execute implements nodesdk.Node.
func (n *node) Execute(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	p, err := n.buildParams(ctx, in)
	if err != nil {
		return nodesdk.Output{}, err
	}
	res, err := n.caller.Execute(ctx, p, &inputCallbacks{in: in, maxBlob: n.opts.MaxBlobBytes})
	if err != nil {
		var pe *pluginproto.Error
		if errors.As(err, &pe) {
			return nodesdk.Output{}, pe.NodeError()
		}
		return nodesdk.Output{}, fmt.Errorf("plugin node %s: %w", n.desc.Type, err)
	}
	if want := n.desc.PortCount(false, nodesdk.PortMain); len(res.Outputs) != want {
		return nodesdk.Output{}, fmt.Errorf("plugin node %s returned %d outputs, descriptor declares %d", n.desc.Type, len(res.Outputs), want)
	}
	return res.ToOutput(), nil
}

func (n *node) buildParams(ctx context.Context, in nodesdk.Input) (pluginproto.ExecuteParams, error) {
	id, err := n.newCallID()
	if err != nil {
		return pluginproto.ExecuteParams{}, err
	}
	p := pluginproto.ExecuteParams{
		CallID:    id,
		Node:      pluginproto.NodeRef{Type: n.desc.Type, Version: n.desc.Version},
		Execution: pluginproto.FromExecutionInfo(in.ExecutionInfo()),
	}
	ports := n.desc.PortCount(true, nodesdk.PortMain)
	p.Inputs = make([][]pluginproto.Item, ports)
	for i := range ports {
		p.Inputs[i] = pluginproto.FromItems(in.Items(i))
	}

	count := max(1, len(in.Items(0)))
	p.Params = make([]map[string]any, count)
	p.ParamErrors = make([]map[string]string, count)
	for i := range count {
		p.Params[i] = map[string]any{}
		p.ParamErrors[i] = map[string]string{}
		for _, prop := range n.desc.Properties {
			if prop.Type == nodesdk.PropNotice {
				continue
			}
			v, err := in.Param(ctx, prop.Name, i)
			if err != nil {
				p.ParamErrors[i][prop.Name] = err.Error()
				continue
			}
			p.Params[i][prop.Name] = v
		}
	}

	p.Credentials = map[string]pluginproto.Credential{}
	p.CredentialErrors = map[string]string{}
	for _, ref := range n.desc.Credentials {
		c, err := in.Credential(ctx, ref.Name)
		if err != nil {
			p.CredentialErrors[ref.Name] = err.Error()
			continue
		}
		p.Credentials[ref.Name] = pluginproto.Credential{Type: c.Type, ID: c.ID, Version: c.Version, Data: c.Data}
	}
	return p, nil
}

func (n *node) newCallID() (string, error) {
	b := make([]byte, callIDBytes)
	if _, err := io.ReadFull(n.opts.Rand, b); err != nil {
		return "", fmt.Errorf("generating call id: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// inputCallbacks serves plugin callbacks from the node's Input.
type inputCallbacks struct {
	in      nodesdk.Input
	maxBlob int64
}

func (c *inputCallbacks) ReadBlob(ctx context.Context, ref pluginproto.BinaryRef) ([]byte, error) {
	if c.maxBlob > 0 && ref.Size > c.maxBlob {
		return nil, fmt.Errorf("blob %q is %d bytes, above the %d byte limit", ref.Name, ref.Size, c.maxBlob)
	}
	rc, err := c.in.Blob().Open(ctx, ref.ToBinaryRef())
	if err != nil {
		return nil, fmt.Errorf("opening blob: %w", err)
	}
	defer func() { _ = rc.Close() }()
	var r io.Reader = rc
	if c.maxBlob > 0 {
		r = io.LimitReader(rc, c.maxBlob+1)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("reading blob: %w", err)
	}
	if c.maxBlob > 0 && int64(len(data)) > c.maxBlob {
		return nil, fmt.Errorf("blob is larger than the %d byte limit", c.maxBlob)
	}
	return data, nil
}

func (c *inputCallbacks) WriteBlob(ctx context.Context, name, mime string, data []byte) (pluginproto.BinaryRef, error) {
	ref, err := c.in.Blob().Put(ctx, name, mime, bytes.NewReader(data))
	if err != nil {
		return pluginproto.BinaryRef{}, fmt.Errorf("storing blob: %w", err)
	}
	return pluginproto.FromBinaryRef(ref), nil
}

func (c *inputCallbacks) Eval(ctx context.Context, expr string, itemIndex int) (any, error) {
	v, err := c.in.EvalExpression(ctx, expr, itemIndex)
	if err != nil {
		return nil, fmt.Errorf("evaluating expression: %w", err)
	}
	return v, nil
}

func (c *inputCallbacks) Log(level, msg string, attrs map[string]any) {
	var l slog.Level
	switch level {
	case pluginproto.LogDebug:
		l = slog.LevelDebug
	case pluginproto.LogWarn:
		l = slog.LevelWarn
	case pluginproto.LogError:
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	args := make([]any, 0, len(attrs)*2)
	for k, v := range attrs {
		args = append(args, k, v)
	}
	c.in.Logger().Log(context.Background(), l, msg, args...)
}
