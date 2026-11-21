# ONNX Runtime Installation Guide

ONNX Runtime is a shared library required by BirdNET-Go for neural network inference. It powers the BirdNET v2.4 classifier, Perch v2 embeddings, BattyBirdNET bat detection, range filtering, and BirdNET Geomodel features.

> **Note:** ONNX Runtime will become the sole inference backend in a future release. TensorFlow Lite support is being phased out. If you are setting up a new installation, only ONNX Runtime is needed going forward.

## Do You Need This Guide?

**Docker / container installs (`install.sh`):** ONNX Runtime is baked into the container image. No action needed.

**Release tarballs (`.tar.gz` from GitHub Releases):** The ONNX Runtime library is bundled in every tarball. You just need to install it to the right location on your system. See the [Installing from Release Tarballs](#installing-from-release-tarballs) section.

**Building from source:** You need to download ONNX Runtime yourself. See [Manual Download](#manual-download).

## Required Version

BirdNET-Go requires **ONNX Runtime 1.31 or newer**; release builds and the Docker image ship 1.32.0. The Go bindings request ONNX Runtime C API version 31, which 1.31 and later provide; older versions fail to load. Releases up to and including 20261014 need ONNX Runtime 1.25.x.

## Installing from Release Tarballs

Every release tarball includes the ONNX Runtime library for the target platform. After extracting the archive, install the library to a system path so the dynamic linker can find it.

### Linux

```bash
# Extract the tarball
tar xzf birdnet-go-linux-amd64-*.tar.gz

# Install the library system-wide
# Remove the old link first, so the copy does not write into an older versioned file
sudo rm -f /usr/lib/libonnxruntime.so && sudo cp libonnxruntime.so /usr/lib/
sudo ldconfig

# Verify
ldconfig -p | grep onnxruntime
```

Expected output:

```text
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

```text
C:\BirdNET-Go\
  birdnet-go.exe
  onnxruntime.dll
```

If you want to run `birdnet-go.exe` from any directory, keep the DLL in a dedicated folder and add that folder to your user `PATH`. Avoid copying the DLL into `C:\Windows\System32`: that is a global, system-wide install that affects every application and is easy to get wrong.

```powershell
# Example: keep onnxruntime.dll in C:\BirdNET-Go and add that folder to PATH
[Environment]::SetEnvironmentVariable("PATH", "$env:PATH;C:\BirdNET-Go", "User")
```

This updates your user `PATH` only and does not require Administrator privileges. Open a new terminal afterwards for the change to take effect.

## Manual Download

If you are building from source or need to install ONNX Runtime separately, download it from the official `Microsoft.ML.OnnxRuntime` NuGet package. It is one zip archive that holds the libraries for every platform. Each snippet below checks the package against its SHA-256 before using it.

### Linux (x86_64 / amd64)

```bash
VERSION=1.32.0

curl -fsSL "https://api.nuget.org/v3-flatcontainer/microsoft.ml.onnxruntime/${VERSION}/microsoft.ml.onnxruntime.${VERSION}.nupkg" \
  -o onnxruntime.nupkg
echo "a9150bc2992cbbb6722070da9910e1ffa989a8bfc67bdd5bb8e745ca755641db  onnxruntime.nupkg" | sha256sum -c - && {
  unzip -p onnxruntime.nupkg runtimes/linux-x64/native/libonnxruntime.so > libonnxruntime.so &&
  unzip -p onnxruntime.nupkg runtimes/linux-x64/native/libonnxruntime_providers_shared.so > libonnxruntime_providers_shared.so &&
  sudo install -m 755 libonnxruntime.so "/usr/lib/libonnxruntime.so.${VERSION}" &&
  sudo install -m 755 libonnxruntime_providers_shared.so /usr/lib/ &&
  sudo ln -sf "libonnxruntime.so.${VERSION}" /usr/lib/libonnxruntime.so.1 &&
  sudo ln -sf libonnxruntime.so.1 /usr/lib/libonnxruntime.so &&
  sudo ldconfig &&
  rm onnxruntime.nupkg libonnxruntime.so libonnxruntime_providers_shared.so
}
```

The main library is installed as `libonnxruntime.so.${VERSION}` because BirdNET-Go reads the installed version from that file name. The two links point at it, so an update never writes through a link into an older version's file.

### Linux (aarch64 / arm64)

```bash
VERSION=1.32.0

curl -fsSL "https://api.nuget.org/v3-flatcontainer/microsoft.ml.onnxruntime/${VERSION}/microsoft.ml.onnxruntime.${VERSION}.nupkg" \
  -o onnxruntime.nupkg
echo "a9150bc2992cbbb6722070da9910e1ffa989a8bfc67bdd5bb8e745ca755641db  onnxruntime.nupkg" | sha256sum -c - && {
  unzip -p onnxruntime.nupkg runtimes/linux-arm64/native/libonnxruntime.so > libonnxruntime.so &&
  unzip -p onnxruntime.nupkg runtimes/linux-arm64/native/libonnxruntime_providers_shared.so > libonnxruntime_providers_shared.so &&
  sudo install -m 755 libonnxruntime.so "/usr/lib/libonnxruntime.so.${VERSION}" &&
  sudo install -m 755 libonnxruntime_providers_shared.so /usr/lib/ &&
  sudo ln -sf "libonnxruntime.so.${VERSION}" /usr/lib/libonnxruntime.so.1 &&
  sudo ln -sf libonnxruntime.so.1 /usr/lib/libonnxruntime.so &&
  sudo ldconfig &&
  rm onnxruntime.nupkg libonnxruntime.so libonnxruntime_providers_shared.so
}
```

This covers Raspberry Pi 4/5 (64-bit OS), NVIDIA Jetson, and other ARM64 single-board computers.

### macOS (Apple Silicon / arm64)

```bash
VERSION=1.32.0

curl -fsSL "https://api.nuget.org/v3-flatcontainer/microsoft.ml.onnxruntime/${VERSION}/microsoft.ml.onnxruntime.${VERSION}.nupkg" \
  -o onnxruntime.nupkg
echo "a9150bc2992cbbb6722070da9910e1ffa989a8bfc67bdd5bb8e745ca755641db  onnxruntime.nupkg" | shasum -a 256 -c - && {
  unzip -p onnxruntime.nupkg runtimes/osx-arm64/native/libonnxruntime.dylib > libonnxruntime.dylib &&
  sudo mkdir -p /usr/local/lib &&
  sudo install -m 755 libonnxruntime.dylib "/usr/local/lib/libonnxruntime.${VERSION}.dylib" &&
  sudo ln -sf "libonnxruntime.${VERSION}.dylib" /usr/local/lib/libonnxruntime.dylib &&
  { sudo xattr -d com.apple.quarantine /usr/local/lib/libonnxruntime*.dylib 2>/dev/null || true; } &&
  rm onnxruntime.nupkg libonnxruntime.dylib
}
```

The NuGet package has no Intel Mac (x86_64) build. BirdNET-Go does not publish pre-built binaries for Intel Macs either.

### Windows (x86_64 / amd64)

Download from PowerShell:

```powershell
$VERSION = "1.32.0"

Invoke-WebRequest `
  -Uri "https://api.nuget.org/v3-flatcontainer/microsoft.ml.onnxruntime/$VERSION/microsoft.ml.onnxruntime.$VERSION.nupkg" `
  -OutFile onnxruntime.nupkg
if ((Get-FileHash onnxruntime.nupkg -Algorithm SHA256).Hash -eq "a9150bc2992cbbb6722070da9910e1ffa989a8bfc67bdd5bb8e745ca755641db") {
  Add-Type -AssemblyName System.IO.Compression.FileSystem
  $zip = [System.IO.Compression.ZipFile]::OpenRead("$PWD\onnxruntime.nupkg")
  try {
    $entry = $zip.GetEntry("runtimes/win-x64/native/onnxruntime.dll")
    [System.IO.Compression.ZipFileExtensions]::ExtractToFile($entry, "$PWD\onnxruntime.dll", $true)
  } catch {
    throw
  } finally {
    $zip.Dispose()
  }
  Remove-Item onnxruntime.nupkg
} else {
  throw "onnxruntime.nupkg does not match the expected SHA-256"
}
```

Place `onnxruntime.dll` in the same directory as `birdnet-go.exe`.

### Using the Taskfile (Build from Source)

If you have the BirdNET-Go source tree and [Task](https://taskfile.dev) installed:

```bash
task download-onnxruntime
```

This automatically detects your platform, downloads the package, checks its SHA-256, and installs the library to the appropriate system path. It requires `sudo` on Linux.

## Platform Support Matrix

| Platform | Architecture          | Release Tarball | Manual Download | Docker |
| -------- | --------------------- | :-------------: | :-------------: | :----: |
| Linux    | x86_64 (amd64)        |       Yes       |       Yes       |  Yes   |
| Linux    | aarch64 (arm64)       |       Yes       |       Yes       |  Yes   |
| macOS    | Apple Silicon (arm64) |       Yes       |       Yes       |   -    |
| macOS    | Intel (x86_64)        |        -        |        -        |   -    |
| Windows  | x86_64 (amd64)        |       Yes       |       Yes       |   -    |

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

```text
INFO  [inference] ONNX Runtime initialized version=1.32.0 library_path=/usr/lib/libonnxruntime.so ort_disable_telemetry=1 telemetry_disabled=true telemetry_api_disabled=true
```

If the library is missing, you will see an error like:

```text
error while loading shared libraries: libonnxruntime.so: cannot open shared object file
```

## Telemetry

Official ONNX Runtime 1.29+ builds include Microsoft telemetry that is on by default. BirdNET-Go sets `ORT_DISABLE_TELEMETRY=1` before it loads ONNX Runtime, unless you have set that variable yourself. It also switches ONNX Runtime telemetry events off through the API after loading, because Windows builds use a backend that ignores the variable.

## Troubleshooting

### "error while loading shared libraries: libonnxruntime.so"

**Linux:** The library is not in the dynamic linker search path.

```bash
# Option 1: Install system-wide
# Remove the old link first, so the copy does not write into an older versioned file
sudo rm -f /usr/lib/libonnxruntime.so && sudo cp libonnxruntime.so /usr/lib/
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

If BirdNET-Go reports version incompatibility errors, install ONNX Runtime 1.31 or newer (releases ship 1.32.0), as described at the top of this guide.

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

- [ONNX Runtime NuGet package](https://www.nuget.org/packages/Microsoft.ML.OnnxRuntime) - official release packages
- [BirdNET-Go documentation](https://github.com/tphakala/birdnet-go/wiki) - guides and reference
- [BirdNET-Go GitHub](https://github.com/tphakala/birdnet-go) - issues and discussions
