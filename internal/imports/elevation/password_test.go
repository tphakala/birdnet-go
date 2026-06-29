package elevation

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPasswordUnmarshalAndClear(t *testing.T) {
	var body struct {
		Password Password `json:"password"`
	}
	require.NoError(t, json.Unmarshal([]byte(`{"password":"hunter2"}`), &body))
	assert.Equal(t, []byte("hunter2"), body.Password.Bytes())

	body.Password.Clear()
	assert.Nil(t, body.Password.Bytes())
}

func TestPasswordNeverSerializesCleartext(t *testing.T) {
	p := Password("hunter2")
	assert.Equal(t, "[REDACTED]", p.String())

	out, err := json.Marshal(struct {
		P Password `json:"p"`
	}{P: p})
	require.NoError(t, err)
	assert.NotContains(t, string(out), "hunter2")
	assert.Contains(t, string(out), "[REDACTED]")
}

func TestPasswordRedactedInSlog(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	log.Info("attempt", slog.Any("password", Password("hunter2")))
	assert.NotContains(t, buf.String(), "hunter2")
	assert.Contains(t, buf.String(), "[REDACTED]")
}

func TestPasswordUnmarshalRejectsBadEscape(t *testing.T) {
	// Call UnmarshalJSON directly with a malformed token: encoding/json validates
	// string escapes itself and would reject this before our decoder runs, so a
	// direct call is the only way to exercise the decoder's error path (which
	// zeroes the partial decode before returning).
	var p Password
	err := p.UnmarshalJSON([]byte(`"a\x"`))
	require.Error(t, err)
	assert.Empty(t, p.Bytes())
}

func TestPasswordUnmarshalHandlesEscapes(t *testing.T) {
	var body struct {
		Password Password `json:"password"`
	}
	// A password containing a quote and a backslash, JSON-escaped.
	require.NoError(t, json.Unmarshal([]byte(`{"password":"a\"b\\c"}`), &body))
	assert.Equal(t, []byte(`a"b\c`), body.Password.Bytes())
}

func TestPasswordUnmarshalHandlesSurrogatePair(t *testing.T) {
	var body struct {
		Password Password `json:"password"`
	}
	// U+1F600 JSON-escaped as a UTF-16 surrogate pair must decode to the single
	// 4-byte UTF-8 code point, not two corrupted halves. The escaped \u form (not
	// a raw emoji) is required to exercise the surrogate-pair decode path.
	payload := []byte("{\"password\":\"p\\uD83D\\uDE00\"}")
	require.NoError(t, json.Unmarshal(payload, &body))
	assert.Equal(t, append([]byte("p"), "\U0001F600"...), body.Password.Bytes())
}

func TestPasswordUnmarshalRejectsNullByte(t *testing.T) {
	var body struct {
		Password Password `json:"password"`
	}
	// A JSON-escaped NUL (backslash-u-0000) must be rejected: sudo -S and PAM read
	// the password as a C string and stop at the first NUL, so a truncated
	// credential must never be silently accepted. The escape is written as a Go
	// string literal so no actual NUL byte appears in this source file.
	payload := []byte("{\"password\":\"ab\\u0000cd\"}")
	err := json.Unmarshal(payload, &body)
	require.Error(t, err)
	assert.Nil(t, body.Password.Bytes())
}
