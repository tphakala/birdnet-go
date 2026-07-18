package diagnostics

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/shirou/gopsutil/v3/disk"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/sysinfo"
)

const (
	// maxDBScanDepth bounds the db_files_found walk below each root
	// (root itself is depth 0).
	maxDBScanDepth = 3
	// maxDBScanEntries caps the number of matches recorded.
	maxDBScanEntries = 200
	// dbBackupSuffix is the consolidation backup suffix
	// (GenerateBackupPath produces <path>.<timestamp>.old).
	dbBackupSuffix = ".old"
)

// dbScanSkipDirs are directory names never descended into during the
// db_files_found walk: media trees that by design hold no databases and
// can contain tens of thousands of files.
var dbScanSkipDirs = map[string]struct{}{"clips": {}, "hls": {}}

// BootParams carries the caller-supplied facts for a boot record. All
// fields are plain types so callers in internal/analysis can map
// datastore/v2 StartupState values without this package importing them.
type BootParams struct {
	// Version and BuildDate come from conf.Settings (ldflags-injected).
	Version   string
	BuildDate string
	// ConfigPath is the active config file; Existed/Defaulted reflect
	// conf.DefaultConfigCreated().
	ConfigPath      string
	ConfigExisted   bool
	ConfigDefaulted bool
	// Dialect is "sqlite" or "mysql".
	Dialect string
	// ConfiguredDBPath and V2SidecarPath are the SQLite file paths
	// (empty for MySQL).
	ConfiguredDBPath string
	V2SidecarPath    string
	// StartupDecision is datastoreV2 StartupState.Decision.
	StartupDecision string
	// MigrationState is the string form of StartupState.MigrationStatus.
	MigrationState string
	// DataDir and ConfigDir root the directory listings and db file scan.
	DataDir   string
	ConfigDir string
}

// RecordBoot assembles the full boot snapshot, appends it, appends a
// config_defaulted event when applicable, diffs against the previous boot,
// and appends any anomaly records (each also logged at WARN). The
// anomalies are RETURNED so a caller that imports internal/telemetry can
// report them to Sentry; this package is deliberately telemetry-free.
// Entirely best-effort: it never returns an error and never panics on
// I/O failure. The journal records are always written regardless of what
// the caller does with the return values.
func RecordBoot(j *Journal, p *BootParams) (*BootRecord, []AnomalyRecord) {
	// Defense-in-depth: honor the "never panics" contract even if a caller
	// passes a nil journal (the emit helpers already nil-check via
	// appendBestEffort; this guards the direct TrimIfNeeded/LastBoot calls).
	if j == nil {
		return nil, nil
	}
	log := getLogger()

	if err := j.TrimIfNeeded(); err != nil {
		log.Warn("journal trim failed", logger.Error(err))
	}

	prev, hasPrev := j.LastBoot()
	cur := buildBootRecord(p)

	appendBestEffort(j, RecordTypeBoot, cur)

	if p.ConfigDefaulted {
		RecordConfigDefaulted(j, p.ConfigPath)
	}

	var anomalies []AnomalyRecord
	if hasPrev {
		anomalies = detectAnomalies(prev, cur)
		for i := range anomalies {
			appendBestEffort(j, RecordTypeAnomaly, &anomalies[i])
			log.Warn("boot anomaly detected",
				logger.String("kind", anomalies[i].Kind),
				logger.String("previous", anomalies[i].Previous),
				logger.String("current", anomalies[i].Current))
		}
	}
	return cur, anomalies
}

// buildBootRecord gathers every field of the boot snapshot.
func buildBootRecord(p *BootParams) *BootRecord {
	rec := &BootRecord{
		RecordHeader: NewRecordHeader(RecordTypeBoot),
		App: AppInfo{
			Version:   p.Version,
			BuildDate: p.BuildDate,
		},
		Runtime: RuntimeInfo{
			OS:        runtime.GOOS,
			Arch:      runtime.GOARCH,
			GoVersion: runtime.Version(),
			Container: sysinfo.IsContainer(),
			PID:       os.Getpid(),
		},
		Config: ConfigInfo{
			Path:      p.ConfigPath,
			Existed:   p.ConfigExisted,
			Defaulted: p.ConfigDefaulted,
		},
		Datastore: DatastoreSnapshot{
			Dialect:         p.Dialect,
			ConfiguredPath:  p.ConfiguredDBPath,
			V2SidecarPath:   p.V2SidecarPath,
			StartupDecision: p.StartupDecision,
			MigrationState:  p.MigrationState,
		},
	}

	if p.ConfiguredDBPath != "" {
		if abs, err := filepath.Abs(p.ConfiguredDBPath); err == nil {
			rec.Datastore.ResolvedAbsPath = abs
		}
		if fi, err := os.Stat(p.ConfiguredDBPath); err == nil {
			rec.Datastore.ConfiguredExists = true
			rec.Datastore.ConfiguredSize = fi.Size()
		}
	}
	if p.V2SidecarPath != "" {
		if fi, err := os.Stat(p.V2SidecarPath); err == nil {
			rec.Datastore.V2SidecarExists = true
			rec.Datastore.V2SidecarSize = fi.Size()
		}
	}

	dirs := dedupeDirs(p.DataDir, p.ConfigDir)
	rec.Datastore.DBFilesFound = scanDBFiles(dirs)

	raw := CollectRawDeployment(p.DataDir, p.ConfigDir)
	rec.Cwd = raw.WorkingDirectory
	rec.Mounts = raw.Mounts
	rec.DataDirFiles = raw.DataDirFiles
	rec.ConfigDirFiles = raw.ConfigDirFiles
	rec.Errors = raw.CollectionErrors

	for _, dir := range dirs {
		if usage, err := disk.Usage(dir); err == nil {
			rec.Disk = append(rec.Disk, DiskUsage{
				Path:       dir,
				FreeBytes:  usage.Free,
				TotalBytes: usage.Total,
			})
		}
	}
	return rec
}

// dedupeDirs returns the non-empty, distinct directories among the inputs.
func dedupeDirs(dirs ...string) []string {
	out := make([]string, 0, len(dirs))
	seen := make(map[string]struct{}, len(dirs))
	for _, d := range dirs {
		if d == "" {
			continue
		}
		if _, ok := seen[d]; ok {
			continue
		}
		seen[d] = struct{}{}
		out = append(out, d)
	}
	return out
}

// isDBRelatedFile reports whether a file name looks like a database,
// its WAL/SHM companion, or a consolidation backup.
func isDBRelatedFile(name string) bool {
	return strings.HasSuffix(name, ".db") ||
		strings.HasSuffix(name, ".db-wal") ||
		strings.HasSuffix(name, ".db-shm") ||
		(strings.Contains(name, ".db.") && strings.HasSuffix(name, dbBackupSuffix))
}

// scanDBFiles walks each root (bounded by maxDBScanDepth and
// maxDBScanEntries, skipping media dirs) collecting db-related files.
// This is the single most important boot-record field: it answers "is
// the old database sitting somewhere we did not look?" (GitHub #3956).
func scanDBFiles(roots []string) []DBFileInfo {
	var found []DBFileInfo
	for _, root := range roots {
		if len(found) >= maxDBScanEntries {
			break
		}
		rootDepth := strings.Count(filepath.Clean(root), string(os.PathSeparator))
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil //nolint:nilerr // best-effort walk: skip unreadable entries
			}
			if len(found) >= maxDBScanEntries {
				return filepath.SkipAll
			}
			depth := strings.Count(filepath.Clean(path), string(os.PathSeparator)) - rootDepth
			if d.IsDir() {
				if _, skip := dbScanSkipDirs[d.Name()]; skip && path != root {
					return filepath.SkipDir
				}
				if depth >= maxDBScanDepth {
					return filepath.SkipDir
				}
				return nil
			}
			if !isDBRelatedFile(d.Name()) {
				return nil
			}
			info, infoErr := d.Info()
			if infoErr != nil {
				return nil //nolint:nilerr // best-effort walk
			}
			found = append(found, DBFileInfo{
				Path:     path,
				Size:     info.Size(),
				Modified: info.ModTime(),
			})
			return nil
		})
	}
	return found
}
