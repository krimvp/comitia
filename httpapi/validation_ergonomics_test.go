package httpapi

import (
	"errors"
	"strings"
	"testing"

	"github.com/krimvp/comitia"
)

func TestRequestUnknownFieldGuidance(t *testing.T) {
	for _, tc := range []struct {
		raw, want string
		target    any
	}{
		{`{"context_id":"c","after_sequence":"private-value"}`, `did you mean "after"?`, new(replayRequest)},
		{`{"context_id":"c","through":"private-value"}`, `did you mean "after"?`, new(ackRequest)},
		{`{"context_id":"c","message":{"dedupe_key":"d","envelope":{"schma":"private-value"}}}`, `did you mean "schema"?`, new(appendRequest)},
	} {
		err := decodeRequest(strings.NewReader(tc.raw), tc.target)
		if !errors.Is(err, comitia.ErrInvalid) || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "accepted fields:") || strings.Contains(err.Error(), "private-value") {
			t.Fatalf("error: %v", err)
		}
	}
}
