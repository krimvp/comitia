package comitia_test

import (
	"testing"
	"time"

	"github.com/krimvp/comitia"
)

// TestLimitValues pins the shared limits. Adapters derive help, schema,
// description and error text from them, and clients rely on the values, so a
// change here is a deliberate change of the documented contract.
func TestLimitValues(t *testing.T) {
	for _, c := range []struct {
		name      string
		got, want int64
	}{
		{"DefaultListLimit", comitia.DefaultListLimit, 100},
		{"MaxListLimit", comitia.MaxListLimit, 1000},
		{"MinLease", int64(comitia.MinLease), int64(time.Second)},
		{"MaxLease", int64(comitia.MaxLease), int64(24 * time.Hour)},
		{"DefaultLease", int64(comitia.DefaultLease), int64(300 * time.Second)},
		{"DefaultMessageWait", int64(comitia.DefaultMessageWait), int64(30 * time.Second)},
		{"MaxMessageWait", int64(comitia.MaxMessageWait), int64(60 * time.Second)},
		{"MaxEnvelopeBytes", comitia.MaxEnvelopeBytes, 65536},
		{"MaxEnvelopeRecipients", comitia.MaxEnvelopeRecipients, 64},
		{"MaxTaskReasonBytes", comitia.MaxTaskReasonBytes, 4096},
	} {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
}
