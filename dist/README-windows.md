# BirdNET-Go - Windows Installation

## Tarball Contents

- `birdnet-go.exe` - application binary
- `onnxruntime.dll` - ONNX Runtime library (required for Perch v2, BattyBirdNET, and geomodel features)
- `tensorflowlite_c.dll` - TensorFlow Lite C library (required for BirdNET v2.4)

## Quick Install

1. Extract the tarball to a folder of your choice (e.g., `C:\BirdNET-Go\`)
2. Keep the `.dll` files in the **same folder** as `birdnet-go.exe`
3. Double-click `birdnet-go.exe` or run it from a terminal

That's it. Windows loads DLLs from the application directory automatically.

## Step-by-Step Guide

### 1. Extract the archive

Use Windows Explorer (right-click, Extract All) or a tool like 7-Zip to
extract the `.tar.gz` archive.

### 2. Keep DLLs alongside the binary

The `.dll` files **must** remain in the same directory as `birdnet-go.exe`.
Windows searches the application directory first when loading DLLs, so no
system-wide installation is needed.

```
C:\BirdNET-Go\
  birdnet-go.exe
  onnxruntime.dll
  tensorflowlite_c.dll
```

### 3. First run

Open a terminal (Command Prompt or PowerShell) in the extraction folder:

```powershell
cd C:\BirdNET-Go
.\birdnet-go.exe
```

On first launch BirdNET-Go will:

- Create a default `config.yaml` in the current directory
- Download the BirdNET v2.4 model (~45 MB)
- Start the web interface on port 8080

Open `http://localhost:8080` in your browser to access the dashboard.

### 4. Windows Firewall

On first run, Windows may show a firewall prompt. Allow access on private
networks so you can reach the dashboard from other devices on your LAN.

## Alternative: System-Wide DLL Installation

If you want to run `birdnet-go.exe` from any directory without carrying the
DLLs alongside it, copy them to a directory in your system PATH:

```powershell
copy onnxruntime.dll C:\Windows\System32\
copy tensorflowlite_c.dll C:\Windows\System32\
```

This requires Administrator privileges.

## Running as a Windows Service

Use a tool like [NSSM](https://nssm.cc/) to install BirdNET-Go as a service:

```powershell
nssm install BirdNET-Go C:\BirdNET-Go\birdnet-go.exe
nssm set BirdNET-Go AppDirectory C:\BirdNET-Go
nssm start BirdNET-Go
```

## Troubleshooting

**"The code execution cannot proceed because onnxruntime.dll was not found"**

The DLL is not in the same directory as the executable, or not in the system
PATH. Move the DLL files to the same folder as `birdnet-go.exe`.

**"The code execution cannot proceed because tensorflowlite_c.dll was not found"**

Same fix: ensure the DLL is in the same folder as the executable.

**SmartScreen warning on first run**

Windows may show "Windows protected your PC" because the binary is not
code-signed. Click "More info" then "Run anyway".

## More Information

- Documentation: https://birdnet-go.dev
- GitHub: https://github.com/tphakala/birdnet-go
- Discussions: https://github.com/tphakala/birdnet-go/discussions
