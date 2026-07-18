package diagnostics

import (
	"os"
	"strings"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/sysinfo"
)

const (
	// MaxDirEntries caps directory listing size (matches the former
	// support-side maxDataDirEntries).
	MaxDirEntries = 500
	// mountInfoPath is the container-init mount table.
	mountInfoPath = "/proc/1/mountinfo"
	// mountInfoMinFields is the minimum field count of a mountinfo line.
	mountInfoMinFields = 10
	// mountInfoDestIdx is the mount-point field index.
	mountInfoDestIdx = 4
	// mountInfoSeparatorSkip is fields required after the "-" separator.
	mountInfoSeparatorSkip = 3
)

// systemMountPrefixes are mount destinations to skip (not diagnostic-relevant).
var systemMountPrefixes = []string{"/proc", "/sys", "/dev", "/run", "/tmp", "/etc/resolv", "/etc/hostname", "/etc/hosts"}

// RawDeployment holds raw, un-anonymized deployment facts. Consumers that
// ship this off-host (the support collector) MUST anonymize; local
// consumers (the journal) store it as-is on the user's own disk.
type RawDeployment struct {
	WorkingDirectory string
	Mounts           []Mount
	DataDirFiles     []DirEntryInfo
	ConfigDirFiles   []DirEntryInfo
	CollectionErrors []string
}

// CollectRawDeployment gathers working directory, mounts, and directory
// listings for the given data and config directories. An empty directory
// argument (e.g. no DataDir on MySQL boots) skips that listing without
// recording an error. Individual failures are recorded in
// CollectionErrors; the call itself never fails. The "docker mounts:"
// error prefix is kept verbatim from the former support-side gatherer so
// existing dump-triage tooling keeps matching.
func CollectRawDeployment(dataDir, configDir string) *RawDeployment {
	raw := &RawDeployment{}

	cwd, err := os.Getwd()
	if err != nil {
		raw.CollectionErrors = append(raw.CollectionErrors, "working directory: "+err.Error())
	} else {
		raw.WorkingDirectory = cwd
	}

	if dataDir != "" {
		if files, err := ListDirectory(dataDir); err != nil {
			raw.CollectionErrors = append(raw.CollectionErrors, "data directory: "+err.Error())
		} else {
			raw.DataDirFiles = files
		}
	}

	if configDir != "" {
		if files, err := ListDirectory(configDir); err != nil {
			raw.CollectionErrors = append(raw.CollectionErrors, "config directory: "+err.Error())
		} else {
			raw.ConfigDirFiles = files
		}
	}

	if mounts, err := CollectMounts(); err != nil {
		raw.CollectionErrors = append(raw.CollectionErrors, "docker mounts: "+err.Error())
	} else {
		raw.Mounts = mounts
	}

	return raw
}

// ListDirectory lists a directory's immediate entries with metadata,
// truncated at MaxDirEntries. Names are returned RAW.
func ListDirectory(dir string) ([]DirEntryInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, errors.New(err).
			Component("diagnostics").
			Category(errors.CategoryFileIO).
			Context("operation", "list_directory").
			Build()
	}
	allocSize := min(len(entries), MaxDirEntries)
	files := make([]DirEntryInfo, 0, allocSize)
	for i, e := range entries {
		if i >= MaxDirEntries {
			getLogger().Warn("directory listing truncated",
				logger.Int("limit", MaxDirEntries),
				logger.Int("total", len(entries)))
			break
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, DirEntryInfo{
			Name:     e.Name(),
			Size:     info.Size(),
			Modified: info.ModTime(),
			IsDir:    e.IsDir(),
		})
	}
	return files, nil
}

// CollectMounts reads /proc/1/mountinfo when running in a container.
// Returns (nil, nil) outside containers.
func CollectMounts() ([]Mount, error) {
	if !sysinfo.IsContainer() {
		return nil, nil
	}
	data, err := os.ReadFile(mountInfoPath) //nolint:gosec // Constant path
	if err != nil {
		return nil, errors.New(err).
			Component("diagnostics").
			Category(errors.CategoryFileIO).
			Context("operation", "read_mountinfo").
			Build()
	}
	return ParseMountInfo(string(data)), nil
}

// ParseMountInfo parses /proc/1/mountinfo content and extracts non-system
// bind mounts with RAW sources.
// Format: id parent major:minor root mount-point mount-options ... - fs-type mount-source super-options
func ParseMountInfo(content string) []Mount {
	if content == "" {
		return nil
	}
	var mounts []Mount
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

		mounts = append(mounts, Mount{
			Source:      source,
			Destination: destination,
			FSType:      fsType,
			Options:     options,
		})
	}
	return mounts
}
