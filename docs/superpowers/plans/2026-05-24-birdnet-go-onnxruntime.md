# birdnet-go-onnxruntime Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create the `tphakala/birdnet-go-onnxruntime` GitHub repository with CI workflows that build ONNX Runtime from source with EP support and publish per-platform archives to GitHub Releases.

**Architecture:** A build-only repository containing shell/PowerShell scripts that compile ORT from the official Microsoft source with EP flags enabled, a GitHub Actions workflow that orchestrates per-platform builds and publishes release archives, and a minimal Go smoke test to verify ABI compatibility. The git tag is the single source of truth for versioning.

**Tech Stack:** GitHub Actions, Bash, PowerShell, CMake/Ninja (via ORT's build.py), Go (smoke test only), Python 3 (ORT build system dependency)

**Spec:** `docs/superpowers/specs/2026-05-24-birdnet-go-onnxruntime-design.md`

---

## File Map

| File | Purpose |
|------|---------|
| `scripts/build-linux-x64.sh` | Build ORT with OpenVINO EP on Linux x86_64 |
| `scripts/build-win-x64.ps1` | Build ORT with OpenVINO + DirectML EPs on Windows x86_64 |
| `scripts/package.sh` | Create archive with metadata from build output |
| `test/go.mod` | Go module for smoke test (depends on `yalue/onnxruntime_go`) |
| `test/smoke_test.go` | Load ORT library, init runtime, verify C-API ABI |
| `.github/workflows/build.yml` | CI: build per platform, run smoke test, create release |
| `Makefile` | Helper targets: `release`, `local-build-linux` |
| `LICENSE` | MIT license (matches ORT) |
| `README.md` | Usage, EP matrix, version bumping instructions |

---

## Task 1: Create GitHub repository and initial scaffolding

**Files:**
- Create: `LICENSE`, `README.md`, `Makefile`, `.gitignore`

- [ ] **Step 1: Create the GitHub repository**

```bash
gh repo create tphakala/birdnet-go-onnxruntime --public \
  --description "Pre-compiled ONNX Runtime with Execution Provider support for BirdNET-Go" \
  --license mit
```

- [ ] **Step 2: Clone the repository locally**

```bash
cd /home/thakala/src
git clone git@github.com:tphakala/birdnet-go-onnxruntime.git
cd birdnet-go-onnxruntime
```

- [ ] **Step 3: Create .gitignore**

```gitignore
# Build outputs
build/
*.tar.gz
*.zip

# ORT source (checked out during build)
onnxruntime/

# OS artifacts
.DS_Store
Thumbs.db
```

- [ ] **Step 4: Create Makefile**

```makefile
.PHONY: release local-build-linux help

ORT_VERSION ?= 1.25.1
BUILD ?= 1
TAG = v$(ORT_VERSION)-$(BUILD)

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

release: ## Create and push a release tag (ORT_VERSION=1.25.1 BUILD=1)
	@if git rev-parse "$(TAG)" >/dev/null 2>&1; then \
		echo "Error: tag $(TAG) already exists"; exit 1; \
	fi
	git tag "$(TAG)"
	git push origin "$(TAG)"
	@echo "Pushed tag $(TAG) - CI will build and create the release"

local-build-linux: ## Build ORT with OpenVINO EP locally (requires OpenVINO SDK)
	./scripts/build-linux-x64.sh "$(ORT_VERSION)"
```

- [ ] **Step 5: Create README.md**

```markdown
# birdnet-go-onnxruntime

Pre-compiled ONNX Runtime shared libraries with hardware Execution Provider (EP)
support for [BirdNET-Go](https://github.com/tphakala/birdnet-go).

## Available Builds

| Platform | Architecture | Execution Providers | Format |
|----------|-------------|---------------------|--------|
| Linux | x86_64 | OpenVINO, CPU | tar.gz |
| Windows | x86_64 | OpenVINO, DirectML, CPU | zip |

## Download

Download archives from [GitHub Releases](https://github.com/tphakala/birdnet-go-onnxruntime/releases).

Each archive contains:
- ONNX Runtime shared libraries with EP support compiled in
- `metadata.json` with version info, checksums, and runtime dependency notes

## Versioning

Tags follow `v{ORT_VERSION}-{BUILD_NUMBER}`:
- `v1.25.1-1` - first build of ORT 1.25.1
- `v1.25.1-2` - re-release of same ORT version
- `v1.26.0-1` - new ORT version

## Creating a Release

```bash
make release ORT_VERSION=1.25.1 BUILD=1
```

This creates and pushes tag `v1.25.1-1`. GitHub Actions builds all platforms
and creates the release automatically.

## Runtime Dependencies

These builds include only the ONNX Runtime libraries. EP-specific runtime
dependencies must be installed separately:

### OpenVINO (Linux)
- Install OpenVINO runtime from Intel APT repo
- Install `intel-opencl-icd` for GPU compute
- Add user to `render` and `video` groups for `/dev/dri` access

### OpenVINO (Windows)
- Install OpenVINO runtime from intel.com

### DirectML (Windows)
- Requires DirectX 12 compatible GPU
- DirectML runtime is included in Windows 10 1903+

## License

MIT License (same as ONNX Runtime)
```

- [ ] **Step 6: Commit initial scaffolding**

```bash
git add .gitignore Makefile README.md
git commit -m "Initial repository scaffolding

Makefile with release tagging helper, README with EP matrix and usage docs."
```

---

## Task 2: Linux build script

**Files:**
- Create: `scripts/build-linux-x64.sh`

- [ ] **Step 1: Create the scripts directory**

```bash
mkdir -p scripts
```

- [ ] **Step 2: Write the Linux build script**

```bash
#!/usr/bin/env bash
# Build ONNX Runtime with OpenVINO EP for Linux x86_64.
# Usage: ./scripts/build-linux-x64.sh <ort-version>
# Expects: OpenVINO SDK installed, python3, cmake, ninja-build available.

set -euo pipefail

ORT_VERSION="${1:?Usage: $0 <ort-version>}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
ORT_SRC="${REPO_ROOT}/onnxruntime"
BUILD_DIR="${ORT_SRC}/build/Linux-x64"
OUTPUT_DIR="${REPO_ROOT}/build/linux-x64"

# Find OpenVINO cmake config directory
find_openvino_dir() {
    local dir
    for dir in /usr/lib/cmake/openvino* /opt/intel/openvino*/runtime/cmake; do
        if [ -d "$dir" ]; then
            echo "$dir"
            return
        fi
    done
    echo ""
}

OPENVINO_DIR=$(find_openvino_dir)
if [ -z "$OPENVINO_DIR" ]; then
    echo "Error: OpenVINO SDK not found. Install from Intel APT repo:"
    echo "  apt-get install openvino-toolkit-2025.4"
    exit 1
fi
echo "Found OpenVINO at: $OPENVINO_DIR"

# Clone or update ORT source
if [ ! -d "$ORT_SRC" ]; then
    echo "Cloning ONNX Runtime v${ORT_VERSION}..."
    git clone --depth 1 --branch "v${ORT_VERSION}" \
        https://github.com/microsoft/onnxruntime.git "$ORT_SRC"
else
    echo "ORT source already present at $ORT_SRC"
fi

# Determine parallelism (limit to avoid OOM on CI runners with 7GB RAM)
PARALLEL=${BUILD_PARALLEL:-4}
echo "Building with --parallel $PARALLEL"

# Build
cd "$ORT_SRC"
python3 tools/ci_build/build.py \
    --build_dir "$BUILD_DIR" \
    --config Release \
    --parallel "$PARALLEL" \
    --build_shared_lib \
    --use_openvino GPU \
    --skip_tests \
    --cmake_generator Ninja \
    --cmake_extra_defines \
        OpenVINO_DIR="$OPENVINO_DIR" \
        onnxruntime_BUILD_UNIT_TESTS=OFF \
        onnxruntime_DISABLE_GENERATION_OPS=ON \
    --compile_no_warning_as_error

# Collect output
mkdir -p "$OUTPUT_DIR"
cp "$BUILD_DIR/Release/libonnxruntime.so.${ORT_VERSION}" "$OUTPUT_DIR/"
cp "$BUILD_DIR/Release/libonnxruntime_providers_openvino.so" "$OUTPUT_DIR/"
cp "$BUILD_DIR/Release/libonnxruntime_providers_shared.so" "$OUTPUT_DIR/"

echo "Build complete. Output in $OUTPUT_DIR:"
ls -lh "$OUTPUT_DIR/"
```

- [ ] **Step 3: Make executable and commit**

```bash
chmod +x scripts/build-linux-x64.sh
git add scripts/build-linux-x64.sh
git commit -m "Add Linux x86_64 build script for ORT with OpenVINO EP

Builds from microsoft/onnxruntime source with --use_openvino GPU.
Limits parallelism to avoid OOM on CI runners. Auto-discovers
OpenVINO cmake config directory."
```

---

## Task 3: Windows build script

**Files:**
- Create: `scripts/build-win-x64.ps1`

- [ ] **Step 1: Write the Windows build script**

```powershell
# Build ONNX Runtime with OpenVINO + DirectML EPs for Windows x86_64.
# Usage: .\scripts\build-win-x64.ps1 -OrtVersion 1.25.1
# Expects: OpenVINO SDK, Python 3, CMake, Ninja, Visual Studio Build Tools.

param(
    [Parameter(Mandatory=$true)]
    [string]$OrtVersion
)

$ErrorActionPreference = "Stop"

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$RepoRoot = Split-Path -Parent $ScriptDir
$OrtSrc = Join-Path $RepoRoot "onnxruntime"
$BuildDir = Join-Path $OrtSrc "build\Windows-x64"
$OutputDir = Join-Path $RepoRoot "build\win-x64"

# Find OpenVINO installation
$OpenVinoDir = $null
$SearchPaths = @(
    "C:\Program Files (x86)\Intel\openvino*\runtime\cmake",
    "C:\opt\intel\openvino*\runtime\cmake",
    "$env:INTEL_OPENVINO_DIR\runtime\cmake"
)
foreach ($pattern in $SearchPaths) {
    $found = Get-ChildItem -Path $pattern -Directory -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($found) {
        $OpenVinoDir = $found.FullName
        break
    }
}

# Also check pip-installed OpenVINO
if (-not $OpenVinoDir) {
    $pipDir = python -c "import openvino; import os; print(os.path.dirname(openvino.__file__))" 2>$null
    if ($pipDir -and (Test-Path "$pipDir\cmake")) {
        $OpenVinoDir = "$pipDir\cmake"
    }
}

if (-not $OpenVinoDir) {
    Write-Error "OpenVINO SDK not found. Install via pip (pip install openvino) or from intel.com."
    exit 1
}
Write-Host "Found OpenVINO at: $OpenVinoDir"

# Clone ORT source if not present
if (-not (Test-Path $OrtSrc)) {
    Write-Host "Cloning ONNX Runtime v$OrtVersion..."
    git clone --depth 1 --branch "v$OrtVersion" `
        https://github.com/microsoft/onnxruntime.git $OrtSrc
}

# Determine parallelism
$Parallel = if ($env:BUILD_PARALLEL) { $env:BUILD_PARALLEL } else { "4" }
Write-Host "Building with --parallel $Parallel"

# Build with both OpenVINO and DirectML
Set-Location $OrtSrc
python tools\ci_build\build.py `
    --build_dir $BuildDir `
    --config Release `
    --parallel $Parallel `
    --build_shared_lib `
    --use_openvino GPU `
    --use_dml `
    --skip_tests `
    --cmake_generator Ninja `
    --cmake_extra_defines `
        OpenVINO_DIR="$OpenVinoDir" `
        onnxruntime_BUILD_UNIT_TESTS=OFF `
        onnxruntime_DISABLE_GENERATION_OPS=ON `
    --compile_no_warning_as_error

# Collect output
New-Item -ItemType Directory -Path $OutputDir -Force | Out-Null
$ReleaseDir = Join-Path $BuildDir "Release"

Copy-Item "$ReleaseDir\onnxruntime.dll" $OutputDir
Copy-Item "$ReleaseDir\onnxruntime_providers_shared.dll" $OutputDir

# Copy EP provider DLLs (may or may not exist depending on build config)
$epDlls = @(
    "onnxruntime_providers_openvino.dll",
    "onnxruntime_providers_dml.dll"
)
foreach ($dll in $epDlls) {
    $src = Join-Path $ReleaseDir $dll
    if (Test-Path $src) {
        Copy-Item $src $OutputDir
        Write-Host "Copied: $dll"
    } else {
        Write-Warning "EP DLL not found (may be statically linked): $dll"
    }
}

Write-Host "Build complete. Output in $OutputDir`:"
Get-ChildItem $OutputDir | Format-Table Name, Length
```

- [ ] **Step 2: Commit**

```bash
git add scripts/build-win-x64.ps1
git commit -m "Add Windows x86_64 build script for ORT with OpenVINO + DirectML

Builds from microsoft/onnxruntime source with --use_openvino GPU --use_dml.
Auto-discovers OpenVINO from system install or pip package."
```

---

## Task 4: Packaging script

**Files:**
- Create: `scripts/package.sh`

- [ ] **Step 1: Write the packaging script**

This script takes the build output directory, platform info, and version, then creates the archive with `metadata.json`.

```bash
#!/usr/bin/env bash
# Package ORT build output into a release archive with metadata.
# Usage: ./scripts/package.sh <platform> <arch> <ort-version> <build-number> [ort-commit]
# Example: ./scripts/package.sh linux x64 1.25.1 1 abc123def
#
# Reads libraries from build/<platform>-<arch>/
# Writes archive to dist/

set -euo pipefail

PLATFORM="${1:?Usage: $0 <platform> <arch> <ort-version> <build-number> [ort-commit]}"
ARCH="${2:?}"
ORT_VERSION="${3:?}"
BUILD_NUMBER="${4:?}"
ORT_COMMIT="${5:-unknown}"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

TAG="v${ORT_VERSION}-${BUILD_NUMBER}"
ARTIFACT_NAME="onnxruntime-${PLATFORM}-${ARCH}-${TAG}"
BUILD_INPUT="${REPO_ROOT}/build/${PLATFORM}-${ARCH}"
STAGING="${REPO_ROOT}/dist/${ARTIFACT_NAME}"
DIST_DIR="${REPO_ROOT}/dist"

if [ ! -d "$BUILD_INPUT" ]; then
    echo "Error: build output not found at $BUILD_INPUT"
    exit 1
fi

# Create staging directory
rm -rf "$STAGING"
mkdir -p "$STAGING/lib"
cp "$BUILD_INPUT"/* "$STAGING/lib/"

# Compute per-library checksums and sizes
libraries_json="["
first=true
for lib in "$STAGING/lib"/*; do
    name=$(basename "$lib")
    size=$(stat --format="%s" "$lib" 2>/dev/null || stat -f "%z" "$lib")
    sha=$(sha256sum "$lib" | cut -d' ' -f1)
    if [ "$first" = true ]; then
        first=false
    else
        libraries_json+=","
    fi
    libraries_json+=$(printf '\n    {"name": "%s", "size": %s, "sha256": "%s"}' "$name" "$size" "$sha")
done
libraries_json+=$'\n  ]'

# Detect EPs from library names
eps_json="{"
eps_json+='"CPU": {"library": null}'

if ls "$STAGING/lib"/*providers_openvino* >/dev/null 2>&1; then
    openvino_lib=$(basename "$STAGING/lib"/*providers_openvino*)
    openvino_ver="${OPENVINO_VERSION:-unknown}"
    eps_json+=","
    eps_json+=$(printf '\n    "OpenVINO": {"library": "%s", "sdk_version": "%s", "runtime_deps": {"linux_packages": ["intel-opencl-icd"], "user_groups": ["render", "video"], "notes": "Install OpenVINO runtime from Intel APT repo or download from intel.com"}}' "$openvino_lib" "$openvino_ver")
fi

if ls "$STAGING/lib"/*providers_dml* >/dev/null 2>&1; then
    dml_lib=$(basename "$STAGING/lib"/*providers_dml*)
    eps_json+=","
    eps_json+=$(printf '\n    "DirectML": {"library": "%s", "runtime_deps": {"notes": "Requires DirectX 12 compatible GPU and Windows 10 1903+"}}' "$dml_lib")
fi

eps_json+=$'\n  }'

# Build date
build_date=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
runner="${GITHUB_RUNNER_NAME:-local}"

# Write metadata
cat > "$STAGING/metadata.json" <<METADATA
{
  "ort_version": "${ORT_VERSION}",
  "build_number": ${BUILD_NUMBER},
  "tag": "${TAG}",
  "platform": "${PLATFORM}",
  "arch": "${ARCH}",
  "execution_providers": ${eps_json},
  "build_info": {
    "build_date": "${build_date}",
    "runner": "${runner}",
    "ort_commit": "${ORT_COMMIT}"
  },
  "libraries": ${libraries_json}
}
METADATA

# Create archive
mkdir -p "$DIST_DIR"
if [ "$PLATFORM" = "win" ]; then
    archive_name="${ARTIFACT_NAME}.zip"
    (cd "$DIST_DIR" && zip -r "$archive_name" "$ARTIFACT_NAME")
else
    archive_name="${ARTIFACT_NAME}.tar.gz"
    tar -czf "$DIST_DIR/$archive_name" -C "$DIST_DIR" "$ARTIFACT_NAME"
fi

# Create checksums file (separate from archive, uploaded alongside it)
archive_sha=$(sha256sum "$DIST_DIR/$archive_name" | cut -d' ' -f1)
echo "$archive_sha  $archive_name" > "$DIST_DIR/${ARTIFACT_NAME}.sha256"

echo "Archive: $DIST_DIR/$archive_name"
echo "SHA256:  $archive_sha"
echo "archive_path=$DIST_DIR/$archive_name" >> "${GITHUB_OUTPUT:-/dev/null}"
echo "archive_name=$archive_name" >> "${GITHUB_OUTPUT:-/dev/null}"
echo "archive_sha256=$archive_sha" >> "${GITHUB_OUTPUT:-/dev/null}"
```

- [ ] **Step 2: Make executable and commit**

```bash
chmod +x scripts/package.sh
git add scripts/package.sh
git commit -m "Add packaging script for release archives with metadata

Creates tar.gz (Linux) or zip (Windows) archives containing ORT
libraries and metadata.json with checksums, EP mapping, and
runtime dependency info."
```

---

## Task 5: Go smoke test

**Files:**
- Create: `test/go.mod`, `test/smoke_test.go`

- [ ] **Step 1: Initialize Go module**

```bash
mkdir -p test
cd test
go mod init github.com/tphakala/birdnet-go-onnxruntime/test
go get github.com/yalue/onnxruntime_go@v1.30.1
cd ..
```

- [ ] **Step 2: Write the smoke test**

```go
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	ort "github.com/yalue/onnxruntime_go"
)

func main() {
	libDir := os.Getenv("ORT_LIB_DIR")
	if libDir == "" {
		fmt.Fprintln(os.Stderr, "ORT_LIB_DIR not set")
		os.Exit(1)
	}

	var libName string
	switch runtime.GOOS {
	case "linux":
		matches, _ := filepath.Glob(filepath.Join(libDir, "libonnxruntime.so.*.*.*"))
		if len(matches) == 0 {
			fmt.Fprintln(os.Stderr, "libonnxruntime.so.*.*.* not found in "+libDir)
			os.Exit(1)
		}
		libName = matches[0]
	case "windows":
		libName = filepath.Join(libDir, "onnxruntime.dll")
	case "darwin":
		matches, _ := filepath.Glob(filepath.Join(libDir, "libonnxruntime.*.dylib"))
		if len(matches) == 0 {
			fmt.Fprintln(os.Stderr, "libonnxruntime.*.dylib not found in "+libDir)
			os.Exit(1)
		}
		libName = matches[0]
	default:
		fmt.Fprintf(os.Stderr, "unsupported OS: %s\n", runtime.GOOS)
		os.Exit(1)
	}

	fmt.Printf("Loading ORT library: %s\n", libName)
	ort.SetSharedLibraryPath(libName)

	if err := ort.InitializeEnvironment(); err != nil {
		fmt.Fprintf(os.Stderr, "InitializeEnvironment failed: %v\n", err)
		os.Exit(1)
	}
	defer ort.DestroyEnvironment()

	version := ort.GetVersion()
	fmt.Printf("ORT version: %s\n", version)

	providers, err := ort.GetAvailableProviders()
	if err != nil {
		fmt.Fprintf(os.Stderr, "GetAvailableProviders failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Available providers: %v\n", providers)

	if len(providers) == 0 {
		fmt.Fprintln(os.Stderr, "No providers available")
		os.Exit(1)
	}

	fmt.Println("Smoke test PASSED")
}
```

- [ ] **Step 3: Verify the smoke test compiles**

```bash
cd test && go build -o /dev/null . && cd ..
```

- [ ] **Step 4: Run smoke test against local build (if available)**

```bash
cd test
ORT_LIB_DIR=/home/thakala/src/onnxruntime/build/Linux-x64-openvino/Release go run .
cd ..
```

Expected output:
```
Loading ORT library: /home/thakala/src/onnxruntime/build/Linux-x64-openvino/Release/libonnxruntime.so.1.25.1
ORT version: 1.25.1
Available providers: [OpenVINOExecutionProvider CPUExecutionProvider]
Smoke test PASSED
```

- [ ] **Step 5: Commit**

```bash
git add test/
git commit -m "Add Go smoke test for ORT library ABI verification

Loads the built library via yalue/onnxruntime_go, initializes the
runtime, and lists available execution providers. Catches C-API
ABI mismatches that simple dlopen checks would miss."
```

---

## Task 6: GitHub Actions workflow

**Files:**
- Create: `.github/workflows/build.yml`

- [ ] **Step 1: Create the workflow directory**

```bash
mkdir -p .github/workflows
```

- [ ] **Step 2: Write the build workflow**

```yaml
name: Build ONNX Runtime

on:
  push:
    tags:
      - 'v*'
  workflow_dispatch:
    inputs:
      platform:
        description: 'Platform to build (leave empty for all)'
        required: false
        default: ''
        type: choice
        options:
          - ''
          - linux
          - windows

permissions:
  contents: write

env:
  OPENVINO_VERSION: '2025.4.1'

jobs:
  parse-version:
    runs-on: ubuntu-latest
    outputs:
      ort_version: ${{ steps.parse.outputs.ort_version }}
      build_number: ${{ steps.parse.outputs.build_number }}
      tag: ${{ steps.parse.outputs.tag }}
    steps:
      - name: Parse version from tag or generate for dispatch
        id: parse
        run: |
          if [[ "${{ github.ref_type }}" == "tag" ]]; then
            TAG="${{ github.ref_name }}"
          else
            # For workflow_dispatch without a tag, use a dev tag
            TAG="v1.25.1-dev"
          fi
          # Parse: v1.25.1-1 -> ort_version=1.25.1, build_number=1
          if [[ "$TAG" =~ ^v([0-9]+\.[0-9]+\.[0-9]+)-(.+)$ ]]; then
            echo "ort_version=${BASH_REMATCH[1]}" >> "$GITHUB_OUTPUT"
            echo "build_number=${BASH_REMATCH[2]}" >> "$GITHUB_OUTPUT"
            echo "tag=$TAG" >> "$GITHUB_OUTPUT"
          else
            echo "::error::Tag '$TAG' does not match pattern v{VERSION}-{BUILD}"
            exit 1
          fi

  build-linux:
    needs: parse-version
    if: inputs.platform == '' || inputs.platform == 'linux'
    runs-on: ubuntu-24.04
    env:
      ORT_VERSION: ${{ needs.parse-version.outputs.ort_version }}
      BUILD_NUMBER: ${{ needs.parse-version.outputs.build_number }}
    steps:
      - name: Checkout this repo
        uses: actions/checkout@v6

      - name: Free disk space
        run: |
          sudo rm -rf /usr/local/lib/android /usr/share/dotnet /opt/ghc
          df -h /

      - name: Install OpenVINO SDK
        run: |
          wget -qO- https://apt.repos.intel.com/intel-gpg-keys/GPG-PUB-KEY-INTEL-SW-PRODUCTS.PUB \
            | gpg --dearmor | sudo tee /usr/share/keyrings/intel-openvino.gpg >/dev/null
          echo "deb [signed-by=/usr/share/keyrings/intel-openvino.gpg] https://apt.repos.intel.com/openvino/2025 ubuntu24 main" \
            | sudo tee /etc/apt/sources.list.d/intel-openvino.list
          sudo apt-get update
          sudo apt-get install -y openvino-toolkit-${{ env.OPENVINO_VERSION }}

      - name: Install build dependencies
        run: |
          sudo apt-get install -y python3 python3-pip cmake ninja-build protobuf-compiler

      - name: Checkout ONNX Runtime source
        uses: actions/checkout@v6
        with:
          repository: microsoft/onnxruntime
          ref: v${{ env.ORT_VERSION }}
          path: onnxruntime
          fetch-depth: 1
          submodules: recursive

      - name: Get ORT commit hash
        id: ort-commit
        run: |
          cd onnxruntime
          echo "commit=$(git rev-parse --short HEAD)" >> "$GITHUB_OUTPUT"

      - name: Build ORT with OpenVINO EP
        run: |
          export BUILD_PARALLEL=4
          ./scripts/build-linux-x64.sh "${{ env.ORT_VERSION }}"

      - name: Verify build output
        run: |
          echo "Build output:"
          ls -lh build/linux-x64/
          # Verify all expected libraries exist and are non-empty
          test -s build/linux-x64/libonnxruntime.so.${{ env.ORT_VERSION }}
          test -s build/linux-x64/libonnxruntime_providers_openvino.so
          test -s build/linux-x64/libonnxruntime_providers_shared.so
          # Verify linkage
          ldd build/linux-x64/libonnxruntime.so.${{ env.ORT_VERSION }}

      - name: Setup Go
        uses: actions/setup-go@v6
        with:
          go-version: '1.24'

      - name: Run Go smoke test
        run: |
          cd test
          ORT_LIB_DIR=${{ github.workspace }}/build/linux-x64 go run .

      - name: Package archive
        id: package
        run: |
          export OPENVINO_VERSION="${{ env.OPENVINO_VERSION }}"
          export GITHUB_RUNNER_NAME="${{ runner.name }}"
          ./scripts/package.sh linux x64 "${{ env.ORT_VERSION }}" "${{ env.BUILD_NUMBER }}" "${{ steps.ort-commit.outputs.commit }}"

      - name: Upload artifact
        uses: actions/upload-artifact@v4
        with:
          name: onnxruntime-linux-x64
          path: |
            dist/onnxruntime-linux-x64-*.tar.gz
            dist/onnxruntime-linux-x64-*.sha256

  build-windows:
    needs: parse-version
    if: inputs.platform == '' || inputs.platform == 'windows'
    runs-on: windows-2022
    env:
      ORT_VERSION: ${{ needs.parse-version.outputs.ort_version }}
      BUILD_NUMBER: ${{ needs.parse-version.outputs.build_number }}
    steps:
      - name: Checkout this repo
        uses: actions/checkout@v6

      - name: Setup Python
        uses: actions/setup-python@v5
        with:
          python-version: '3.12'

      - name: Install OpenVINO via pip
        run: |
          pip install openvino==${{ env.OPENVINO_VERSION }}

      - name: Install CMake and Ninja
        run: |
          choco install cmake ninja -y

      - name: Checkout ONNX Runtime source
        uses: actions/checkout@v6
        with:
          repository: microsoft/onnxruntime
          ref: v${{ env.ORT_VERSION }}
          path: onnxruntime
          fetch-depth: 1
          submodules: recursive

      - name: Get ORT commit hash
        id: ort-commit
        shell: bash
        run: |
          cd onnxruntime
          echo "commit=$(git rev-parse --short HEAD)" >> "$GITHUB_OUTPUT"

      - name: Build ORT with OpenVINO + DirectML EPs
        run: |
          $env:BUILD_PARALLEL = "4"
          .\scripts\build-win-x64.ps1 -OrtVersion "${{ env.ORT_VERSION }}"

      - name: Verify build output
        shell: pwsh
        run: |
          Write-Host "Build output:"
          Get-ChildItem build\win-x64 | Format-Table Name, Length
          # Verify core DLL exists
          if (-not (Test-Path "build\win-x64\onnxruntime.dll")) {
              Write-Error "onnxruntime.dll not found"
              exit 1
          }

      - name: Setup Go
        uses: actions/setup-go@v6
        with:
          go-version: '1.24'

      - name: Run Go smoke test
        shell: bash
        run: |
          cd test
          ORT_LIB_DIR="${{ github.workspace }}\\build\\win-x64" go run .

      - name: Package archive
        shell: bash
        run: |
          export OPENVINO_VERSION="${{ env.OPENVINO_VERSION }}"
          export GITHUB_RUNNER_NAME="${{ runner.name }}"
          ./scripts/package.sh win x64 "${{ env.ORT_VERSION }}" "${{ env.BUILD_NUMBER }}" "${{ steps.ort-commit.outputs.commit }}"

      - name: Upload artifact
        uses: actions/upload-artifact@v4
        with:
          name: onnxruntime-win-x64
          path: |
            dist/onnxruntime-win-x64-*.zip
            dist/onnxruntime-win-x64-*.sha256

  release:
    needs: [parse-version, build-linux, build-windows]
    if: github.ref_type == 'tag'
    runs-on: ubuntu-latest
    env:
      TAG: ${{ needs.parse-version.outputs.tag }}
      ORT_VERSION: ${{ needs.parse-version.outputs.ort_version }}
    steps:
      - name: Download all artifacts
        uses: actions/download-artifact@v4
        with:
          path: artifacts

      - name: List artifacts
        run: find artifacts -type f

      - name: Create GitHub Release
        run: |
          gh release create "${{ env.TAG }}" \
            artifacts/**/* \
            --repo "${{ github.repository }}" \
            --title "ONNX Runtime ${{ env.ORT_VERSION }} (Build ${{ needs.parse-version.outputs.build_number }})" \
            --notes "$(cat <<'NOTES'
          ## ONNX Runtime ${{ env.ORT_VERSION }}

          Pre-compiled ONNX Runtime with Execution Provider support for BirdNET-Go.

          ### Included EPs

          | Platform | EPs |
          |----------|-----|
          | Linux x86_64 | OpenVINO, CPU |
          | Windows x86_64 | OpenVINO, DirectML, CPU |

          ### Runtime Dependencies

          These archives contain only the ONNX Runtime shared libraries. EP-specific
          runtime dependencies must be installed separately. See metadata.json in
          each archive for details.

          ### Checksums

          Verify archive integrity using the SHA256 checksum in the metadata.json
          inside each archive, or use:
          ```
          sha256sum onnxruntime-*.tar.gz onnxruntime-*.zip
          ```
          NOTES
          )"
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/build.yml
git commit -m "Add GitHub Actions workflow for building and releasing ORT

Builds ORT from source with OpenVINO EP (Linux) and OpenVINO + DirectML
(Windows). Parses version from git tag. Creates GitHub Release with
platform archives on tag push. Supports manual workflow_dispatch for
testing individual platform builds."
```

---

## Task 7: Push and verify

- [ ] **Step 1: Push all commits to GitHub**

```bash
git push origin main
```

- [ ] **Step 2: Verify the repository is accessible**

```bash
gh repo view tphakala/birdnet-go-onnxruntime
```

---

## Task 8: Test with a manual workflow dispatch

- [ ] **Step 1: Trigger a Linux-only test build**

```bash
gh workflow run build.yml --repo tphakala/birdnet-go-onnxruntime \
  -f platform=linux
```

- [ ] **Step 2: Monitor the build**

```bash
gh run list --repo tphakala/birdnet-go-onnxruntime --limit 1
gh run watch --repo tphakala/birdnet-go-onnxruntime
```

- [ ] **Step 3: If the build succeeds, download and inspect the artifact**

```bash
gh run download --repo tphakala/birdnet-go-onnxruntime -n onnxruntime-linux-x64
tar -tzf onnxruntime-linux-x64-v1.25.1-dev/onnxruntime-linux-x64-v1.25.1-dev.tar.gz
```

- [ ] **Step 4: If the build fails, check logs and fix**

```bash
gh run view --repo tphakala/birdnet-go-onnxruntime --log-failed
```

Common issues to watch for:
- OOM during compilation (reduce `BUILD_PARALLEL` to 2)
- OpenVINO SDK package name changes (check Intel APT repo)
- Disk space exhaustion (add more cleanup in "Free disk space" step)
- Submodule checkout failures (verify `submodules: recursive`)

---

## Task 9: Create the first release

- [ ] **Step 1: Once both platform builds pass in workflow_dispatch, create the release tag**

```bash
cd /home/thakala/src/birdnet-go-onnxruntime
make release ORT_VERSION=1.25.1 BUILD=1
```

- [ ] **Step 2: Monitor the release build**

```bash
gh run watch --repo tphakala/birdnet-go-onnxruntime
```

- [ ] **Step 3: Verify the release was created**

```bash
gh release view v1.25.1-1 --repo tphakala/birdnet-go-onnxruntime
```

- [ ] **Step 4: Download and verify a release artifact locally**

```bash
gh release download v1.25.1-1 --repo tphakala/birdnet-go-onnxruntime \
  -p "onnxruntime-linux-x64-*"
tar -xzf onnxruntime-linux-x64-v1.25.1-1.tar.gz
cat onnxruntime-linux-x64-v1.25.1-1/metadata.json | python3 -m json.tool
```

Verify:
- `ort_version` is `1.25.1`
- `execution_providers` includes `OpenVINO` and `CPU`
- `libraries` array has SHA256 checksums
- `archive.sha256` is populated
