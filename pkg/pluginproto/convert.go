package pluginproto

import (
	"time"

	"github.com/engi-hq/engi/pkg/nodesdk"
)

// FromDescriptor converts a node descriptor to its wire form.
func FromDescriptor(d nodesdk.Descriptor) Descriptor {
	out := Descriptor{
		Type: d.Type, Version: d.Version, DisplayName: d.DisplayName, Description: d.Description,
		Group: string(d.Group), Icon: d.Icon, MergePolicy: string(d.MergePolicy),
		Inputs: fromPorts(d.Inputs), Outputs: fromPorts(d.Outputs), Properties: fromProperties(d.Properties),
	}
	for _, c := range d.Credentials {
		out.Credentials = append(out.Credentials, CredentialRef{Name: c.Name, Type: c.Type, Required: c.Required})
	}
	return out
}

// ToDescriptor converts a wire descriptor to the node SDK form.
func (d Descriptor) ToDescriptor() nodesdk.Descriptor {
	out := nodesdk.Descriptor{
		Type: d.Type, Version: d.Version, DisplayName: d.DisplayName, Description: d.Description,
		Group: nodesdk.Group(d.Group), Icon: d.Icon, MergePolicy: nodesdk.MergePolicy(d.MergePolicy),
		Inputs: toPorts(d.Inputs), Outputs: toPorts(d.Outputs), Properties: toProperties(d.Properties),
	}
	for _, c := range d.Credentials {
		out.Credentials = append(out.Credentials, nodesdk.CredentialRef{Name: c.Name, Type: c.Type, Required: c.Required})
	}
	return out
}

func fromPorts(ps []nodesdk.PortSpec) []PortSpec {
	var out []PortSpec
	for _, p := range ps {
		out = append(out, PortSpec{Name: p.Name, Kind: string(p.Kind), DisplayName: p.DisplayName, Required: p.Required, MaxConnections: p.MaxConnections})
	}
	return out
}

func toPorts(ps []PortSpec) []nodesdk.PortSpec {
	var out []nodesdk.PortSpec
	for _, p := range ps {
		out = append(out, nodesdk.PortSpec{Name: p.Name, Kind: nodesdk.PortKind(p.Kind), DisplayName: p.DisplayName, Required: p.Required, MaxConnections: p.MaxConnections})
	}
	return out
}

func fromProperties(ps []nodesdk.Property) []Property {
	var out []Property
	for _, p := range ps {
		w := Property{
			Name: p.Name, DisplayName: p.DisplayName, Type: string(p.Type), Default: p.Default, Required: p.Required,
			Placeholder: p.Placeholder, Description: p.Description, Children: fromProperties(p.Children),
		}
		for _, o := range p.Options {
			w.Options = append(w.Options, Option{Name: o.Name, Value: o.Value, Description: o.Description})
		}
		if p.DisplayOptions != nil {
			w.DisplayOptions = &DisplayOptions{Show: p.DisplayOptions.Show, Hide: p.DisplayOptions.Hide}
		}
		out = append(out, w)
	}
	return out
}

func toProperties(ps []Property) []nodesdk.Property {
	var out []nodesdk.Property
	for _, p := range ps {
		s := nodesdk.Property{
			Name: p.Name, DisplayName: p.DisplayName, Type: nodesdk.PropertyType(p.Type), Default: p.Default, Required: p.Required,
			Placeholder: p.Placeholder, Description: p.Description, Children: toProperties(p.Children),
		}
		for _, o := range p.Options {
			s.Options = append(s.Options, nodesdk.Option{Name: o.Name, Value: o.Value, Description: o.Description})
		}
		if p.DisplayOptions != nil {
			s.DisplayOptions = &nodesdk.DisplayOptions{Show: p.DisplayOptions.Show, Hide: p.DisplayOptions.Hide}
		}
		out = append(out, s)
	}
	return out
}

// FromItems converts items to their wire form.
func FromItems(items []nodesdk.Item) []Item {
	out := make([]Item, len(items))
	for i, it := range items {
		w := Item{JSON: it.JSON}
		if w.JSON == nil {
			w.JSON = map[string]any{}
		}
		if len(it.Binary) > 0 {
			w.Binary = make(map[string]BinaryRef, len(it.Binary))
			for k, b := range it.Binary {
				w.Binary[k] = FromBinaryRef(b)
			}
		}
		for _, p := range it.PairedItem {
			w.PairedItem = append(w.PairedItem, PairedItem(p))
		}
		if it.Error != nil {
			e := ItemError(*it.Error)
			w.Error = &e
		}
		out[i] = w
	}
	return out
}

// ToItems converts wire items to the node SDK form.
func ToItems(items []Item) []nodesdk.Item {
	out := make([]nodesdk.Item, len(items))
	for i, w := range items {
		it := nodesdk.Item{JSON: w.JSON}
		if it.JSON == nil {
			it.JSON = map[string]any{}
		}
		if len(w.Binary) > 0 {
			it.Binary = make(map[string]nodesdk.BinaryRef, len(w.Binary))
			for k, b := range w.Binary {
				it.Binary[k] = b.ToBinaryRef()
			}
		}
		for _, p := range w.PairedItem {
			it.PairedItem = append(it.PairedItem, nodesdk.PairedItem(p))
		}
		if w.Error != nil {
			e := nodesdk.ItemError(*w.Error)
			it.Error = &e
		}
		out[i] = it
	}
	return out
}

// FromBinaryRef converts a binary reference to its wire form.
func FromBinaryRef(b nodesdk.BinaryRef) BinaryRef { return BinaryRef(b) }

// ToBinaryRef converts a wire binary reference to the node SDK form.
func (b BinaryRef) ToBinaryRef() nodesdk.BinaryRef { return nodesdk.BinaryRef(b) }

// FromExecutionInfo converts execution info to its wire form.
func FromExecutionInfo(e nodesdk.ExecutionInfo) ExecutionInfo { return ExecutionInfo(e) }

// ToExecutionInfo converts wire execution info to the node SDK form.
func (e ExecutionInfo) ToExecutionInfo() nodesdk.ExecutionInfo { return nodesdk.ExecutionInfo(e) }

// FromOutput converts a node output to an execute result. A webhook Response
// has no wire form in v1 and is dropped.
func FromOutput(o nodesdk.Output) ExecuteResult {
	r := ExecuteResult{Outputs: make([][]Item, len(o.Ports))}
	for i, p := range o.Ports {
		r.Outputs[i] = FromItems(p)
	}
	if o.Wait != nil {
		w := &Wait{ResumeToken: o.Wait.ResumeToken}
		if !o.Wait.Until.IsZero() {
			t := o.Wait.Until
			w.Until = &t
		}
		r.Wait = w
	}
	return r
}

// ToOutput converts an execute result to a node output.
func (r ExecuteResult) ToOutput() nodesdk.Output {
	o := nodesdk.Output{Ports: make([][]nodesdk.Item, len(r.Outputs))}
	for i, p := range r.Outputs {
		o.Ports[i] = ToItems(p)
	}
	if r.Wait != nil {
		w := &nodesdk.Wait{ResumeToken: r.Wait.ResumeToken}
		if r.Wait.Until != nil {
			w.Until = r.Wait.Until.In(time.UTC)
		}
		o.Wait = w
	}
	return o
}
