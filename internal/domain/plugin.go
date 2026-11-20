package domain

// PluginState says whether a plugin's nodes are available.
type PluginState string

// Plugin states.
const (
	// PluginLoaded: the plugin started and its node types are registered.
	PluginLoaded PluginState = "loaded"
	// PluginFailed: the plugin could not be loaded; Error says why.
	PluginFailed PluginState = "failed"
)

// PluginStatus is what the server knows about one installed plugin.
type PluginStatus struct {
	Name    string
	Version string
	// Transport is "stdio" or "http".
	Transport string
	// Nodes lists the node types the plugin registered (loaded) or declares
	// in its manifest (failed).
	Nodes []string
	State PluginState
	Error string
}
