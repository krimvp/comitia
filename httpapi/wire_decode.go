package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"reflect"
	"strings"
	"time"

	"github.com/krimvp/comitia"
)

// Legacy nested Go field names are read-only aliases. Normalization happens
// before decoding so aliases cannot overwrite a canonical field silently.
func decodeRequest(body io.Reader, target any) error {
	d := json.NewDecoder(body)
	var raw json.RawMessage
	if err := d.Decode(&raw); err != nil {
		return comitia.InvalidJSON(err, "request")
	}
	if err := ensureEOF(d); err != nil {
		return comitia.Invalidf("multiple JSON values")
	}
	raw, err := normalizeFields(raw, reflect.TypeOf(target).Elem(), false, true)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return comitia.InvalidJSON(err, "request")
	}
	return nil
}

func normalizeFields(raw json.RawMessage, typ reflect.Type, legacy, strict bool) (json.RawMessage, error) {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ == reflect.TypeOf(json.RawMessage{}) || typ == reflect.TypeOf(time.Time{}) {
		return raw, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if typ.Kind() == reflect.Struct {
			return nil, comitia.Invalidf("expected JSON object")
		}
		return raw, nil
	}
	if typ.Kind() == reflect.Slice {
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, comitia.InvalidJSON(err, "request")
		}
		for i := range items {
			v, err := normalizeFields(items[i], typ.Elem(), true, strict)
			if err != nil {
				return nil, err
			}
			items[i] = v
		}
		return json.Marshal(items)
	}
	if typ.Kind() != reflect.Struct {
		return raw, nil
	}
	fields := make(map[string]reflect.StructField)
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		key := strings.Split(f.Tag.Get("json"), ",")[0]
		if key == "-" {
			continue
		}
		if key == "" {
			key = f.Name
		}
		fields[key] = f
		if legacy {
			fields[f.Name] = f
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	tok, err := d.Token()
	if err != nil || tok != json.Delim('{') {
		return nil, comitia.Invalidf("expected JSON object")
	}
	object := make(map[string]json.RawMessage)
	for d.More() {
		tok, err := d.Token()
		if err != nil {
			return nil, comitia.InvalidJSON(err, "request")
		}
		key := tok.(string)
		f, ok := fields[key]
		if !ok {
			if strict {
				return nil, comitia.Invalidf("unknown field %q", key)
			}
			var ignored json.RawMessage
			if err := d.Decode(&ignored); err != nil {
				return nil, comitia.InvalidJSON(err, "response")
			}
			continue
		}
		canonical := strings.Split(f.Tag.Get("json"), ",")[0]
		if canonical == "" {
			canonical = f.Name
		}
		if _, exists := object[canonical]; exists {
			return nil, comitia.Invalidf("conflicting fields for %s", canonical)
		}
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			return nil, comitia.InvalidJSON(err, "request")
		}
		// Null optional members preserve omission defaults; objects still require
		// an object when present. The top-level request can never be null.
		if f.Type.Kind() == reflect.Pointer && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			object[canonical] = value
			continue
		}
		value, err = normalizeFields(value, f.Type, true, strict)
		if err != nil {
			return nil, err
		}
		object[canonical] = value
	}
	if _, err := d.Token(); err != nil {
		return nil, comitia.InvalidJSON(err, "request")
	}
	if _, old := object["ttl"]; old {
		if _, canonical := object["ttl_seconds"]; canonical {
			return nil, comitia.Invalidf("conflicting ttl and ttl_seconds fields")
		}
		if _, canonical := object["lease_seconds"]; canonical {
			return nil, comitia.Invalidf("conflicting ttl and lease_seconds fields")
		}
	}
	if bytes.Equal(object["open_ended"], []byte("true")) {
		if _, exists := object["ttl"]; exists {
			return nil, comitia.Invalidf("open_ended excludes ttl")
		}
		if _, exists := object["lease_seconds"]; exists {
			return nil, comitia.Invalidf("open_ended excludes lease_seconds")
		}
	}
	return json.Marshal(object)
}

func listLimit(n *int) (int, error) {
	if n == nil {
		return 100, nil
	}
	if *n < 1 || *n > 1000 {
		return 0, comitia.Invalidf("limit must be between 1 and 1000")
	}
	return *n, nil
}

func durationSeconds(seconds *float64, legacy *time.Duration, fallback time.Duration, open bool) (time.Duration, error) {
	if legacy != nil && seconds != nil || open && (legacy != nil || seconds != nil) {
		return 0, comitia.Invalidf("conflicting duration fields")
	}
	if open {
		return comitia.OpenLease, nil
	}
	if legacy != nil {
		if *legacy <= 0 {
			return 0, comitia.Invalidf("ttl must be positive")
		}
		return *legacy, nil
	}
	if seconds == nil {
		if fallback <= 0 {
			return 0, comitia.Invalidf("ttl_seconds required")
		}
		return fallback, nil
	}
	if math.IsNaN(*seconds) || math.IsInf(*seconds, 0) || *seconds <= 0 || *seconds >= float64(math.MaxInt64)/float64(time.Second) {
		return 0, comitia.Invalidf("seconds must be positive and fit a duration")
	}
	d := time.Duration(*seconds * float64(time.Second))
	if d <= 0 {
		return 0, comitia.Invalidf("seconds must be at least one nanosecond")
	}
	return d, nil
}

func requiredResult(err error, values ...any) error {
	if err != nil {
		return err
	}
	for _, v := range values {
		if reflect.ValueOf(v).IsNil() {
			return fmt.Errorf("malformed remote response: missing result")
		}
	}
	return nil
}

func validateCollections(out any) error {
	return validateCollectionValue(reflect.ValueOf(out).Elem())
}
func validateCollectionValue(value reflect.Value) error {
	typ := value.Type()
	for i := 0; i < value.NumField(); i++ {
		f := value.Field(i)
		if f.Kind() == reflect.Slice && f.IsNil() && !strings.Contains(typ.Field(i).Tag.Get("json"), "omitempty") {
			return fmt.Errorf("malformed remote response: missing collection %s", typ.Field(i).Name)
		}
		if f.Kind() == reflect.Pointer && !f.IsNil() {
			f = f.Elem()
		}
		if f.Kind() == reflect.Struct && f.Type() != reflect.TypeOf(time.Time{}) {
			if err := validateCollectionValue(f); err != nil {
				return err
			}
		}
	}
	return nil
}
