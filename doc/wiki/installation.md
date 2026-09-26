# Installation

This document provides instructions for installing BirdNET-Go.

There are four main ways to install BirdNET-Go:

1.  **Using `install.sh` (Recommended for Linux):** This script automates the setup of BirdNET-Go within a Docker container, including dependencies, configuration prompts, performance optimization, and systemd service creation. This is the easiest and recommended method for supported Linux distributions (Debian 11+, Ubuntu 20.04+, Raspberry Pi OS Bullseye+).
2.  **Using Docker Compose (Linux only):** Set up BirdNET-Go using Docker Compose for a more flexible containerized approach. This offers better configurability and easier management than manual Docker installation. See the [Docker Compose Guide](docker_compose_guide.md) for detailed instructions.
3.  **Manual Docker Installation (Advanced, Linux only):** Manually run the BirdNET-Go Docker container. This offers more control but requires managing the container lifecycle yourself.
4.  **Manual Binary Installation (All platforms):** Download pre-compiled binaries. This is currently the only supported method for Windows and macOS users. This approach avoids Docker; the release archive bundles the machine learning libraries (TensorFlow Lite C and ONNX Runtime), and you only install FFmpeg and SoX separately if you need the features that use them (see [Manual Binary Installation](#manual-binary-installation-all-platforms)). You manage the application process yourself.

## Container Registry Options

BirdNET-Go Docker images are available from two registries:

### GitHub Container Registry (Primary)

- **Registry**: `ghcr.io/tphakala/birdnet-go`
- **Advantages**: Tightly integrated with source code, automatic builds
- **Tags**: `:nightly`, `:latest`, `:v1.2.3`

### Docker Hub (Mirror)

- **Registry**: `tphakala/birdnet-go`
- **Advantages**: Familiar to Docker users, no GitHub account needed
- **Tags**: `:nightly`, `:latest`, `:v1.2.3`

Both registries contain identical images. You can use either registry interchangeably in all examples below.

## Recommended Method: `install.sh` (Linux)

This script streamlines the installation process on compatible Linux systems (Debian 11+, Ubuntu 20.04+, Raspberry Pi OS 64-bit Bullseye or newer).

**What the script does:**

- Checks system prerequisites (OS version, 64-bit architecture, Docker installation, user groups).
- Installs Docker and necessary dependencies (`alsa-utils`, `curl`, `jq`, etc.) if they are missing.
- Pulls the latest `nightly` BirdNET-Go Docker image (defaults to `ghcr.io/tphakala/birdnet-go:nightly`).
- Creates necessary directories (`~/birdnet-go-app/config` and `~/birdnet-go-app/data`) for persistent configuration and data storage.
- Downloads a base `config.yaml` file.
- Guides you through initial configuration (web port, audio input source, audio export format, locale, location, optional password protection).
- Optimizes performance settings (like `birdnet.overlap` for [Deep Detection](guide.md#deep-detection)) based on detected hardware (e.g., Raspberry Pi model).
- Creates and enables a systemd service (`birdnet-go.service`) for automatic startup and management.

**How to run:**

1.  Open a terminal on your Linux machine.
2.  Download and execute the script:

    ```bash
    curl -fsSL https://github.com/tphakala/birdnet-go/raw/main/install.sh -o install.sh
    bash ./install.sh
    ```

3.  Follow the on-screen prompts to configure your installation. The script will ask for `sudo` permissions when needed to install packages or manage services.
4.  If the script adds your user to the `docker` or `audio` groups, you may need to **log out and log back in**, then re-run `bash ./install.sh` to continue.

**After Installation:**

- BirdNET-Go will be running as a systemd service.
- Configuration is stored in `~/birdnet-go-app/config/config.yaml`.
- Data (database, clips) is stored in `~/birdnet-go-app/data`.
- You can access the web interface via `http://<your-ip-address>:<port>` (the script will display the correct URL, typically using port 8080 unless changed during setup).
- Manage the service using standard systemd commands:
  - Check status: `sudo systemctl status birdnet-go.service`
  - Stop service: `sudo systemctl stop birdnet-go.service`
  - Start service: `sudo systemctl start birdnet-go.service`
  - Restart service: `sudo systemctl restart birdnet-go.service`
  - View logs: `journalctl -u birdnet-go.service -f`

_(See [Systemd Service Details](#systemd-service-details-installsh-method) below for more information on the service configuration)_.

**Updating an `install.sh` Installation:**

If you installed BirdNET-Go using the `install.sh` script, updating is straightforward:

1.  It is **recommended to download a fresh copy** of the script each time, as it may contain improvements:
    ```bash
    curl -fsSL https://github.com/tphakala/birdnet-go/raw/main/install.sh -o install.sh
    ```
2.  Run the downloaded script:
    ```bash
    bash ./install.sh
    ```
3.  The script will detect your existing installation and offer an option to "Check for updates".
4.  Selecting this option will:
    - Stop the running BirdNET-Go service and container.
    - Pull the latest `nightly` Docker image.
    - Update the systemd service file if necessary.
    - Restart the BirdNET-Go service with the new image.
    - Your existing configuration and data in `~/birdnet-go-app/` will be preserved.

## Troubleshooting `install.sh`

The installer runs a series of preflight checks and stops with a clear message if one fails. The most common failures, and how to fix them, are below. Every step is also written to a timestamped install log at `~/birdnet-go-app/data/logs/install-<timestamp>.log`, so open the newest file there if you need more detail.

### `This script requires systemd as the init system`

`install.sh` installs BirdNET-Go as a systemd service and starts the Docker service through systemd, so systemd must be the init system (PID 1). You will hit this on minimal container images, some VPS templates, and **WSL running the default init**.

On WSL, enable systemd once and restart the distribution:

1.  Edit `/etc/wsl.conf` inside your WSL distribution (create it if it does not exist), for example with `sudo nano /etc/wsl.conf`. Make sure it contains:

    ```ini
    [boot]
    systemd=true
    ```

    If the file already has content, do not overwrite it: keep the existing sections and just add the `[boot]` section, or add `systemd=true` under an existing `[boot]` section.

2.  From a **Windows** PowerShell or Command Prompt, fully restart WSL:

    ```powershell
    wsl --shutdown
    ```

3.  Reopen your distribution and re-run `bash ./install.sh`. Confirm it worked with `ps -p 1 -o comm=`, which should now print `systemd`.

Enabling systemd in WSL needs a reasonably recent WSL release (0.67.6 or newer, which ships with current Windows 10 and 11). If you would rather not deal with systemd at all, BirdNET-Go also runs **natively on Windows** with no Docker and no WSL; see [Manual Binary Installation](#manual-binary-installation-all-platforms).

### `Docker cannot be accessed by user ...`

This means the installer's `docker info` check failed. There are three common causes; work through them in order:

```bash
# 1. Is your user in the docker group?
groups "$USER"

# 2. Is the Docker service running?
sudo systemctl status docker --no-pager

# 3. Can Docker be reached at all?
docker info
```

- **Your user is not in the `docker` group.** The installer adds it for you with `sudo usermod -aG docker "$USER"`, but group membership only takes effect in a **new login session**. Log out and back in, then re-run the installer. On WSL, closing the terminal is not enough; run `wsl --shutdown` from Windows and reopen the distribution. To apply the group in your current shell without logging out, run `newgrp docker`.
- **The Docker service is not running.** Start it and enable it on boot:

  ```bash
  sudo systemctl enable --now docker
  ```

  If `/var/run/docker.sock` does not exist, the daemon is not running; start the service and retry `docker info`.

- **The socket is not accessible.** Check that the daemon is up and the socket has group access with `ls -l /var/run/docker.sock` (it should be owned by `root:docker`).

On WSL specifically, use the Docker packages the installer sets up (or `sudo apt install docker.io`) together with systemd, as described above. Docker Desktop's WSL integration gives you a working `docker` command but does not run as a systemd service, so `install.sh` cannot manage it.

### The installer says to log out and log back in

When the script installs Docker or adds you to the `docker`/`audio` groups, it exits and asks you to log out and back in before re-running it. This is expected: Linux only applies new group membership to new login sessions. Start a fresh session (log out and in, or on WSL run `wsl --shutdown` from Windows and reopen the distribution), then run `bash ./install.sh` again to continue.

For runtime problems after a successful install (no sound, web UI unreachable, container restarts), see [Docker Installation Troubleshooting](guide.md#docker-installation-troubleshooting) and the [FAQ](faq.md).

## Using Docker Compose (Linux only)

For a more flexible containerized approach than the manual Docker installation, you can use Docker Compose which offers better configurability and easier management.

A [premade docker-compose.yml](https://github.com/tphakala/birdnet-go/blob/main/Docker/docker-compose.yml) file is available in the repository. This file includes:

- The BirdNET-Go container configuration with the latest nightly image
- Environment variables for customization (timezone, user permissions, etc.)
- Volume mounts for persistent configuration and data storage
- RAM disk (tmpfs) for HLS streaming segments to improve performance
- Device mounts for sound card access
- An optional Cloudflared service (commented out) for secure internet access

Please refer to the [Docker Compose Guide](docker_compose_guide.md) for detailed instructions on setting up BirdNET-Go with Docker Compose.

## Manual Docker Installation (Advanced, Linux only)

This method requires Docker to be installed on your system. See the [official Docker installation guide](https://docs.docker.com/engine/install/).

```bash
docker run -ti --rm \\
  --name birdnet-go \\
  -p <host_port>:8080 \\
  --env TZ="<TZ identifier>" \\
  --env BIRDNET_UID=$(id -u) \\
  --env BIRDNET_GID=$(id -g) \\
  --device /dev/snd \\
  --add-host="host.docker.internal:host-gateway" \\
  -v </path/on/host/to/config>:/config \\
  -v </path/on/host/to/data>:/data \\
  ghcr.io/tphakala/birdnet-go:nightly
```

**Alternative using Docker Hub:**

```bash
docker run -ti --rm \\
  --name birdnet-go \\
  -p <host_port>:8080 \\
  --env TZ="<TZ identifier>" \\
  --env BIRDNET_UID=$(id -u) \\
  --env BIRDNET_GID=$(id -g) \\
  --device /dev/snd \\
  --add-host="host.docker.internal:host-gateway" \\
  -v </path/on/host/to/config>:/config \\
  -v </path/on/host/to/data>:/data \\
  tphakala/birdnet-go:nightly
```

**Parameters:**

| Parameter                                                              | Function                                                                                                                                                                                       | Example Value                |
| :--------------------------------------------------------------------- | :--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | :--------------------------- |
| `-p <host_port>:8080`                                                  | Maps a port on your host machine to the container's web server port (8080).                                                                                                                    | `-p 8080:8080`               |
| `--env TZ="<TZ identifier>"`                                           | Sets the timezone inside the container. See [Wikipedia list](https://en.wikipedia.org/wiki/List_of_tz_database_time_zones#List).                                                               | `TZ="Europe/Berlin"`         |
| `--env BIRDNET_UID=$(id -u)`                                           | Runs the container process with your host user's ID for correct file permissions.                                                                                                              | _Keep as is_                 |
| `--env BIRDNET_GID=$(id -g)`                                           | Runs the container process with your host user's group ID.                                                                                                                                     | _Keep as is_                 |
| `--device /dev/snd`                                                    | Mounts host audio devices into the container. Required for sound card input.                                                                                                                   | _Keep as is_                 |
| `--add-host="host.docker.internal:host-gateway"`                       | Allows the container to potentially reach services running on the host machine itself.                                                                                                         | _Keep as is_                 |
| `-v </path/on/host/to/config>:/config`                                 | Mounts a directory from your host for persistent configuration. BirdNET-Go will read/write `config.yaml` here.                                                                                 | `-v $HOME/bn-config:/config` |
| `-v </path/on/host/to/data>:/data`                                     | Mounts a directory from your host for persistent data (database, audio clips, logs).                                                                                                           | `-v $HOME/bn-data:/data`     |
| `ghcr.io/tphakala/birdnet-go:nightly` or `tphakala/birdnet-go:nightly` | The BirdNET-Go Docker image to use. Available from GitHub Container Registry or Docker Hub. `:nightly` is recommended for latest features. `:latest` points to the most recent stable release. |                              |

**Notes:**

- You need to create the host directories (`</path/on/host/to/config>`, `</path/on/host/to/data>`) before running the command.
- Ensure the user running the command has the correct permissions to access Docker and the specified host directories.
- You will need to manually create/edit the `config.yaml` file in your mapped config directory. Refer to the [Configuration](guide.md#configuration) section in the Wiki.
- You are responsible for managing the container's lifecycle (starting, stopping, updating).

## Manual Binary Installation (All platforms)

This method does not use Docker. The release archive bundles the machine learning libraries, so a minimal run needs no extra downloads; you only install the optional tools (FFmpeg, SoX, `libasound2`) for the features that use them, and you manage the process yourself.

1.  **Download Binary:** Go to the [BirdNET-Go Releases page](https://github.com/tphakala/birdnet-go/releases) and download the pre-compiled archive for your operating system (Linux, macOS, Windows) and architecture.
2.  **Machine learning libraries are bundled:** The release archive already includes the TensorFlow Lite C library and the ONNX Runtime library next to the executable (`tensorflowlite_c.dll` and `onnxruntime.dll` on Windows, `libtensorflowlite_c.so`/`libonnxruntime.so` on Linux, `libtensorflowlite_c.dylib`/`libonnxruntime.dylib` on macOS). Each archive contains a `README.md` with the exact per-platform steps; in short:
    - **Windows:** Keep the `.dll` files in the same folder as `birdnet-go.exe`. Windows loads them from the application directory automatically, so no further setup is needed.
    - **Linux:** Copy the `.so` files to a directory on the library search path and refresh the linker cache (`sudo cp libtensorflowlite_c.so libonnxruntime.so /usr/local/lib/ && sudo ldconfig`), or, without root, run the binary from the extracted folder with `LD_LIBRARY_PATH="$(pwd):$LD_LIBRARY_PATH"`.
    - **macOS:** Copy the `.dylib` files to `/usr/local/lib/` (the dynamic linker searches it by default), then clear the Gatekeeper quarantine attribute from the binary and both libraries, naming each path explicitly:

      ```bash
      xattr -d com.apple.quarantine birdnet-go
      xattr -d com.apple.quarantine /usr/local/lib/libtensorflowlite_c.dylib
      xattr -d com.apple.quarantine /usr/local/lib/libonnxruntime.dylib
      ```

    You only need to download the library separately (from [tphakala/tflite_c Releases](https://github.com/tphakala/tflite_c/releases), `v2.17.1` or newer for XNNPACK support) if you build BirdNET-Go from source rather than using a release archive. See the [ONNX Runtime Installation Guide](onnx-runtime-installation.md) if you need to install ONNX Runtime manually.

3.  **Install Optional Dependencies:** These external tools are not bundled. Install the ones you need for the features you use:
    - **FFmpeg:** Required for RTSP stream capture and on-demand clip transcoding in the web interface. WAV, FLAC and Opus are encoded natively and do not need FFmpeg. AAC and MP3 export use FFmpeg by default, but each has a native encoder available as an opt-in preview via the `BIRDNET_AAC_ENCODER=native` and `BIRDNET_MP3_ENCODER=native` environment variables. The [Live Audio Streaming](guide.md#live-audio-streaming) feature is now encoded natively and no longer uses FFmpeg at all. Loudness normalization of saved clips is done natively for every format and no longer needs FFmpeg. Install using your system's package manager (e.g., `sudo apt install ffmpeg` on Debian/Ubuntu, `brew install ffmpeg` on macOS, or download a build from [ffmpeg.org](https://ffmpeg.org/download.html) on Windows).
    - **SoX:** Required for rendering spectrograms in the web interface. Install using your system's package manager (e.g., `sudo apt install sox` on Debian/Ubuntu, `brew install sox` on macOS, or download from the [SoX project](https://sourceforge.net/projects/sox/) on Windows).
    - **libasound2 (Linux only):** Required for microphone audio capture. Install with `sudo apt install libasound2-dev` on Debian/Ubuntu.
4.  **Place Executable:** Put the `birdnet-go` binary wherever you want to run it. On **Windows**, keep the bundled `.dll` files in that same folder (Windows loads them from the application directory). On **Linux** and **macOS**, placing the libraries next to the binary is not enough on its own, because the dynamic linker does not search the application directory by default; make them findable using the system-path or `LD_LIBRARY_PATH` approach from step 2.
5.  **Run BirdNET-Go:** Open a terminal or command prompt, navigate to the directory containing the `birdnet-go` executable, and run it (e.g., `./birdnet-go` on Linux/macOS, or double-click `birdnet-go.exe` on Windows).
6.  **Configuration:** On the first run, BirdNET-Go will create a default `config.yaml` file. Edit this file according to your needs. See the [Configuration](guide.md#configuration) section in the Wiki for details and default file locations per OS.
7.  **Process Management:** You are responsible for managing the BirdNET-Go process (running it in the background, ensuring it restarts on boot, etc.) using tools like `systemd`, `supervisor`, `screen`, or Task Scheduler (Windows).

## Systemd Service Details (`install.sh` Method)

The `install.sh` script creates a systemd unit file at `/etc/systemd/system/birdnet-go.service`. Here is a template of the generated file:

```ini
[Unit]
Description=BirdNET-Go
After=docker.service
Requires=docker.service

[Service]
Restart=always
ExecStart=/usr/bin/docker run --rm \\
    --name birdnet-go \\
    -p <web_port>:8080 \\              # Port mapping (e.g., 8080:8080)
    --env TZ="<Timezone>" \\             # System timezone (e.g., "Europe/Berlin")
    --env BIRDNET_UID=<Host_User_ID> \\  # Your user ID
    --env BIRDNET_GID=<Host_Group_ID> \\ # Your group ID
    --add-host="host.docker.internal:host-gateway" \\
    --device /dev/snd \\                # Mounts audio devices
    -v <config_dir_path>:/config \\     # Maps host config dir (~/birdnet-go-app/config)
    -v <data_dir_path>:/data \\         # Maps host data dir (~/birdnet-go-app/data)
    ghcr.io/tphakala/birdnet-go:nightly # Docker image (or tphakala/birdnet-go:nightly from Docker Hub)

[Install]
WantedBy=multi-user.target
```

**Key Parts Explained:**

- `Restart=always`: Ensures the service restarts automatically if it stops unexpectedly.
- `ExecStart`: The command used to start the Docker container.
  - `--rm`: Automatically removes the container when it stops.
  - `--name birdnet-go`: Assigns a name to the container.
  - `-p <web_port>:8080`: Maps the host port chosen during installation to the container's port 8080.
  - `--env TZ`: Sets the container's timezone to match the host's.
  - `--env BIRDNET_UID/GID`: Ensures files created by the container (in mapped volumes) have the correct host user/group ownership.
  - `--add-host`: Allows the container to connect back to services potentially running on the host.
  - `--device /dev/snd`: Makes host sound devices available inside the container.
  - `-v ...:/config`, `-v ...:/data`: Mount the host directories for persistent configuration and data.
  - `ghcr.io/tphakala/birdnet-go:nightly`: Specifies the Docker image to run. Alternative: `tphakala/birdnet-go:nightly` from Docker Hub.
- `WantedBy=multi-user.target`: Ensures the service starts during the normal boot process.
