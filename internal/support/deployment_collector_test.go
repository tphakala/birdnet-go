package support

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Systemd service file tests ---

func TestCollectSystemdServiceFile_MissingFile(t *testing.T) {
	c := &Collector{sensitiveKeys: DefaultSensitiveKeys()}
	content, err := c.collectSystemdServiceFile("/nonexistent/path/birdnet-go.service")
	require.NoError(t, err)
	assert.Empty(t, content)
}

func TestCollectSystemdServiceFile_BasicContent(t *testing.T) {
	tmpDir := t.TempDir()
	servicePath := filepath.Join(tmpDir, "birdnet-go.service")
	serviceContent := "[Unit]\nDescription=BirdNET-Go\nAfter=network.target\n\n[Service]\nType=simple\nWorkingDirectory=/home/user/birdnet-go-app/data\nExecStart=/usr/local/bin/birdnet-go\nRestart=always\n\n[Install]\nWantedBy=multi-user.target\n"
	require.NoError(t, os.WriteFile(servicePath, []byte(serviceContent), 0o644))

	c := &Collector{sensitiveKeys: DefaultSensitiveKeys()}
	content, err := c.collectSystemdServiceFile(servicePath)
	require.NoError(t, err)
	assert.Contains(t, content, "WorkingDirectory=/home/user/birdnet-go-app/data")
	assert.Contains(t, content, "ExecStart=/usr/local/bin/birdnet-go")
}

func TestCollectSystemdServiceFile_ScrubsSensitiveEnvVars(t *testing.T) {
	tmpDir := t.TempDir()
	servicePath := filepath.Join(tmpDir, "birdnet-go.service")
	serviceContent := "[Service]\nEnvironment=DATA_DIR=/data\nEnvironment=BIRDWEATHER_ID=abc123secret\nEnvironment=MQTT_PASSWORD=supersecret\nExecStart=/usr/local/bin/birdnet-go\n"
	require.NoError(t, os.WriteFile(servicePath, []byte(serviceContent), 0o644))

	c := &Collector{sensitiveKeys: DefaultSensitiveKeys()}
	content, err := c.collectSystemdServiceFile(servicePath)
	require.NoError(t, err)
	assert.Contains(t, content, "DATA_DIR=/data")
	assert.Contains(t, content, "BIRDWEATHER_ID=[REDACTED]")
	assert.Contains(t, content, "MQTT_PASSWORD=[REDACTED]")
	assert.Contains(t, content, "ExecStart=/usr/local/bin/birdnet-go")
}

func TestCollectSystemdServiceFile_ScrubsQuotedEnvVars(t *testing.T) {
	tmpDir := t.TempDir()
	servicePath := filepath.Join(tmpDir, "birdnet-go.service")
	serviceContent := "[Service]\nEnvironment=\"MQTT_PASSWORD=supersecret\"\nEnvironment='BIRDWEATHER_ID=abc123'\n"
	require.NoError(t, os.WriteFile(servicePath, []byte(serviceContent), 0o644))

	c := &Collector{sensitiveKeys: DefaultSensitiveKeys()}
	content, err := c.collectSystemdServiceFile(servicePath)
	require.NoError(t, err)
	// Quotes must be preserved in redacted output
	assert.Contains(t, content, `Environment="MQTT_PASSWORD=[REDACTED]"`)
	assert.Contains(t, content, `Environment='BIRDWEATHER_ID=[REDACTED]'`)
}

// --- Deployment info + anonymization tests ---
// Raw mount-parsing and directory-listing tests moved to internal/diagnostics
// (the raw gatherer). These cover the support-side anonymization policy only.

func namesOf(files []DataDirectoryFile) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Name)
	}
	return out
}

func TestCollectDeploymentInfoConfigListingAndAllowlist(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	configDir := t.TempDir()
	// Standard dirs must survive anonymization; a user-specific dir must not.
	for _, d := range []string{"clips", "models", "logs", "hls", "my-private-recordings"} {
		require.NoError(t, os.MkdirAll(filepath.Join(dataDir, d), 0o750))
	}
	require.NoError(t, os.WriteFile(filepath.Join(dataDir, "birdnet.db"), []byte("x"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "diagnostics"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte("y"), 0o600))

	c := NewCollector(configDir, dataDir, "sys-id", "20260716")
	info := c.collectDeploymentInfo(t.Context(), true)
	require.NotNil(t, info)

	dataNames := namesOf(info.DataDirectoryFiles)
	assert.Contains(t, dataNames, "clips")
	assert.Contains(t, dataNames, "models")
	assert.Contains(t, dataNames, "logs")
	assert.Contains(t, dataNames, "hls")
	assert.Contains(t, dataNames, "birdnet.db", "plain files keep raw names, as before")
	assert.NotContains(t, dataNames, "my-private-recordings", "user dirs are anonymized")

	require.NotEmpty(t, info.ConfigDirectoryFiles, "config listing is new and populated")
	cfgNames := namesOf(info.ConfigDirectoryFiles)
	assert.Contains(t, cfgNames, "config.yaml")
	assert.Contains(t, cfgNames, "diagnostics", "diagnostics is a standard dir name")
}

func TestCollectDeploymentInfoNoAnonymization(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dataDir, "my-private-recordings"), 0o750))
	c := NewCollector(t.TempDir(), dataDir, "sys-id", "20260716")
	info := c.collectDeploymentInfo(t.Context(), false)
	assert.Contains(t, namesOf(info.DataDirectoryFiles), "my-private-recordings",
		"anonymizePII=false keeps raw names")
}

// --- Orchestration test ---

func TestCollectDeploymentInfo_Orchestration(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "birdnet.db"), make([]byte, 2048), 0o644))

	c := &Collector{
		dataPath:      tmpDir,
		sensitiveKeys: DefaultSensitiveKeys(),
	}
	info := c.collectDeploymentInfo(t.Context(), false)

	assert.NotEmpty(t, info.WorkingDirectory)
	assert.Len(t, info.DataDirectoryFiles, 1)
	assert.Equal(t, "birdnet.db", info.DataDirectoryFiles[0].Name)
	assert.Empty(t, info.SystemdServiceFile)
	assert.Nil(t, info.DockerMounts)
}
