package pluginproto

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// ManifestFile is the name of the manifest in a plugin directory.
const ManifestFile = "engi-plugin.yaml"

// ReservedTypePrefix starts the node types of built-in nodes; plugins may not
// use it.
const ReservedTypePrefix = "engi."

// Manifest describes an installable plugin (docs/15 section 15.5).
type Manifest struct {
	Name        string   `yaml:"name"`
	Version     string   `yaml:"version"`
	Description string   `yaml:"description"`
	Runtime     Runtime  `yaml:"runtime"`
	Nodes       []string `yaml:"nodes"`
}

// Runtime says how to reach the plugin: a local command (stdio) or a URL (HTTP).
type Runtime struct {
	// Command runs from the plugin directory; the first element is the program.
	Command []string `yaml:"command"`
	// Env is added to the variables inherited per plugins.inheritEnv.
	Env map[string]string `yaml:"env"`
	// URL is the base URL of an HTTP plugin; requests go to URL + "/rpc".
	URL string `yaml:"url"`
	// TokenFile holds a bearer token sent to an HTTP plugin.
	TokenFile string `yaml:"tokenFile"`
}

// IsHTTP reports whether the plugin is reached over HTTP.
func (r Runtime) IsHTTP() bool { return r.URL != "" }

var (
	nameRE   = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$`)
	semverRE = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)
	typeRE   = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]*(\.[a-zA-Z][a-zA-Z0-9_-]*)+$`)
)

// ParseManifest decodes and validates a manifest. Unknown keys are an error,
// so a typo does not pass silently.
func ParseManifest(data []byte) (Manifest, error) {
	var m Manifest
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("parsing %s: %w", ManifestFile, err)
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, fmt.Errorf("invalid %s: %w", ManifestFile, err)
	}
	return m, nil
}

// ValidName reports whether s is a valid plugin name.
func ValidName(s string) bool { return nameRE.MatchString(s) }

// ValidVersion reports whether s is a semantic version.
func ValidVersion(s string) bool { return semverRE.MatchString(s) }

// Validate reports every problem with the manifest.
func (m Manifest) Validate() error {
	var errs []error
	if !ValidName(m.Name) {
		errs = append(errs, fmt.Errorf("name %q: use lower-case letters, digits and dashes", m.Name))
	}
	if !ValidVersion(m.Version) {
		errs = append(errs, fmt.Errorf("version %q is not a semantic version", m.Version))
	}
	hasCmd, hasURL := len(m.Runtime.Command) > 0, m.Runtime.URL != ""
	switch {
	case hasCmd == hasURL:
		errs = append(errs, errors.New("runtime: set exactly one of command and url"))
	case hasURL:
		u, err := url.Parse(m.Runtime.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, fmt.Errorf("runtime.url %q must be an absolute http or https URL", m.Runtime.URL))
		}
		if len(m.Runtime.Env) > 0 {
			errs = append(errs, errors.New("runtime.env only applies to a command"))
		}
	default:
		if strings.TrimSpace(m.Runtime.Command[0]) == "" {
			errs = append(errs, errors.New("runtime.command: the program must not be empty"))
		}
		if m.Runtime.TokenFile != "" {
			errs = append(errs, errors.New("runtime.tokenFile only applies to a url"))
		}
	}
	if len(m.Nodes) == 0 {
		errs = append(errs, errors.New("nodes: list at least one node type"))
	}
	seen := map[string]bool{}
	for _, t := range m.Nodes {
		errs = append(errs, CheckNodeType(t)...)
		if seen[t] {
			errs = append(errs, fmt.Errorf("nodes: %q is listed twice", t))
		}
		seen[t] = true
	}
	return errors.Join(errs...)
}

// CheckNodeType reports why t cannot be a plugin node type, if it cannot.
func CheckNodeType(t string) []error {
	var errs []error
	if !typeRE.MatchString(t) {
		errs = append(errs, fmt.Errorf("node type %q: use dot-separated names such as acme.text.reverse", t))
	}
	if strings.HasPrefix(t, ReservedTypePrefix) {
		errs = append(errs, fmt.Errorf("node type %q: the %q prefix is reserved for built-in nodes", t, ReservedTypePrefix))
	}
	return errs
}
