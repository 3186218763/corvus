package builtin

import (
	"encoding/json"
	"fmt"
)

// decodeArgs unmarshals the model-supplied tool arguments. The "invalid args"
// wording is shared by every builtin so retry-on-bad-JSON behavior does not
// depend on which tool the model called.
func decodeArgs(args json.RawMessage, out any) error {
	if err := json.Unmarshal(args, out); err != nil {
		return fmt.Errorf("invalid args: %w", err)
	}
	return nil
}
