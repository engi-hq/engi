package nodesdk

// PropertyType is the kind of a node parameter.
type PropertyType string

// Property types.
const (
	PropString            PropertyType = "string"
	PropNumber            PropertyType = "number"
	PropBoolean           PropertyType = "boolean"
	PropOptions           PropertyType = "options"
	PropMultiOptions      PropertyType = "multiOptions"
	PropJSON              PropertyType = "json"
	PropCode              PropertyType = "code"
	PropDateTime          PropertyType = "dateTime"
	PropCollection        PropertyType = "collection"
	PropFixedCollection   PropertyType = "fixedCollection"
	PropResourceLocator   PropertyType = "resourceLocator"
	PropCredentialsSelect PropertyType = "credentialsSelect"
	PropNotice            PropertyType = "notice"
)

// Valid reports whether t is a known property type.
func (t PropertyType) Valid() bool {
	switch t {
	case PropString, PropNumber, PropBoolean, PropOptions, PropMultiOptions, PropJSON,
		PropCode, PropDateTime, PropCollection, PropFixedCollection, PropResourceLocator,
		PropCredentialsSelect, PropNotice:
		return true
	}
	return false
}

// Option is one choice of an options property.
type Option struct {
	Name        string
	Value       any
	Description string
}

// DisplayOptions shows or hides a property depending on other parameter values.
// Each map is keyed by parameter name; a property is shown when all Show
// entries match and no Hide entry matches. A condition matches when the
// parameter's current value equals any of the listed values.
type DisplayOptions struct {
	Show map[string][]any
	Hide map[string][]any
}

// Property is one entry of a node's typed parameter schema.
type Property struct {
	Name        string
	DisplayName string
	Type        PropertyType
	Default     any
	Required    bool
	Placeholder string
	Description string
	// Options lists the choices of options and multiOptions properties.
	Options []Option
	// Children is the nested schema of collection and fixedCollection properties.
	Children       []Property
	DisplayOptions *DisplayOptions
}
