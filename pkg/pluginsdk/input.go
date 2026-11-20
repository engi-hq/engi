package pluginsdk

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"maps"

	"github.com/engi-hq/engi/pkg/nodesdk"
	"github.com/engi-hq/engi/pkg/pluginproto"
)

// hostCaller sends requests to the host; nil over HTTP.
type hostCaller interface {
	Call(ctx context.Context, method string, params, result any) error
}

// input is the nodesdk.Input built from one execute request.
type input struct {
	p      pluginproto.ExecuteParams
	host   hostCaller
	client nodesdk.HTTPClient
	logger *slog.Logger
}

var _ nodesdk.Input = (*input)(nil)

func (in *input) Items(port int) []nodesdk.Item {
	if port < 0 || port >= len(in.p.Inputs) {
		return nil
	}
	items := pluginproto.ToItems(in.p.Inputs[port])
	for i := range items {
		items[i].JSON = maps.Clone(items[i].JSON)
	}
	return items
}

func (in *input) Param(_ context.Context, name string, itemIndex int) (nodesdk.Value, error) {
	if itemIndex < 0 || itemIndex >= len(in.p.Params) {
		return nil, fmt.Errorf("parameter %q: item index %d out of range (%d)", name, itemIndex, len(in.p.Params))
	}
	if itemIndex < len(in.p.ParamErrors) {
		if msg, ok := in.p.ParamErrors[itemIndex][name]; ok {
			// docs/15: a parameter the host could not resolve fails the node
			// with the host's message, like a built-in node's parameter error.
			return nil, &nodesdk.NodeError{Message: msg, ItemIndex: &itemIndex}
		}
	}
	return in.p.Params[itemIndex][name], nil
}

func (in *input) Credential(_ context.Context, name string) (nodesdk.Credential, error) {
	if msg, ok := in.p.CredentialErrors[name]; ok {
		return nodesdk.Credential{}, &nodesdk.NodeError{Message: msg}
	}
	c, ok := in.p.Credentials[name]
	if !ok {
		return nodesdk.Credential{}, fmt.Errorf("credential %q is not bound", name)
	}
	return nodesdk.Credential{Type: c.Type, ID: c.ID, Version: c.Version, Data: c.Data}, nil
}

func (in *input) EvalExpression(ctx context.Context, expression string, itemIndex int) (nodesdk.Value, error) {
	if in.host == nil {
		return nil, pluginproto.Unsupported("expression.eval")
	}
	var res pluginproto.EvalResult
	req := pluginproto.EvalParams{CallID: in.p.CallID, Expression: expression, ItemIndex: itemIndex}
	if err := in.host.Call(ctx, pluginproto.MethodEvalExpression, req, &res); err != nil {
		return nil, fmt.Errorf("evaluating expression: %w", err)
	}
	return res.Value, nil
}

func (in *input) SubPort(context.Context, nodesdk.PortKind, int) ([]nodesdk.Supplied, error) {
	return nil, pluginproto.Unsupported("sub-ports")
}

func (in *input) Blob() nodesdk.BlobAccess { return blobAccess{in: in} }

func (in *input) HTTP() nodesdk.HTTPClient { return in.client }

func (in *input) Logger() *slog.Logger { return in.logger }

func (in *input) ExecutionInfo() nodesdk.ExecutionInfo { return in.p.Execution.ToExecutionInfo() }

// blobAccess reads and writes blobs through the host.
type blobAccess struct{ in *input }

func (b blobAccess) Put(ctx context.Context, name, mime string, r io.Reader) (nodesdk.BinaryRef, error) {
	if b.in.host == nil {
		return nodesdk.BinaryRef{}, pluginproto.Unsupported("blob.write")
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return nodesdk.BinaryRef{}, fmt.Errorf("reading blob data: %w", err)
	}
	var ref pluginproto.BinaryRef
	req := pluginproto.BlobWriteParams{CallID: b.in.p.CallID, Name: name, MIME: mime, Data: data}
	if err := b.in.host.Call(ctx, pluginproto.MethodBlobWrite, req, &ref); err != nil {
		return nodesdk.BinaryRef{}, fmt.Errorf("writing blob: %w", err)
	}
	return ref.ToBinaryRef(), nil
}

func (b blobAccess) Open(ctx context.Context, ref nodesdk.BinaryRef) (io.ReadCloser, error) {
	if b.in.host == nil {
		return nil, pluginproto.Unsupported("blob.read")
	}
	var res pluginproto.BlobReadResult
	req := pluginproto.BlobReadParams{CallID: b.in.p.CallID, Ref: pluginproto.FromBinaryRef(ref)}
	if err := b.in.host.Call(ctx, pluginproto.MethodBlobRead, req, &res); err != nil {
		return nil, fmt.Errorf("reading blob: %w", err)
	}
	return io.NopCloser(bytes.NewReader(res.Data)), nil
}
