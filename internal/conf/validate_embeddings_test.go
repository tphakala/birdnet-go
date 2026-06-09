package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateEmbeddingsSettings(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		cfg     EmbeddingsConfig
		wantErr bool
	}{
		{name: "empty format defaults ok", cfg: EmbeddingsConfig{}, wantErr: false},
		{name: "fp16 ok", cfg: EmbeddingsConfig{Storage: EmbeddingsStorageConfig{Format: "fp16"}}, wantErr: false},
		{name: "int8 rejected (unimplemented)", cfg: EmbeddingsConfig{Storage: EmbeddingsStorageConfig{Format: "int8"}}, wantErr: true},
		{name: "unknown format rejected", cfg: EmbeddingsConfig{Storage: EmbeddingsStorageConfig{Format: "bogus"}}, wantErr: true},
		{name: "negative maxrows rejected", cfg: EmbeddingsConfig{Storage: EmbeddingsStorageConfig{MaxRows: -1}}, wantErr: true},
		{name: "zero maxrows ok (means default)", cfg: EmbeddingsConfig{Storage: EmbeddingsStorageConfig{MaxRows: 0}}, wantErr: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := tt.cfg
			err := validateEmbeddingsSettings(&cfg)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// finalizeValidation (storage.go) treats every error returned by a validator
// as fatal regardless of message text, so an unsupported embeddings format
// fails startup. This pins that contract from the embeddings side; the
// message-independence regression test lives in storage_test.go.
func TestValidateEmbeddingsSettings_UnsupportedFormatIsFatal(t *testing.T) {
	t.Parallel()
	err := validateEmbeddingsSettings(&EmbeddingsConfig{
		Storage: EmbeddingsStorageConfig{Format: "int8"},
	})
	require.Error(t, err)
	assert.ErrorContains(t, err, "not supported")
}
