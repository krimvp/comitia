package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/krimvp/comitia"
)

func TestEnvelopeUnknownFieldGuidance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "envelope.json")
	if err := os.WriteFile(path, []byte(`{"schma":"private-value","payload":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := readEnvelope(path)
	if !errors.Is(err, comitia.ErrInvalid) || !strings.Contains(err.Error(), "accepted fields:") || !strings.Contains(err.Error(), `did you mean "schema"?`) || strings.Contains(err.Error(), "private-value") {
		t.Fatalf("error: %v", err)
	}
}
