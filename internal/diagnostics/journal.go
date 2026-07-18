package diagnostics

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const (
	// journalDirName is the subdirectory of the config dir holding the journal.
	journalDirName = "diagnostics"
	// JournalFileName is the journal file name inside journalDirName.
	JournalFileName = "journal.jsonl"
	// maxJournalRecords caps the number of retained records after a trim.
	maxJournalRecords = 500
	// maxJournalBytes caps the journal file size (1 MiB); exceeding either
	// cap triggers a trim keeping the newest records.
	maxJournalBytes = 1 << 20
	// journalFileMode is the permission mode for the journal file.
	journalFileMode = 0o600
	// journalDirMode is the permission mode for the diagnostics directory.
	journalDirMode = 0o750
)

// getLogger returns the module logger for this package.
func getLogger() logger.Logger {
	return logger.Global().Module("diagnostics")
}

// processStart approximates process start for shutdown uptime reporting.
var processStart = time.Now()

// Journal is an append-only JSONL event journal. Safe for concurrent use
// within a process; O_APPEND keeps line writes atomic across processes for
// the small record sizes involved.
type Journal struct {
	mu   sync.Mutex
	path string
}

// NewJournal returns a Journal writing to the given file path.
func NewJournal(path string) *Journal {
	return &Journal{path: path}
}

// Path returns the journal file path.
func (j *Journal) Path() string { return j.path }

// JournalPathIn returns the journal file path under the given config
// directory: <configDir>/diagnostics/journal.jsonl. Exported so consumers
// that already know their config directory (the support collector's
// configPath) can locate the journal without re-resolving it.
func JournalPathIn(configDir string) string {
	return filepath.Join(configDir, journalDirName, JournalFileName)
}

// DefaultJournalPath resolves <configDir>/diagnostics/journal.jsonl using
// conf.ResolveConfigDir (which honors the --config flag and falls back to
// conf.GetDefaultConfigPaths()[0], i.e. /config in the Docker layout).
func DefaultJournalPath() (string, error) {
	dir, err := conf.ResolveConfigDir()
	if err != nil {
		return "", errors.New(err).
			Component("diagnostics").
			Category(errors.CategoryConfiguration).
			Context("operation", "resolve_journal_path").
			Build()
	}
	return JournalPathIn(dir), nil
}

var (
	defaultJournalOnce sync.Once
	defaultJournal     *Journal
)

// Default returns the shared process-wide Journal at DefaultJournalPath.
// If the path cannot be resolved, it returns a Journal with an empty path
// whose Append fails; emit helpers swallow that failure (best-effort).
func Default() *Journal {
	defaultJournalOnce.Do(func() {
		path, err := DefaultJournalPath()
		if err != nil {
			getLogger().Warn("cannot resolve journal path, journaling disabled",
				logger.Error(err))
		}
		defaultJournal = NewJournal(path)
	})
	return defaultJournal
}

// Append marshals rec and appends it as one line. Creates the parent
// directory on first use. Callers on startup paths must treat errors as
// best-effort (log and continue).
func (j *Journal) Append(rec any) error {
	if j.path == "" {
		return errors.Newf("journal path not configured").
			Component("diagnostics").
			Category(errors.CategoryConfiguration).
			Context("operation", "journal_append").
			Build()
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return errors.New(err).
			Component("diagnostics").
			Category(errors.CategoryValidation).
			Context("operation", "journal_marshal").
			Build()
	}

	j.mu.Lock()
	defer j.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(j.path), journalDirMode); err != nil {
		return errors.New(err).
			Component("diagnostics").
			Category(errors.CategoryFileIO).
			Context("operation", "journal_mkdir").
			Build()
	}
	f, err := os.OpenFile(j.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, journalFileMode)
	if err != nil {
		return errors.New(err).
			Component("diagnostics").
			Category(errors.CategoryFileIO).
			Context("operation", "journal_open").
			Build()
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return errors.New(err).
			Component("diagnostics").
			Category(errors.CategoryFileIO).
			Context("operation", "journal_write").
			Build()
	}
	return nil
}

// TrimIfNeeded rewrites the journal via temp-file-plus-rename when it
// exceeds maxJournalRecords or maxJournalBytes, keeping the newest records.
// Intended to run once per startup (RecordBoot calls it).
func (j *Journal) TrimIfNeeded() error {
	j.mu.Lock()
	defer j.mu.Unlock()

	fi, err := os.Stat(j.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return errors.New(err).
			Component("diagnostics").
			Category(errors.CategoryFileIO).
			Context("operation", "journal_trim_stat").
			Build()
	}

	data, err := os.ReadFile(j.path)
	if err != nil {
		return errors.New(err).
			Component("diagnostics").
			Category(errors.CategoryFileIO).
			Context("operation", "journal_trim_read").
			Build()
	}
	lines := splitNonEmptyLines(string(data))
	if len(lines) <= maxJournalRecords && fi.Size() <= maxJournalBytes {
		return nil
	}

	// Keep the newest maxJournalRecords lines, then keep dropping the
	// oldest until under the byte cap.
	if len(lines) > maxJournalRecords {
		lines = lines[len(lines)-maxJournalRecords:]
	}
	total := 0
	for _, l := range lines {
		total += len(l) + 1
	}
	for len(lines) > 1 && total > maxJournalBytes {
		total -= len(lines[0]) + 1
		lines = lines[1:]
	}

	tmp := j.path + ".tmp"
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(tmp, []byte(content), journalFileMode); err != nil {
		return errors.New(err).
			Component("diagnostics").
			Category(errors.CategoryFileIO).
			Context("operation", "journal_trim_write").
			Build()
	}
	if err := os.Rename(tmp, j.path); err != nil {
		_ = os.Remove(tmp)
		return errors.New(err).
			Component("diagnostics").
			Category(errors.CategoryFileIO).
			Context("operation", "journal_trim_rename").
			Build()
	}
	return nil
}

// LastBoot returns the most recent boot record, tolerating malformed lines.
// Returns (nil, false) when the journal is missing, empty, or has no boot.
func (j *Journal) LastBoot() (*BootRecord, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()

	data, err := os.ReadFile(j.path)
	if err != nil {
		// A missing journal is the normal first-boot case; anything else
		// (e.g. a permission error) would silently suppress anomaly
		// detection, so surface it at Debug for diagnosability.
		if !os.IsNotExist(err) {
			getLogger().Debug("failed to read journal for last boot", logger.Error(err))
		}
		return nil, false
	}
	lines := splitNonEmptyLines(string(data))
	for _, line := range slices.Backward(lines) {
		var probe struct {
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(line), &probe) != nil {
			continue // malformed line (torn write), skip
		}
		if probe.Type != RecordTypeBoot {
			continue
		}
		var rec BootRecord
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		return &rec, true
	}
	return nil, false
}

// splitNonEmptyLines splits s on newlines, dropping blank lines.
func splitNonEmptyLines(s string) []string {
	raw := strings.Split(s, "\n")
	out := make([]string, 0, len(raw))
	for _, l := range raw {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// appendBestEffort appends rec and logs a WARN on failure. All lifecycle
// emit helpers funnel through this so a journal problem never propagates.
func appendBestEffort(j *Journal, recordType string, rec any) {
	if j == nil {
		return
	}
	if err := j.Append(rec); err != nil {
		getLogger().Warn("failed to append journal record",
			logger.String("record_type", recordType),
			logger.Error(err))
	}
}

// RecordFreshDB journals creation of a fresh empty database. Best-effort.
func RecordFreshDB(j *Journal, path, reason string) {
	appendBestEffort(j, RecordTypeDBFreshCreated, &FreshDBRecord{
		RecordHeader: NewRecordHeader(RecordTypeDBFreshCreated),
		Path:         path,
		Reason:       reason,
	})
}

// RecordMigration journals a migration state transition. Best-effort.
// records is the migrated record count when known, 0 otherwise.
func RecordMigration(j *Journal, phase, from, to string, records int64) {
	appendBestEffort(j, RecordTypeMigration, &MigrationRecord{
		RecordHeader: NewRecordHeader(RecordTypeMigration),
		Phase:        phase,
		From:         from,
		To:           to,
		Records:      records,
	})
}

// RecordConsolidation journals a consolidation attempt. Best-effort.
// result is one of "success", "failed", "resumed", "rolled_back".
func RecordConsolidation(j *Journal, from, to, backupPath, result string) {
	appendBestEffort(j, RecordTypeConsolidation, &ConsolidationRecord{
		RecordHeader: NewRecordHeader(RecordTypeConsolidation),
		From:         from,
		To:           to,
		BackupPath:   backupPath,
		Result:       result,
	})
}

// RecordConfigDefaulted journals generation of a default config. Best-effort.
func RecordConfigDefaulted(j *Journal, path string) {
	appendBestEffort(j, RecordTypeConfigDefaulted, &ConfigDefaultedRecord{
		RecordHeader: NewRecordHeader(RecordTypeConfigDefaulted),
		Path:         path,
	})
}

// RecordShutdown journals application shutdown with process uptime. Best-effort.
func RecordShutdown(j *Journal, clean bool) {
	appendBestEffort(j, RecordTypeShutdown, &ShutdownRecord{
		RecordHeader:  NewRecordHeader(RecordTypeShutdown),
		Clean:         clean,
		UptimeSeconds: int64(time.Since(processStart).Seconds()),
	})
}
