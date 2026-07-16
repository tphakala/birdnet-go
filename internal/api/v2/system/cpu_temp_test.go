package system

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadCPUTemperature(t *testing.T) {
	tempDir := t.TempDir()

	// Create thermal_zone0 with cpu-thermal
	zone0 := filepath.Join(tempDir, "thermal_zone0")
	if err := os.Mkdir(zone0, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(zone0, "type"), []byte("cpu-thermal\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(zone0, "temp"), []byte("45000\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create thermal_zone1 with an invalid type but high temp
	zone1 := filepath.Join(tempDir, "thermal_zone1")
	if err := os.Mkdir(zone1, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(zone1, "type"), []byte("acpitz\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(zone1, "temp"), []byte("102800\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	celsius, details, err := readCPUTemperature(tempDir)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if celsius != 45.0 {
		t.Errorf("expected 45.0, got %f", celsius)
	}

	if details != "Source: thermal_zone0, Type: cpu-thermal" {
		t.Errorf("expected 'Source: thermal_zone0, Type: cpu-thermal', got '%s'", details)
	}
}
