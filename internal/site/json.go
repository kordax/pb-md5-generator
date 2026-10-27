package site

import (
	"encoding/json"
	"fmt"
)

// validateJSON rejects ambiguous duplicate members and excessively nested input
// before decoding the typed contract. encoding/json otherwise accepts duplicates.
func validateJSON(decoder *json.Decoder, depth int) error {
	if depth > 16 {
		return fmt.Errorf("manifest JSON nesting exceeds 16 levels")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]bool)
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok {
				return fmt.Errorf("invalid object member")
			}
			if seen[name] {
				return fmt.Errorf("duplicate JSON member %q", name)
			}
			seen[name] = true
			if err := validateJSON(decoder, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := validateJSON(decoder, depth+1); err != nil {
				return err
			}
		}
	}
	_, err = decoder.Token()
	return err
}
