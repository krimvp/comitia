// Decoding and validating tool arguments.

package mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/krimvp/comitia"
)

func decode(raw json.RawMessage, dst any) error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '{' {
		return comitia.Invalidf("arguments must be a JSON object")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		detail := comitia.InvalidJSONFields(err, "arguments", raw, dst)
		if strings.HasPrefix(err.Error(), "json: unknown field ") {
			return newUnknownFieldError(strings.TrimPrefix(detail.Error(), "invalid input: "))
		}
		return detail
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return comitia.Invalidf("arguments must contain one JSON object")
	}
	return nil
}
func required(values ...string) error {
	for i := 0; i < len(values); i += 2 {
		v := values[i+1]
		if strings.TrimSpace(v) == "" {
			return comitia.Invalidf("%s required", values[i])
		}
	}
	return nil
}

// seconds reads lease_seconds: zero is comitia.DefaultLease, otherwise whole
// seconds between comitia.MinLease and comitia.MaxLease.
func seconds(n int64) (time.Duration, error) {
	if n == 0 {
		n = leaseSeconds(comitia.DefaultLease)
	}
	if n < leaseSeconds(comitia.MinLease) || n > leaseSeconds(comitia.MaxLease) {
		return 0, comitia.Invalidf("lease_seconds must be between %d and %d", leaseSeconds(comitia.MinLease), leaseSeconds(comitia.MaxLease))
	}
	return time.Duration(n) * time.Second, nil
}
