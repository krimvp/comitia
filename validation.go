package comitia

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
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

// UnknownField lists accepted keys and suggests a nearby spelling. Names come
// from the contract; request values are never included.
func UnknownField(name string, fields []string) error {
	fields = append([]string(nil), fields...)
	sort.Strings(fields)
	detail := fmt.Sprintf("unknown field %q", name)
	if len(fields) == 0 {
		return Invalidf("%s; no fields are accepted", detail)
	}
	detail += "; accepted fields: " + strings.Join(fields, ", ")
	nearest, distance := "", len(name)+1
	for _, field := range fields {
		if len(name) > 128 {
			break
		}
		d := fieldDistance(strings.ToLower(name), strings.ToLower(field))
		if strings.HasPrefix(name, field+"_") || strings.HasSuffix(name, "_"+field) {
			d = 1
		}
		if name == "through" && (field == "sequence" || field == "after") {
			d = 1
		}
		if d < distance {
			nearest, distance = field, d
		} else if d == distance {
			if nearest != "" && strings.EqualFold(nearest, field) {
				if field == strings.ToLower(field) {
					nearest = field
				}
			} else {
				nearest = ""
			}
		}
	}
	if nearest != "" && distance <= max(2, len(name)/3) {
		detail += fmt.Sprintf("; did you mean %q?", nearest)
	}
	return Invalidf("%s", detail)
}

func fieldDistance(a, b string) int {
	previous := make([]int, len(b)+1)
	for i := range previous {
		previous[i] = i
	}
	for i := 0; i < len(a); i++ {
		current := make([]int, len(b)+1)
		current[0] = i + 1
		for j := 0; j < len(b); j++ {
			cost := 0
			if a[i] != b[j] {
				cost = 1
			}
			current[j+1] = min(current[j]+1, previous[j+1]+1, previous[j]+cost)
		}
		previous = current
	}
	return previous[len(b)]
}

// InvalidJSONFields adds the keys accepted at the failing object's level.
// InvalidJSON remains available to decoders without a request contract.
func InvalidJSONFields(err error, field string, raw json.RawMessage, target any) error {
	const prefix = "json: unknown field "
	if strings.HasPrefix(err.Error(), prefix) {
		name, e := strconv.Unquote(strings.TrimPrefix(err.Error(), prefix))
		if e == nil {
			if fields, ok := unknownObjectFields(raw, reflect.TypeOf(target), name); ok {
				return UnknownField(name, fields)
			}
		}
	}
	return InvalidJSON(err, field)
}

func jsonFieldTypes(typ reflect.Type) map[string]reflect.Type {
	for typ != nil && typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ == nil || typ.Kind() != reflect.Struct {
		return nil
	}
	fields := make(map[string]reflect.Type)
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.PkgPath != "" {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if f.Anonymous && name == "" {
			for key, child := range jsonFieldTypes(f.Type) {
				fields[key] = child
			}
			continue
		}
		if name == "" {
			name = f.Name
		}
		fields[name] = f.Type
	}
	return fields
}

func unknownObjectFields(raw json.RawMessage, typ reflect.Type, name string) ([]string, bool) {
	for typ != nil && typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ == nil || typ == reflect.TypeOf(json.RawMessage{}) {
		return nil, false
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil {
		return nil, false
	}
	fields := jsonFieldTypes(typ)
	for d.More() {
		var childType reflect.Type
		switch token {
		case json.Delim('{'):
			keyToken, err := d.Token()
			if err != nil {
				return nil, false
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, false
			}
			if fields != nil {
				childType = fields[key]
				if childType == nil {
					// encoding/json also accepts case-insensitive aliases.
					for candidate, t := range fields {
						if strings.EqualFold(candidate, key) {
							childType = t
							break
						}
					}
				}
				if key == name && childType == nil {
					keys := make([]string, 0, len(fields))
					for candidate := range fields {
						keys = append(keys, candidate)
					}
					return keys, true
				}
			} else if typ.Kind() == reflect.Map {
				childType = typ.Elem()
			}
		case json.Delim('['):
			if typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array {
				childType = typ.Elem()
			}
		default:
			return nil, false
		}
		var child json.RawMessage
		if d.Decode(&child) != nil {
			return nil, false
		}
		if keys, ok := unknownObjectFields(child, childType, name); ok {
			return keys, true
		}
	}
	return nil, false
}

// Only these fixed reference descriptions can cross the public error boundary.
// Missing and unreadable targets deliberately share the same description.
type referenceError struct{ field string }

func (e referenceError) Error() string {
	switch e.field {
	case "task_id":
		return "forbidden: task_id must name a task in a readable context; use list_tasks"
	case "reply_to", "thread_id":
		return "forbidden: " + e.field + " must name a message in a readable context; use get_message or replay_messages"
	default:
		return "forbidden: " + e.field + " is not a reader of this context; use list_participants"
	}
}
func (e referenceError) Unwrap() error { return ErrForbidden }

func safeReferenceMessage(err error) string {
	var ref referenceError
	if errors.As(err, &ref) {
		return ref.Error()
	}
	var remote *OperationalError
	if !errors.As(err, &remote) {
		return ""
	}
	fields := []string{"task_id", "reply_to", "thread_id"}
	for i := 0; i < 64; i++ {
		fields = append(fields, fmt.Sprintf("recipient_session_ids[%d]", i))
	}
	for _, field := range fields {
		message := (referenceError{field}).Error()
		if remote.Info.Message == message {
			return message
		}
	}
	return ""
}
