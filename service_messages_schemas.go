package comitia

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/krimvp/comitia/internal/schema"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func (s *Service) ListMessageSchemas(ctx context.Context, token string) ([]MessageSchemaDescriptor, error) {
	if err := s.schemaDiscoveryAuth(ctx, token); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(s.messageSchemaDescriptors))
	for id := range s.messageSchemaDescriptors {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]MessageSchemaDescriptor, 0, len(ids))
	for _, id := range ids {
		out = append(out, schema.CloneMessageSchema(s.messageSchemaDescriptors[id]))
	}
	return out, nil
}

func (s *Service) GetMessageSchema(ctx context.Context, token, id string) (MessageSchemaDescriptor, error) {
	if err := s.schemaDiscoveryAuth(ctx, token); err != nil {
		return MessageSchemaDescriptor{}, err
	}
	descriptor, ok := s.messageSchemaDescriptors[id]
	if !ok {
		return MessageSchemaDescriptor{}, ErrNotFound
	}
	return schema.CloneMessageSchema(descriptor), nil
}

func (s *Service) schemaDiscoveryAuth(ctx context.Context, token string) error {
	return s.store.View(ctx, func(r ReadStore) error { _, err := s.auth(r, token); return err })
}

func (s *Service) configureMessageSchemaDescriptors(configured map[string]MessageSchemaDescriptor) error {
	s.messageSchemaDescriptors = schema.BuiltinMessageSchemaDescriptors()
	for id := range s.messageSchemas {
		if _, ok := s.messageSchemaDescriptors[id]; !ok {
			s.messageSchemaDescriptors[id] = MessageSchemaDescriptor{ID: id, PayloadSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`), Opaque: true, Requirements: []string{"Additional payload constraints are defined by the extension validator; supply Config.MessageSchemaDescriptors for a complete contract."}, Examples: []MessageSchemaExample{}}
		}
	}
	for id, d := range configured {
		if _, ok := s.messageSchemas[id]; !ok || d.ID != "" && d.ID != id || d.Opaque || d.ServerAuthored {
			return Invalidf("invalid extension message schema descriptor")
		}
		if _, builtin := schema.BuiltinMessageSchemas()[id]; builtin {
			return Invalidf("built-in message schema descriptors cannot be replaced")
		}
		d.ID = id
		payloadSchema, err := jsonschema.UnmarshalJSON(bytes.NewReader(d.PayloadSchema))
		if err != nil {
			return Invalidf("invalid payload JSON Schema for %s", id)
		}
		object, ok := payloadSchema.(map[string]any)
		if !ok || object["type"] != "object" {
			return Invalidf("payload JSON Schema must describe an object for %s", id)
		}
		compiler := jsonschema.NewCompiler()
		compiler.UseLoader(jsonschema.SchemeURLLoader{})
		// A supplied descriptor must be self-contained; compilation never fetches URLs.
		url := "urn:comitia:message-schema:" + id
		if err := compiler.AddResource(url, payloadSchema); err != nil {
			return Invalidf("invalid payload JSON Schema for %s", id)
		}
		compiled, err := compiler.Compile(url)
		if err != nil {
			return Invalidf("invalid payload JSON Schema for %s", id)
		}
		if len(d.Examples) == 0 {
			return Invalidf("message schema examples required for %s", id)
		}
		if d.Requirements == nil {
			d.Requirements = []string{}
		}
		s.messageSchemaDescriptors[id] = schema.CloneMessageSchema(d)
		for _, example := range d.Examples {
			payload, err := jsonschema.UnmarshalJSON(bytes.NewReader(example.Envelope.Payload))
			if example.Envelope.Schema != id || err != nil {
				return Invalidf("invalid message schema example for %s", id)
			}
			if err := compiled.Validate(payload); err != nil {
				return Invalidf("example does not match payload JSON Schema for %s", id)
			}
			if _, err := s.normalizeEnvelope(&example.Envelope, example.Body); err != nil {
				return fmt.Errorf("%w: invalid message schema example for %s", ErrInvalid, id)
			}
		}
	}
	return nil
}
