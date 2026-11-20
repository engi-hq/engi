// Package teamswebhook defines the Microsoft Teams incoming-webhook credential
// type. The webhook URL itself is the secret.
package teamswebhook

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Name is the credential type id.
const Name = "engi.teamsWebhook"

// Type returns the credential type.
func Type() nodesdk.CredentialType {
	return nodesdk.CredentialType{
		Name: Name, DisplayName: "Microsoft Teams webhook", Icon: "message-square",
		Fields: []nodesdk.CredentialField{
			{Property: nodesdk.Property{Name: "url", DisplayName: "Webhook URL", Type: nodesdk.PropString, Required: true}, Secret: true},
		},
		Authenticate: &nodesdk.AuthSpec{Kind: nodesdk.AuthCustom, Custom: authenticate},
	}
}

// authenticate points the request at the webhook URL.
func authenticate(req *http.Request, data map[string]any) error {
	raw, _ := data["url"].(string)
	if raw == "" {
		return errors.New("credential field \"url\" is not set")
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Host == "" {
		return fmt.Errorf("credential field \"url\" is not an absolute URL")
	}
	req.URL = u
	req.Host = u.Host
	return nil
}
