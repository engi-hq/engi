package nodesdk

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// CredentialField is one field of a credential type's schema. It reuses the
// node Property schema and adds the flags credentials need.
type CredentialField struct {
	Property
	// Secret fields are write-only: they are encrypted at rest and never
	// returned by the API; callers see a sentinel instead.
	Secret bool
	// Managed fields are owned by the system (for example OAuth2 tokens). They
	// are never accepted from callers, never returned and survive updates.
	Managed bool
}

// AuthKind selects how a credential is applied to an outgoing HTTP request.
type AuthKind string

// Authentication kinds.
const (
	// AuthHeader sets a request header.
	AuthHeader AuthKind = "header"
	// AuthQuery adds a URL query parameter.
	AuthQuery AuthKind = "query"
	// AuthBasic sets an HTTP Basic Authorization header.
	AuthBasic AuthKind = "basic"
	// AuthBearer sets "Authorization: Bearer <token>".
	AuthBearer AuthKind = "bearer"
	// AuthCustom calls AuthSpec.Custom.
	AuthCustom AuthKind = "custom"
)

// AuthSpec declares how a credential type authenticates an HTTP request.
// Field names refer to keys of the decrypted credential data.
type AuthSpec struct {
	Kind AuthKind
	// Name is the constant header or query parameter name; NameField reads it
	// from the credential data instead (it wins when both are set).
	Name      string
	NameField string
	// ValueField holds the header or query value.
	ValueField string
	// Prefix is prepended to the header value, for example "Token ".
	Prefix string
	// UserField and PasswordField feed AuthBasic.
	UserField, PasswordField string
	// TokenField feeds AuthBearer.
	TokenField string
	// Custom is called for AuthCustom.
	Custom func(req *http.Request, data map[string]any) error
}

// ErrNoAuthentication is returned when a credential type declares no way to
// authenticate HTTP requests.
var ErrNoAuthentication = errors.New("credential type has no authenticate declaration")

// Authenticate applies the credential data to req.
func (a AuthSpec) Authenticate(req *http.Request, data map[string]any) error {
	field := func(name string) (string, error) {
		if name == "" {
			return "", errors.New("authenticate declaration names no field")
		}
		v, ok := data[name]
		if !ok || v == nil {
			return "", fmt.Errorf("credential field %q is not set", name)
		}
		return fmt.Sprint(v), nil
	}
	switch a.Kind {
	case AuthHeader, AuthQuery:
		name := a.Name
		if a.NameField != "" {
			var err error
			if name, err = field(a.NameField); err != nil {
				return err
			}
		}
		if name == "" {
			return errors.New("authenticate declaration has no name")
		}
		val, err := field(a.ValueField)
		if err != nil {
			return err
		}
		if a.Kind == AuthHeader {
			req.Header.Set(name, a.Prefix+val)
			return nil
		}
		q := req.URL.Query()
		q.Set(name, a.Prefix+val)
		req.URL.RawQuery = q.Encode()
	case AuthBasic:
		user, err := field(a.UserField)
		if err != nil {
			return err
		}
		pass, _ := field(a.PasswordField) // an empty password is allowed
		req.SetBasicAuth(user, pass)
	case AuthBearer:
		tok, err := field(a.TokenField)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	case AuthCustom:
		if a.Custom == nil {
			return errors.New("custom authenticate declaration has no function")
		}
		return a.Custom(req, data)
	default:
		return fmt.Errorf("unknown authenticate kind %q", a.Kind)
	}
	return nil
}

// TestSpec declares a cheap request that proves a credential works.
type TestSpec struct {
	// Method defaults to GET.
	Method string
	// URL is the constant URL; URLField reads it from the credential data.
	URL      string
	URLField string
	// ExpectStatus is the success status; zero accepts any 2xx.
	ExpectStatus int
}

// OAuth2Grant is an OAuth2 grant type.
type OAuth2Grant string

// OAuth2 grant types.
const (
	GrantClientCredentials OAuth2Grant = "clientCredentials"
	GrantAuthorizationCode OAuth2Grant = "authorizationCode"
)

// OAuth2Spec marks a credential type as OAuth2 and names the data fields the
// service reads. The access token is exposed to authenticate as AccessField.
type OAuth2Spec struct {
	GrantField, AuthURLField, TokenURLField string
	ClientIDField, ClientSecretField        string
	ScopeField                              string
	// AuthStyleField selects how client credentials reach the token endpoint:
	// "header" (HTTP Basic, the default) or "body".
	AuthStyleField string
	// TokenField is the managed field holding the stored token set.
	TokenField string
	// AccessField is the key under which the resolved access token appears in
	// the credential data handed to nodes.
	AccessField string
}

// CredentialType is the code-defined description of a kind of credential.
type CredentialType struct {
	// Name is the namespaced identifier, for example "engi.http.basic".
	Name        string
	DisplayName string
	Icon        string
	Fields      []CredentialField
	// Authenticate, when set, lets HTTP-based nodes apply this credential.
	Authenticate *AuthSpec
	// Test, when set, backs the "test credential" action.
	Test *TestSpec
	// OAuth2, when set, enables token handling by the credential service.
	OAuth2 *OAuth2Spec
}

// Field returns the field with the given name.
func (t CredentialType) Field(name string) (CredentialField, bool) {
	for _, f := range t.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return CredentialField{}, false
}

// Validate checks the type for internal consistency.
func (t CredentialType) Validate() error {
	var errs []error
	if t.Name == "" {
		errs = append(errs, errors.New("name must not be empty"))
	}
	seen := map[string]struct{}{}
	for i, f := range t.Fields {
		switch {
		case f.Name == "":
			errs = append(errs, fmt.Errorf("fields[%d]: name must not be empty", i))
		case !f.Type.Valid():
			errs = append(errs, fmt.Errorf("fields[%d] %q: unknown type %q", i, f.Name, f.Type))
		}
		if _, dup := seen[f.Name]; dup {
			errs = append(errs, fmt.Errorf("fields[%d]: duplicate name %q", i, f.Name))
		}
		seen[f.Name] = struct{}{}
	}
	return errors.Join(errs...)
}

// RunTest sends the declared test request through client, authenticated with
// data. It returns an error when the request fails or the status is wrong.
func (t CredentialType) RunTest(ctx context.Context, client HTTPClient, data map[string]any) error {
	if t.Test == nil {
		return ErrNoTest
	}
	target := t.Test.URL
	if t.Test.URLField != "" {
		target = strings.TrimSpace(fmt.Sprint(data[t.Test.URLField]))
	}
	method := t.Test.Method
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, target, http.NoBody)
	if err != nil {
		return fmt.Errorf("building test request: %w", err)
	}
	if t.Authenticate != nil {
		if err := t.Authenticate.Authenticate(req, data); err != nil {
			return fmt.Errorf("authenticating test request: %w", err)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("test request failed: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	ok := resp.StatusCode >= 200 && resp.StatusCode <= 299
	if t.Test.ExpectStatus != 0 {
		ok = resp.StatusCode == t.Test.ExpectStatus
	}
	if !ok {
		return fmt.Errorf("test request returned status %d", resp.StatusCode)
	}
	return nil
}

// ErrNoTest is returned by RunTest for types that declare no test request.
var ErrNoTest = errors.New("credential type has no test")

// BasicAuthValue returns the Authorization header value for user and password.
func BasicAuthValue(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}
