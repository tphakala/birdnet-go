package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewGitHubClient_TrimsTrailingSlash(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		baseURL string
		want    string
	}{
		{name: "no slash", baseURL: "https://api.github.com", want: "https://api.github.com"},
		{name: "one trailing slash", baseURL: "https://api.github.com/", want: "https://api.github.com"},
		{name: "multiple trailing slashes", baseURL: "https://api.github.com///", want: "https://api.github.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, newGitHubClient(tt.baseURL, "").baseURL)
		})
	}
}

func TestGitHubClient_DownloadOK(t *testing.T) {
	t.Parallel()
	const body = "abc123  birdnet-go-linux-amd64.tar.gz\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	data, err := newGitHubClient(srv.URL, "").Download(t.Context(), srv.URL)
	require.NoError(t, err)
	assert.Equal(t, body, string(data))
}

func TestGitHubClient_DownloadRejectsOversized(t *testing.T) {
	t.Parallel()
	big := bytes.Repeat([]byte("a"), maxAssetBytes+10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(big)
	}))
	defer srv.Close()

	_, err := newGitHubClient(srv.URL, "").Download(t.Context(), srv.URL)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds")
}
