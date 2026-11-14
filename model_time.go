package comitia

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

func id() string {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b[:])
}

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// ts trims trailing fractional zeros, so its strings do not sort by time:
// "05Z" follows "05.25Z". sortableTS is the fixed-width form; sortableTime
// renders SQL mapping a stored ts() column, including rows written by older
// binaries, to it. Indexes must repeat the expression exactly.
const sortableLayout = "2006-01-02T15:04:05.000000000Z"

func sortableTS(t time.Time) string { return t.UTC().Format(sortableLayout) }

func parse(s string) time.Time { t, _ := time.Parse(time.RFC3339Nano, s); return t }
