# ONNX Runtime Installation Guide

ONNX Runtime is a shared library required by BirdNET-Go for neural network inference. It powers the BirdNET v2.4 classifier, Perch v2 embeddings, BattyBirdNET bat detection, range filtering, and BirdNET Geomodel features.

> **Note:** ONNX Runtime will become the sole inference backend in a future release. TensorFlow Lite support is being phased out. If you are setting up a new installation, only ONNX Runtime is needed going forward.

## Do You Need This Guide?

**Docker / container installs (`install.sh`):** ONNX Runtime is baked into the container image. No action needed.

**Release tarballs (`.tar.gz` from GitHub Releases):** The ONNX Runtime library is bundled in every tarball. You just need to install it to the right location on your system. See the [Installing from Release Tarballs](#installing-from-release-tarballs) section.

**Building from source:** You need to download ONNX Runtime yourself. See [Manual Download](#manual-download).

## Required Version

BirdNET-Go requires **ONNX Runtime 1.25.x**. The Go bindings (`onnxruntime_go`) are compiled against `ORT_API_VERSION 25`, which only exists in the 1.25.x series. Older versions (1.24.x and below) and newer major versions will fail to load.

## Installing from Release Tarballs

Every release tarball includes the ONNX Runtime library for the target platform. After extracting the archive, install the library to a system path so the dynamic linker can find it.

### Linux

```bash
# Extract the tarball
tar xzf birdnet-go-linux-amd64-*.tar.gz

# Install the library system-wide
sudo cp libonnxruntime.so /usr/lib/
sudo ldconfig

# Verify
ldconfig -p | grep onnxruntime
```

Expected output:

```
libonnxruntime.so (libc6,x86-64) => /usr/lib/libonnxruntime.so
```

If you don't have root access, use `LD_LIBRARY_PATH` instead:

```bash
export LD_LIBRARY_PATH="$(pwd):$LD_LIBRARY_PATH"
./birdnet-go
```

Add the export to your `~/.bashrc` or `~/.profile` to make it persistent.

### macOS

```bash
# Extract the tarball
tar xzf birdnet-go-darwin-arm64-*.tar.gz

# Install the library
sudo mkdir -p /usr/local/lib
sudo cp libonnxruntime.dylib /usr/local/lib/

# Remove quarantine attribute (macOS Gatekeeper)
xattr -d com.apple.quarantine /usr/local/lib/libonnxruntime.dylib

# Verify
ls -la /usr/local/lib/libonnxruntime.dylib
```

macOS does not use `ldconfig`. The dynamic linker searches `/usr/local/lib/` by default.

If you prefer not to copy system-wide:

```bash
export DYLD_LIBRARY_PATH="$(pwd):$DYLD_LIBRARY_PATH"
./birdnet-go
```

Note: macOS System Integrity Protection strips `DYLD_LIBRARY_PATH` when launching through `sudo` or `launchd`. Use the `/usr/local/lib/` method for service installations.

### Windows

After extracting the archive, keep `onnxruntime.dll` in the **same directory** as `birdnet-go.exe`. Windows loads DLLs from the application directory automatically, so no additional installation is needed.

```
C:\BirdNET-Go\
  birdnet-go.exe
  onnxruntime.dll
```

If you want to run `birdnet-go.exe` from any directory, copy the DLL to a location in your system PATH:

```powershell
copy onnxruntime.dll C:\Windows\System32\
```

This requires Administrator privileges.

## Manual Download

If you are building from source or need to install ONNX Runtime separately, download it from the official Microsoft releases.

### Linux (x86_64 / amd64)

```bash
VERSION=1.25.1

curl -fsSL "https://github.com/microsoft/onnxruntime/releases/download/v${VERSION}/onnxruntime-linux-x64-${VERSION}.tgz" \
  -o onnxruntime.tgz

tar xzf onnxruntime.tgz --strip-components=1
sudo cp lib/libonnxruntime*.so* /usr/lib/
sudo ldconfig
rm -rf onnxruntime.tgz include lib
```

### Linux (aarch64 / arm64)

```bash
VERSION=1.25.1

curl -fsSL "https://github.com/microsoft/onnxruntime/releases/download/v${VERSION}/onnxruntime-linux-aarch64-${VERSION}.tgz" \
  -o onnxruntime.tgz

tar xzf onnxruntime.tgz --strip-components=1
sudo cp lib/libonnxruntime*.so* /usr/lib/
sudo ldconfig
rm -rf onnxruntime.tgz include lib
```

This covers Raspberry Pi 4/5 (64-bit OS), NVIDIA Jetson, and other ARM64 single-board computers.

### macOS (Apple Silicon / arm64)

```bash
VERSION=1.25.1

curl -fsSL "https://github.com/microsoft/onnxruntime/releases/download/v${VERSION}/onnxruntime-osx-arm64-${VERSION}.tgz" \
  -o onnxruntime.tgz

# Extract (BSD tar needs special handling)
mkdir -p onnxruntime-tmp
tar xzf onnxruntime.tgz -C onnxruntime-tmp
cp onnxruntime-tmp/onnxruntime-*/lib/libonnxruntime*.dylib /usr/local/lib/
rm -rf onnxruntime.tgz onnxruntime-tmp
```

For Intel Macs (x86_64), replace `arm64` with `x86_64` in the download URL:

```bash
VERSION=1.25.1

curl -fsSL "https://github.com/microsoft/onnxruntime/releases/download/v${VERSION}/onnxruntime-osx-x86_64-${VERSION}.tgz" \
  -o onnxruntime.tgz
```

Note: BirdNET-Go does not currently publish pre-built binaries for Intel Macs. You would need to build from source.

### Windows (x86_64 / amd64)

Download from a browser or PowerShell:

```powershell
$VERSION = "1.25.1"

Invoke-WebRequest `
  -Uri "https://github.com/microsoft/onnxruntime/releases/download/v$VERSION/onnxruntime-win-x64-$VERSION.zip" `
  -OutFile onnxruntime.zip

Expand-Archive onnxruntime.zip -DestinationPath onnxruntime-tmp
Copy-Item onnxruntime-tmp\onnxruntime-*\lib\onnxruntime.dll -Destination .
Remove-Item -Recurse onnxruntime.zip, onnxruntime-tmp
```

Place `onnxruntime.dll` in the same directory as `birdnet-go.exe`.

### Using the Taskfile (Build from Source)

If you have the BirdNET-Go source tree and [Task](https://taskfile.dev) installed:

```bash
task download-onnxruntime
```

This automatically detects your platform, downloads the correct archive, and installs the library to the appropriate system path. It requires `sudo` on Linux.

## Platform Support Matrix

| Platform | Architecture | Release Tarball | Manual Download | Docker |
|----------|-------------|:-:|:-:|:-:|
| Linux | x86_64 (amd64) | Yes | Yes | Yes |
| Linux | aarch64 (arm64) | Yes | Yes | Yes |
| macOS | Apple Silicon (arm64) | Yes | Yes | - |
| macOS | Intel (x86_64) | - | Yes | - |
| Windows | x86_64 (amd64) | Yes | Yes | - |

Platforms marked with "-" are not officially supported but may work with manual setup.

## Verification

### Linux

```bash
# Check the library is in the linker cache
ldconfig -p | grep onnxruntime

# Check the library version
readelf -d /usr/lib/libonnxruntime.so | grep SONAME
```

### macOS

```bash
# Check the library exists
ls -la /usr/local/lib/libonnxruntime*.dylib

# Check the library can be loaded
otool -L /usr/local/lib/libonnxruntime.dylib
```

### Windows

```powershell
# Check the DLL exists alongside the binary
dir C:\BirdNET-Go\onnxruntime.dll

# Or check system-wide
where.exe onnxruntime.dll
```

### BirdNET-Go Startup Check

When BirdNET-Go starts, it logs the ONNX Runtime version it loaded. Look for a line like:

```
ONNX Runtime initialized (version 1.25.1)
```

If the library is missing, you will see an error like:

```
error while loading shared libraries: libonnxruntime.so: cannot open shared object file
```

## Troubleshooting

### "error while loading shared libraries: libonnxruntime.so"

**Linux:** The library is not in the dynamic linker search path.

```bash
# Option 1: Install system-wide
sudo cp libonnxruntime.so /usr/lib/
sudo ldconfig

# Option 2: Set library path for current session
export LD_LIBRARY_PATH="/path/to/library:$LD_LIBRARY_PATH"
```

### "dyld: Library not loaded: libonnxruntime.dylib"

**macOS:** The library is not in `/usr/local/lib/` or not in `DYLD_LIBRARY_PATH`.

```bash
sudo cp libonnxruntime.dylib /usr/local/lib/
```

If you get a Gatekeeper block, remove the quarantine attribute:

```bash
xattr -d com.apple.quarantine /usr/local/lib/libonnxruntime.dylib
```

### "onnxruntime.dll was not found"

**Windows:** The DLL is not in the same directory as `birdnet-go.exe` and not in the system PATH.

Move `onnxruntime.dll` to the same folder as the executable.

### Wrong ONNX Runtime version

If BirdNET-Go reports version incompatibility errors, download the exact version listed at the top of this guide (currently 1.25.1). Newer major versions may introduce breaking API changes.

### Permission denied errors

**Linux:** Ensure the library file is readable:

```bash
sudo chmod 644 /usr/lib/libonnxruntime.so
sudo ldconfig
```

**macOS:** Same approach:

```bash
sudo chmod 644 /usr/local/lib/libonnxruntime.dylib
```

### Raspberry Pi / ARM SBC

Use the Linux aarch64 package. Make sure you are running a **64-bit OS**. The 32-bit (armhf) architecture is not supported by ONNX Runtime.

Check your architecture with:

```bash
uname -m
# Should show: aarch64
```

If it shows `armv7l`, you are running a 32-bit OS and need to switch to a 64-bit image (e.g., Raspberry Pi OS 64-bit).

## Further Reading

- [ONNX Runtime GitHub](https://github.com/microsoft/onnxruntime) - official releases and documentation
- [BirdNET-Go documentation](https://birdnet-go.dev)
- [BirdNET-Go GitHub](https://github.com/tphakala/birdnet-go) - issues and discussions
