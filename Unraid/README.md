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
- 📱 **Mobile-Friendly**: Responsive design works great on phones and tablets
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
   - Set Template Repository to: `https://raw.githubusercontent.com/tphakala/birdnet-go/main/unraid/birdnet-go.xml`
   - Click **Save**

2. **Install from Template**:
   - Search for "BirdNET-Go" in your templates
   - Click the template to install
   - Configure settings and click **Apply**

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

### Audio Device Requirements

BirdNET-Go requires access to audio input devices. The template automatically includes:

- `--device /dev/snd` - Access to all sound devices
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

- **Web Interface**: Default port 8080 (configurable)
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

BirdNET-Go supports MQTT for Home Assistant integration:

```yaml
# configuration.yaml
sensor:
  - platform: mqtt
    name: "Latest Bird Detection"
    state_topic: "birdnet/detection"
    value_template: "{{ value_json.species }}"
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
