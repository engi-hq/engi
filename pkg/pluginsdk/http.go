package pluginsdk

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/engi-hq/engi/pkg/nodesdk"
	"github.com/engi-hq/engi/pkg/pluginproto"
)

// rpcPath is the only path the HTTP transport serves.
const rpcPath = "/rpc"

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string             `json:"jsonrpc"`
	ID      json.RawMessage    `json:"id"`
	Result  json.RawMessage    `json:"result,omitempty"`
	Error   *pluginproto.Error `json:"error,omitempty"`
}

// Handler serves the nodes over the HTTP transport: one JSON-RPC request per
// POST /rpc. It panics at construction on an invalid node set (none, or a
// duplicate type and version), like http.Handle does for a bad pattern.
func Handler(info pluginproto.Info, opts Options, nodes ...nodesdk.Node) http.Handler {
	s, err := newServer(info, opts, nodes)
	if err != nil {
		panic("pluginsdk: " + err.Error())
	}
	return &httpServer{s: s, sem: make(chan struct{}, s.opts.MaxConcurrent)}
}

type httpServer struct {
	s   *server
	sem chan struct{}
}

func (h *httpServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != rpcPath {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req rpcRequest
	body := http.MaxBytesReader(w, r.Body, int64(h.s.opts.MaxMessageBytes))
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		code, msg := pluginproto.CodeParseError, "invalid JSON"
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			code, msg = pluginproto.CodeInvalidRequest, "request too large"
		}
		writeRPC(w, rpcResponse{ID: json.RawMessage("null"), Error: &pluginproto.Error{Code: code, Message: msg}})
		return
	}
	if req.ID == nil {
		// A notification: the HTTP transport has nothing to cancel or stop.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	select {
	case h.sem <- struct{}{}:
		defer func() { <-h.sem }()
	default:
		writeRPC(w, rpcResponse{ID: req.ID, Error: &pluginproto.Error{Code: pluginproto.CodeBusy, Message: "too many concurrent requests"}})
		return
	}
	res := rpcResponse{ID: req.ID}
	out, err := h.s.handle(r.Context(), pluginproto.Request{ID: req.ID, Method: req.Method, Params: req.Params})
	if err == nil {
		res.Result, err = marshalResult(out)
	}
	if err != nil {
		res.Result, res.Error = nil, pluginproto.ErrorFrom(err)
	}
	writeRPC(w, res)
}

func marshalResult(v any) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, &pluginproto.Error{Code: pluginproto.CodeInternalError, Message: "encoding result: " + err.Error()}
	}
	return b, nil
}

func writeRPC(w http.ResponseWriter, res rpcResponse) {
	res.JSONRPC = "2.0"
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}
