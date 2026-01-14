package birdnet

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

func TestBatchRequest_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		request BatchRequest
		wantErr bool
	}{
		{
			name: "valid request",
			request: BatchRequest{
				Sample:     make([]float32, SampleSize),
				SourceID:   "test-source",
				ResultChan: make(chan BatchResponse, 1),
			},
			wantErr: false,
		},
		{
			name: "nil sample",
			request: BatchRequest{
				Sample:     nil,
				SourceID:   "test-source",
				ResultChan: make(chan BatchResponse, 1),
			},
			wantErr: true,
		},
		{
			name: "wrong sample size",
			request: BatchRequest{
				Sample:     make([]float32, 1000),
				SourceID:   "test-source",
				ResultChan: make(chan BatchResponse, 1),
			},
			wantErr: true,
		},
		{
			name: "nil result channel",
			request: BatchRequest{
				Sample:     make([]float32, SampleSize),
				SourceID:   "test-source",
				ResultChan: nil,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.request.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestBatchResponse_HasError(t *testing.T) {
	t.Parallel()

	t.Run("with error", func(t *testing.T) {
		t.Parallel()
		resp := BatchResponse{
			Results: nil,
			Err:     assert.AnError,
		}
		assert.True(t, resp.HasError())
	})

	t.Run("without error", func(t *testing.T) {
		t.Parallel()
		resp := BatchResponse{
			Results: []datastore.Results{{Species: "Robin", Confidence: 0.9}},
			Err:     nil,
		}
		assert.False(t, resp.HasError())
	})
}
