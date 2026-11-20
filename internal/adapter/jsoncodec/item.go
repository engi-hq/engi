package jsoncodec

import (
	"fmt"

	"github.com/engi-hq/engi/internal/domain"
)

type itemDTO struct {
	JSON       map[string]any       `json:"json"`
	Binary     map[string]binaryDTO `json:"binary,omitempty"`
	PairedItem []pairedDTO          `json:"pairedItem,omitempty"`
	Error      *errorDTO            `json:"error,omitempty"`
}

type binaryDTO struct {
	Ref  string `json:"ref"`
	MIME string `json:"mime,omitempty"`
	Name string `json:"name,omitempty"`
	Size int64  `json:"size,omitempty"`
}

type pairedDTO struct {
	Input int `json:"input"`
	Item  int `json:"item"`
}

type errorDTO struct {
	Kind        string `json:"kind,omitempty"`
	Node        string `json:"node,omitempty"`
	Message     string `json:"message"`
	Description string `json:"description,omitempty"`
	HTTPStatus  int    `json:"httpStatus,omitempty"`
	ItemIndex   *int   `json:"itemIndex,omitempty"`
}

// DecodeItems parses a JSON array of items.
func DecodeItems(data []byte) ([]domain.Item, error) {
	var dtos []itemDTO
	if err := decodeStrict(data, &dtos); err != nil {
		return nil, fmt.Errorf("decoding items: %w", err)
	}
	return itemsToDomain(dtos), nil
}

func itemsToDomain(dtos []itemDTO) []domain.Item {
	if dtos == nil {
		return nil
	}
	out := make([]domain.Item, len(dtos))
	for i, d := range dtos {
		it := domain.Item{JSON: d.JSON, Error: d.Error.toDomain()}
		if it.JSON == nil {
			it.JSON = map[string]any{}
		}
		if len(d.Binary) > 0 {
			it.Binary = make(map[string]domain.BinaryRef, len(d.Binary))
			for k, b := range d.Binary {
				it.Binary[k] = domain.BinaryRef(b)
			}
		}
		for _, p := range d.PairedItem {
			it.PairedItem = append(it.PairedItem, domain.ItemRef(p))
		}
		out[i] = it
	}
	return out
}

func itemsToDTO(items []domain.Item) []itemDTO {
	if items == nil {
		return nil
	}
	out := make([]itemDTO, len(items))
	for i, it := range items {
		d := itemDTO{JSON: it.JSON, Error: errorToDTO(it.Error)}
		if d.JSON == nil {
			d.JSON = map[string]any{}
		}
		if len(it.Binary) > 0 {
			d.Binary = make(map[string]binaryDTO, len(it.Binary))
			for k, b := range it.Binary {
				d.Binary[k] = binaryDTO(b)
			}
		}
		for _, p := range it.PairedItem {
			d.PairedItem = append(d.PairedItem, pairedDTO(p))
		}
		out[i] = d
	}
	return out
}

func (e *errorDTO) toDomain() *domain.ErrorInfo {
	if e == nil {
		return nil
	}
	return &domain.ErrorInfo{
		Kind: domain.ErrorKind(e.Kind), Node: e.Node, Message: e.Message,
		Description: e.Description, HTTPStatus: e.HTTPStatus, ItemIndex: e.ItemIndex,
	}
}

func errorToDTO(e *domain.ErrorInfo) *errorDTO {
	if e == nil {
		return nil
	}
	return &errorDTO{
		Kind: string(e.Kind), Node: e.Node, Message: e.Message,
		Description: e.Description, HTTPStatus: e.HTTPStatus, ItemIndex: e.ItemIndex,
	}
}
