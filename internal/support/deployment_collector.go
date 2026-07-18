package support

import (
	"bufio"
	"context"
	"os"
	"strings"

	"github.com/tphakala/birdnet-go/internal/diagnostics"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

const (
	systemdServicePath = "/etc/systemd/system/birdnet-go.service"
	maxServiceFileSize = 64 * 1024 // 64KB limit for service files
)

// standardDirNames are well-known BirdNET-Go directory names that are never
// anonymized in dump listings: they carry diagnostic signal and no PII.
var standardDirNames = map[string]struct{}{
	"clips": {}, "models": {}, "logs": {}, "hls": {}, "diagnostics": {},
}

// anonymizeListing converts raw entries to dump entries, anonymizing only
// user-specific directory names (files keep raw names, matching the
// pre-existing behavior; standard dir names are preserved).
func anonymizeListing(entries []diagnostics.DirEntryInfo, anonymizePII bool) []DataDirectoryFile {
	if entries == nil {
		return nil
	}
	files := make([]DataDirectoryFile, 0, len(entries))
	for _, e := range entries {
		name := e.Name
		if anonymizePII && e.IsDir {
			if _, standard := standardDirNames[name]; !standard {
				name = privacy.AnonymizePath(name)
			}
		}
		files = append(files, DataDirectoryFile{
			Name:     name,
			Size:     e.Size,
			Modified: e.Modified,
			IsDir:    e.IsDir,
		})
	}
	return files
}

// collectDeploymentInfo gathers deployment context for the support dump.
// Raw facts come from internal/diagnostics; anonymization is applied here,
// at ship time only.
func (c *Collector) collectDeploymentInfo(_ context.Context, anonymizePII bool) *DeploymentInfo {
	info := &DeploymentInfo{}

	raw := diagnostics.CollectRawDeployment(c.dataPath, c.configPath)
	info.CollectionErrors = append(info.CollectionErrors, raw.CollectionErrors...)

	switch {
	case raw.WorkingDirectory == "":
		// error already recorded by the gatherer
	case anonymizePII:
		info.WorkingDirectory = privacy.AnonymizePath(raw.WorkingDirectory)
	default:
		info.WorkingDirectory = raw.WorkingDirectory
	}

	if content, err := c.collectSystemdServiceFile(systemdServicePath); err != nil {
		info.CollectionErrors = append(info.CollectionErrors, "systemd service: "+err.Error())
	} else {
		info.SystemdServiceFile = content
	}

	info.DataDirectoryFiles = anonymizeListing(raw.DataDirFiles, anonymizePII)
	info.ConfigDirectoryFiles = anonymizeListing(raw.ConfigDirFiles, anonymizePII)

	if len(raw.Mounts) > 0 {
		mounts := make([]DockerMount, 0, len(raw.Mounts))
		for _, m := range raw.Mounts {
			source := m.Source
			if anonymizePII {
				source = privacy.AnonymizePath(source)
			}
			mounts = append(mounts, DockerMount{
				Source:      source,
				Destination: m.Destination,
				Type:        m.FSType,
				Options:     m.Options,
			})
		}
		info.DockerMounts = mounts
	}

	return info
}

// collectSystemdServiceFile reads and scrubs the systemd service file.
func (c *Collector) collectSystemdServiceFile(path string) (string, error) {
	fi, err := os.Stat(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if fi.Size() > maxServiceFileSize {
		getLogger().Warn("systemd service file too large, skipping",
			logger.Int64("size", fi.Size()),
			logger.Int("limit", maxServiceFileSize),
		)
		return "", nil
	}

	data, err := os.ReadFile(path) //nolint:gosec // Path is a constant or test-injected
	if err != nil {
		return "", err
	}

	return c.scrubServiceFileContent(string(data)), nil
}

// scrubServiceFileContent redacts sensitive Environment= values.
func (c *Collector) scrubServiceFileContent(content string) string {
	var result strings.Builder
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Environment=") {
			line = c.scrubEnvironmentLine(line)
		}
		result.WriteString(line)
		result.WriteByte('\n')
	}
	return result.String()
}

// scrubEnvironmentLine redacts the value of a systemd Environment= line if the key is sensitive.
func (c *Collector) scrubEnvironmentLine(line string) string {
	prefix := "Environment="
	idx := strings.Index(line, prefix)
	if idx == -1 {
		return line
	}
	kvPart := line[idx+len(prefix):]

	// Detect and strip surrounding quotes, preserving them for output
	var quoteChar string
	if len(kvPart) >= 2 && (kvPart[0] == '"' || kvPart[0] == '\'') && kvPart[len(kvPart)-1] == kvPart[0] {
		quoteChar = string(kvPart[0])
		kvPart = kvPart[1 : len(kvPart)-1]
	}

	eqIdx := strings.IndexByte(kvPart, '=')
	if eqIdx == -1 {
		return line
	}

	key := kvPart[:eqIdx]
	lowerKey := strings.ToLower(key)
	for _, sensitive := range c.sensitiveKeys {
		if isSensitiveKey(lowerKey, sensitive) {
			return line[:idx+len(prefix)] + quoteChar + key + "=[REDACTED]" + quoteChar
		}
	}
	return line
}
