package testutil

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"
)

// RuleOnlyZoneRule is the POSIX rule of the location RuleOnlyLocation returns: EET with EEST
// in summer.
const RuleOnlyZoneRule = "EET-2EEST,M3.5.0/3,M10.5.0/4"

// RuleOnlyLocation returns a location whose only transition lies at the Unix epoch, so every
// later instant is resolved from its POSIX rule (RuleOnlyZoneRule), as it is for the years after
// the last transition of a slim zone file. The result does not depend on the zone files of the
// host.
func RuleOnlyLocation(tb testing.TB) *time.Location {
	tb.Helper()
	loc, err := time.LoadLocationFromTZData("Test/RuleOnly", ruleOnlyTZif(tb))
	if err != nil {
		tb.Fatalf("load rule-only test zone: %v", err)
	}
	return loc
}

// ruleOnlyTZif builds a minimal version 2 TZif file whose single transition lies at the epoch.
func ruleOnlyTZif(tb testing.TB) []byte {
	tb.Helper()

	const (
		utcOffsetSeconds = 2 * 3600
		abbreviations    = "EET\x00"
	)
	put := func(w *bytes.Buffer, v any) {
		if err := binary.Write(w, binary.BigEndian, v); err != nil {
			tb.Fatalf("write TZif data: %v", err)
		}
	}
	writeHeader := func(w *bytes.Buffer) {
		w.WriteString("TZif2")
		w.Write(make([]byte, 15))
		// isutcnt, isstdcnt, leapcnt, timecnt, typecnt, charcnt
		for _, n := range []uint32{0, 0, 0, 1, 1, uint32(len(abbreviations))} {
			put(w, n)
		}
	}
	writeBody := func(w *bytes.Buffer, transitionTime any) {
		put(w, transitionTime)
		w.WriteByte(0) // transition type index
		put(w, int32(utcOffsetSeconds))
		w.WriteByte(0) // not DST
		w.WriteByte(0) // abbreviation index
		w.WriteString(abbreviations)
	}

	var w bytes.Buffer
	writeHeader(&w)
	writeBody(&w, int32(0))
	writeHeader(&w)
	writeBody(&w, int64(0))
	w.WriteString("\n" + RuleOnlyZoneRule + "\n")
	return w.Bytes()
}
