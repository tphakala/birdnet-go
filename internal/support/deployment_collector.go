package support

import (
	"bufio"
	"context"
	"os"
	"strings"

	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

const (
	systemdServicePath     = "/etc/systemd/system/birdnet-go.service"
	maxServiceFileSize     = 64 * 1024 // 64KB limit for service files
	maxDataDirEntries      = 500
	mountInfoPath          = "/proc/1/mountinfo"
	dockerEnvPath          = "/.dockerenv"
	mountInfoMinFields     = 10
	mountInfoDestIdx       = 4
	mountInfoSeparatorSkip = 3
)

// systemMountPrefixes are mount destinations to skip (not useful for diagnostics).
var systemMountPrefixes = []string{"/proc", "/sys", "/dev", "/run", "/tmp", "/etc/resolv", "/etc/hostname", "/etc/hosts"}

// collectDeploymentInfo gathers deployment context for the support dump.
func (c *Collector) collectDeploymentInfo(ctx context.Context, anonymizePII bool) *DeploymentInfo {
	info := &DeploymentInfo{}

	cwd, err := os.Getwd()
	switch {
	case err != nil:
		info.CollectionErrors = append(info.CollectionErrors, "working directory: "+err.Error())
	case anonymizePII:
		info.WorkingDirectory = privacy.AnonymizePath(cwd)
	default:
		info.WorkingDirectory = cwd
	}

	if content, err := c.collectSystemdServiceFile(systemdServicePath); err != nil {
		info.CollectionErrors = append(info.CollectionErrors, "systemd service: "+err.Error())
	} else {
		info.SystemdServiceFile = content
	}

	if files, err := c.collectDataDirectoryListing(anonymizePII); err != nil {
		info.CollectionErrors = append(info.CollectionErrors, "data directory: "+err.Error())
	} else {
		info.DataDirectoryFiles = files
	}

	if mounts, err := c.collectDockerMounts(ctx, anonymizePII); err != nil {
		info.CollectionErrors = append(info.CollectionErrors, "docker mounts: "+err.Error())
	} else {
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

	eqIdx := strings.IndexByte(kvPart, '=')
	if eqIdx == -1 {
		return line
	}

	key := kvPart[:eqIdx]
	lowerKey := strings.ToLower(key)
	for _, sensitive := range c.sensitiveKeys {
		if isSensitiveKey(lowerKey, sensitive) {
			return line[:idx+len(prefix)] + key + "=[REDACTED]"
		}
	}
	return line
}

// collectDataDirectoryListing lists files in the data directory with metadata.
func (c *Collector) collectDataDirectoryListing(anonymizePII bool) ([]DataDirectoryFile, error) {
	entries, err := os.ReadDir(c.dataPath)
	if err != nil {
		return nil, err
	}

	files := make([]DataDirectoryFile, 0, len(entries))
	for i, e := range entries {
		if i >= maxDataDirEntries {
			getLogger().Warn("data directory listing truncated",
				logger.Int("limit", maxDataDirEntries),
				logger.Int("total", len(entries)),
			)
			break
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		name := e.Name()
		if anonymizePII && e.IsDir() {
			name = privacy.AnonymizePath(name)
		}
		files = append(files, DataDirectoryFile{
			Name:     name,
			Size:     info.Size(),
			Modified: info.ModTime(),
			IsDir:    e.IsDir(),
		})
	}
	return files, nil
}

// collectDockerMounts reads container mount info from /proc/1/mountinfo.
func (c *Collector) collectDockerMounts(_ context.Context, anonymizePII bool) ([]DockerMount, error) {
	if _, err := os.Stat(dockerEnvPath); os.IsNotExist(err) {
		return nil, nil
	}

	data, err := os.ReadFile(mountInfoPath) //nolint:gosec // Constant path
	if err != nil {
		return nil, err
	}

	return parseMountInfo(string(data), anonymizePII), nil
}

// parseMountInfo parses /proc/1/mountinfo format and extracts bind mounts.
// Format: id parent major:minor root mount-point mount-options ... - fs-type mount-source super-options
func parseMountInfo(content string, anonymizePII bool) []DockerMount {
	if content == "" {
		return nil
	}

	var mounts []DockerMount
	for line := range strings.SplitSeq(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < mountInfoMinFields {
			continue
		}

		source := fields[3]
		destination := fields[mountInfoDestIdx]
		options := fields[5]

		// Find the separator "-" to get fs type
		sepIdx := -1
		for i, f := range fields {
			if f == "-" {
				sepIdx = i
				break
			}
		}
		if sepIdx == -1 || sepIdx+mountInfoSeparatorSkip > len(fields) {
			continue
		}
		fsType := fields[sepIdx+1]

		// Skip system mounts and root
		if destination == "/" {
			continue
		}
		skip := false
		for _, prefix := range systemMountPrefixes {
			if strings.HasPrefix(destination, prefix) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}

		if anonymizePII {
			source = privacy.AnonymizePath(source)
		}

		mounts = append(mounts, DockerMount{
			Source:      source,
			Destination: destination,
			Type:        fsType,
			Options:     options,
		})
	}
	return mounts
}
