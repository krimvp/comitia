package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"github.com/krimvp/comitia"
)

func readEnvelope(path string) (*comitia.MessageEnvelope, error) {
	if path == "" { return nil, nil }
	f, err := os.Open(path)
	if err != nil { return nil, err }
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil { return nil, err }
	if len(raw) > 65536 { return nil, comitia.ErrInvalid }
	d := json.NewDecoder(bytes.NewReader(raw)); d.DisallowUnknownFields()
	var env *comitia.MessageEnvelope
	if err = d.Decode(&env); err != nil { return nil, err }
	if env == nil { return nil, comitia.ErrInvalid }
	if d.Decode(new(any)) != io.EOF { return nil, comitia.ErrInvalid }
	return env, nil
}
