// Package telegram defines the Telegram bot credential type.
package telegram

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Name is the credential type id.
const Name = "engi.telegram"

// TokenSegment is the placeholder path segment a request carries where the Bot
// API wants "bot<token>". Authenticate swaps in the real segment, so the token
// is only ever in the request the credential is applied to.
const TokenSegment = "bot-token"

// Type returns the credential type.
func Type() nodesdk.CredentialType {
	return nodesdk.CredentialType{
		Name: Name, DisplayName: "Telegram", Icon: "send",
		Fields: []nodesdk.CredentialField{
			{Property: nodesdk.Property{Name: "token", DisplayName: "Bot token", Type: nodesdk.PropString, Required: true,
				Description: "The token @BotFather gave you."}, Secret: true},
		},
		Authenticate: &nodesdk.AuthSpec{Kind: nodesdk.AuthCustom, Custom: authenticate},
	}
}

func authenticate(req *http.Request, data map[string]any) error {
	tok, _ := data["token"].(string)
	if tok == "" {
		return errors.New("credential field \"token\" is not set")
	}
	seg := "/" + TokenSegment + "/"
	if !strings.Contains(req.URL.Path, seg) {
		return fmt.Errorf("request path lacks the %q segment", TokenSegment)
	}
	req.URL.Path = strings.Replace(req.URL.Path, seg, "/bot"+tok+"/", 1)
	req.URL.RawPath = ""
	return nil
}
