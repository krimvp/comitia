package httpapi

import (
	"encoding/json"
	"reflect"
	"strings"
	"time"

	"github.com/krimvp/comitia"
)

const wireHeader = "X-Comitia-Wire"
const canonicalWire = "canonical"

// The v1 default keeps the original record names. Canonical responses require
// explicit negotiation; old servers safely ignore the new Client's header.
func legacyValue(value reflect.Value) any {
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return nil
		}
		return legacyValue(value.Elem())
	}
	switch v := value.Interface().(type) {
	case principalDTO:
		return comitia.Principal(v)
	case sessionDTO:
		return comitia.Session(v)
	case credentialDTO:
		return v.core()
	case workspaceDTO:
		return comitia.Workspace(v)
	case contextDTO:
		return comitia.WorkContext(v)
	case referenceDTO:
		return comitia.Reference(v)
	case grantDTO:
		return comitia.Grant(v)
	case messageDTO:
		return comitia.Message(v)
	case participantDTO:
		return comitia.Participant(v)
	case taskDTO:
		return comitia.Task(v)
	case claimDTO:
		return v.core()
	case pageDTO:
		return v.core()
	case taskPageDTO:
		return v.core()
	case participantPageDTO:
		return v.core()
	case createContextInput:
		return comitia.CreateContextInput(v)
	case childContextInput:
		return comitia.CreateChildContextInput(v)
	case referenceInput:
		return comitia.AddReferenceInput(v)
	case grantInput:
		return comitia.GrantInput(v)
	case messageInput:
		return comitia.MessageInput(v)
	case taskInput:
		return comitia.TaskInput(v)
	case delegateInput:
		ttl := time.Duration(0)
		if v.TTL != nil {
			ttl = *v.TTL
		}
		return comitia.DelegateInput{PrincipalName: v.PrincipalName, TTL: ttl, Grants: mapSlice(v.Grants, func(g delegatedGrantInput) comitia.DelegatedGrant { return comitia.DelegatedGrant(g) })}
	case json.RawMessage, time.Time:
		return v
	}
	switch value.Kind() {
	case reflect.Struct:
		out := make(map[string]any)
		typ := value.Type()
		for i := 0; i < value.NumField(); i++ {
			field := value.Field(i)
			tag := strings.Split(typ.Field(i).Tag.Get("json"), ",")
			if tag[0] == "-" {
				continue
			}
			if len(tag) > 1 && tag[1] == "omitempty" && field.IsZero() {
				continue
			}
			key := tag[0]
			if key == "" {
				key = typ.Field(i).Name
			}
			out[key] = legacyValue(field)
		}
		return out
	case reflect.Slice:
		out := make([]any, value.Len())
		for i := range out {
			out[i] = legacyValue(value.Index(i))
		}
		return out
	default:
		return value.Interface()
	}
}

// Old v1 Handlers omitted empty lists. Only negotiated canonical responses
// require collection members; preserve normalized API results for old servers.
func legacyCollections(value reflect.Value) {
	typ := value.Type()
	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		if field.Kind() == reflect.Slice && field.IsNil() && !strings.Contains(typ.Field(i).Tag.Get("json"), "omitempty") {
			field.Set(reflect.MakeSlice(field.Type(), 0, 0))
		}
		if field.Kind() == reflect.Pointer {
			if field.IsNil() && field.Type().Elem().Kind() == reflect.Slice {
				field.Set(reflect.New(field.Type().Elem()))
				field.Elem().Set(reflect.MakeSlice(field.Type().Elem(), 0, 0))
			}
			if !field.IsNil() {
				field = field.Elem()
			}
		}
		if field.Kind() == reflect.Struct && field.Type() != reflect.TypeOf(time.Time{}) {
			legacyCollections(field)
		}
	}
}
