package testutil

import (
	"archive/zip"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	// goCommand and its arguments print the root of the Go toolchain that runs the tests.
	goCommand    = "go"
	goEnvCommand = "env"
	goRootVar    = "GOROOT"

	// zoneinfoZipParts is the location of the zone database inside GOROOT.
	zoneinfoZipLib  = "lib"
	zoneinfoZipDir  = "time"
	zoneinfoZipFile = "zoneinfo.zip"
)

// toolchainZoneData holds the zones parsed from the toolchain's zone database, or the error
// that stopped it.
type toolchainZoneData struct {
	zones map[string]*time.Location
	err   error
}

// loadToolchainZones is evaluated once per test binary.
var loadToolchainZones = sync.OnceValue(readToolchainZones)

// ToolchainZones returns every zone of the Go toolchain's own zone database
// ($(go env GOROOT)/lib/time/zoneinfo.zip), aliases included, keyed by IANA name.
//
// Tests that sweep zones use it instead of time.LoadLocation so the result does not depend on
// the zone files of the host. The path comes from `go env GOROOT` because runtime.GOROOT is
// deprecated and empty under -trimpath, and the GOROOT environment variable is not set inside
// go test. A missing or unreadable database fails the test: it is never skipped, so a runner
// without it cannot silently drop the coverage. The returned map is shared, do not modify it.
func ToolchainZones(tb testing.TB) map[string]*time.Location {
	tb.Helper()
	data := loadToolchainZones()
	if data.err != nil {
		tb.Fatalf("load toolchain zones: %v", data.err)
	}
	return data.zones
}

func readToolchainZones() toolchainZoneData {
	out, err := exec.Command(goCommand, goEnvCommand, goRootVar).Output()
	if err != nil {
		return toolchainZoneData{err: err}
	}
	goroot := strings.TrimSpace(string(out))
	reader, err := zip.OpenReader(filepath.Join(goroot, zoneinfoZipLib, zoneinfoZipDir, zoneinfoZipFile))
	if err != nil {
		return toolchainZoneData{err: err}
	}
	defer func() { _ = reader.Close() }()

	zones := make(map[string]*time.Location, len(reader.File))
	for _, f := range reader.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return toolchainZoneData{err: err}
		}
		raw, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return toolchainZoneData{err: err}
		}
		loc, err := time.LoadLocationFromTZData(f.Name, raw)
		if err != nil {
			return toolchainZoneData{err: err}
		}
		zones[f.Name] = loc
	}
	return toolchainZoneData{zones: zones}
}
