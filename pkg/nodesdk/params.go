package nodesdk

import (
	"context"
	"fmt"
	"math"
)

// ParamString resolves a parameter that must be a string.
func ParamString(ctx context.Context, in Input, name string, itemIndex int) (string, error) {
	v, err := in.Param(ctx, name, itemIndex)
	if err != nil {
		return "", err
	}
	s, ok := v.(string)
	if !ok && v != nil {
		return "", fmt.Errorf("parameter %q must be a string, got %T", name, v)
	}
	return s, nil
}

// ParamBool resolves a parameter that must be a boolean.
func ParamBool(ctx context.Context, in Input, name string, itemIndex int) (bool, error) {
	v, err := in.Param(ctx, name, itemIndex)
	if err != nil {
		return false, err
	}
	b, ok := v.(bool)
	if !ok && v != nil {
		return false, fmt.Errorf("parameter %q must be a boolean, got %T", name, v)
	}
	return b, nil
}

// ParamInt resolves a parameter that must be a whole number.
func ParamInt(ctx context.Context, in Input, name string, itemIndex int) (int, error) {
	v, err := in.Param(ctx, name, itemIndex)
	if err != nil {
		return 0, err
	}
	switch n := v.(type) {
	case nil:
		return 0, nil
	case int:
		return n, nil
	case int64:
		return int(n), nil
	case float64:
		if n != math.Trunc(n) {
			return 0, fmt.Errorf("parameter %q must be a whole number, got %v", name, n)
		}
		return int(n), nil
	}
	return 0, fmt.Errorf("parameter %q must be a number, got %T", name, v)
}
