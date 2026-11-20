package pluginsdk

import (
	"context"
	"log/slog"

	"github.com/engi-hq/engi/pkg/pluginproto"
)

// notifier sends a notification to the host.
type notifier interface {
	Notify(method string, params any) error
}

// hostLogHandler is a slog.Handler that sends each record to the host as a
// log notification.
type hostLogHandler struct {
	n notifier
	// callID is the execute call the logger belongs to; the host routes the
	// line to that call's node logger. Empty for a logger outside a call.
	callID string
	attrs  []slog.Attr
	prefix string
}

func (h *hostLogHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *hostLogHandler) Handle(_ context.Context, r slog.Record) error {
	attrs := map[string]any{}
	for _, a := range h.attrs {
		addAttr(attrs, "", a)
	}
	r.Attrs(func(a slog.Attr) bool {
		addAttr(attrs, h.prefix, a)
		return true
	})
	p := pluginproto.LogParams{Level: levelName(r.Level), Message: r.Message, CallID: h.callID}
	if len(attrs) > 0 {
		p.Attrs = attrs
	}
	return h.n.Notify(pluginproto.MethodLog, p) //nolint:wrapcheck // logging has no caller to wrap for
}

func (h *hostLogHandler) WithAttrs(as []slog.Attr) slog.Handler {
	c := *h
	c.attrs = append(append([]slog.Attr{}, h.attrs...), qualify(h.prefix, as)...)
	return &c
}

func (h *hostLogHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	c := *h
	c.prefix = h.prefix + name + "."
	return &c
}

func qualify(prefix string, as []slog.Attr) []slog.Attr {
	if prefix == "" {
		return as
	}
	out := make([]slog.Attr, len(as))
	for i, a := range as {
		a.Key = prefix + a.Key
		out[i] = a
	}
	return out
}

func addAttr(dst map[string]any, prefix string, a slog.Attr) {
	v := a.Value.Resolve()
	if v.Kind() == slog.KindGroup {
		p := prefix
		if a.Key != "" {
			p += a.Key + "."
		}
		for _, g := range v.Group() {
			addAttr(dst, p, g)
		}
		return
	}
	if a.Key == "" {
		return
	}
	if err, ok := v.Any().(error); ok {
		dst[prefix+a.Key] = err.Error()
		return
	}
	dst[prefix+a.Key] = v.Any()
}

func levelName(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return pluginproto.LogError
	case l >= slog.LevelWarn:
		return pluginproto.LogWarn
	case l >= slog.LevelInfo:
		return pluginproto.LogInfo
	}
	return pluginproto.LogDebug
}
