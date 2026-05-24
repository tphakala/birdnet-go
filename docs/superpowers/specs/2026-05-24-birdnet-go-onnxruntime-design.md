# birdnet-go-onnxruntime: Pre-compiled ONNX Runtime with EP Support

**Date:** 2026-05-24
**Status:** Draft
**Related:** Forgejo #816 (Hardware acceleration plugin system for ONNX inference)

## Overview

A new GitHub repository (`tphakala/birdnet-go-onnxruntime`) that builds and distributes pre-compiled ONNX Runtime shared libraries with hardware-specific Execution Providers (EPs) enabled. These libraries serve as the download source for BirdNET-Go's hardware acceleration plugin system.

The repository contains only build infrastructure (CI workflows, build scripts) and produces release artifacts (platform-specific archives containing ORT shared libraries and metadata). No application code.

## Motivation

BirdNET-Go ships with CPU-only ONNX Runtime. Users with Intel iGPUs, NVIDIA GPUs, or DirectX 12 hardware can benefit from hardware-accelerated inference, but building ORT with EP support from source is complex and time-consuming. Pre-compiled builds eliminate this barrier.

OpenVINO GPU EP on an i7-1260P (Iris Xe) matches 8-thread CPU performance at 58ms/inference while freeing all CPU cores, and scales to 22.6 segments/s with batch=16 (32% throughput gain). This is compelling for multi-source setups.

## Scope

### In scope

- GitHub Actions CI workflows that build ORT from source with EP support
- Per-platform archives containing ORT shared libraries (no EP runtime dependencies)
- GitHub Releases as the distribution mechanism
- Metadata files describing each build (version, EPs, platform)
- Easy version bumping for new ORT releases

### Out of scope

- EP runtime dependencies (OpenVINO SDK, DirectML runtime, CUDA toolkit) - user's responsibility
- BirdNET-Go integration code (separate work in birdnet-go repo, Forgejo #816)
- Model optimization or conversion
- ARM64 builds (future, not initial release)
- TFLite hardware delegates

## Versioning

Tags follow the pattern `v{ORT_VERSION}-{BUILD_NUMBER}`:

- `v1.25.1-1` - first build of ORT 1.25.1
- `v1.25.1-2` - re-release (packaging fix, added EP, etc.)
- `v1.26.0-1` - new ORT version

**The git tag is the single source of truth.** The CI workflow parses the tag string to extract the ORT version and build number. No VERSION or BUILD_NUMBER files in the repo; the tag drives everything. This avoids the dual-source-of-truth problem where files and tags can drift.

A `Makefile` or shell helper can simplify tag creation:

```bash
# In the repo, a simple helper:
make release ORT_VERSION=1.25.1 BUILD=1
# Equivalent to: git tag v1.25.1-1 && git push origin v1.25.1-1
```

## Repository Structure

```
birdnet-go-onnxruntime/
├── .github/
│   └── workflows/
│       └── build.yml              # Build + release workflow
├── scripts/
│   ├── build-linux-x64.sh        # Build ORT with all Linux EPs (OpenVINO)
│   ├── build-win-x64.ps1         # Build ORT with all Windows EPs (OpenVINO + DirectML)
│   └── package.sh                # Archive creation with metadata
├── test/
│   ├── go.mod                    # Minimal Go module for smoke tests
│   └── smoke_test.go             # Load ORT, init runtime, create dummy session
├── Makefile                       # Helper targets (release tagging, local builds)
├── LICENSE                        # MIT (matching ORT license)
└── README.md                      # Build instructions, usage, EP matrix
```

## Platform and EP Matrix

Initial release targets two platforms with all compatible EPs bundled per platform:

| Platform | Architecture | EPs Included | Archive Format |
|----------|-------------|--------------|----------------|
| Linux | x86_64 | OpenVINO, CPU | tar.gz |
| Windows | x86_64 | OpenVINO, DirectML, CPU | zip |

Future expansion (not in initial release):

| Platform | Architecture | Additional EPs |
|----------|-------------|----------------|
| Linux | x86_64 | CUDA, TensorRT |
| Windows | x86_64 | CUDA, TensorRT |
| Linux | aarch64 | XNNPACK (already built manually) |
| macOS | arm64 | CoreML |
| Windows | arm64 | QNN |

Each platform build includes ALL compatible EPs. Users select which EP to activate at runtime. The CPU EP is always included as a fallback.

## Build Process

### CI Workflow (`.github/workflows/build.yml`)

**Triggers:**
- `workflow_dispatch` with optional platform selection (for testing individual builds)
- Tag push matching `v*` (for releases)

**Jobs:**

#### Linux x86_64

Runner: `ubuntu-24.04`

Steps:
1. Checkout this repo (for scripts and version files)
2. Checkout `microsoft/onnxruntime` at tag `v{VERSION}`
3. Install OpenVINO SDK from Intel APT repository (ubuntu24 channel)
4. Install build dependencies: `python3`, `cmake`, `ninja-build`, `protobuf-compiler`
5. Build ORT:
   ```bash
   python3 tools/ci_build/build.py \
     --build_dir ./build/Linux-x64 \
     --config Release \
     --parallel $(nproc) \
     --build_shared_lib \
     --use_openvino GPU \
     --skip_tests \
     --cmake_generator Ninja \
     --cmake_extra_defines \
       onnxruntime_BUILD_UNIT_TESTS=OFF \
       onnxruntime_DISABLE_GENERATION_OPS=ON \
     --compile_no_warning_as_error
   ```
6. Collect output libraries
7. Generate `metadata.json`
8. Package into `onnxruntime-linux-x64-v{VERSION}-{BUILD}.tar.gz`
9. Upload as workflow artifact

#### Windows x86_64

Runner: `windows-2022`

Steps:
1. Checkout this repo and `microsoft/onnxruntime`
2. Install OpenVINO SDK (Intel's Windows distribution)
3. Install DirectML NuGet package (build dependency)
4. Install build tools: Python, CMake, Ninja, Visual Studio Build Tools
5. Build ORT with `--use_openvino GPU --use_dml`
6. Collect output DLLs
7. Generate `metadata.json`
8. Package into `onnxruntime-win-x64-v{VERSION}-{BUILD}.zip`
9. Upload as workflow artifact

#### Release Job

Runs after all build jobs complete (on tag push only):

1. Download all workflow artifacts
2. Create GitHub Release with tag name
3. Attach all archives to the release
4. Generate release notes listing EPs, platforms, and ORT version

### Resource Constraints

GitHub Actions free runners have 7GB RAM and ~14GB free disk. These are tight for ORT builds:

- **Memory**: `--parallel $(nproc)` with heavy C++ templates can OOM on 7GB. Limit parallelism to 2-4 jobs (e.g., `--parallel 4`) and monitor. If builds OOM, reduce further.
- **Disk**: ORT source (~2GB) + build artifacts (~5GB) + EP SDKs (~500MB-1GB) = ~8GB. Fits in 14GB but leaves little margin. Add cleanup steps between build phases if needed.

### Build Caching

GitHub Actions has a 10GB repo-wide cache limit. The ORT source alone is ~2GB, so caching must be selective:

- Cache pip packages used by ORT's build system (~200MB)
- Cache OpenVINO SDK installation (~500MB)
- Do NOT cache the full ORT source or CMake build directory (too large, evicts other caches)
- Instead, use `actions/checkout` with `fetch-depth: 1` for a shallow clone of ORT

### Expected Build Times

- Linux x86_64: ~30-60 minutes (depends on parallelism setting)
- Windows x86_64: ~45-90 minutes (Windows builds are typically slower)
- Total workflow: ~60-90 minutes (builds run in parallel)

## Artifact Format

### Archive Contents

**Linux (`onnxruntime-linux-x64-v1.25.1-1.tar.gz`):**
```
onnxruntime-linux-x64-v1.25.1-1/
├── lib/
│   ├── libonnxruntime.so.1.25.1
│   ├── libonnxruntime_providers_openvino.so
│   └── libonnxruntime_providers_shared.so
└── metadata.json
```

**Windows (`onnxruntime-win-x64-v1.25.1-1.zip`):**
```
onnxruntime-win-x64-v1.25.1-1/
├── lib/
│   ├── onnxruntime.dll
│   ├── onnxruntime_providers_openvino.dll
│   ├── onnxruntime_providers_dml.dll
│   └── onnxruntime_providers_shared.dll
└── metadata.json
```

### Metadata Schema

```json
{
  "ort_version": "1.25.1",
  "build_number": 1,
  "tag": "v1.25.1-1",
  "platform": "linux",
  "arch": "x86_64",
  "archive": {
    "name": "onnxruntime-linux-x64-v1.25.1-1.tar.gz",
    "sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
  },
  "execution_providers": {
    "OpenVINO": {
      "library": "libonnxruntime_providers_openvino.so",
      "sdk_version": "2025.4.1",
      "runtime_deps": {
        "linux_packages": ["intel-opencl-icd"],
        "user_groups": ["render", "video"],
        "notes": "Install OpenVINO runtime from Intel APT repo or download from intel.com"
      }
    },
    "CPU": {
      "library": null
    }
  },
  "build_info": {
    "build_date": "2026-05-24T12:00:00Z",
    "runner": "ubuntu-24.04",
    "ort_commit": "abc123def"
  },
  "libraries": [
    {
      "name": "libonnxruntime.so.1.25.1",
      "size": 32100000,
      "sha256": "..."
    },
    {
      "name": "libonnxruntime_providers_openvino.so",
      "size": 1900000,
      "sha256": "..."
    },
    {
      "name": "libonnxruntime_providers_shared.so",
      "size": 15000,
      "sha256": "..."
    }
  ]
}
```

Key design choices in the metadata:

- **`archive.sha256`**: BirdNET-Go verifies the archive checksum before extraction.
- **`execution_providers` is a map, not a list**: Each EP explicitly names its provider library and runtime dependencies. The CPU EP has `"library": null` since it's built into the core ORT library.
- **Per-library SHA256**: For post-extraction integrity verification.

## Version Bumping Process

Since the tag is the single source of truth, all version operations are tag operations:

### Bumping ORT version (e.g., 1.25.1 to 1.26.0)

1. Update EP SDK version pins in build scripts if needed
2. `make release ORT_VERSION=1.26.0 BUILD=1` (creates and pushes tag `v1.26.0-1`)
3. CI builds and creates release automatically

### Re-releasing same ORT version

1. Fix the build scripts or packaging
2. `make release ORT_VERSION=1.25.1 BUILD=2` (creates and pushes tag `v1.25.1-2`)

### Adding a new EP to a platform

1. Update the platform's build script to include the new EP flags
2. Update metadata generation to include the new EP
3. Re-release: `make release ORT_VERSION=1.25.1 BUILD=2`

### Failed CI run recovery

If a tagged build fails in CI, fix the issue and re-run the workflow from GitHub Actions UI (same tag). No need to delete and recreate the tag.

## Download URL Pattern

GitHub Releases URLs follow a predictable pattern that BirdNET-Go can construct:

```
https://github.com/tphakala/birdnet-go-onnxruntime/releases/download/v{VERSION}-{BUILD}/{ARTIFACT_NAME}
```

Examples:
- `https://github.com/tphakala/birdnet-go-onnxruntime/releases/download/v1.25.1-1/onnxruntime-linux-x64-v1.25.1-1.tar.gz`
- `https://github.com/tphakala/birdnet-go-onnxruntime/releases/download/v1.25.1-1/onnxruntime-win-x64-v1.25.1-1.zip`

BirdNET-Go can also use the GitHub API to list releases and discover available artifacts dynamically.

## Security Considerations

- All builds run in GitHub Actions (auditable, reproducible)
- SHA256 checksums in `metadata.json` for integrity verification
- BirdNET-Go should verify checksums after download
- Libraries are built from the official `microsoft/onnxruntime` source at a pinned tag
- No third-party binary blobs; all dependencies come from official sources (Intel APT, NuGet)

## Testing Strategy

### Build verification (in CI)

- Verify expected libraries are produced (file existence, non-zero size)
- Verify library can be loaded: `python3 -c "import ctypes; ctypes.CDLL('./libonnxruntime.so.1.25.1')"`
- Verify EP provider library exists and links correctly (`ldd` on Linux, `dumpbin /dependents` on Windows)
- Run a minimal Go smoke test: a small Go program that loads the library via `yalue/onnxruntime_go`, initializes the runtime, and creates a dummy session with CPU EP. This catches C-API ABI mismatches that ctypes loading would miss. The smoke test binary lives in this repo under `test/`.

### Integration testing (manual, not in CI)

- Download release artifact on target hardware with the appropriate EP hardware
- Replace BirdNET-Go's ORT library with the EP-enabled one
- Verify EP activation and inference works
- This is covered by the BirdNET-Go plugin system testing (Forgejo #816)

## Open Questions

1. **OpenVINO SDK on Windows:** The Intel APT repo approach works well on Linux. Windows needs a different approach: Intel distributes OpenVINO as an MSI installer, a pip package, or via vcpkg. The CI script needs to determine which works best in a GitHub Actions Windows runner. Likely the pip package (`openvino-dev`) or a direct archive download is simplest.
2. **Build time limits:** GitHub Actions free runners have a 6-hour job limit. ORT builds should finish well within this (~30-45 min Linux, ~45-60 min Windows based on local experience), but monitor actual CI times. The ORT source checkout (~2GB) dominates cold-start time; caching helps.
3. **CUDA builds (future):** Will need the CUDA toolkit (~4GB) installed in CI. GitHub's free runners have ~14GB free disk. May be tight with ORT source + CUDA + build artifacts. Evaluate when adding CUDA support.
4. **Disk space:** The ORT source tree is ~2GB, build artifacts can be ~5GB. GitHub Actions runners have ~14GB free. Combined with EP SDKs, this may get tight for Windows builds with multiple EPs. Monitor and consider cleanup steps between build phases.
