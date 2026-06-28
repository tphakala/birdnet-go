// Package elevation implements the BirdNET-Pi import permission elevation
// ladder: it decides whether source data can be read directly, copied via
// passwordless sudo, copied with an in-app sudo password, or only through
// copy-paste remediation. It wraps the privileged import-stage subcommand
// behind an injectable runner so the policy is testable without real sudo.
package elevation

import (
	"encoding/json"
	"log/slog"
	"unicode/utf8"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// redactedPlaceholder is what a Password renders as in any string or JSON form.
const redactedPlaceholder = "[REDACTED]"

// errBadPassword marks a malformed JSON password token.
var errBadPassword = errors.NewStd("invalid password encoding")

// Password is a memory-only sudo password. It is single-use: feed Bytes() to the
// privileged process stdin once, then Clear() it. It never serializes in
// cleartext and never appears in logs.
type Password []byte

// UnmarshalJSON decodes a JSON string token directly into a clearable []byte,
// never through an intermediate Go string. A Go string's backing array is
// immutable and runtime-managed: it cannot be zeroed and would leave a permanent
// cleartext copy of the password in memory until GC. We hand-roll the JSON
// string unescape (the standard escapes plus \uXXXX) into a fresh byte buffer so
// the only cleartext bytes live in a slice Clear() can wipe.
func (p *Password) UnmarshalJSON(b []byte) error {
	out, err := decodeJSONString(b)
	if err != nil {
		// Zero any partially-decoded cleartext before discarding it.
		clear(out)
		return err
	}
	*p = out
	return nil
}

// decodeJSONString decodes a JSON string token into a fresh byte slice, handling
// the standard escapes plus \uXXXX. On error it returns whatever was decoded so
// far so the caller can zero it.
func decodeJSONString(b []byte) ([]byte, error) {
	if len(b) < 2 || b[0] != '"' || b[len(b)-1] != '"' {
		return nil, errBadPassword
	}
	body := b[1 : len(b)-1]
	out := make([]byte, 0, len(body))
	// Use an explicit index variable rather than a range form because the \u
	// handler advances the index by 4 to skip the hex digits, which is not
	// expressible with for i := range len(body).
	i := 0
	for i < len(body) {
		c := body[i]
		i++
		if c != '\\' {
			out = append(out, c)
			continue
		}
		if i >= len(body) {
			return out, errBadPassword
		}
		escaped := body[i]
		i++
		switch escaped {
		case '"', '\\', '/':
			out = append(out, escaped)
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'u':
			if i+4 > len(body) {
				return out, errBadPassword
			}
			var r rune
			for j := range 4 {
				d, ok := hexVal(body[i+j])
				if !ok {
					return out, errBadPassword
				}
				r = r<<4 | rune(d)
			}
			i += 4
			var enc [utf8.UTFMax]byte
			n := utf8.EncodeRune(enc[:], r)
			out = append(out, enc[:n]...)
		default:
			return out, errBadPassword
		}
	}
	return out, nil
}

// hexVal returns the numeric value of a single hex digit and whether it was valid.
func hexVal(c byte) (int, bool) {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0'), true
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10, true
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10, true
	}
	return 0, false
}

// LogValue redacts the password for slog. Without this, slog.Any on a raw []byte
// dumps the bytes (base64), leaking the secret into logs.
func (p Password) LogValue() slog.Value { return slog.StringValue(redactedPlaceholder) }

// String renders the password as a fixed redaction marker so it cannot leak via
// %v/%s formatting in logs or error context.
func (p Password) String() string { return redactedPlaceholder }

// MarshalJSON renders the password as a redaction marker, never the cleartext.
func (p Password) MarshalJSON() ([]byte, error) {
	return json.Marshal(redactedPlaceholder)
}

// Bytes returns the raw password bytes for single-use stdin feeding.
func (p Password) Bytes() []byte { return []byte(p) }

// Clear zeroes the password bytes and releases the slice.
func (p *Password) Clear() {
	if p == nil {
		return
	}
	clear(*p)
	*p = nil
}
