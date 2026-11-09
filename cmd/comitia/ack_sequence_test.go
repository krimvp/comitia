package main

import (
	"errors"
	"strconv"
	"testing"

	"github.com/krimvp/comitia"
)

// Like MCP ack_messages, ack names the sequence instead of acking 0.
func TestCLIAckRequiresSequence(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(strconv.FormatBool(remote), func(t *testing.T) {
			base, cid := instanceCommandSetup(t, remote)
			if err := commandErr(t, append(base, "ack", "--context-id", cid)...); !errors.Is(err, comitia.ErrInvalid) {
				t.Fatalf("missing sequence: %v", err)
			}
			command(t, append(base, "ack", "--context-id", cid, "--sequence", "0")...)
		})
	}
}
