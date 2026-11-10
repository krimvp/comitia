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
	raw, err := readInput(path, 65537)
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

// readInput reads at most limit bytes from a file, or from stdin for "-".
func readInput(path string, limit int64) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(io.LimitReader(os.Stdin, limit))
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, limit))
}

// readBody returns the --body-file contents verbatim, bounded like a message body.
func readBody(path string) (string, error) {
	raw, err := readInput(path, comitia.MaxMessageBodyBytes+1)
	if err != nil {
		return "", err
	}
	if len(raw) > comitia.MaxMessageBodyBytes {
		return "", comitia.Invalidf("body must be at most %d bytes", comitia.MaxMessageBodyBytes)
	}
	return string(raw), nil
}
