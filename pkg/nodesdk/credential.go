package nodesdk

import (
	"fmt"
	"net/http"
)

// Authenticate applies the credential to an outgoing HTTP request using the
// authenticate declaration of its type (header, query, basic, bearer or
// custom). OAuth2 credentials arrive with a fresh access token.
func (c Credential) Authenticate(req *http.Request) error {
	if c.Auth == nil {
		return fmt.Errorf("credential type %q: %w", c.Type, ErrNoAuthentication)
	}
	return c.Auth.Authenticate(req, c.Data)
}
