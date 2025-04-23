# Installation

This document provides instructions for installing BirdNET-Go.

There are four main ways to install BirdNET-Go:

1.  **Using `install.sh` (Recommended for Linux):** This script automates the setup of BirdNET-Go within a Docker container, including dependencies, configuration prompts, performance optimization, and systemd service creation. This is the easiest and recommended method for supported Linux distributions (Debian 11+, Ubuntu 20.04+, Raspberry Pi OS Bullseye+).
2.  **Using Docker Compose (Linux only):** Set up BirdNET-Go using Docker Compose for a more flexible containerized approach. This offers better configurability and easier management than manual Docker installation. See the [Docker Compose Guide](docker_compose_guide.md) for detailed instructions.
3.  **Manual Docker Installation (Advanced, Linux only):** Manually run the BirdNET-Go Docker container. This offers more control but requires managing the container lifecycle yourself.
4.  **Manual Binary Installation (All platforms):** Download pre-compiled binaries. This is currently the only supported method for Windows and macOS users. This approach avoids Docker but requires manually installing dependencies (TensorFlow Lite C library, FFmpeg, SoX) and managing the application process.

## Recommended Method: `install.sh` (Linux)

This script streamlines the installation process on compatible Linux systems (Debian 11+, Ubuntu 20.04+, Raspberry Pi OS 64-bit Bullseye or newer).

**What the script does:**

*   Checks system prerequisites (OS version, 64-bit architecture, Docker installation, user groups).
*   Installs Docker and necessary dependencies (`alsa-utils`, `curl`, `jq`, etc.) if they are missing.
*   Pulls the latest `nightly` BirdNET-Go Docker image (`ghcr.io/tphakala/birdnet-go:nightly`).
*   Creates necessary directories (`~/birdnet-go-app/config` and `~/birdnet-go-app/data`) for persistent configuration and data storage.
*   Downloads a base `config.yaml` file.
*   Guides you through initial configuration (web port, audio input source, audio export format, locale, location, optional password protection).
*   Optimizes performance settings (like `birdnet.overlap` for [Deep Detection](guide.md#deep-detection)) based on detected hardware (e.g., Raspberry Pi model).
*   Creates and enables a systemd service (`birdnet-go.service`) for automatic startup and management.

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

*   BirdNET-Go will be running as a systemd service.
*   Configuration is stored in `~/birdnet-go-app/config/config.yaml`.
*   Data (database, clips) is stored in `~/birdnet-go-app/data`.
*   You can access the web interface via `http://<your-ip-address>:<port>` (the script will display the correct URL, typically using port 8080 unless changed during setup).
*   Manage the service using standard systemd commands:
    *   Check status: `sudo systemctl status birdnet-go.service`
    *   Stop service: `sudo systemctl stop birdnet-go.service`
    *   Start service: `sudo systemctl start birdnet-go.service`
    *   Restart service: `sudo systemctl restart birdnet-go.service`
    *   View logs: `journalctl -u birdnet-go.service -f`

*(See [Systemd Service Details](#systemd-service-details) below for more information on the service configuration)*.

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
    *   Stop the running BirdNET-Go service and container.
    *   Pull the latest `nightly` Docker image.
    *   Update the systemd service file if necessary.
    *   Restart the BirdNET-Go service with the new image.
    *   Your existing configuration and data in `~/birdnet-go-app/` will be preserved.

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

**Parameters:**

| Parameter                                   | Function                                                                                                                               | Example Value         |
| :------------------------------------------ | :------------------------------------------------------------------------------------------------------------------------------------- | :-------------------- |
| `-p <host_port>:8080`                       | Maps a port on your host machine to the container's web server port (8080).                                                              | `-p 8080:8080`        |
| `--env TZ="<TZ identifier>"`                | Sets the timezone inside the container. See [Wikipedia list](https://en.wikipedia.org/wiki/List_of_tz_database_time_zones#List).         | `TZ="Europe/Berlin"`  |
| `--env BIRDNET_UID=$(id -u)`                | Runs the container process with your host user's ID for correct file permissions.                                                      | *Keep as is*          |
| `--env BIRDNET_GID=$(id -g)`                | Runs the container process with your host user's group ID.                                                                               | *Keep as is*          |
| `--device /dev/snd`                         | Mounts host audio devices into the container. Required for sound card input.                                                           | *Keep as is*          |
| `--add-host="host.docker.internal:host-gateway"` | Allows the container to potentially reach services running on the host machine itself.                                                   | *Keep as is*          |
| `-v </path/on/host/to/config>:/config`      | Mounts a directory from your host for persistent configuration. BirdNET-Go will read/write `config.yaml` here.                           | `-v $HOME/bn-config:/config` |
| `-v </path/on/host/to/data>:/data`          | Mounts a directory from your host for persistent data (database, audio clips, logs).                                                    | `-v $HOME/bn-data:/data`   |
| `ghcr.io/tphakala/birdnet-go:nightly`       | The BirdNET-Go Docker image to use. `:nightly` is recommended for the latest features. `:latest` points to the most recent stable release. |                       |

**Notes:**

*   You need to create the host directories (`</path/on/host/to/config>`, `</path/on/host/to/data>`) before running the command.
*   Ensure the user running the command has the correct permissions to access Docker and the specified host directories.
*   You will need to manually create/edit the `config.yaml` file in your mapped config directory. Refer to the [Configuration](guide.md#configuration) section in the Wiki.
*   You are responsible for managing the container's lifecycle (starting, stopping, updating).

## Manual Binary Installation (All platforms)

This method does not use Docker but requires manual dependency installation.

1.  **Download Binary:** Go to the [BirdNET-Go Releases page](https://github.com/tphakala/birdnet-go/releases) and download the pre-compiled binary suitable for your operating system (Linux, macOS, Windows) and architecture.
2.  **Download TFLite Library:** Download the corresponding TensorFlow Lite C library from [tphakala/tflite\_c Releases](https://github.com/tphakala/tflite_c/releases). Follow the installation instructions there (copying the `.so`, `.dylib`, or `.dll` file to the correct system path or the BirdNET-Go executable directory). Version `v2.17.1` or newer is recommended for best performance (XNNPACK support).
3.  **Install Dependencies:**
    *   **FFmpeg:** Required for RTSP stream capture, audio export to formats other than WAV (MP3, AAC, FLAC, Opus), and the [Live Audio Streaming](guide.md#live-audio-streaming) feature. Install using your system's package manager (e.g., `sudo apt install ffmpeg` on Debian/Ubuntu, `brew install ffmpeg` on macOS).
    *   **SoX:** Required for rendering spectrograms in the web interface. Install using your system's package manager (e.g., `sudo apt install sox` on Debian/Ubuntu, `brew install sox` on macOS).
4.  **Place Executable:** Extract the downloaded BirdNET-Go binary and place it in your desired directory.
5.  **Run BirdNET-Go:** Open a terminal or command prompt, navigate to the directory containing the `birdnet-go` executable, and run it (e.g., `./birdnet-go`).
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
    ghcr.io/tphakala/birdnet-go:nightly # Docker image

[Install]
WantedBy=multi-user.target
```

**Key Parts Explained:**

*   `Restart=always`: Ensures the service restarts automatically if it stops unexpectedly.
*   `ExecStart`: The command used to start the Docker container.
    *   `--rm`: Automatically removes the container when it stops.
    *   `--name birdnet-go`: Assigns a name to the container.
    *   `-p <web_port>:8080`: Maps the host port chosen during installation to the container's port 8080.
    *   `--env TZ`: Sets the container's timezone to match the host's.
    *   `--env BIRDNET_UID/GID`: Ensures files created by the container (in mapped volumes) have the correct host user/group ownership.
    *   `--add-host`: Allows the container to connect back to services potentially running on the host.
    *   `--device /dev/snd`: Makes host sound devices available inside the container.
    *   `-v ...:/config`, `-v ...:/data`: Mount the host directories for persistent configuration and data.
    *   `ghcr.io/tphakala/birdnet-go:nightly`: Specifies the Docker image to run.
*   `WantedBy=multi-user.target`: Ensures the service starts during the normal boot process.
