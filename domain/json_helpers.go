package domain

import (
	"encoding/json"
	"fmt"
)

// ApplyOptionalFloat parses an optional JSON number into target. An empty or
// null raw value clears target; otherwise the value is unmarshaled, validated,
// and stored.
func ApplyOptionalFloat(raw json.RawMessage, validate func(float64) error, target **float64) error {
	if len(raw) == 0 {
		return nil
	}
	if string(raw) == "null" {
		*target = nil
		return nil
	}
	var v float64
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("invalid number: %w", err)
	}
	if err := validate(v); err != nil {
		return err
	}
	*target = &v
	return nil
}

// ApplyOptionalInt is the integer variant of ApplyOptionalFloat for top_k.
func ApplyOptionalInt(raw json.RawMessage, validate func(int) error, target **int) error {
	if len(raw) == 0 {
		return nil
	}
	if string(raw) == "null" {
		*target = nil
		return nil
	}
	var v int
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("invalid integer: %w", err)
	}
	if err := validate(v); err != nil {
		return err
	}
	*target = &v
	return nil
}
