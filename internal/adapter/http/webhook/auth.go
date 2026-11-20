package webhook

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/engi-hq/engi/internal/service/trigger"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// authResult tells what an accepted call used to authenticate.
type authResult struct {
	// header is the lower-case request header that carried the secret, if any;
	// it is kept out of the trigger item.
	header string
}

// authenticate checks the call against the node's authentication option. A
// refusal says nothing about why: unknown credentials, wrong user and wrong
// secret all look the same.
func (h *Handler) authenticate(ctx context.Context, r *http.Request, route trigger.Route) (authResult, bool) {
	switch route.Spec.Auth {
	case "", nodesdk.WebhookAuthNone:
		return authResult{}, true
	case nodesdk.WebhookAuthBasic, nodesdk.WebhookAuthHeader:
	default:
		return authResult{}, false
	}
	cred, err := h.d.Credentials.Resolve(ctx, route.Credential)
	if err != nil {
		h.d.Logger.Error("loading the webhook credential failed", "workflow", route.WorkflowID, "node", route.Node, "error", err)
		return authResult{}, false
	}
	if route.Spec.Auth == nodesdk.WebhookAuthBasic {
		user, pass, ok := r.BasicAuth()
		wantUser, _ := cred.Data["user"].(string)
		wantPass, _ := cred.Data["password"].(string)
		// Both comparisons always run, so timing does not show which part was wrong.
		userOK := subtle.ConstantTimeCompare([]byte(user), []byte(wantUser))
		passOK := subtle.ConstantTimeCompare([]byte(pass), []byte(wantPass))
		return authResult{}, ok && userOK&passOK == 1
	}
	name, _ := cred.Data["name"].(string)
	want, _ := cred.Data["value"].(string)
	if name == "" || want == "" {
		return authResult{}, false
	}
	got := r.Header.Get(name)
	return authResult{header: strings.ToLower(name)}, subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
