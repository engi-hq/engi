package jsoncodec

import (
	"encoding/json"
	"fmt"

	"github.com/engi-hq/engi/internal/domain"
)

// EncodeRunData serializes the per-node runs of an execution.
func EncodeRunData(runs map[string][]domain.NodeRun) ([]byte, error) {
	dto := make(map[string][]nodeRunDTO, len(runs))
	for node, rs := range runs {
		out := make([]nodeRunDTO, len(rs))
		for i, r := range rs {
			out[i] = runToDTO(r)
		}
		dto[node] = out
	}
	b, err := json.Marshal(dto)
	if err != nil {
		return nil, fmt.Errorf("encoding run data: %w", err)
	}
	return b, nil
}

// DecodeRunData is the inverse of EncodeRunData.
func DecodeRunData(data []byte) (map[string][]domain.NodeRun, error) {
	var dto map[string][]nodeRunDTO
	if err := decodeStrict(data, &dto); err != nil {
		return nil, fmt.Errorf("decoding run data: %w", err)
	}
	runs := make(map[string][]domain.NodeRun, len(dto))
	for node, rs := range dto {
		out := make([]domain.NodeRun, len(rs))
		for i, r := range rs {
			out[i] = runToDomain(r)
		}
		runs[node] = out
	}
	return runs, nil
}

// EncodeErrorInfo serializes an execution error; nil encodes to "null".
func EncodeErrorInfo(e *domain.ErrorInfo) ([]byte, error) {
	b, err := json.Marshal(errorToDTO(e))
	if err != nil {
		return nil, fmt.Errorf("encoding error info: %w", err)
	}
	return b, nil
}

// DecodeErrorInfo is the inverse of EncodeErrorInfo.
func DecodeErrorInfo(data []byte) (*domain.ErrorInfo, error) {
	var dto *errorDTO
	if err := decodeStrict(data, &dto); err != nil {
		return nil, fmt.Errorf("decoding error info: %w", err)
	}
	return dto.toDomain(), nil
}
