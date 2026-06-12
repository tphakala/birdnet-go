// Command gen generates the per-locale species name dictionaries embedded by the
// speciesdict package. For each dashboard UI locale it asks openfauna for a
// self-contained scientific-name -> common-name map (localized names with an English
// fallback baked in) and writes it as deterministic, precompressed gzip JSON.
//
// Determinism matters: the output is committed and guarded by a CI drift gate, so the
// generator must produce byte-identical files for a given dataset. json.Marshal sorts
// map keys, the gzip level is fixed, and the gzip header is left at its zero values
// (mtime 0, OS 255, no name), so re-running on the same dataset yields the same bytes.
//
// Run via: go generate ./internal/speciesdict
package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/tphakala/birdnet-go/internal/openfauna"
)

// uiLocales is the set of dashboard UI locales for which a dictionary is generated.
// This is the build-time counterpart of frontend/src/lib/i18n/config.ts (the UI's
// single source of truth for supported locales); keep the two in sync. The speciesdict
// allowlist test and the CI drift gate guard against accidental divergence.
var uiLocales = []string{
	"cs", "da", "de", "en", "es", "fi", "fr", "hu",
	"it", "lv", "nb", "nl", "pl", "pt", "sk", "sv",
}

func main() {
	outDir := flag.String("out", "data", "output directory for generated dictionaries")
	flag.Parse()

	if err := run(*outDir); err != nil {
		log.Fatalf("gen-species-dict: %v", err)
	}
}

func run(outDir string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create output dir %q: %w", outDir, err)
	}

	for _, locale := range uiLocales {
		dict, err := openfauna.BuildLocaleDictionary(locale)
		if err != nil {
			return fmt.Errorf("build dictionary for %q: %w", locale, err)
		}

		// Compact, sorted-key JSON (json.Marshal sorts map keys) keeps the output
		// deterministic and small.
		data, err := json.Marshal(dict)
		if err != nil {
			return fmt.Errorf("marshal dictionary for %q: %w", locale, err)
		}

		gz, err := compress(data)
		if err != nil {
			return fmt.Errorf("compress dictionary for %q: %w", locale, err)
		}

		path := filepath.Join(outDir, locale+".json.gz")
		if err := os.WriteFile(path, gz, 0o644); err != nil { //nolint:gosec // public, non-secret static asset
			return fmt.Errorf("write %q: %w", path, err)
		}
		fmt.Printf("wrote %s (%d species, %d bytes gzip)\n", path, len(dict), len(gz))
	}
	return nil
}

// compress gzips data at a fixed level with a zero-valued header so the result is
// byte-deterministic across machines and runs.
func compress(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(data); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
