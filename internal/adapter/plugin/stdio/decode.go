package stdio

import (
	"encoding/json"

	"github.com/engi-hq/engi/pkg/pluginproto"
)

func decode(raw []byte, dst any) error {
	if err := json.Unmarshal(raw, dst); err != nil {
		return &pluginproto.Error{Code: pluginproto.CodeInvalidParams, Message: "invalid params: " + err.Error()}
	}
	return nil
}
