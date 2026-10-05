package main

import (
	"bytes"
	"encoding/json"
	"github.com/krimvp/comitia"
	"io"
	"os"
)

func readEnvelope(path string) (*comitia.MessageEnvelope, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil {
		return nil, err
	}
	if len(raw) > 65536 {
		return nil, comitia.Invalidf("envelope must be at most 65536 bytes")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	var env *comitia.MessageEnvelope
	if err = d.Decode(&env); err != nil {
		return nil, comitia.InvalidJSONFields(err, "envelope", raw, new(comitia.MessageEnvelope))
	}
	if env == nil {
		return nil, comitia.Invalidf("envelope must contain one JSON object")
	}
	if d.Decode(new(any)) != io.EOF {
		return nil, comitia.Invalidf("envelope must contain one JSON value")
	}
	return env, nil
}
