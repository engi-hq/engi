package jsoncodec

import (
	"encoding/json"
	"fmt"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/service/engine"
)

type inputDTO struct {
	StartNode string    `json:"startNode"`
	Items     []itemDTO `json:"items,omitempty"`
	// Partial-run and unsaved-document extras (see domain.ExecutionInput).
	Destination          string               `json:"destination,omitempty"`
	DestinationExclusive bool                 `json:"destinationExclusive,omitempty"`
	PinData              map[string][]itemDTO `json:"pinData,omitempty"`
	Workflow             *workflowDTO         `json:"workflow,omitempty"`
	TraceParent          string               `json:"traceParent,omitempty"`
}

// savedProbe finds out which kind of payload a blob holds.
type savedProbe struct {
	Input *inputDTO `json:"input"`
}

// Codec implements engine.StateCodec on the JSON wire format.
type Codec struct{}

var _ engine.StateCodec = Codec{}

// EncodeState implements engine.StateCodec.
func (Codec) EncodeState(s *engine.State) ([]byte, error) { return EncodeState(s) }

// DecodeState implements engine.StateCodec.
func (Codec) DecodeState(data []byte) (*engine.State, error) { return DecodeState(data) }

// EncodeInput implements engine.StateCodec.
func (Codec) EncodeInput(in domain.ExecutionInput) ([]byte, error) {
	raw, err := json.Marshal(struct {
		Input inputDTO `json:"input"`
	}{inputToDTO(in)})
	if err != nil {
		return nil, fmt.Errorf("encoding execution input: %w", err)
	}
	return raw, nil
}

// DecodeSaved implements engine.StateCodec.
func (Codec) DecodeSaved(data []byte) (*domain.ExecutionInput, *engine.State, error) {
	var probe savedProbe
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, nil, fmt.Errorf("decoding saved execution payload: %w", err)
	}
	if probe.Input != nil {
		in := inputToDomain(*probe.Input)
		return &in, nil, nil
	}
	st, err := DecodeState(data)
	if err != nil {
		return nil, nil, err
	}
	return nil, st, nil
}

func inputToDTO(in domain.ExecutionInput) inputDTO {
	d := inputDTO{
		StartNode: in.StartNode, Items: itemsToDTO(in.Items),
		Destination: in.Destination, DestinationExclusive: in.DestinationExclusive, TraceParent: in.TraceParent,
	}
	if len(in.PinData) > 0 {
		d.PinData = make(map[string][]itemDTO, len(in.PinData))
		for k, v := range in.PinData {
			d.PinData[k] = itemsToDTO(v)
		}
	}
	if in.Workflow != nil {
		w := workflowToDTO(*in.Workflow)
		d.Workflow = &w
	}
	return d
}

func inputToDomain(d inputDTO) domain.ExecutionInput {
	in := domain.ExecutionInput{
		StartNode: d.StartNode, Items: itemsToDomain(d.Items),
		Destination: d.Destination, DestinationExclusive: d.DestinationExclusive, TraceParent: d.TraceParent,
	}
	if len(d.PinData) > 0 {
		in.PinData = make(map[string][]domain.Item, len(d.PinData))
		for k, v := range d.PinData {
			in.PinData[k] = itemsToDomain(v)
		}
	}
	if d.Workflow != nil {
		w := d.Workflow.toDomain()
		in.Workflow = &w
	}
	return in
}
