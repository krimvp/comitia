package comitia

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Invalidf names a failed field or rule while preserving ErrInvalid. Callers
// must use safe descriptions, never credentials or rejected input values.
func Invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// InvalidJSON describes decoder failures without exposing input values.
func InvalidJSON(err error, field string) error {
	const unknown = "json: unknown field "
	if strings.HasPrefix(err.Error(), unknown) {
		return Invalidf("unknown field %s", strings.TrimPrefix(err.Error(), unknown))
	}
	var mismatch *json.UnmarshalTypeError
	if errors.As(err, &mismatch) && mismatch.Field != "" {
		return Invalidf("%s must have type %s", mismatch.Field, mismatch.Type)
	}
	return Invalidf("%s must be valid JSON with the expected types", field)
}
