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
	c := &Collector{sensitiveKeys: defaultSensitiveKeys()}
	content, err := c.collectSystemdServiceFile("/nonexistent/path/birdnet-go.service")
	require.NoError(t, err)
	assert.Empty(t, content)
}

func TestCollectSystemdServiceFile_BasicContent(t *testing.T) {
	tmpDir := t.TempDir()
	servicePath := filepath.Join(tmpDir, "birdnet-go.service")
	serviceContent := "[Unit]\nDescription=BirdNET-Go\nAfter=network.target\n\n[Service]\nType=simple\nWorkingDirectory=/home/user/birdnet-go-app/data\nExecStart=/usr/local/bin/birdnet-go\nRestart=always\n\n[Install]\nWantedBy=multi-user.target\n"
	require.NoError(t, os.WriteFile(servicePath, []byte(serviceContent), 0o644))

	c := &Collector{sensitiveKeys: defaultSensitiveKeys()}
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

	c := &Collector{sensitiveKeys: defaultSensitiveKeys()}
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

	c := &Collector{sensitiveKeys: defaultSensitiveKeys()}
	content, err := c.collectSystemdServiceFile(servicePath)
	require.NoError(t, err)
	// Quotes must be preserved in redacted output
	assert.Contains(t, content, `Environment="MQTT_PASSWORD=[REDACTED]"`)
	assert.Contains(t, content, `Environment='BIRDWEATHER_ID=[REDACTED]'`)
}

// --- Data directory listing tests ---

func TestCollectDataDirectoryListing_EmptyDir(t *testing.T) {
	tmpDir := t.TempDir()
	c := &Collector{dataPath: tmpDir}

	files, err := c.collectDataDirectoryListing(false)
	require.NoError(t, err)
	assert.Empty(t, files)
}

func TestCollectDataDirectoryListing_WithFiles(t *testing.T) {
	tmpDir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "birdnet.db"), make([]byte, 1024), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "birdnet.db-wal"), make([]byte, 512), 0o644))
	require.NoError(t, os.Mkdir(filepath.Join(tmpDir, "logs"), 0o755))

	c := &Collector{dataPath: tmpDir}
	files, err := c.collectDataDirectoryListing(false)
	require.NoError(t, err)
	assert.Len(t, files, 3)

	var dbFile *DataDirectoryFile
	for i := range files {
		if files[i].Name == "birdnet.db" {
			dbFile = &files[i]
			break
		}
	}
	require.NotNil(t, dbFile)
	assert.Equal(t, int64(1024), dbFile.Size)
	assert.False(t, dbFile.IsDir)
}

func TestCollectDataDirectoryListing_NonexistentDir(t *testing.T) {
	c := &Collector{dataPath: "/nonexistent/path"}
	files, err := c.collectDataDirectoryListing(false)
	require.Error(t, err)
	assert.Nil(t, files)
}

// --- Docker mount tests ---

func TestParseMountInfo_BasicMounts(t *testing.T) {
	mountInfo := "22 1 8:1 / / rw,relatime - ext4 /dev/sda1 rw\n29 22 0:26 / /proc rw,nosuid,nodev,noexec,relatime - proc proc rw\n35 22 0:27 / /sys rw,nosuid,nodev,noexec,relatime - sysfs sysfs rw\n45 22 8:1 /home/user/data /data rw,relatime - ext4 /dev/sda1 rw\n50 22 8:1 /home/user/config /config rw,relatime - ext4 /dev/sda1 rw\n"

	mounts := parseMountInfo(mountInfo, false)

	assert.Len(t, mounts, 2)
	assert.Equal(t, "/home/user/data", mounts[0].Source)
	assert.Equal(t, "/data", mounts[0].Destination)
	assert.Equal(t, "ext4", mounts[0].Type)
}

func TestParseMountInfo_AnonymizesPII(t *testing.T) {
	mountInfo := "45 22 8:1 /home/user/data /data rw,relatime - ext4 /dev/sda1 rw\n"
	mounts := parseMountInfo(mountInfo, true)

	require.Len(t, mounts, 1)
	assert.NotEqual(t, "/home/user/data", mounts[0].Source)
	assert.Equal(t, "/data", mounts[0].Destination)
}

func TestParseMountInfo_EmptyInput(t *testing.T) {
	mounts := parseMountInfo("", false)
	assert.Empty(t, mounts)
}

func TestCollectDockerMounts_NotInContainer(t *testing.T) {
	c := &Collector{}
	mounts, err := c.collectDockerMounts(t.Context(), false)
	require.NoError(t, err)
	assert.Nil(t, mounts)
}

// --- Orchestration test ---

func TestCollectDeploymentInfo_Orchestration(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "birdnet.db"), make([]byte, 2048), 0o644))

	c := &Collector{
		dataPath:      tmpDir,
		sensitiveKeys: defaultSensitiveKeys(),
	}
	info := c.collectDeploymentInfo(t.Context(), false)

	assert.NotEmpty(t, info.WorkingDirectory)
	assert.Len(t, info.DataDirectoryFiles, 1)
	assert.Equal(t, "birdnet.db", info.DataDirectoryFiles[0].Name)
	assert.Empty(t, info.SystemdServiceFile)
	assert.Nil(t, info.DockerMounts)
}
