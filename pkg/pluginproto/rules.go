package pluginproto

import (
	"errors"
	"fmt"
	"slices"

	"github.com/engi-hq/engi/pkg/nodesdk"
)

// allowedGroups are the node groups a v1 plugin may declare.
var allowedGroups = []nodesdk.Group{nodesdk.GroupAction, nodesdk.GroupTransform, nodesdk.GroupFlow}

// CheckDescriptor reports why a plugin may not declare d (docs/15 section
// 15.4): the type must be listed in declared and not reserved, the group must
// be action, transform or flow, every port must be main, and d must pass
// nodesdk.Descriptor.Validate.
func CheckDescriptor(d nodesdk.Descriptor, declared []string) error {
	errs := CheckNodeType(d.Type)
	if !slices.Contains(declared, d.Type) {
		errs = append(errs, fmt.Errorf("type %q is not listed in the manifest's nodes", d.Type))
	}
	if !slices.Contains(allowedGroups, d.Group) {
		errs = append(errs, fmt.Errorf("group %q: plugins may declare %v nodes only", d.Group, allowedGroups))
	}
	for _, ports := range [][]nodesdk.PortSpec{d.Inputs, d.Outputs} {
		for _, p := range ports {
			if p.EffectiveKind() != nodesdk.PortMain {
				errs = append(errs, fmt.Errorf("port %q: plugins may only use main ports, not %q", p.Name, p.Kind))
			}
		}
	}
	if err := d.Validate(); err != nil {
		errs = append(errs, err)
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("node %q version %d: %w", d.Type, d.Version, err)
	}
	return nil
}
