# BirdNET-Go for Unraid

This directory contains the Unraid Community Applications template and documentation for running BirdNET-Go on Unraid systems.

## Overview

BirdNET-Go is a real-time bird species identification system that uses deep learning models to analyze audio streams and identify bird species with confidence scores. It's perfect for:

- **Backyard Birding**: Monitor birds visiting your garden or feeder
- **Research**: Collect data on local bird populations and behavior
- **Wildlife Monitoring**: Long-term species monitoring with automated recording
- **Education**: Learn about local bird species through audio identification

## Features

- 🎵 **Real-time Audio Analysis**: Continuous monitoring with instant species identification
- 🌐 **Beautiful Web Dashboard**: Modern interface with spectrograms and audio playback
- 📊 **Comprehensive Statistics**: Daily, weekly, and monthly detection reports
- 🎧 **Multiple Audio Sources**: Support for USB microphones, sound cards, and RTSP streams
- 🌍 **Location-based Filtering**: Species filtering based on your geographic location
- 🔊 **Audio Clip Export**: Save interesting detections in multiple formats (WAV, FLAC, AAC, MP3, Opus)
- 📱 **Tablet and Desktop UI**: Responsive design for tablet and desktop screens, with touch support on tablets (phones are not a supported layout yet)
- 🔌 **Integration Ready**: MQTT support for home automation and IoT projects

## Installation via Unraid Community Applications

### Method 1: Through Community Applications (Recommended)

1. **Install Community Applications Plugin** (if not already installed):
   - Go to **Apps** tab in Unraid WebGUI
   - Click **Install** on Community Applications

2. **Install BirdNET-Go**:
   - Go to **Apps** tab
   - Search for "BirdNET-Go"
   - Click **Install**
   - Configure the settings (see Configuration section below)
   - Click **Apply**

### Method 2: Manual Template Installation

If BirdNET-Go is not yet available in Community Applications:

1. **Add Template URL**:
   - Go to **Docker** tab in Unraid WebGUI
   - Click **Add Container**
   - Set Template Repository to: `https://raw.githubusercontent.com/tphakala/birdnet-go/main/Unraid/birdnet-go.xml`
   - Click **Save**

2. **Install from Template**:
   - Search for "BirdNET-Go" in your templates
   - Click the template to install
   - Configure settings and click **Apply**

## Host Networking Template (Recommended for New Installs)

`birdnet-go-host.xml` in this directory is a second template that runs the container with host networking. It is recommended for new installs, because multicast (mDNS) then reaches the app directly. The existing bridge template (`birdnet-go.xml`) and the Community Applications listing are not changed, and existing containers keep working as they are.

**Support status: best effort, untested on real Unraid.** No Unraid system was available to test this template. The sections below separate what is verified from what is only expected.

### What changes

- The container uses the host network stack, so there is no port mapping. The app listens on the **WebUI Port** setting (variable `BIRDNET_WEBSERVER_PORT`, default 8080). Use 1024 or higher: the app runs as a non-root user.
- The template uses the container name `birdnet-go` (the name the Community Applications template uses) and the default appdata paths (`/mnt/user/appdata/birdnet-go/config` and `/data`), so it can take over the data of an existing container. Docker container names are case-sensitive: a container created from this repository's bridge template (`birdnet-go.xml`) is named `BirdNET-Go`, so it is a separate container and must be stopped and removed first (below).
- The D-Bus path is not part of this template. Host networking does not need it.
- It also sets `--security-opt no-new-privileges=true`, `--cap-drop NET_RAW` and `--stop-timeout 20`, and keeps `--add-host="host.docker.internal:host-gateway"`. Keep that last one: it keeps the app's local-subnet check (subnet authentication bypass) the same as in bridge mode.
- The web interface listens on all interfaces. It cannot be limited to localhost in host mode.
- If you enabled Prometheus telemetry before switching, set its listen address to `127.0.0.1:8090` or a LAN IP in Settings first. In host mode it binds port 8090 on every interface.
- With subnet bypass enabled, IPv6 clients are seen by their real address. Add your IPv6 prefix to the subnet list if you rely on bypass for them.

### Verified and unverified

Verified (from this repository's code, Docker's documentation, and a Docker 29 host that is not Unraid):

- `BIRDNET_WEBSERVER_PORT` sets the listening port, and the image health check follows it.
- A container with `--network host`, `no-new-privileges` and `NET_RAW` dropped starts, drops privileges to the configured UID, and passes its health check.
- Resolving `.local` names works in host mode with the host's `/run/avahi-daemon` mounted.
- Going back to a bridge setup with a custom port needs `webserver.port` reset (see below).

Expected, but not verified on Unraid:

- **WebUI link:** the Docker tab link is built from `[PORT:8080]` and is expected to open port 8080 even if you changed the port. If it is wrong, open `http://<server>:<port>` directly.
- **Device entries:** the template uses `Type="Device"` entries for `/dev/snd` and `/dev/dri`. If your Unraid version does not show or apply them, add `--device /dev/snd` to **Extra Parameters** instead.
- **Avahi socket:** whether Unraid runs avahi-daemon on the host is not verified. If `/var/run/avahi-daemon` does not exist on your server, remove the **Avahi socket (mDNS)** path entry. `.local` names then resolve only through unicast DNS.
- **Name clash:** how Unraid handles installing a template whose container name (`birdnet-go`) already exists is not verified. Do not rely on it: stop and remove the old container yourself first (below).
- **Install path:** the steps below for installing the template by hand are expected to work but are not verified.
- **Port setting:** whether Unraid applies the WebUI Port variable as expected under host networking is not verified. If the web interface is not reachable on the port you chose, check the container log for the port it bound.

### Installing the template

This repository's template is not what Community Applications serves. To use it:

1. Download `https://raw.githubusercontent.com/tphakala/birdnet-go/main/Unraid/birdnet-go-host.xml` and place it in the dockerMan user template folder on the flash drive. The folder is expected to be `/boot/config/plugins/dockerMan/templates-user/` (not verified; check that your existing templates are stored there). Choose a file name that does not overwrite a template you want to keep.
2. In the **Docker** tab choose **Add Container** and select the template from the template list.
3. Check the paths and the WebUI Port, then click **Apply**.
4. Open the port in your firewall if you use one on the server.

### Switching an existing container (optional)

You do not need to switch. If you want to:

1. Note the current **Config Directory** and **Data Directory** paths of the old container.
2. Stop and remove the old container. Do not run both: they would use the same database and the same sound card.
3. Install the host template and set both paths to the ones you noted. Appdata is not touched by removing the container.
4. To go back, install the bridge template (or reinstall from Community Applications). If you used a WebUI Port other than 8080, first set `webserver.port: 8080` in `config/config.yaml`: the port is saved there when you save settings, and in bridge mode the container maps the host port to container port 8080.

## Configuration

### Required Settings

| Setting              | Default                               | Description                                |
| -------------------- | ------------------------------------- | ------------------------------------------ |
| **WebUI Port**       | `8080`                                | Port for accessing the web interface       |
| **Config Directory** | `/mnt/user/appdata/birdnet-go/config` | Configuration files storage                |
| **Data Directory**   | `/mnt/user/appdata/birdnet-go/data`   | Database and audio clips storage           |
| **Timezone**         | `America/New_York`                    | Container timezone for accurate timestamps |

### Advanced Settings

| Setting      | Default | Description                                 |
| ------------ | ------- | ------------------------------------------- |
| **User ID**  | `99`    | User ID for file permissions (99 = nobody)  |
| **Group ID** | `100`   | Group ID for file permissions (100 = users) |

### .local (mDNS) Hostnames

The template maps the host's `/var/run/avahi-daemon` (name resolution) and `/var/run/dbus` (DNS-SD service discovery) read-only (advanced settings **Avahi socket (mDNS)** and **D-Bus (service discovery)**). This needs avahi-daemon on the host; the socket locations on Unraid are not verified. Do not add `:z` or `:Z`; remove the D-Bus path to opt out, and always when User ID is 0 (uid 0 on the system bus is host root). See [RTSP troubleshooting](../doc/wiki/rtsp-troubleshooting.md#using-local-mdns-hostnames-in-containers).

The host networking template maps only `/var/run/avahi-daemon` (advanced setting **Avahi socket (mDNS)**). It has no D-Bus path. The socket location on Unraid is not verified, as above.

### Audio Device Requirements

BirdNET-Go requires access to audio input devices. The template automatically includes:

- `--device /dev/snd` - Access to all sound devices. On a server without a sound card (RTSP streams only), remove it (bridge template: from **Extra Parameters**; host template: clear the sound device entry), or the container does not start.
- `--add-host="host.docker.internal:host-gateway"` - Network access for RTSP streams

## Audio Configuration

### USB Microphones and Sound Cards

1. **Connect your audio device** to your Unraid server
2. **Start the container** - BirdNET-Go will auto-detect available devices
3. **Configure audio source**:
   - Open the web interface at `http://your-unraid-ip:8080`
   - Go to **Settings** → **Audio Capture**
   - Select your preferred audio device
   - Click **Save**

### RTSP Audio Streams

For IP cameras or RTSP audio sources:

1. **Configure RTSP stream**:
   - Go to **Settings** → **Audio Capture**
   - Switch to **RTSP Stream** mode
   - Enter your RTSP URL: `rtsp://username:password@camera-ip:port/stream`
   - Click **Save**

## Storage Requirements

### Disk Space

- **Minimum**: 2GB for Docker image and basic operation
- **Recommended**: 10GB+ for audio clip storage and long-term data
- **Database**: Grows ~1MB per day with moderate bird activity
- **Audio Clips**: Varies based on export settings and bird activity

### Recommended Share Configuration

Create dedicated shares for better organization:

```
/mnt/user/appdata/birdnet-go/    # Application data
├── config/                      # Configuration files
│   ├── config.yaml             # Main configuration
│   └── hls/                    # Temporary streaming files (tmpfs)
└── data/                       # Persistent data
    ├── birdnet.db              # SQLite database
    ├── clips/                  # Audio recordings
    └── logs/                   # Application logs
```

## Performance Optimization

### Hardware Recommendations

- **CPU**: Modern x86_64 with AVX2 support (Intel Haswell 2013+ or AMD equivalent)
- **RAM**: 2GB minimum, 4GB+ recommended for better caching
- **Storage**: SSD recommended for database and configuration files

### Unraid-Specific Optimizations

1. **Use Cache Drive**: Place appdata on SSD cache for better performance
2. **CPU Pinning**: Pin container to specific CPU cores if needed
3. **Memory Limits**: Set appropriate memory limits based on your usage

## Networking and Security

### Port Configuration

- **Web Interface**: Default port 8080 (configurable). With the host networking template the port is the **WebUI Port** variable and the app listens on it directly; use 1024 or higher
- **No incoming connections required** for basic operation
- **RTSP streams**: Outgoing connections to camera IPs if used

### Security Considerations

BirdNET-Go includes built-in authentication options:

1. **Basic Authentication**: Username/password protection
2. **OAuth2**: Google or GitHub authentication
3. **Network Security**: Limit access via Unraid network settings

Configure security in the web interface under **Settings** → **Security**.

## Troubleshooting

### Common Issues

**Container won't start:**

- Check Unraid logs: **Tools** → **System Log**
- Verify audio device permissions
- Ensure sufficient disk space

**No audio detected:**

- Verify USB audio device is connected and recognized by Unraid
- Check container has access to `/dev/snd`
- Test audio device with: `arecord -l` from Unraid terminal

**Web interface not accessible:**

- Verify port 8080 is not in use by another service
- Check container logs for startup errors
- Ensure firewall/network settings allow access

**Performance issues:**

- Move appdata to SSD cache drive
- Increase container memory limit
- Check CPU usage during bird detection

### Getting Help

1. **Check Logs**: View container logs in Unraid Docker tab
2. **Community Support**: Visit [BirdNET-Go Discussions](https://github.com/tphakala/birdnet-go/discussions)
3. **Report Issues**: [GitHub Issues](https://github.com/tphakala/birdnet-go/issues)
4. **Unraid Forums**: Post in the Unraid Community Applications section

## Integration Examples

### Home Assistant

BirdNET-Go supports MQTT for Home Assistant integration. Enabling Home Assistant MQTT discovery in BirdNET-Go (Settings > Integrations > MQTT) registers the detection sensors automatically, so the manual sensor below is only needed if you prefer to configure it by hand. The example uses the default base topic `birdnet` (change it to match `realtime.mqtt.topic` if you set a custom topic) and reads the detection's common name from `value_json.CommonName`. Modern Home Assistant configures MQTT sensors under the `mqtt:` key:

```yaml
# configuration.yaml
mqtt:
  sensor:
    - name: "Latest Bird Detection"
      state_topic: "birdnet"
      value_template: "{{ value_json.CommonName }}"
```

### Node-RED

Create automation flows based on bird detections using MQTT nodes.

### Notifications

Set up notifications for rare bird species or high-confidence detections.

## Updating

### Via Community Applications

1. Go to **Apps** tab
2. Check for updates in the **Installed Apps** section
3. Click **Update** if available

### Manual Update

1. Go to **Docker** tab
2. Click **Force Update** on the BirdNET-Go container
3. The container will download the latest nightly image

## Backup and Restore

### Configuration Backup

Essential files to backup:

- `/mnt/user/appdata/birdnet-go/config/config.yaml`
- `/mnt/user/appdata/birdnet-go/data/birdnet.db`

### Full Backup

Use Unraid's built-in backup tools or third-party plugins to backup the entire appdata directory.

## License

BirdNET-Go is open source software. See the [main repository](https://github.com/tphakala/birdnet-go) for license details.
