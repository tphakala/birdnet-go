# Building

This document describes methods for building BirdNET-Go from source.

There are two primary approaches:

1.  **Using the Devcontainer (Recommended for Development):** Provides a consistent, pre-configured Docker environment with all dependencies installed. Ideal for contributing or making code changes.
2.  **Building Locally (Using Task):** Uses the `task` build tool (`Taskfile.yml`) directly on your host machine. Requires manual installation of some prerequisites but is the standard way to build binaries.

## Devcontainer (Recommended for Development)

For development work within VSCode, using the provided [devcontainer](https://code.visualstudio.com/docs/remote/containers) is highly recommended.

1.  Open the project directory in VSCode.
2.  Ensure you have Docker installed and the [Remote Development](https://marketplace.visualstudio.com/items?itemName=ms-vscode-remote.vscode-remote-extensionpack) extension pack.
3.  Press `F1` or `Shift-Command-P` (Mac) / `Ctrl+Shift+P` (Windows/Linux), type `Reopen in Container`, and select it.
4.  VSCode will build the container defined in [`.devcontainer/devcontainer.json`](.devcontainer/devcontainer.json) and reopen the project inside it.

**Inside the Devcontainer:**

*   All Go, CGO, TensorFlow, FFmpeg, and SoX dependencies are pre-installed.
*   Your source code is mounted into the container.
*   An `air` development server ([`.air.toml`](.air.toml)) is automatically started. It watches for code changes (`.go`, `.html`, `.css`, `.js`, etc.) and triggers:
    *   Tailwind CSS recompilation.
    *   Go application rebuild.
    *   Live reload of the running application.
*   You can access the development web server at [http://localhost:8080](http://localhost:8080).

**Devcontainer Notes:**

*   **Dependency Changes:** If you modify `.devcontainer/devcontainer.json`, use the "Rebuild Container" command in VSCode.
*   **Docker Engine:** Ensure your host has a full Docker engine installation (not just `docker.io`), as `buildx` support is needed for potential cross-compilation features within the container.

## Building Locally (Using Task)

This project uses [Task](https://taskfile.dev/) (`Taskfile.yml`) as its build system. It simplifies building, testing, and managing dependencies.

### 1. Install Prerequisites

*   **Go:** Install the Go programming language (version 1.21 or later recommended). Follow the [official Go installation guide](https://golang.org/doc/install).
*   **Task:** Install the `task` executable. Follow the [Task installation guide](https://taskfile.dev/installation/).
*   **Git:** Needed to clone repositories (BirdNET-Go and TensorFlow headers).
*   **wget/curl:** Needed by `task` to download TFLite libraries.
*   **Standard Build Tools:** Ensure you have `gcc`, `make`, etc. (`build-essential` on Debian/Ubuntu).
*   **(Optional) `air`:** For live-reloading development server locally (if not using the Devcontainer). Install via `go install github.com/cosmtrek/air@latest`.
*   **(Optional) Node.js/npm:** Required by `task generate-tailwindcss` and `air` for Tailwind CSS generation.
*   **(Optional) MinGW (for Windows Cross-Compilation):** If building the Windows binary *on Linux*, install `mingw-w64`. On Debian/Ubuntu:
    ```bash
    sudo apt update
    sudo apt install mingw-w64-tools gcc-mingw-w64-x86-64
    ```

### 2. Prepare Dependencies (Handled mostly by Task)

*   **TensorFlow Lite C Library:** `task` will automatically download the correct pre-compiled library (from [tphakala/tflite_c](https://github.com/tphakala/tflite_c/releases/tag/{{TFLITE_VERSION}})) for your target OS/architecture when you run a build task. It places the library in the expected system path (e.g., `/usr/lib`, `/usr/local/lib`, `/opt/homebrew/lib`, or `/usr/x86_64-w64-mingw32/lib` for Windows cross-compile) and creates necessary symlinks.
*   **TensorFlow Headers:** `task` checks if TensorFlow source code (needed for C header files for CGO) is present in `$HOME/src/tensorflow`. If not, it clones the specific tag (`{{TFLITE_VERSION}}`) required.

### 3. Build BirdNET-Go

Navigate to the cloned BirdNET-Go project directory in your terminal.

*   **Build for your native OS/Architecture:**
    ```bash
    task build # Or simply 'task'
    ```
    This automatically detects your OS and architecture and runs the appropriate build task (e.g., `task linux_amd64`). The binary will be placed in the `bin/` directory (e.g., `bin/birdnet-go`).

*   **Cross-Compile for a specific target:**
    ```bash
    # Example: Build for Windows AMD64 (requires MinGW on Linux host)
    task windows_amd64

    # Example: Build for Linux ARM64 (might require cross-compiler tools if not on ARM64 host)
    task linux_arm64

    # Example: Build for macOS ARM64 (Apple Silicon)
    task darwin_arm64
    ```
    See `Taskfile.yml` for all available targets (`linux_amd64`, `linux_arm64`, `windows_amd64`, `darwin_amd64`, `darwin_arm64`). Binaries appear in `bin/` (e.g., `bin/birdnet-go.exe`).

*   **Clean Build Artifacts:**
    ```bash
    task clean
    ```

### 4. Run Tests

*   **Run all tests:**
    ```bash
    task test
    ```
*   **Run tests with verbose output:**
    ```bash
    task test-verbose
    ```
*   **Run tests with coverage report:**
    ```bash
    task test-coverage
    ```
    Coverage report will be generated in `coverage/coverage.html`.

### 5. Development with Live Reload (using `air`)

If you prefer local development with automatic rebuilding and restarting on changes (similar to the Devcontainer experience):

1.  Ensure `air` is installed (`go install github.com/cosmtrek/air@latest`).
2.  Ensure Node.js/npm is installed for Tailwind CSS generation.
3.  Prepare dependencies (TensorFlow headers/library) by running a build task once (e.g., `task build`).
4.  Start the `air` server:
    ```bash
    air
    ```
    `air` will use the configuration in [`.air.toml`](.air.toml) to watch files, recompile Tailwind CSS, rebuild the Go binary, and restart the server on changes.
