# BirdNET-Go

BirdNET-Go is an application inspired by BirdNET-Pi and BirdNET Analyzer. It aims to be a high-performance and easy-to-deploy alternative to both of these.

## BirdNET-Go Features

- Audio analysis based on the BirdNET 2.4 tflite model
- 24/7 real-time analysis of soundcard capture
- Real-time analysis output compatible with OBS chat log input for wildlife streams
- BirdWeather API support for real-time analysis
- File analysis of WAV files
- Analysis output options: Raven table, CSV file, SQLite, or MySQL database
- Localized species labels, with extensive language support (over 30 languages)
- Runs on Windows, Linux (including Raspberry Pi), and macOS
- Minimal runtime dependencies; the BirdNET TensorFlow Lite model and other supporting files are embedded in the executable
- Web dashboard with visualization capabilities
- Weather integration through OpenWeather or Yr.no
- MQTT support for IoT integration
- Advanced audio processing with equalizer filters
- Privacy and dog bark filtering capabilities
- Dynamic threshold adjustment for better detection
- OAuth2 authentication options for security
- Optional privacy-first error tracking and telemetry with Prometheus-compatible endpoint
- Sound level monitoring in 1/3rd octave bands with MQTT/SSE/Prometheus integration and configurable debug logging (supports both sound card and RTSP sources)

## Supported Platforms

BirdNET-Go has been successfully tested on:

- Raspberry Pi 3B+ with 512MB RAM
- Raspberry Pi 4B with 4GB RAM
- Raspberry Pi 5 with 4GB RAM
- Intel NUC running Windows 10
- Intel desktop PC running Windows 11
- Intel MacBook Pro

For 24/7 real-time detection, the Raspberry Pi 3B+ is more than sufficient. It can process 3-second segments in approximately 500ms.

See the [Recommended Hardware](hardware.md) document for detailed recommendations on hardware for optimal performance, especially regarding the web interface and advanced features.

Note: TPU accelerators such as Coral.AI are not supported due to incompatibility with the BirdNET tflite model.

## Installation

### Docker Installation (Recommended for Linux)

The easiest way to install BirdNET-Go on Debian, Ubuntu, or Raspberry Pi OS is using the provided installation script which sets up BirdNET-Go as a Docker container:

```bash
curl -fsSL https://github.com/tphakala/birdnet-go/raw/main/install.sh -o install.sh
bash ./install.sh
```

**Container Registry Options:**

- **GitHub Container Registry (Primary)**: `ghcr.io/tphakala/birdnet-go`
- **Docker Hub (Mirror)**: `tphakala/birdnet-go`

Both registries contain identical images. The installation script uses GitHub Container Registry by default.

The script will:

- Check system prerequisites and install required packages
- Pull the latest BirdNET-Go Docker image from the primary registry
- Guide you through configuration (audio input, location, language, etc.)
- Create a systemd service for automatic start on boot
- Set up directories for configuration and data persistence
- Optionally configure privacy-first error tracking to help improve BirdNET-Go

The installation script includes several features:

- Support for both direct audio capture and RTSP stream sources
- Multiple audio export formats (WAV, FLAC, AAC, MP3, Opus)
- Automatic performance optimization based on detected hardware
- Configuration of web interface security
- Support for over 40 languages for species labels

### Docker Compose Installation

For users who prefer Docker Compose for container management, BirdNET-Go can also be set up using this approach. Docker Compose offers more flexibility and makes it easier to manage container configurations.

A [premade docker-compose.yml](https://github.com/tphakala/birdnet-go/blob/main/Docker/docker-compose.yml) file is available in the repository. This file includes:

- The BirdNET-Go container configuration with the latest nightly image
- Environment variables for customization (timezone, user permissions, etc.)
- Volume mounts for persistent configuration and data storage
- RAM disk (tmpfs) for HLS streaming segments to improve performance
- Device mounts for sound card access
- An optional Cloudflared service (commented out) for secure internet access

See the [Docker Compose Guide](docker_compose_guide.md) for detailed instructions on setting up BirdNET-Go with Docker Compose, including internet access configuration using Cloudflare Tunnel and security options.

### Manual Installation

Pre-compiled BirdNET-Go executables are also available at https://github.com/tphakala/birdnet-go/releases/. To install manually, download and unzip birdnet-go in any directory you wish to run it in, on Windows for example c:\users\username\birdnet-go.

#### External Dependencies

BirdNET-Go has minimal external dependencies, but requires a few specific tools for certain features:

- **TensorFlow Lite C library**: Required for the core audio analysis functionality
- **FFmpeg**: Required for RTSP stream capture, audio export to formats other than WAV (MP3, AAC, FLAC, Opus), and for the HLS live stream feature in the web interface
- **SoX**: Required for rendering spectrograms in the web interface

> **Note**: When using the Docker installation method, all these dependencies are already included in the Docker image, so you don't need to install them separately. This is one of the major advantages of using the Docker-based installation.

For manual installations, you'll need to install these dependencies separately on your system.

#### TensorFlow Lite C library

In addition to BirdNET-Go executable you also need TensorFlow Lite C library which is available for download at https://github.com/tphakala/tflite_c/releases. Download library for your target platform and install it in proper library path for your system:

- On Linux copy **libtensorflowlite_c.so** to **/usr/local/lib** and run "**sudo ldconfig**"
- On macOS **libtensorflowlite_c.dylib** to **/usr/local/lib**
- On Windows copy **libtensorflowlite_c.so** to BirdNET-Go executable directory or any other directory in system PATH

> **Note**: For optimal performance using the XNNPACK delegate (enabled by default via `usexnnpack: true` in config), ensure you have downloaded version `v2.17.1` or newer of the TensorFlow Lite C library. If a compatible library is not found, BirdNET-Go will fall back to standard CPU processing.

## Configuration

BirdNET-Go accepts several settings from command line but main configuration method is through configuration file which is created when birdnet-go is first run.

### Configuration File Locations

Configuration file location is operating system dependent and there are alternative locations for user preference.

On Linux and macOS:

- Default and primary location: **$HOME/.config/birdnet-go/config.yaml**
- Alternative location (system-wide): **/etc/birdnet-go/config.yaml**

On Windows:

- Default and primary location: **config.yaml** in the birdnet-go.exe installation directory
- Secondary location: **C:\User\username\AppData\Local\birdnet-go\config.yaml**

### Configuration File Format

The configuration file uses the YAML format, which does not recognize tabs as indentation. Below is a comprehensive breakdown of the available configuration options based on the code analysis:

```yaml
# BirdNET-Go configuration
# Paths support environment variables such as $HOME and %appdata%

debug: false # Enable debug messages for troubleshooting

# Main application settings
main:
  name: BirdNET-Go # Name of this node, used to identify the source of notes
  timeas24h: true # true for 24-hour time format, false for 12-hour time format
  log:
    enabled: false # Enable main application logging
    path: logs/birdnet.log # Path to log file
    rotation: daily # Log rotation type: daily, weekly, or size
    maxsize: 10485760 # Maximum log size in bytes for size rotation (10MB)
    rotationday: Sunday # Day of the week for weekly rotation

# BirdNET model specific settings
birdnet:
  debug: false # Enable debug mode for BirdNET functionality
  sensitivity: 1.0 # Sigmoid sensitivity, 0.1 to 1.5
  threshold: 0.8 # Threshold for prediction confidence to report, 0.0 to 1.0
  overlap: 0.0 # Overlap between chunks, 0.0 to 2.9
  latitude: 60.1699 # Latitude of recording location for prediction filtering
  longitude: 24.9384 # Longitude of recording location for prediction filtering
  threads: 0 # Number of CPU threads to use (0 = use all available, automatically optimized for P-cores if detected)
  locale: en-uk # Language to use for labels
  modelpath: "" # Path to external model file (empty for embedded)
  labelpath: "" # Path to external label file (empty for embedded)
  usexnnpack: true # Use XNNPACK delegate for inference acceleration
  rangefilter:
    debug: false # Enable debug mode for range filter
    model: "" # Range filter model to use. "" (default) uses V2, "legacy" uses V1.
    threshold:
      0.01 # Range filter species occurrence threshold (0.0-1.0)
      # Default (0.01) is recommended for most users
      # Conservative values (0.05-0.1): Fewer species, higher occurrence probability
      # Strict values (0.1-0.3): Only species with strong occurrence probability
      # Very strict values (0.5+): Only the most common species for your area

# Realtime processing settings
realtime:
  interval: 15 # Minimum interval between repeating detections in seconds
  processingtime: false # Report processing time for each prediction

  # Audio settings
  audio:
    source: "" # Audio source to use for analysis
    ffmpegpath: "" # Path to ffmpeg (runtime value)
    soxpath: "" # Path to sox (runtime value)
    streamtransport: auto # Preferred transport for audio streaming: auto, sse, or ws
    soundlevel:
      enabled: false # Enable sound level monitoring in 1/3rd octave bands
      interval: 10 # Measurement interval in seconds (default: 10)
    export:
      debug: false # Enable audio export debug
      enabled: false # Export audio clips containing identified bird calls
      path: clips/ # Path to audio clip export directory
      type: wav # Audio file type: wav, mp3, or flac
      bitrate: 192k # Bitrate for audio export
      retention:
        debug: false # Enable retention debug
        policy: none # Retention policy: none, age, or usage
        maxage: 30d # Maximum age of audio clips to keep
        maxusage: 85% # Maximum disk usage percentage before cleanup
        minclips: 5 # Minimum number of clips per species to keep
        checkInterval: 15 # Cleanup check interval in minutes (default: 15)
    equalizer:
      enabled: false # Enable equalizer filters
      filters:
        - type: LowPass # Filter type: LowPass, HighPass, BandPass, etc.
          frequency: 10000 # Filter frequency in Hz
          q: 0.7 # Filter Q factor
          gain: 0 # Filter gain (only for certain types)
          width: 0 # Filter width (only for BandPass and BandReject)
          passes: 1 # Filter passes for added attenuation or gain

  # Web dashboard settings
  dashboard:
    thumbnails:
      debug: false # Enable debug mode for thumbnails
      summary: true # Show thumbnails on summary table
      recent: true # Show thumbnails on recent table
    summarylimit: 20 # Limit for the number of species shown in the summary table

  # Dynamic threshold adjustment
  dynamicthreshold:
    enabled: false # Enable dynamic threshold adjustment
    debug: false # Enable debug mode for dynamic threshold
    trigger: 0.5 # Trigger threshold for dynamic adjustment
    min: 0.3 # Minimum threshold for dynamic adjustment
    validhours: 24 # Number of hours to consider for dynamic threshold

  # OBS chat log settings
  log:
    enabled: false # Enable OBS chat log
    path: birdnet.txt # Path to OBS chat log

  # BirdWeather API integration
  birdweather:
    enabled: false # Enable BirdWeather uploads
    debug: false # Enable debug mode for BirdWeather API
    id: "00000" # BirdWeather ID / Token
    threshold: 0.9 # Threshold of prediction confidence for uploads
    locationaccuracy: 10 # Accuracy of location in meters
    retrysettings:
      enabled: true # Enable retry mechanism
      maxretries: 5 # Maximum number of retry attempts
      initialdelay: 5 # Initial delay before first retry in seconds
      maxdelay: 300 # Maximum delay between retries in seconds
      backoffmultiplier: 2.0 # Multiplier for exponential backoff

  # Weather integration settings
  weather:
    provider: "yrno" # Weather provider: none, yrno, or openweather
    pollinterval: 30 # Weather data polling interval in minutes
    debug: false # Enable debug mode for weather integration
    openweather:
      enabled: false # Enable OpenWeather integration (legacy setting, use 'provider' above)
      apikey: "" # OpenWeather API key
      endpoint: "https://api.openweathermap.org/data/2.5/weather" # OpenWeather API endpoint
      units: "metric" # Units of measurement: standard, metric, or imperial
      language: "en" # Language code for the response

  # Privacy and filtering settings
  privacyfilter:
    debug: false # Enable debug mode for privacy filter
    enabled: false # Enable privacy filter
    confidence: 0.8 # Confidence threshold for human detection

  dogbarkfilter:
    debug: false # Enable debug mode for dog bark filter
    enabled: false # Enable dog bark filter to prevent misdetections during dog barking
    confidence: 0.8 # Confidence threshold for dog bark detection
    remember: 60 # How long to remember barks for filtering (in seconds)
    species: ["Eurasian Eagle-Owl", "Hooded Crow"] # Species prone to dog bark confusion

  # RTSP streaming settings
  rtsp:
    transport: "tcp" # RTSP Transport Protocol: tcp or udp
    urls: [] # RTSP stream URLs

  # MQTT integration
  mqtt:
    enabled: false # Enable MQTT
    broker: "localhost:1883" # MQTT broker URL (e.g., mqtt://host:port or mqtts://host:port)
    topic: "birdnet/detections" # MQTT topic
    username: "" # MQTT username
    password: "" # MQTT password
    retain: false # Retain messages (useful for Home Assistant)
    retrysettings:
      enabled: true # Enable retry mechanism
      maxretries: 5 # Maximum number of retry attempts
      initialdelay: 5 # Initial delay before first retry in seconds
      maxdelay: 300 # Maximum delay between retries in seconds
      backoffmultiplier: 2.0 # Multiplier for exponential backoff

  # Telemetry settings
  telemetry:
    enabled: false # Enable Prometheus compatible telemetry endpoint
    listen: "localhost:9090" # IP address and port to listen on (e.g., 0.0.0.0:9090)

  # Species-specific settings
  species:
    include: [] # Always include these species, bypassing range/occurrence filters
    exclude: [] # Always exclude these species, regardless of confidence
    config: # Per-species configuration overrides
      "European Robin": # Use the exact species name from BirdNET labels
        threshold: 0.75 # Custom confidence threshold for this species
        actions: # List of actions to execute on detection (currently only one action per species supported)
          - type: ExecuteCommand # Action type (only ExecuteCommand supported currently)
            command: "/path/to/notify_script.sh" # Full path to the script/command
            parameters: ["CommonName", "Confidence"] # Parameters to pass to the command
            executedefaults: true # true: run default actions (DB, MQTT, etc.) AND this command. false: run ONLY this command.

  # Species tracking settings (NEW)
  speciesTracking:
    enabled: true # Enable tracking of new species discoveries (default: true)
    newSpeciesWindowDays: 7 # Days to show "New Species" badge after first detection (default: 7)
    syncIntervalMinutes: 60 # How often to sync tracking data with database in minutes (default: 60)

    # Yearly tracking - tracks first arrivals each calendar year
    yearlyTracking:
      enabled: true # Enable yearly "New This Year" tracking (default: true)
      resetMonth: 1 # Month when yearly tracking resets (1-12, default: 1 for January)
      resetDay: 1 # Day of month when yearly tracking resets (1-31, default: 1)
      windowDays: 7 # Days to show "New This Year" badge after first yearly detection (default: 7)

    # Seasonal tracking - tracks first arrivals each season
    seasonalTracking:
      enabled: true # Enable seasonal "New This Season" tracking (default: true)
      windowDays: 7 # Days to show "New This Season" badge after first seasonal detection (default: 7)
      # Custom season definitions (optional - auto-adjusts for hemisphere if not specified)
      seasons:
        spring:
          startMonth: 3 # March (Northern Hemisphere default)
          startDay: 20 # Spring equinox
        summer:
          startMonth: 6 # June
          startDay: 21 # Summer solstice
        fall:
          startMonth: 9 # September
          startDay: 22 # Fall equinox
        winter:
          startMonth: 12 # December
          startDay: 21 # Winter solstice

# Web server settings
webserver:
  debug: false # Enable debug mode for web server
  enabled: true # Enable web server
  port: "8080" # Port for web server
  log:
    enabled: false # Enable web server logging
    path: logs/webserver.log # Path to log file
    rotation: daily # Log rotation type: daily, weekly, or size
    maxsize: 10485760 # Maximum log size in bytes for size rotation (10MB)
    rotationday: Sunday # Day of the week for weekly rotation

# Security settings
security:
  debug: false # Enable debug mode for security features
  host:
    "" # Primary hostname used for TLS certificates, OAuth redirect URLs, and notification links
    # Set this to your public hostname when using a reverse proxy (e.g., "birdnet.home.arpa")
    # Can also be set via BIRDNET_HOST environment variable
    # Falls back to "localhost" if not configured (works for direct access only)
  autotls: false # Enable automatic TLS certificate management using Let's Encrypt
  redirecttohttps: true # Redirect HTTP to HTTPS
  allowsubnetbypass:
    enabled: false # Enable subnet bypass for authentication
    subnet: "192.168.1.0/24" # Subnet to bypass authentication
  basicauth:
    enabled: false # Enable password authentication
    password: "" # Password for admin interface
    clientid: "" # Client ID for OAuth2
    clientsecret: "" # Client secret for OAuth2
    redirecturi: "" # Redirect URI for OAuth2
    authcodeexp: 10m # Duration for authorization code
    accesstokenexp: 1h # Duration for access token
  googleauth:
    enabled: false # Enable Google OAuth2
    clientid: "" # Google client ID
    clientsecret: "" # Google client secret
    redirecturi: "" # Google redirect URI
    userid: "" # Valid Google user ID
  githubauth:
    enabled: false # Enable GitHub OAuth2
    clientid: "" # GitHub client ID
    clientsecret: "" # GitHub client secret
    redirecturi: "" # GitHub redirect URI
    userid: "" # Valid GitHub user ID
  sessionsecret: "" # Secret for session cookie

# Output settings
# Error tracking and telemetry (optional)
sentry:
  enabled: false # true to enable privacy-first error tracking (opt-in)

output:
  # SQLite database output settings
  sqlite:
    enabled: false # Enable SQLite output
    path: birdnet.db # Path to SQLite database

  # MySQL database output settings
  mysql:
    enabled: false # Enable MySQL output
    username: birdnet # MySQL database username
    password: secret # MySQL database user password
    database: birdnet # MySQL database name
    host: localhost # MySQL database host
    port: 3306 # MySQL database port
```

### Command Line Interface

While the primary configuration is done via `config.yaml`, BirdNET-Go also offers several command-line operations:

```bash
birdnet [command] [flags]
```

**Available Commands:**

- `realtime`: (Default) Starts the real-time analysis using the configuration file.
- `file`: Analyzes a single audio file. Requires `-i <filepath>`.
- `directory`: Analyzes all audio files in a directory. Requires `-i <dirpath>`. Can optionally use `--recursive` and `--watch`.
- `benchmark`: Runs a performance benchmark on the current system.
- `range`: Manages the range filter database (used for location-based species filtering).
  - `range update`: Downloads or updates the range filter database.
  - `range info`: Displays information about the current range filter database.
  - `range print`: Shows all species that pass the current threshold for your location and date, with their probability scores.
- `support`: Generates a support bundle containing logs and configuration (with sensitive data masked) for troubleshooting.
- `authors`: Displays author information.
- `license`: Displays software license information.
- `help`: Shows help for any command.

**Global Flags (can be used with most commands):**

Many configuration options can be overridden via command-line flags (e.g., `--threshold 0.7`, `--locale fr`). Run `birdnet [command] --help` to see all available flags for a specific command. Some common global flags include:

- `-d, --debug`: Enable debug output.
- `-s, --sensitivity`: Set sigmoid sensitivity (0.0 to 1.5).
- `-t, --threshold`: Set confidence threshold (0.1 to 1.0).
- `-j, --threads`: Set number of CPU threads (0 for auto).
- `--locale`: Set language for labels (e.g., `en-us`, `de`).
- `--latitude`, `--longitude`: Set location coordinates.
- `--overlap`: Set analysis overlap (0.0 to 2.9).

### Supported Languages for Species Labels

BirdNET-Go supports an extensive list of languages for species labels. This is significantly expanded from what was shown in the original wiki page:

- Afrikaans (af)
- Arabic (ar)
- Bulgarian (bg)
- Catalan (ca)
- Chinese (zh)
- Croatian (hr)
- Czech (cs)
- Danish (da)
- Dutch (nl)
- English (UK) (en-uk)
- English (US) (en-us)
- Estonian (et)
- Finnish (fi)
- French (fr)
- German (de)
- Greek (el)
- Hebrew (he)
- Hungarian (hu)
- Icelandic (is)
- Indonesian (id)
- Italian (it)
- Japanese (ja)
- Korean (ko)
- Latvian (lv)
- Lithuanian (lt)
- Malayalam (ml)
- Norwegian (no)
- Polish (pl)
- Portuguese (pt)
- Portuguese (Brazil) (pt-br)
- Portuguese (Portugal) (pt-pt)
- Romanian (ro)
- Russian (ru)
- Serbian (sr)
- Slovak (sk)
- Slovenian (sl)
- Spanish (es)
- Swedish (sv)
- Thai (th)
- Turkish (tr)
- Ukrainian (uk)

## Troubleshooting

### Docker Installation Troubleshooting

If you're having issues with your Docker-based BirdNET-Go installation, here are some common commands and solutions:

#### Service Management

```bash
# Check service status
sudo systemctl status birdnet-go

# Start the service
sudo systemctl start birdnet-go

# Stop the service
sudo systemctl stop birdnet-go

# Restart the service
sudo systemctl restart birdnet-go

# View logs (most recent entries)
sudo journalctl -u birdnet-go -n 50

# Follow logs in real-time
sudo journalctl -fu birdnet-go
```

#### Common Issues and Solutions

1. **No sound detected**:
   - Check that your user is in the audio group: `groups $USER`
   - Verify audio device is connected and recognized: `arecord -l`
   - Ensure the Docker container has access to audio devices by checking the service file

2. **Web interface not accessible**:
   - Verify the service is running: `sudo systemctl status birdnet-go`
   - Check that port 8080 (or your configured port) is not blocked by a firewall
   - Confirm the port binding in the Docker container: `docker ps | grep birdnet-go`

3. **Container exits immediately after starting**:
   - Check logs for errors: `sudo journalctl -u birdnet-go -n 100`
   - Verify correct volume mappings in the service file
   - Check permissions on the config and data directories

4. **Low detection rate or poor performance**:
   - Verify your latitude/longitude settings are correct in config.yaml
   - Check if audio device is working properly: `arecord -d 5 -f S16_LE -r 22050 test.wav`
   - Adjust sensitivity and threshold settings in the configuration file

5. **Constant 'WARNING: BirdNET processing time exceeded buffer length' messages:**
   - If you have enabled Deep Detection (by setting a high `birdnet.overlap` value, e.g., 2.7), your system might not be powerful enough to keep up with the increased analysis rate. Consider reducing the `birdnet.overlap` value or using more powerful hardware (RPi 4/5 or better recommended for Deep Detection).

#### Updating a Docker Installation (`install.sh` method)

If you installed BirdNET-Go using the recommended `install.sh` script, you can update to the latest version by simply re-running the script:

1.  It is **recommended to download a fresh copy** of the script each time, as it may contain improvements:
    ```bash
    curl -fsSL https://github.com/tphakala/birdnet-go/raw/main/install.sh -o install.sh
    ```
2.  Run the downloaded script:
    ```bash
    bash ./install.sh
    ```
3.  The script will detect your installation and offer an "Update" option. Selecting it will stop the service, pull the newest `nightly` image, update the service configuration if needed, and restart BirdNET-Go. Your configuration and data will be preserved.

### Timezone Configuration

BirdNET-Go uses timezone settings to ensure accurate timestamps for bird detections and proper scheduling of features. If you notice timestamp mismatches or scheduling issues, you may need to adjust the timezone configuration.

#### For install.sh Deployments

If you installed BirdNET-Go using the recommended `install.sh` script, the timezone is configured during installation and stored in the systemd service file.

##### Checking Current Timezone

To see what timezone BirdNET-Go is currently using:

```bash
# Check the timezone setting in the systemd service
grep "TZ=" /etc/systemd/system/birdnet-go.service
```

This will show something like: `--env TZ="Europe/Helsinki"`

##### Changing the Timezone

To change the timezone for an existing installation:

1. **Edit the systemd service file:**

   ```bash
   sudo nano /etc/systemd/system/birdnet-go.service
   ```

2. **Find the line containing `--env TZ=` and update it:**

   ```bash
   # Change from:
   --env TZ="Europe/London" \

   # To your desired timezone, for example:
   --env TZ="US/Eastern" \
   ```

3. **Reload systemd and restart the service:**

   ```bash
   sudo systemctl daemon-reload
   sudo systemctl restart birdnet-go
   ```

4. **Verify the change took effect:**
   ```bash
   # Check logs to see timestamps
   sudo journalctl -u birdnet-go -n 20
   ```

##### Common Timezone Examples

- **United States:** `US/Eastern`, `US/Central`, `US/Mountain`, `US/Pacific`
- **Europe:** `Europe/London`, `Europe/Berlin`, `Europe/Paris`, `Europe/Helsinki`
- **Asia:** `Asia/Tokyo`, `Asia/Singapore`, `Asia/Dubai`, `Asia/Kolkata`
- **Australia:** `Australia/Sydney`, `Australia/Melbourne`, `Australia/Perth`
- **Other:** `UTC` (Coordinated Universal Time)

##### Finding Your Timezone

To find the correct timezone string for your location:

```bash
# List all available timezones
timedatectl list-timezones

# Search for a specific country/city
timedatectl list-timezones | grep -i "york"
```

#### For Docker Compose Deployments

If you're using Docker Compose, the timezone is typically set via an environment variable in your `docker-compose.yml` file:

```yaml
services:
  birdnet-go:
    environment:
      - TZ=US/Eastern # Change this to your timezone
```

After updating, restart the container:

```bash
docker-compose down
docker-compose up -d
```

#### For Binary Installations

If you're running BirdNET-Go directly as a binary (not using Docker), it uses the system timezone by default. To override it:

```bash
# Set timezone environment variable before running
TZ="US/Eastern" ./birdnet-go realtime
```

Or add it to your startup script or systemd service file if you've created one manually.

#### Important Notes

> **⚠️ Custom Deployments:** The instructions above apply specifically to installations done via the official `install.sh` script. Custom Docker Compose setups or manual binary deployments may handle timezone configuration differently.

> **💡 System vs Application Timezone:** BirdNET-Go can use a different timezone than your system. This is useful if you want your system in one timezone but want BirdNET-Go to record detections in another (e.g., UTC for standardized scientific data).

> **🔄 After Updates:** When updating BirdNET-Go using the `install.sh` script, your timezone settings are preserved automatically as of recent versions.

#### Troubleshooting Timezone Issues

**Problem:** Bird detections show wrong timestamps

**Solution:** Check that the timezone in the systemd service matches your actual location. The timezone affects both the displayed time and any time-based features.

**Problem:** Scheduled features run at wrong times

**Solution:** Ensure the TZ environment variable is set correctly. Some features like dynamic thresholds or scheduled exports rely on accurate time settings.

**Problem:** Timezone reverts after update

**Solution:** If you're using an older version of the install script, manually re-apply your timezone change after updates. The latest version preserves timezone settings during updates.

### Support Script

For more comprehensive troubleshooting, BirdNET-Go provides a support script that collects diagnostic information while protecting your privacy:

```bash
curl -fsSL https://github.com/tphakala/birdnet-go/raw/main/support.sh -o support.sh
sudo bash ./support.sh
```

The script will:

- Collect system information (hardware, OS, etc.)
- Gather Docker configuration and logs
- Retrieve BirdNET-Go configuration (with sensitive data masked)
- Capture systemd service information
- Collect audio device information
- Create a support bundle for sharing with developers

### Reporting Issues

If you encounter problems that you can't resolve, please open an issue on the GitHub repository:

1. Go to https://github.com/tphakala/birdnet-go/issues
2. Click "New Issue"
3. Fill out the issue template carefully, including:
   - What you expected to happen
   - What actually happened
   - Complete steps to reproduce the issue
   - Relevant logs and configuration details
   - Attach the support bundle generated by the support script if available

Providing detailed information in your issue report will help the developers understand and resolve your problem more quickly.

## BirdNET Detection Pipeline

Understanding how BirdNET-Go processes audio and applies various filters is crucial for optimizing your detection accuracy. The detection process follows a multi-stage pipeline where settings are applied in a specific order of precedence.

### Detection Flow Overview

The detection process follows a multi-stage pipeline where settings are applied in a specific order of precedence:

```mermaid
graph TD
    A[Audio Input] --> B[BirdNET AI Analysis]
    B --> C{Range Filter Check}
    C -->|Species Not Allowed| D[🚫 Discard Detection]
    C -->|Species Allowed| E{Confidence Threshold}
    E -->|Below Threshold| D
    E -->|Above Threshold| F{Deep Detection Check}
    F -->|Insufficient Matches| G[⏳ Hold in Memory]
    F -->|Sufficient Matches| H{Privacy Filter}
    H -->|Human Detected| D
    H -->|No Human| I{Dog Bark Filter}
    I -->|Recent Bark| D
    I -->|No Recent Bark| J[✅ Accept Detection]
    G --> K{Timeout Reached?}
    K -->|No| G
    K -->|Yes| L{Min Detections Met?}
    L -->|No| D
    L -->|Yes| H
```

### Stage 1: Range Filter (Highest Precedence)

The **Range Filter** acts as the primary gatekeeper, determining which species are even possible to detect based on location and time. This stage has the highest precedence and cannot be overridden by confidence settings.

#### Components (in order of precedence):

1. **Always Exclude Species** (Absolute Override)
   - Species in this list are **never** detected, regardless of any other settings
   - Useful for filtering out non-bird sounds (Dog, Siren, Gun) or problematic species
   - Configured via: Settings → Species → "Always Exclude Species"

2. **Always Include Species** (Absolute Override)
   - Species in this list are **always** allowed, bypassing location-based filtering
   - Automatically assigned maximum probability score (1.0)
   - Configured via: Settings → Species → "Always Include Species"

3. **Custom Action Species** (Automatic Include)
   - Species with configured custom actions are automatically included
   - Also assigned maximum probability score (1.0)

4. **Location-Based Filtering**
   - Uses AI model trained on eBird data to determine species probability
   - Considers latitude, longitude, and week of year
   - Filtered by `birdnet.rangefilter.threshold` (default: 0.01)

```yaml
# Range filter configuration
birdnet:
  latitude: 60.1699
  longitude: 24.9384
  rangefilter:
    threshold: 0.01 # Lower = more permissive, higher = more strict
```

### Stage 2: Confidence Threshold

After the range filter allows a species, individual detections must meet confidence requirements.

#### Confidence Sources (in order of precedence):

1. **Custom Species Threshold** (Highest)

   ```yaml
   realtime:
     species:
       config:
         "European Robin":
           threshold: 0.75 # Overrides global threshold
   ```

2. **Dynamic Threshold** (If enabled)
   - Automatically adjusts thresholds based on detection patterns
   - Can lower thresholds for frequently detected species
3. **Global BirdNET Threshold** (Default)
   ```yaml
   birdnet:
     threshold: 0.8 # Default confidence requirement
   ```

### Dynamic Threshold System

The Dynamic Threshold feature intelligently adapts detection sensitivity for individual species based on recent high-confidence detections. This system helps improve detection rates for species that are actively present in your area while maintaining accuracy.

#### How Dynamic Thresholds Work

```mermaid
graph TD
    A[Bird Detection] --> B{Dynamic Threshold<br/>Enabled?}
    B -->|No| C[Use Base Threshold]
    B -->|Yes| D{Species Has<br/>Dynamic Threshold?}

    D -->|No| E[Initialize Dynamic<br/>Threshold for Species]
    D -->|Yes| F{Confidence ><br/>Trigger Value?}

    E --> F

    F -->|Yes| G[High Confidence<br/>Detection!]
    F -->|No| H{Timer Expired?}

    G --> I[Increment<br/>High Conf Count]
    I --> J[Reset Timer<br/>+ValidHours]
    J --> K{Check High<br/>Conf Count}

    K -->|Count = 1| L[Level 1:<br/>Threshold × 0.75]
    K -->|Count = 2| M[Level 2:<br/>Threshold × 0.5]
    K -->|Count ≥ 3| N[Level 3:<br/>Threshold × 0.25]

    H -->|Yes| O[Reset to<br/>Base Threshold]
    H -->|No| P[Keep Current<br/>Threshold]

    L --> Q{Below Min<br/>Threshold?}
    M --> Q
    N --> Q
    O --> R[Apply Threshold]
    P --> R

    Q -->|Yes| S[Use Min<br/>Threshold]
    Q -->|No| T[Use Calculated<br/>Threshold]

    S --> R
    T --> R
    C --> R

    R --> U[Detection Processed<br/>with Final Threshold]
```

#### Configuration

```yaml
realtime:
  dynamicthreshold:
    enabled: true # Enable dynamic threshold adjustment (default: true)
    debug: false # Enable debug logging for threshold changes
    trigger: 0.90 # Confidence level that triggers threshold reduction
    min: 0.20 # Minimum allowed threshold (safety floor)
    validhours: 24 # Hours before threshold resets to base value
```

#### Key Parameters Explained

1. **`trigger`** (default: 0.90)
   - The confidence level that activates dynamic threshold adjustment
   - When a detection exceeds this value, the system starts lowering the threshold for that species
   - Example: With trigger=0.90, only very high-confidence detections (90%+) will activate the dynamic adjustment

2. **`min`** (default: 0.20)
   - The absolute minimum threshold value
   - Prevents the threshold from dropping too low, maintaining detection quality
   - Acts as a safety floor to prevent excessive false positives

3. **`validhours`** (default: 24)
   - Duration (in hours) that the lowered threshold remains active
   - Timer resets with each new high-confidence detection
   - After this period without high-confidence detections, the threshold returns to base value

#### Threshold Adjustment Levels

The system uses a progressive adjustment based on the number of high-confidence detections:

| High Conf Count | Level | Threshold Multiplier | Example (Base: 0.8) |
| --------------- | ----- | -------------------- | ------------------- |
| 0               | 0     | 1.0× (base)          | 0.80                |
| 1               | 1     | 0.75×                | 0.60                |
| 2               | 2     | 0.50×                | 0.40                |
| 3+              | 3     | 0.25×                | 0.20 (min limit)    |

#### Practical Example

Let's walk through a scenario with these settings:

```yaml
birdnet:
  threshold: 0.8 # Base threshold
realtime:
  dynamicthreshold:
    enabled: true
    trigger: 0.9 # High trigger for quality
    min: 0.2 # Safety floor
    validhours: 24 # 24-hour window
```

**Scenario: Great Horned Owl Detection**

1. **Initial State**: Great Horned Owl has no dynamic threshold, uses base 0.8
2. **First Detection**:
   - 19:30 (dusk) - Great Horned Owl detected with 0.93 confidence (exceeds 0.9 trigger)
   - High confidence count: 1
   - New threshold: 0.8 × 0.75 = 0.6
   - Timer set to expire at 19:30 tomorrow
3. **Night Activity**:
   - 21:45 - Great Horned Owl detected with 0.65 confidence (now passes lowered threshold)
   - 23:15 - Another detection with 0.94 confidence
   - High confidence count: 2
   - New threshold: 0.8 × 0.5 = 0.4
   - Timer reset to 23:15 tomorrow
4. **Pre-dawn Activity**:
   - 04:30 - Detection with 0.91 confidence
   - High confidence count: 3
   - Calculated threshold: 0.8 × 0.25 = 0.2
   - Applied threshold: 0.2 (exactly at min limit)
5. **Daytime Silence**:
   - Owls typically inactive during day
   - No detections from 05:00 to 19:00
6. **Reset Scenario**:
   - If no high-confidence detections occur for 24 hours after 04:30
   - Threshold returns to base 0.8
   - Next evening's first call would need 0.8+ confidence again

#### Benefits and Use Cases

1. **Adaptive Sensitivity**: Automatically becomes more sensitive to species that are actively vocalizing in your area
2. **Quality Maintenance**: High trigger values ensure only quality detections influence the system
3. **Temporal Awareness**: Accounts for daily activity patterns with the time-based reset
4. **Species-Specific**: Each species maintains its own dynamic threshold independently
5. **Safety Limits**: Minimum threshold prevents excessive false positives

#### Best Practices

1. **Conservative Trigger**: Set trigger high (0.8-0.95) to ensure only clear detections adjust thresholds
2. **Reasonable Minimum**: Keep min at or above 0.2 to maintain detection quality
3. **Monitor with Debug**: Enable debug mode initially to understand how thresholds change:
   ```yaml
   dynamicthreshold:
     debug: true # Logs threshold changes
   ```
4. **Combine with Deep Detection**: For best results, use with deep detection to filter false positives:
   ```yaml
   birdnet:
     overlap: 2.7 # Deep detection
   realtime:
     dynamicthreshold:
       enabled: true # Adaptive thresholds
   ```

#### Important Notes

- Dynamic thresholds work **after** the range filter - species must pass location filtering first
- Each species threshold is independent - one species' activity doesn't affect others
- The system automatically cleans up stale thresholds to prevent memory bloat
- Custom species thresholds (if configured) take precedence over dynamic adjustments

### Stage 3: Deep Detection Filter

[Deep Detection](BirdNET‐Go-Guide#deep-detection) uses the `overlap` setting to require multiple detections of the same species within a configurable detection window before accepting it, significantly reducing false positives.

#### How It Works:

1. **Detection Holding**: All detections are held in a pending state for `captureLength - preCaptureLength` seconds from first detection (defaults to 12 seconds with 15s clip length and 3s pre-capture buffer)
2. **Counting Mechanism**: During this window, if the same species is detected again, a counter increments
3. **Threshold Calculation**: The minimum required detections scales with both overlap and detection window duration:

```yaml
birdnet:
  overlap: 2.7 # Enable deep detection (requires more CPU power)
realtime:
  audio:
    export:
      length: 15 # Audio clip length in seconds
      preCapture: 3 # Pre-detection buffer in seconds
```

**Exact Minimum Detections Calculation:**

```
detectionWindow = captureLength - preCaptureLength
segmentLength = max(0.1, 3.0 - overlap)
baseMinDetections = 3.0 / segmentLength
scaleFactor = detectionWindow / 15.0
minDetections = max(1, round(baseMinDetections * scaleFactor))

Examples (with default 12-second detection window):
- overlap: 0.0  → segmentLength: 3.0  → minDetections: 1  (standard mode)
- overlap: 1.5  → segmentLength: 1.5  → minDetections: 2
- overlap: 2.4  → segmentLength: 0.6  → minDetections: 4  (recommended)
- overlap: 2.7  → segmentLength: 0.3  → minDetections: 8  (deep detection)
- overlap: 2.9  → segmentLength: 0.1  → minDetections: 24 (very strict)

With longer detection window (30s clip, 0s pre-capture = 30s window):
- overlap: 2.4  → minDetections: 10 (2x baseline)
- overlap: 2.7  → minDetections: 20 (2x baseline)
```

#### Processing Timeline:

1. **0 seconds**: Species detected for first time, detection window timer starts
2. **0 to detectionWindow seconds**: Additional detections increment the counter
3. **detectionWindow seconds**: Decision point reached:
   - **If count ≥ minDetections**: Detection approved and processed
   - **If count < minDetections**: Detection discarded as "false positive"

#### Key Behavior Notes:

- **Configurable timeout**: Detection window duration is `captureLength - preCaptureLength` (prevents audio clip gaps)
- **No early approval**: Even if minimum detections are met early, the system waits for the full window
- **Quality improvement**: Higher confidence detections within the window replace lower ones
- **Memory efficient**: Only one pending detection per species is held at a time
- **Runtime adaptable**: Changes to overlap, clip length, or pre-capture settings take effect within 1 second

### Stage 4: Privacy and Behavioral Filters

Final stage filters that can discard detections based on environmental conditions:

#### Privacy Filter

```yaml
realtime:
  privacyfilter:
    enabled: true
    confidence: 0.05 # Sensitivity to human voices
```

- Discards bird detections if human speech is detected after the initial detection
- Protects privacy by preventing recordings during conversations

#### Dog Bark Filter

```yaml
realtime:
  dogbarkfilter:
    enabled: true
    confidence: 0.8
    remember: 60 # Seconds to remember bark
    species: ["Eurasian Eagle-Owl", "Hooded Crow"] # Species prone to confusion with barks
```

- **Problem Solved**: The BirdNET AI model frequently confuses dog barks with certain bird calls, especially owl vocalizations (very common: dog bark → Eurasian Eagle-Owl)
- **How It Works**: When BirdNET detects dog barking above the confidence threshold, it temporarily disables detection of species listed in the filter for the specified duration
- **Use Cases**: Prevents false detections during periods of constant dog barking, particularly for species that have acoustic similarities to canine vocalizations
- **Species Selection**: Focus on owl species (especially larger owls) and corvids (crows/ravens) which are most commonly confused with dog barks

### Setting Precedence Summary

**Highest to Lowest Precedence:**

1. **Always Exclude Species** - Absolute veto power
2. **Always Include Species** - Bypasses all location filtering
3. **Custom Action Species** - Automatically included
4. **Range Filter Threshold** - Location-based species filtering
5. **Custom Species Confidence** - Overrides global threshold
6. **Dynamic Threshold** - Automatic adjustment (if enabled)
7. **Global Confidence Threshold** - Default requirement
8. **Deep Detection Filter** - Requires multiple matches
9. **Privacy Filter** - Environmental safety
10. **Dog Bark Filter** - Behavioral filtering

### Optimization Tips

#### For Higher Accuracy (Fewer False Positives):

- Increase `birdnet.threshold` (e.g., 0.9)
- Enable deep detection with `overlap: 2.7`
- Use stricter range filter threshold (e.g., 0.05)
- Enable privacy and dog bark filters (especially for owl/crow confusion)

#### For Higher Sensitivity (Catch More Species):

- Lower `birdnet.threshold` (e.g., 0.6)
- Use permissive range filter threshold (0.01)
- Add rare species to "Always Include" list
- Disable behavioral filters in quiet environments

#### For Specific Species:

- Use custom thresholds for problematic species
- Add reliable local species to "Always Include"
- Add problematic non-bird sounds to "Always Exclude"

### Viewing Your Current Configuration

Use these commands to inspect your current detection settings:

```bash
# View species included by range filter
./birdnet-go range print

# View all CLI options
./birdnet-go help

# View range filter specific options
./birdnet-go help range
```

## BirdNET Range Filter

The BirdNET Range Filter is an intelligent location and time-based filtering system that helps improve detection accuracy by limiting species predictions to those likely to occur in your specific location during the current time of year.

### How It Works

#### Model Overview

The range filter uses a secondary AI model trained on [eBird checklist frequency data](https://support.ebird.org/en/support/solutions/articles/48000948655-ebird-glossary#:~:text=frequency%3A%20as%20of%20a%20species,purple%20grid%20on%20species%20maps) to estimate the probability of bird species occurrence based on three factors:

1. **Latitude** - Your geographic latitude coordinate
2. **Longitude** - Your geographic longitude coordinate
3. **Week of Year** - The current week (1-52) to account for seasonal migration patterns

The model analyzes these inputs and assigns each species a probability score from 0.0 to 1.0, representing how likely that species is to occur in your location during that time period.

#### eBird Data Coverage

The range filter model is built using citizen science data from [eBird](https://ebird.org), which means coverage varies by region:

- **Well-represented regions**: North America, South America, Europe, India, Australia
- **Underrepresented regions**: Large parts of Africa and Asia

In areas with limited eBird data, the model uses expert-curated filter data to provide basic species occurrence information.

### Configuration

#### Basic Setup

The range filter is configured in the `birdnet.rangefilter` section of your configuration file:

```yaml
birdnet:
  latitude: 60.1699 # Your location latitude
  longitude: 24.9384 # Your location longitude
  rangefilter:
    debug: false # Enable debug logging
    model: "" # "" for V2 (default), "legacy" for V1
    threshold: 0.01 # Species occurrence threshold (0.0-1.0)
```

> **Note**: The configuration uses `birdnet.rangefilter` in YAML, while CLI commands use the `range` group (e.g., `birdnet-go range print`). These refer to the same functionality.

#### Understanding the Threshold Parameter

The `threshold` parameter controls which species are included in analysis based on their occurrence probability. **The default value of 0.01 is recommended for most users and rarely needs to be changed** unless you have very specific requirements.

- **Default (0.01)**: Permissive filtering that works well for most locations and use cases
- **Conservative values (0.05-0.1)**: Include fewer species, only those with higher occurrence probability
- **Strict values (0.1-0.3)**: Include only species with strong occurrence probability
- **Very strict values (0.5+)**: Include only the most common species for your area

**Tip**: If the range filter results don't match your expectations for specific species, you can override them using the **Species Settings** in the web interface (Settings → Species) rather than adjusting the global threshold.

### Configuration Examples

#### Example 1: Default Permissive Filtering

```yaml
rangefilter:
  threshold: 0.01 # Default - good for most users
```

- Includes species with ≥1% occurrence probability
- Captures most potential species including occasional visitors
- Balanced approach suitable for most locations

#### Example 2: Conservative Filtering

```yaml
rangefilter:
  threshold: 0.05
```

- Includes species with ≥5% occurrence probability
- Reduces potential false positives from very unlikely species
- Good balance between coverage and accuracy

#### Example 3: Strict Filtering

```yaml
rangefilter:
  threshold: 0.1
```

- Includes only species with ≥10% occurrence probability
- Reduces false positives from unlikely species
- Good for areas with excellent eBird coverage
- May miss rare but possible species

#### Example 4: Very Strict Filtering

```yaml
rangefilter:
  threshold: 0.3
```

- Includes only species with ≥30% occurrence probability
- Focuses on common resident and seasonal species
- Minimizes false positives
- May miss genuine detections of less common species

### Species Override Management

#### Override Behavior

The range filter works alongside your manual species configuration:

1. **Always Include Species**: Species in this list are **always** included regardless of range filter scores
2. **Always Exclude Species**: Species in this list are **always** excluded regardless of range filter scores
3. **Custom Actions**: Species with configured actions are automatically included with maximum score

#### Managing Species Lists via Web Interface

You can easily manage species overrides through the web dashboard:

- Navigate to **Settings → Species** in the web interface
- **"Always Include Species"** section: Add species that should never be filtered out
- **"Always Exclude Species"** section: Add species that should never be detected
  - Useful for non-bird sounds like "Dog", "Siren", "Gun", "Fireworks"
  - Helpful for consistently problematic species in your area
- Changes are applied immediately without restarting the application

### Inspection and Debugging

#### Viewing Current Filter Results

You can inspect what species are included for your location using the CLI command (run `birdnet-go help range` for more options):

```bash
./birdnet-go range print
```

This displays all species that pass the threshold for your current location and date, showing their probability scores.

#### Range Filter Models

BirdNET-Go supports two range filter model versions:

- **V2 (Default)**: Latest model with improved accuracy
- **V1 (Legacy)**: Original model, use `model: "legacy"` if needed for compatibility

The V2 model generally provides better predictions and should be used unless you have specific compatibility requirements.

### Troubleshooting

#### Common Issues and Solutions

**Problem**: Too many false positives

- **Solution**: Increase the `threshold` value (try 0.05 or 0.1)

**Problem**: Missing obvious local species

- **Solution**: Add the species to the "Always Include Species" list via **Settings → Species** in the web interface, or lower the `threshold` value if many species are missing

**Problem**: No location-based filtering occurring

- **Solution**: Verify `latitude` and `longitude` are set correctly (non-zero values)

**Problem**: Seasonal migrants not detected during migration

- **Solution**: Lower the `threshold` temporarily during migration periods, or add specific migrants to the "Always Include Species" list

**Problem**: Non-bird sounds being detected (dogs, sirens, etc.)

- **Solution**: Add these to the "Always Exclude Species" list via **Settings → Species**

## Advanced Features

### Web Dashboard

BirdNET-Go includes a web dashboard that provides visualization and management capabilities. The dashboard features:

- Summary views of detected species
- Recent detections display
- Optional thumbnails for visual identification
- Configurable display limits
- Images are automatically cached in the background to improve loading performance.

### Remote Internet Access

BirdNET-Go can be securely exposed to the internet, allowing you to monitor your birds from anywhere. The **recommended method** is using Cloudflare Tunnel (cloudflared), which provides:

- **Enhanced Security**: No need to open ports on your router/firewall
- **End-to-End Encryption**: All traffic is securely encrypted
- **Performance Benefits**: Static content like spectrograms and audio clips are cached on Cloudflare's global network
- **Simple Setup**: Works with any BirdNET-Go installation method (Docker, Docker Compose, or binary)

For detailed setup instructions and security best practices, see the dedicated [Cloudflare Tunnel Guide](cloudflare_tunnel_guide.md).

> **IMPORTANT SECURITY WARNING**: When exposing BirdNET-Go to the internet, always enable authentication through one of the available methods (Basic Auth, Google OAuth, or GitHub OAuth). Without authentication, anyone with your URL can access your system, delete your data, change settings, and view your location. See the [Authentication section](cloudflare_tunnel_guide.md#enabling-authentication) of the guide for details.

### Weather Integration

The application supports weather data integration from two providers:

- Yr.no (default)
- OpenWeather API (requires API key)

Weather data can be used to correlate bird activity with environmental conditions and is displayed in the dashboard.

### Audio Processing

BirdNET-Go offers advanced audio processing capabilities:

- Support for various audio sources including direct soundcard capture and RTSP streams
- Configurable equalizer with multiple filter types (LowPass, HighPass, BandPass, etc.)
- Audio export in multiple formats (WAV, MP3, FLAC)
- Retention policies for managing exported audio clips

### Audio Clip Retention

If you enable audio clip exporting (`realtime.audio.export.enabled: true`), BirdNET-Go can automatically manage disk space by deleting older recordings based on configured retention policies. This prevents your disk from filling up over time.

The cleanup task runs periodically to check if clips need to be deleted based on the selected policy. The check interval is configurable to balance between timely cleanup and system resource usage.

Configure these options under `realtime.audio.export.retention` in your `config.yaml`:

- **`policy`**: Sets the retention strategy. Options are:
  - **`none`** (Default): No automatic deletion. You are responsible for managing the clip files.
  - **`age`**: Deletes clips older than the specified `maxage`.
  - **`usage`**: Deletes the oldest clips _only when_ the disk usage of the partition containing the clips directory exceeds the `maxusage` percentage. This policy tries to keep at least `minclips` per species, deleting the oldest clips first when cleanup is needed.
- **`maxage`**: (Used with `policy: age`) Maximum age for clips (e.g., `30d` for 30 days, `7d` for 7 days, `24h` for 24 hours). Clips older than this will be deleted.
- **`maxusage`**: (Used with `policy: usage`) The target maximum disk usage percentage (e.g., `85%`). Cleanup triggers when usage exceeds this threshold.
- **`minclips`**: (Used with `policy: usage`) The minimum number of clips to keep for each species, even when cleaning up based on disk usage. This ensures you retain at least some recent examples per species.
- **`checkInterval`**: How often to check if cleanup is needed, in minutes (default: 15). Higher values reduce CPU/IO overhead but may delay cleanup. For usage-based policy, disk usage is checked first before scanning files, so setting this too low won't waste resources when disk usage is below threshold.

### Security Features

The application includes several security options:

- Basic authentication with password protection
- OAuth2 authentication through Google or GitHub
- Automatic TLS certificate management via Let's Encrypt
- IP subnet-based authentication bypass for local networks

#### Setting Up OAuth Authentication

BirdNET-Go supports OAuth2 authentication with Google and GitHub for secure access to your web interface. This is the recommended authentication method when exposing your instance to the internet.

##### Google OAuth Setup

1. **Create a Google Cloud Project**:
   - Go to the [Google Cloud Console](https://console.cloud.google.com/)
   - Create a new project or select an existing one
   - Enable the Google+ API or People API for your project

2. **Configure OAuth Consent Screen**:
   - Navigate to "APIs & Services" → "OAuth consent screen"
   - Choose "External" user type (unless you have a Google Workspace)
   - Fill in the required application information:
     - Application name: `BirdNET-Go`
     - User support email: Your email address
     - Developer contact information: Your email address
   - Add your domain to authorized domains if applicable
   - Save and continue through the scopes and test users sections

3. **Create OAuth Credentials**:
   - Navigate to "APIs & Services" → "Credentials"
   - Click "Create Credentials" → "OAuth client ID"
   - Select "Web application" as the application type
   - Set the name: `BirdNET-Go Web Client`
   - **Authorized redirect URIs**: Add your callback URL:
     - Format: `http://YOUR_HOST:PORT/auth/google/callback`
     - Example: `http://192.168.1.100:8080/auth/google/callback`
     - For internet access: `https://yourdomain.com/auth/google/callback`

4. **Configure BirdNET-Go**:
   - Copy the Client ID and Client Secret from Google
   - In your BirdNET-Go web interface, go to Settings → Security
   - Enable Google OAuth and enter:
     - **Client ID**: Your Google OAuth Client ID
     - **Client Secret**: Your Google OAuth Client Secret
     - **User ID** (optional): Restrict access to specific Google account by entering the user's email address

##### GitHub OAuth Setup

1. **Create a GitHub OAuth App**:
   - Go to GitHub Settings → Developer settings → OAuth Apps
   - Click "New OAuth App"
   - Fill in the application details:
     - **Application name**: `BirdNET-Go`
     - **Homepage URL**: `http://YOUR_HOST:PORT` (your BirdNET-Go URL)
     - **Authorization callback URL**: `http://YOUR_HOST:PORT/auth/github/callback`
       - Example: `http://192.168.1.100:8080/auth/github/callback`
       - For internet access: `https://yourdomain.com/auth/github/callback`
   - Click "Register application"

2. **Generate Client Secret**:
   - After creating the app, click "Generate a new client secret"
   - Copy both the Client ID and Client Secret immediately

3. **Configure BirdNET-Go**:
   - In your BirdNET-Go web interface, go to Settings → Security
   - Enable GitHub OAuth and enter:
     - **Client ID**: Your GitHub OAuth Client ID
     - **Client Secret**: Your GitHub OAuth Client Secret
     - **User ID** (optional): Restrict access to specific GitHub account by entering the username

##### OAuth Configuration Examples

**Configuration file example** (`config.yaml`):

```yaml
security:
  host: "yourdomain.com" # Your domain for HTTPS
  autotls: true # Enable automatic HTTPS certificates

  googleauth:
    enabled: true
    clientid: "123456789-abcdefghijklmnop.apps.googleusercontent.com"
    clientsecret: "GOCSPX-your-secret-key-here"
    userid: "user@gmail.com" # Optional: restrict to specific user

  githubauth:
    enabled: true
    clientid: "Ov23liABCDEFGHIJ1234"
    clientsecret: "your-github-secret-key-here"
    userid: "yourusername" # Optional: restrict to specific user
```

**Environment variables** (Docker):

```bash
BIRDNET_SECURITY_GOOGLEAUTH_ENABLED=true
BIRDNET_SECURITY_GOOGLEAUTH_CLIENTID=123456789-abcdefghijklmnop.apps.googleusercontent.com
BIRDNET_SECURITY_GOOGLEAUTH_CLIENTSECRET=GOCSPX-your-secret-key-here
BIRDNET_SECURITY_GOOGLEAUTH_USERID=user@gmail.com

BIRDNET_SECURITY_GITHUBAUTH_ENABLED=true
BIRDNET_SECURITY_GITHUBAUTH_CLIENTID=Ov23liABCDEFGHIJ1234
BIRDNET_SECURITY_GITHUBAUTH_CLIENTSECRET=your-github-secret-key-here
BIRDNET_SECURITY_GITHUBAUTH_USERID=yourusername
```

##### Important OAuth Notes

- **Callback URLs**: Always use the format `/auth/provider/callback` (e.g., `/auth/google/callback`, `/auth/github/callback`) as shown in the BirdNET-Go settings page
- **HTTPS Requirement**: OAuth providers typically require HTTPS for production use. Enable `autotls: true` or use a reverse proxy with SSL certificates
- **User ID Restrictions**: The optional `userid` field allows you to restrict access to a specific account for enhanced security
- **Local Network**: OAuth authentication works on local networks, but you can also enable subnet bypass for local access without OAuth
- **Multiple Providers**: You can enable both Google and GitHub OAuth simultaneously - users will see both options on the login page

##### Troubleshooting OAuth

**"Invalid redirect URI" errors**:

- Ensure your callback URL in the OAuth app configuration exactly matches the format shown in BirdNET-Go settings
- Check that the protocol (http/https) and port number are correct
- The callback URL should end with `/auth/google/callback` or `/auth/github/callback`

**"Access blocked" errors**:

- For Google OAuth: Ensure your app is verified or add your email to test users
- For GitHub OAuth: Verify the OAuth app is active and the callback URL is correct

**Login button not appearing**:

- Check that OAuth is enabled in BirdNET-Go settings
- Verify your client ID and client secret are correctly configured
- Check the browser console for JavaScript errors

### Filtering Capabilities

BirdNET-Go includes intelligent filtering mechanisms:

- Privacy filter to ignore human voices
- Dog bark filter to prevent misdetections when BirdNET confuses barking with owl/crow calls
- Species-specific inclusion and exclusion lists
- Dynamic threshold adjustment based on detection patterns

### Deep Detection

BirdNET-Go includes a "Deep Detection" feature designed to improve detection reliability and reduce false positives by requiring multiple detections of the same species within a time window.

#### Deep Detection Flow Chart

```mermaid
graph TD
    A[Species Detected<br/>by BirdNET] --> B{First Detection<br/>of This Species?}

    B -->|Yes| C[⏱️ Start 15-Second<br/>Counting Window<br/>Count = 1]
    B -->|No| D[📈 Increment Counter<br/>Count = Count + 1]

    C --> E[🎧 Continue Listening<br/>for More Audio]
    D --> E

    E --> F{Same Species<br/>Detected Again?}
    F -->|Yes| G[⬆️ Add to Count]
    F -->|No| H{15 Seconds<br/>Elapsed?}

    G --> H
    H -->|No| E
    H -->|Yes| I{Count ≥ Required<br/>Minimum?}

    I -->|No| J[❌ Rejected<br/>False Positive]
    I -->|Yes| K{Final Safety<br/>Checks Pass?}

    K -->|No| L[❌ Blocked by<br/>Privacy/Dog Filter]
    K -->|Yes| M[✅ Detection Approved<br/>Saved to Database]

    J --> N[🗂️ Log: Not enough matches]
    L --> O[🗂️ Log: Blocked by filter]
    M --> P[🗂️ Log: Species confirmed]
```

#### How Deep Detection Works

1. **Increased Analysis Frequency**: Higher `overlap` values reduce the step size between audio analysis windows (e.g., from 1.5 seconds to 300ms), causing the BirdNET AI model to run more frequently

2. **Pending Detection System**: All detections are held in memory for exactly **15 seconds** from the first detection

3. **Counting Mechanism**: During the 15-second window, each additional detection of the same species increments a counter

4. **Variable Threshold**: The number of required detections scales with the overlap setting:

   ```
   Required Detections = max(1, 3 / max(0.1, 3.0 - overlap))

   Examples:
   - overlap: 0.0 → 1 detection required (standard mode)
   - overlap: 2.4 → 5 detections required
   - overlap: 2.7 → 10 detections required (typical deep detection)
   - overlap: 2.9 → 30 detections required (very strict)
   ```

5. **Decision Point**: After exactly 15 seconds, the detection is either approved (if minimum count reached) or discarded as a false positive

#### Benefits and Use Cases

- **False Positive Reduction**: Eliminates single spurious detections that don't repeat
- **Lower Threshold Tolerance**: Allows using lower `birdnet.threshold` values (e.g., 0.3-0.6) while maintaining accuracy
- **Quality Selection**: Keeps the highest confidence detection from the 15-second window
- **Consistent Behavior**: All detections are held for exactly 15 seconds, providing predictable timing

#### System Requirements

- **CPU Load**: Significantly increases processing requirements due to higher analysis frequency
- **Recommended Hardware**: Raspberry Pi 4/5 or more powerful systems
- **Performance Monitoring**: Watch for `WARNING: BirdNET processing time exceeded buffer length` messages indicating the system cannot keep up

#### Configuration

- **Docker Installation**: Deep Detection is **enabled by default** with the `install.sh` script, which benchmarks your hardware and sets appropriate overlap values
- **Manual Configuration**: Set `birdnet.overlap` in your `config.yaml`:
  ```yaml
  birdnet:
    overlap: 2.7 # Enable deep detection (10 detections required)
    threshold: 0.5 # Can use lower thresholds with deep detection
  ```
- **Disabling**: Set `overlap: 0.0` for standard single-detection mode

#### Reference

[[GitHub Discussion #302](https://github.com/tphakala/birdnet-go/discussions/302)]

### Live Audio Streaming

BirdNET-Go allows you to listen to the live audio feed directly from the web interface. This is useful for monitoring the audio quality, checking microphone placement, or simply listening to the ambient sounds.

- **How to Use:**
  1.  Locate the microphone icon / audio level indicator in the web interface header.
  2.  Click the icon to open the audio source dropdown.
  3.  If you have multiple audio sources configured (e.g., a sound card and RTSP streams), select the source you wish to listen to.
  4.  Click the play icon (▶️) next to the source name.
  5.  Audio playback will begin using your browser's audio capabilities.
  6.  Click the stop icon (⏹️) to end the stream.
- **Technology:** The live stream uses HLS (HTTP Live Streaming) for broad browser compatibility and efficient delivery.
- **Dependency:** This feature requires **FFmpeg** to be installed and accessible by BirdNET-Go. If FFmpeg is not found, the play button may not appear or function.
- **Server Interaction:** Starting the live stream initiates audio encoding on the server. The stream uses a heartbeat mechanism to stay active while you are listening. Stopping the stream or closing the browser tab/window signals the server to stop the encoding process, conserving server resources.

### Sound Level Monitoring

BirdNET-Go includes an advanced sound level monitoring feature that provides detailed acoustic measurements of your environment in 1/3rd octave bands. This feature is particularly useful for:

- **Environmental noise monitoring**: Track ambient noise levels over time
- **Acoustic habitat assessment**: Understand the soundscape characteristics of your monitoring location
- **IoT integration**: Send detailed sound level data to smart home systems or environmental monitoring platforms
- **Research applications**: Collect standardized acoustic measurements for scientific studies

> **Note**: Sound level calculation supports both sound card and RTSP sources. The monitoring automatically registers processors for all configured audio sources and provides real-time measurements in 1/3rd octave bands.

#### How It Works

The sound level monitoring system:

1. **Analyzes audio in 1/3rd octave bands** following the ISO 266 standard (25 Hz to 20 kHz)
2. **Aggregates measurements over 10-second windows** to provide stable readings
3. **Calculates min/max/mean values** for each frequency band within the window
4. **Publishes data via multiple channels**: MQTT, SSE, and Prometheus metrics

##### Audio Processing Architecture

The sound level measurement system reads the raw audio directly from the source without any equalization or filtering applied. This ensures that measurements reflect the actual acoustic environment:

- **Raw Audio Input**: Audio samples are processed directly as received from the audio source (internal/myaudio/soundlevel.go:268-271)
- **Direct Band Analysis**: Raw samples are passed through octave band filters without any pre-processing or equalization (internal/myaudio/soundlevel.go:308-310)
- **Pure Measurement**: The octave band filters isolate specific frequency bands for measurement but do not apply any equalization - they simply measure the energy present in each frequency band (internal/myaudio/soundlevel.go:143-220)
- **Unmodified Output**: Sound level data is sanitized for JSON compatibility and published without any acoustic modifications (internal/analysis/sound_level.go)

This approach ensures that sound level measurements represent the true acoustic conditions at the monitoring location, which is essential for environmental monitoring, research applications, and compliance with acoustic measurement standards.

#### Configuration

Enable sound level monitoring in your `config.yaml`:

```yaml
realtime:
  audio:
    soundlevel:
      enabled: true # Enable sound level monitoring (default: false)
      interval: 10 # Measurement interval in seconds (default: 10)
      debug: false # Enable debug logging (default: false)
      debug_realtime_logging: false # Enable per-sample debug logs (default: false)
```

> **Note**: Sound level monitoring is disabled by default to avoid performance overhead. Enable it only if you need this functionality.

#### Data Format

Sound level data is published as JSON with the following structure:

```json
{
  "timestamp": "2024-01-15T08:30:45Z",
  "source": "USB Audio Device",
  "name": "Primary Microphone",
  "duration_seconds": 10,
  "octave_bands": {
    "1.0_kHz": {
      "center_frequency_hz": 1000,
      "min_db": -45.2,
      "max_db": -38.7,
      "mean_db": -42.1
    }
    // ... additional frequency bands ...
  }
}
```

#### Integration Examples

##### MQTT Integration

When MQTT is enabled, sound level data is published to the topic:

```
<base_topic>/soundlevel
```

The MQTT message uses a compact JSON format to minimize payload size:

```json
{
  "ts": "2024-01-15T08:30:45Z",
  "src": "USB Audio Device",
  "nm": "Primary Microphone",
  "dur": 10,
  "b": {
    "25.0_Hz": {
      "f": 25.0,
      "n": -85.2,
      "x": -78.7,
      "m": -82.1
    },
    "31.5_Hz": {
      "f": 31.5,
      "n": -83.5,
      "x": -76.2,
      "m": -80.3
    },
    "40.0_Hz": {
      "f": 40.0,
      "n": -81.7,
      "x": -74.5,
      "m": -78.6
    },
    "50.0_Hz": {
      "f": 50.0,
      "n": -79.8,
      "x": -72.3,
      "m": -76.4
    },
    "63.0_Hz": {
      "f": 63.0,
      "n": -77.6,
      "x": -70.1,
      "m": -74.2
    },
    "80.0_Hz": {
      "f": 80.0,
      "n": -75.3,
      "x": -67.8,
      "m": -71.9
    },
    "100.0_Hz": {
      "f": 100.0,
      "n": -72.9,
      "x": -65.4,
      "m": -69.5
    },
    "125.0_Hz": {
      "f": 125.0,
      "n": -70.4,
      "x": -62.9,
      "m": -67.0
    },
    "160.0_Hz": {
      "f": 160.0,
      "n": -67.8,
      "x": -60.3,
      "m": -64.4
    },
    "200.0_Hz": {
      "f": 200.0,
      "n": -65.1,
      "x": -57.6,
      "m": -61.7
    },
    "250.0_Hz": {
      "f": 250.0,
      "n": -62.3,
      "x": -54.8,
      "m": -58.9
    },
    "315.0_Hz": {
      "f": 315.0,
      "n": -59.4,
      "x": -51.9,
      "m": -56.0
    },
    "400.0_Hz": {
      "f": 400.0,
      "n": -56.4,
      "x": -48.9,
      "m": -53.0
    },
    "500.0_Hz": {
      "f": 500.0,
      "n": -53.3,
      "x": -45.8,
      "m": -49.9
    },
    "630.0_Hz": {
      "f": 630.0,
      "n": -50.1,
      "x": -42.6,
      "m": -46.7
    },
    "800.0_Hz": {
      "f": 800.0,
      "n": -46.8,
      "x": -39.3,
      "m": -43.4
    },
    "1.0_kHz": {
      "f": 1000.0,
      "n": -43.3,
      "x": -35.8,
      "m": -39.9
    },
    "1.25_kHz": {
      "f": 1250.0,
      "n": -39.7,
      "x": -32.2,
      "m": -36.3
    },
    "1.6_kHz": {
      "f": 1600.0,
      "n": -36.0,
      "x": -28.5,
      "m": -32.6
    },
    "2.0_kHz": {
      "f": 2000.0,
      "n": -32.2,
      "x": -24.7,
      "m": -28.8
    },
    "2.5_kHz": {
      "f": 2500.0,
      "n": -28.3,
      "x": -20.8,
      "m": -24.9
    },
    "3.15_kHz": {
      "f": 3150.0,
      "n": -24.2,
      "x": -16.7,
      "m": -20.8
    },
    "4.0_kHz": {
      "f": 4000.0,
      "n": -20.0,
      "x": -12.5,
      "m": -16.6
    },
    "5.0_kHz": {
      "f": 5000.0,
      "n": -15.7,
      "x": -8.2,
      "m": -12.3
    },
    "6.3_kHz": {
      "f": 6300.0,
      "n": -11.2,
      "x": -3.7,
      "m": -7.8
    },
    "8.0_kHz": {
      "f": 8000.0,
      "n": -6.6,
      "x": 0.9,
      "m": -3.2
    },
    "10.0_kHz": {
      "f": 10000.0,
      "n": -1.8,
      "x": 5.7,
      "m": 1.6
    },
    "12.5_kHz": {
      "f": 12500.0,
      "n": 3.1,
      "x": 10.6,
      "m": 6.5
    },
    "16.0_kHz": {
      "f": 16000.0,
      "n": 8.1,
      "x": 15.6,
      "m": 11.5
    },
    "20.0_kHz": {
      "f": 20000.0,
      "n": 13.3,
      "x": 20.8,
      "m": 16.7
    }
  }
}
```

**Field Reference:**

- `ts`: ISO8601 timestamp
- `src`: Audio source identifier
- `nm`: Human-readable name of the source
- `dur`: Measurement duration in seconds
- `b`: Octave bands object containing measurements for each frequency band
  - Band key format: `<frequency>_<unit>` (e.g., "1.0_kHz", "250.0_Hz")
  - `f`: Center frequency in Hz
  - `n`: Minimum dB level (1 decimal place)
  - `x`: Maximum dB level (1 decimal place)
  - `m`: Mean/average dB level (1 decimal place)

Example Home Assistant configuration:

```yaml
sensor:
  - platform: mqtt
    name: "Bird Station Sound Level 1kHz"
    state_topic: "birdnet/soundlevel"
    value_template: "{{ value_json.b['1.0_kHz'].m }}"
    unit_of_measurement: "dB"
    device_class: "sound_pressure"
    state_class: "measurement"
```

##### SSE Streaming

Access real-time sound level data via the SSE endpoint:

```
GET /api/v2/soundlevels/stream
```

##### Prometheus Metrics

Sound level data is exposed as Prometheus metrics:

- `birdnet_sound_level_db`: Current sound level for each octave band
- `birdnet_sound_level_processing_duration_seconds`: Processing time histogram
- `birdnet_sound_level_publishing_total`: Publishing success/error counters

#### Performance Considerations

- **CPU Usage**: Sound level analysis adds approximately 5-10% CPU overhead on a Raspberry Pi 4
- **Memory**: Minimal additional memory usage (< 10MB)
- **Network**: Each 10-second measurement produces ~2KB of JSON data per source

#### Use Cases

1. **Environmental Monitoring**: Track noise pollution levels, identify quiet periods for optimal bird detection
2. **Smart Home Integration**: Trigger actions based on ambient noise levels
3. **Research Applications**: Collect standardized acoustic measurements alongside bird detection data
4. **System Diagnostics**: Monitor microphone performance and environmental conditions

#### Technical Details for Advanced Users

##### Digital Signal Processing

The sound level monitoring system implements professional-grade signal processing:

1. **1/3rd Octave Band Filtering**:
   - Implements 30 frequency bands according to ISO 266 standard
   - Center frequencies: 25 Hz to 20 kHz in standard 1/3rd octave steps
   - Uses 2nd order IIR biquad filters based on Robert Bristow-Johnson's audio EQ cookbook
   - Q factor calculation: `Q = f_center / (f_high - f_low)` ≈ 4.318 for 1/3rd octave bands
   - Includes stability checks and numerical overflow protection

2. **RMS and dB Calculation**:
   - RMS (Root Mean Square) calculation over 1-second windows
   - dB conversion: `20 * log10(RMS)` relative to digital full scale
   - Range clamping: -200 dB to +20 dB
   - Non-finite value protection (NaN/Inf handling)

3. **Data Aggregation**:
   - Two-stage aggregation: 1-second measurements → 10-second statistics
   - Provides min/max/mean for each frequency band
   - Continuous processing with sample overflow handling

##### Implementation Architecture

- **Modular Design**: Separate processor instances per audio source
- **Non-blocking Architecture**: Audio processing never blocks capture
- **Channel-based Communication**: 100-element buffered channels
- **Concurrent Publishing**: MQTT, SSE, and metrics updated independently
- **Comprehensive Error Handling**: Graceful degradation on errors

##### Current Limitations

> **Important**: The sound level monitoring system has several limitations that users should be aware of:

1. **No Absolute Calibration**:
   - Measurements are **relative only** (not calibrated to dB SPL)
   - Cannot provide absolute sound pressure levels
   - Useful for relative comparisons and trend analysis, not absolute measurements

2. **No Frequency Weighting**:
   - Provides unweighted (linear) frequency response
   - No A-weighting or C-weighting curves implemented
   - May not correlate directly with perceived loudness

3. **Fixed Aggregation Windows**:
   - Hardcoded 10-second measurement periods
   - Cannot adjust for different temporal resolutions
   - The `interval` setting validates but doesn't change window size

4. **Limited Statistical Analysis**:
   - Only provides min/max/mean values
   - No percentiles (L10, L50, L90) commonly used in environmental noise assessment
   - No peak detection or peak hold functionality

5. **Hardware Dependencies**:
   - Assumes 16-bit audio at system sample rate (typically 48 kHz)
   - Frequency bands above Nyquist frequency (sample_rate/2) are automatically excluded
   - No compensation for microphone frequency response

##### Debug Options

For troubleshooting or detailed analysis:

```yaml
realtime:
  audio:
    soundlevel:
      debug: true # Enable debug logging
      debug_realtime_logging: true # Enable per-sample logging (very verbose!)
```

Debug logs are written to `logs/soundlevel.log`. Use `debug_realtime_logging` sparingly as it generates high log volume.

##### Extending the System

Advanced users interested in extending the sound level monitoring capabilities should note:

1. **Adding Frequency Weighting**: Could be implemented as post-RMS filter curves
2. **Calibration Support**: Would require known reference signals and microphone sensitivity data
3. **Additional Statistics**: Percentile tracking could be added to the aggregation system
4. **Variable Time Windows**: The 10-second window is currently hardcoded but could be made configurable
5. **Peak Detection**: True peak tracking would require additional buffer management

The implementation provides a solid foundation for environmental sound monitoring with robust signal processing and comprehensive error handling. While it cannot provide absolute SPL measurements, it excels at relative sound level monitoring and frequency analysis for research and environmental assessment purposes.

### Push Notifications

BirdNET-Go includes a comprehensive push notification system that can send real-time alerts about bird detections, system errors, and important events to your preferred notification services. This feature enables you to stay informed about what's happening at your monitoring station even when you're away from the web interface.

#### Overview

The push notification system supports multiple delivery methods (providers) and can be configured to send different types of notifications to different services based on priority, type, or custom filters. Each provider operates independently with built-in resilience features like automatic retries, circuit breakers, and rate limiting.

#### Configuring Notification URLs

Push notifications for new bird detections include clickable links to view the detection details in the web interface. To ensure these URLs work correctly when accessing BirdNET-Go through a reverse proxy or from remote locations, you need to configure the hostname:

**Configuration Methods (in priority order):**

1. **Config file** - Set `security.host` in your `config.yaml`:

   ```yaml
   security:
     host: "birdnet.home.arpa" # Your public hostname
   ```

2. **Environment variable** - Set `BIRDNET_HOST` (useful for Docker):

   ```bash
   export BIRDNET_HOST=birdnet.home.arpa
   # or with Docker:
   docker run -e BIRDNET_HOST=birdnet.home.arpa tphakala/birdnet-go
   ```

3. **Localhost fallback** - If neither is set, URLs will use `localhost` (works only for direct local access)

**Docker Compose Example:**

```yaml
services:
  birdnet-go:
    image: tphakala/birdnet-go:nightly
    environment:
      - BIRDNET_HOST=birdnet.home.arpa # Set your hostname here
      - TZ=US/Eastern
    # ... rest of configuration
```

**Why This Matters:**

Without proper hostname configuration, notification URLs will show as `http://localhost:8080/ui/detections/12345`, which won't work when clicked from a phone or remote device. With the hostname configured, URLs will correctly show as `http://birdnet.home.arpa/ui/detections/12345`.

> **Note**: A warning will be logged if BirdNET-Go falls back to using localhost for notification URLs. Configure the hostname using either method above to resolve this warning.

#### Supported Providers

BirdNET-Go supports three types of push notification providers:

##### 1. Shoutrrr (Multi-Service)

The [Shoutrrr](https://containrrr.dev/shoutrrr/) provider supports 20+ notification services through a unified URL format, including:

- **Messaging Apps**: Telegram, Discord, Slack, Matrix, Mattermost, Zulip
- **Push Services**: Pushover, Pushbullet, Ntfy
- **Email**: SMTP, SendGrid, Mailgun
- **Smart Home**: Home Assistant, Gotify
- **Incident Management**: Opsgenie, PagerDuty
- **Voice**: Bark (iOS)

**Configuration Example:**

```yaml
notification:
  push:
    enabled: true
    default_timeout: 30s
    max_retries: 3
    retry_delay: 5s

    providers:
      - type: shoutrrr
        enabled: true
        name: "telegram-alerts"
        urls:
          - "telegram://<YOUR_BOT_TOKEN>@telegram?chats=<YOUR_CHAT_ID>"
        timeout: 10s
        filter:
          types: ["error", "detection"]
          priorities: ["critical", "high"]
```

**Shoutrrr URL Format:**

Each service has its own URL format. Common examples:

- **Telegram**: `telegram://<bot_token>@telegram?chats=<chat_id>`
- **Discord**: `discord://<webhook_token>@<webhook_id>`
- **Slack**: `slack://token-a/token-b/token-c`
- **Email**: `smtp://username:password@host:port/?from=sender@example.com&to=recipient@example.com`
- **Pushover**: `pushover://shoutrrr:<api_token>@<user_key>`

For complete URL format documentation, see the [Shoutrrr documentation](https://containrrr.dev/shoutrrr/v0.8/services/overview/).

##### 2. Webhook (Custom HTTP)

The webhook provider sends notifications as HTTP requests to custom endpoints, ideal for integrating with your own services, APIs, or automation platforms.

**Features:**

- Supports POST, PUT, and PATCH methods
- Multiple authentication types (Bearer, Basic, Custom headers)
- Custom JSON templates
- Multiple endpoints with failover
- Secure secret management (environment variables and files)

**Configuration Example:**

```yaml
notification:
  push:
    providers:
      - type: webhook
        enabled: true
        name: "api-service"
        endpoints:
          - url: "https://api.example.com/webhooks/birdnet"
            method: POST
            timeout: 10s
            headers:
              Content-Type: "application/json"
            auth:
              type: bearer
              token: "${API_TOKEN}" # Reads from environment variable
        filter:
          types: ["detection"]
          metadata_filters:
            confidence: ">0.8"
```

**Authentication Types:**

1. **Bearer Token** (recommended for most APIs):

```yaml
auth:
  type: bearer
  token: "${API_TOKEN}" # From environment variable
  # OR
  token_file: "/run/secrets/api_token" # From file (Kubernetes/Docker Swarm)
```

2. **Basic Authentication**:

```yaml
auth:
  type: basic
  user: "${API_USER}"
  pass: "${API_PASSWORD}"
```

3. **Custom Header**:

```yaml
auth:
  type: custom
  header: "X-API-Key"
  value: "${API_KEY}"
```

**Custom JSON Template:**

You can customize the JSON payload sent to your webhook:

```yaml
template: |
  {
    "event": "{{.Type}}",
    "severity": "{{.Priority}}",
    "bird": "{{.Title}}",
    "details": "{{.Message}}",
    "time": "{{.Timestamp}}",
    "confidence": {{.Metadata.confidence}}
  }
```

Available template fields:

- `{{.ID}}` - Notification unique ID
- `{{.Type}}` - Notification type (error, warning, info, detection, system)
- `{{.Priority}}` - Priority level (critical, high, medium, low)
- `{{.Title}}` - Notification title
- `{{.Message}}` - Notification message
- `{{.Component}}` - Component that generated the notification
- `{{.Timestamp}}` - ISO 8601 timestamp
- `{{.Metadata.key}}` - Any metadata field (e.g., confidence, species)

##### 3. Script (Custom Scripts)

The script provider executes custom shell scripts or programs, allowing complete control over notification handling. Perfect for custom integrations, logging, or triggering actions based on detections.

**Configuration Example:**

```yaml
notification:
  push:
    providers:
      - type: script
        enabled: true
        name: "custom-handler"
        command: "/usr/local/bin/notify.sh"
        args: ["--mode", "production"]
        environment:
          SLACK_WEBHOOK: "${SLACK_WEBHOOK_URL}"
          LOG_PATH: "/var/log/birdnet"
        input_format: both # "json", "env", or "both"
        filter:
          types: ["detection"]
          priorities: ["high", "critical"]
```

**Input Formats:**

- **`env`**: Data passed only through environment variables
- **`json`**: Data passed as JSON on stdin
- **`both`**: Data passed via both environment variables and JSON stdin

**Environment Variables Provided:**

When your script runs, these variables are automatically set:

- `NOTIFICATION_ID` - Unique identifier
- `NOTIFICATION_TYPE` - error, warning, info, detection, or system
- `NOTIFICATION_PRIORITY` - critical, high, medium, or low
- `NOTIFICATION_TITLE` - Notification title
- `NOTIFICATION_MESSAGE` - Notification message
- `NOTIFICATION_COMPONENT` - Source component
- `NOTIFICATION_TIMESTAMP` - ISO 8601 timestamp
- `NOTIFICATION_METADATA_JSON` - JSON string of metadata

**Example Script:**

```bash
#!/bin/bash
# notify.sh - Example notification handler

# Read environment variables
TYPE="$NOTIFICATION_TYPE"
TITLE="$NOTIFICATION_TITLE"
MESSAGE="$NOTIFICATION_MESSAGE"

# Read JSON from stdin if input_format is "json" or "both"
if [ "$INPUT_FORMAT" != "env" ]; then
    JSON=$(cat)
    # Parse JSON with jq if available
    CONFIDENCE=$(echo "$JSON" | jq -r '.metadata.confidence // "N/A"')
fi

# Custom logic based on notification type
case "$TYPE" in
    detection)
        echo "[$(date)] Bird detected: $TITLE (confidence: $CONFIDENCE)" >> /var/log/birds.log
        # Send to custom service
        curl -X POST "$SLACK_WEBHOOK" -d "{\"text\":\"$TITLE detected!\"}"
        ;;
    error)
        echo "[$(date)] ERROR: $MESSAGE" >> /var/log/errors.log
        # Send alert
        ;;
esac

exit 0
```

#### Notification Filters

Filters control which notifications are sent to each provider. You can filter by type, priority, component, or custom metadata fields.

##### Filter by Type

Limit notifications to specific types:

```yaml
filter:
  types: ["error", "detection"] # Only errors and detections
```

Available types:

- `error` - System errors and failures
- `warning` - Warnings and potential issues
- `info` - Informational messages
- `detection` - Bird detection events
- `system` - System status changes

##### Filter by Priority

Limit notifications to specific priority levels:

```yaml
filter:
  priorities: ["critical", "high"] # Only urgent notifications
```

Available priorities:

- `critical` - Immediate action required
- `high` - Important but not urgent
- `medium` - Normal priority
- `low` - Informational only

##### Filter by Component

Limit notifications from specific system components:

```yaml
filter:
  components: ["birdnet", "audio"] # Only BirdNET and audio components
```

##### Filter by Metadata

Filter based on notification metadata, including confidence thresholds for bird detections:

```yaml
filter:
  metadata_filters:
    confidence: ">0.8" # Only high-confidence detections
    species: "Northern Cardinal" # Only specific species
```

**Confidence Operators:**

- `>` - Greater than (e.g., `">0.8"`)
- `>=` - Greater than or equal to (e.g., `">=0.75"`)
- `<` - Less than (e.g., `"<0.5"`)
- `<=` - Less than or equal to (e.g., `"<=0.6"`)
- `=` or `==` - Equal to (e.g., `"=0.9"`)

**Example: High-Confidence Rare Species Alerts:**

```yaml
filter:
  types: ["detection"]
  priorities: ["high", "critical"]
  metadata_filters:
    confidence: ">=0.85" # Only 85%+ confidence
```

#### Advanced Configuration

##### Circuit Breaker

The circuit breaker automatically disables failing providers temporarily to prevent cascading failures:

```yaml
notification:
  push:
    circuit_breaker:
      enabled: true
      max_failures: 5 # Failures before circuit opens
      timeout: 30s # Time before retry attempt
      half_open_max_requests: 1 # Test requests in half-open state
```

**How it works:**

1. **Closed** (normal): All requests pass through
2. **Open** (after max_failures): All requests blocked
3. **Half-Open** (after timeout): Limited test requests allowed
4. **Returns to Closed**: If test requests succeed

##### Health Checks

Periodic health checks verify provider availability:

```yaml
notification:
  push:
    health_check:
      enabled: true
      interval: 60s # Check every minute
      timeout: 10s # Health check timeout
```

##### Rate Limiting

Prevents overwhelming external APIs with too many requests:

```yaml
notification:
  push:
    rate_limiting:
      enabled: true
      requests_per_minute: 60 # Average request rate
      burst_size: 10 # Maximum burst capacity
```

> **Note**: Rate limiting is disabled by default. Circuit breakers usually provide sufficient protection.

#### Complete Configuration Example

Here's a complete example showing multiple providers with different filters:

```yaml
notification:
  push:
    enabled: true
    default_timeout: 30s
    max_retries: 3
    retry_delay: 5s

    # Protection features
    circuit_breaker:
      enabled: true
      max_failures: 5
      timeout: 30s
      half_open_max_requests: 1

    health_check:
      enabled: true
      interval: 60s
      timeout: 10s

    rate_limiting:
      enabled: false # Use circuit breakers instead

    providers:
      # Telegram: Critical errors and high-confidence detections
      - type: shoutrrr
        enabled: true
        name: "telegram-critical"
        urls:
          - "telegram://${TELEGRAM_BOT_TOKEN}@telegram?chats=${TELEGRAM_CHAT_ID}"
        timeout: 10s
        filter:
          types: ["error", "detection"]
          priorities: ["critical", "high"]
          metadata_filters:
            confidence: ">0.9"

      # Webhook: All detections for data analysis
      - type: webhook
        enabled: true
        name: "analysis-api"
        endpoints:
          - url: "https://api.example.com/birds"
            auth:
              type: bearer
              token: "${API_TOKEN}"
        filter:
          types: ["detection"]

      # Script: Rare species alerts with custom handling
      - type: script
        enabled: true
        name: "rare-bird-alert"
        command: "/usr/local/bin/rare_bird_notify.sh"
        input_format: both
        filter:
          types: ["detection"]
          priorities: ["high", "critical"]
          metadata_filters:
            confidence: ">=0.85"

      # Discord: System status updates
      - type: shoutrrr
        enabled: true
        name: "discord-status"
        urls:
          - "discord://${DISCORD_WEBHOOK_TOKEN}@${DISCORD_WEBHOOK_ID}"
        filter:
          types: ["system", "warning"]
```

#### Security Best Practices

**1. Never Commit Secrets**

Always use environment variables or secret files for sensitive data:

```yaml
# ❌ NEVER do this (hardcoded secret)
token: "123456789:ABCdefGHIjklMNOpqrsTUVwxyz"

# ✅ DO this (environment variable)
token: "${TELEGRAM_BOT_TOKEN}"

# ✅ OR this (secret file)
token_file: "/run/secrets/telegram_token"
```

**2. Environment Variables**

Set environment variables when running BirdNET-Go:

```bash
# Docker
docker run -e TELEGRAM_BOT_TOKEN="your-token" birdnet-go

# Docker Compose
echo "TELEGRAM_BOT_TOKEN=your-token" > .env

# Binary
export TELEGRAM_BOT_TOKEN="your-token"
./birdnet-go realtime
```

**3. Secret Files (Kubernetes/Docker Swarm)**

For orchestrated deployments, mount secrets as files:

```yaml
# Kubernetes
apiVersion: v1
kind: Secret
metadata:
  name: birdnet-secrets
stringData:
  telegram-token: "your-token"

# Then reference in config
token_file: "/run/secrets/telegram-token"
```

**4. File Permissions**

Ensure secret files have restrictive permissions:

```bash
chmod 0400 /path/to/secret  # Read-only for owner
```

#### Troubleshooting

##### Common Issues

**Notifications Not Sending:**

1. Check provider is enabled: `enabled: true`
2. Verify filter settings aren't too restrictive
3. Check logs for error messages
4. Test authentication credentials
5. Verify network connectivity

**Circuit Breaker Blocking Requests:**

- Circuit breaker opens after repeated failures
- Wait for timeout period (default 30s)
- Check provider configuration and credentials
- Review logs for underlying errors

**Shoutrrr URL Errors:**

- Verify URL format matches service documentation
- Test URL with `shoutrrr send` CLI tool
- Check special characters are properly encoded
- Ensure bot tokens and IDs are correct

**Script Provider Not Working:**

1. Verify script has execute permissions: `chmod +x script.sh`
2. Check script path is absolute
3. Test script manually with environment variables
4. Review script exit codes (0 = success)
5. Check script logs/output

**Webhook Authentication Failing:**

- Verify environment variables are set correctly
- Check secret file paths and permissions
- Test webhook endpoint with curl:
  ```bash
  curl -H "Authorization: Bearer YOUR_TOKEN" \
       -X POST https://api.example.com/webhook \
       -d '{"test": true}'
  ```
- Review API provider documentation

##### Debug Logging

Enable debug logging to troubleshoot issues:

```yaml
debug: true # Enable global debug logging

notification:
  push:
    enabled: true
    # ... rest of config
```

Debug logs will show:

- Provider initialization
- Filter evaluation decisions
- Circuit breaker state changes
- Retry attempts
- Detailed error messages

#### Use Case Examples

##### 1. Rare Species Alerts to Phone

Send immediate alerts for rare species with high confidence:

```yaml
- type: shoutrrr
  enabled: true
  name: "rare-bird-phone"
  urls:
    - "pushover://shoutrrr:${PUSHOVER_TOKEN}@${PUSHOVER_USER}"
  filter:
    types: ["detection"]
    priorities: ["high", "critical"]
    metadata_filters:
      confidence: ">0.85"
```

##### 2. Error Monitoring to Team Chat

Send system errors to team Slack channel:

```yaml
- type: shoutrrr
  enabled: true
  name: "team-slack"
  urls:
    - "slack://${SLACK_TOKEN_A}/${SLACK_TOKEN_B}/${SLACK_TOKEN_C}"
  filter:
    types: ["error", "warning"]
    priorities: ["critical", "high"]
```

##### 3. Data Pipeline Integration

Send all detections to data analysis API:

```yaml
- type: webhook
  enabled: true
  name: "data-pipeline"
  endpoints:
    - url: "https://data.example.com/ingest"
      auth:
        type: bearer
        token: "${DATA_API_TOKEN}"
  filter:
    types: ["detection"]
```

##### 4. Custom Home Automation

Trigger custom actions via script (turn on lights, play sounds, etc.):

```yaml
- type: script
  enabled: true
  name: "home-automation"
  command: "/home/automation/bird_detected.sh"
  input_format: env
  filter:
    types: ["detection"]
    metadata_filters:
      confidence: ">0.75"
```

### Species Tracking System

BirdNET-Go includes an intelligent species tracking system that helps you discover and monitor bird activity patterns at your location. This feature automatically tracks when new bird species appear and highlights them with special badges to make discoveries easy to spot.

#### How Species Tracking Works

The species tracking system runs automatically in the background, analyzing each bird detection and comparing it against your historical data. When BirdNET-Go detects a bird species that hasn't been seen recently (or ever), it adds special badges to help you notice these exciting discoveries.

#### Types of Species Tracking

The system tracks three different types of "new" species appearances:

##### 🌟 **New Species** (Lifetime First)

- **What it means**: A bird species detected for the very first time at your location
- **Visual indicator**: Animated golden star that gently wiggles to catch your attention
- **When shown**: For 7 days after the first detection (configurable via `newSpeciesWindowDays`)
- **Perfect for**: Discovering birds that have never visited your area before, tracking range expansions, or celebrating truly rare visitors

##### 📅 **New This Year** (Annual First)

- **What it means**: A bird species detected for the first time this calendar year
- **Visual indicator**: Blue calendar icon
- **When shown**: For 7 days after the first detection of the year (configurable via `yearlyTracking.windowDays`)
- **Resets**: January 1st each year (configurable via `yearlyTracking.resetMonth` and `resetDay`)
- **Perfect for**: Tracking yearly visitors, seasonal patterns, and migration timing

##### 🌿 **New This Season** (Seasonal First)

- **What it means**: A bird species detected for the first time this season
- **Visual indicator**: Green leaf icon
- **When shown**: For 7 days after the first seasonal detection (configurable via `seasonalTracking.windowDays`)
- **Seasons**: Spring (March 20), Summer (June 21), Fall (September 22), Winter (December 21) - automatically adjusted for hemisphere
- **Perfect for**: Monitoring seasonal migrations, breeding arrivals, and wintering species

> **Smart Hemisphere Detection**: The system automatically adjusts seasonal definitions based on your latitude - if you're in the Southern Hemisphere, the seasons are flipped appropriately.

#### Where You'll See the Badges

**Dashboard Summary**: The badges appear next to species names in your daily detection summary on the main dashboard. Simply look for the animated star, calendar, or leaf icons.

**Real-time Updates**: New badges appear immediately when species are detected - no need to refresh the page.

**Tooltips**: Hover over any badge to see detailed information like "New species (first seen 2 days ago)" or "First time this spring (3 days ago)".

#### Badge Priority System

When a species qualifies for multiple badges (for example, a bird that's both new this year AND new this season), the system shows only the most significant badge:

1. **🌟 New Species** (highest priority - truly first-time visitors)
2. **📅 New This Year** (medium priority - annual firsts)
3. **🌿 New This Season** (lowest priority - seasonal appearances)

#### Configuration Options

The species tracking system is enabled by default with sensible settings. You can customize it through the web interface settings or directly in your `config.yaml` file under `realtime.speciesTracking`:

##### Main Tracking Settings

- **Enable/Disable** (`enabled`): Turn the entire tracking system on or off (default: `true`)
- **New Species Window** (`newSpeciesWindowDays`): How many days to display the 🌟 "New Species" badge after a bird is detected for the first time ever at your location (default: `7` days)
- **Database Sync Interval** (`syncIntervalMinutes`): How often the system checks the database for historical data and updates tracking information (default: `60` minutes)

##### Yearly Tracking Settings

Configure how the system tracks first arrivals each calendar year:

- **Enable Yearly Tracking** (`yearlyTracking.enabled`): Turn yearly tracking on/off (default: `true`)
- **Reset Date** (`yearlyTracking.resetMonth` and `resetDay`): When to reset the yearly list
  - Default: January 1st (`resetMonth: 1`, `resetDay: 1`)
  - Example: For a June-to-May year, set `resetMonth: 6`, `resetDay: 1`
- **Badge Display Window** (`yearlyTracking.windowDays`): How many days to show the 📅 "New This Year" badge (default: `7` days)

##### Seasonal Tracking Settings

Configure how the system tracks first arrivals each season:

- **Enable Seasonal Tracking** (`seasonalTracking.enabled`): Turn seasonal tracking on/off (default: `true`)
- **Badge Display Window** (`seasonalTracking.windowDays`): How many days to show the 🌿 "New This Season" badge (default: `7` days)
- **Season Definitions** (`seasonalTracking.seasons`): Customize when each season begins
  - **Automatic Hemisphere Detection**: If you don't specify custom seasons, the system automatically adjusts based on your latitude:
    - Northern Hemisphere (latitude > 10°): Spring starts March 20, Summer June 21, etc.
    - Southern Hemisphere (latitude < -10°): Seasons are shifted by 6 months
    - Equatorial regions (latitude -10° to 10°): Uses wet/dry season patterns
  - **Custom Seasons**: You can define your own seasonal boundaries to match local conditions:
    ```yaml
    seasons:
      spring:
        startMonth: 3 # March
        startDay: 20 # Day 20
      # Add more seasons as needed
    ```

##### Time Period Examples

Here are some common configuration scenarios:

**Default settings** (balanced for most users):

```yaml
speciesTracking:
  newSpeciesWindowDays: 7 # Show new species badges for a week
  yearlyTracking:
    windowDays: 7 # Show yearly badges for a week
  seasonalTracking:
    windowDays: 7 # Show seasonal badges for a week
```

**Extended visibility** (for users who check less frequently):

```yaml
speciesTracking:
  newSpeciesWindowDays: 14 # Show new species badges for 2 weeks
  yearlyTracking:
    windowDays: 14 # Show yearly badges for 2 weeks
  seasonalTracking:
    windowDays: 14 # Show seasonal badges for 2 weeks
  syncIntervalMinutes: 30 # Check database more frequently
```

**Research/documentation focus** (longer retention for rare events):

```yaml
speciesTracking:
  newSpeciesWindowDays: 30 # Show lifetime firsts for a month
  yearlyTracking:
    windowDays: 21 # Show yearly firsts for 3 weeks
  seasonalTracking:
    windowDays: 14 # Show seasonal firsts for 2 weeks
```

**Custom birding year** (October to September for fall migration focus):

```yaml
speciesTracking:
  yearlyTracking:
    resetMonth: 10 # Reset on October 1st
    resetDay: 1
    windowDays: 14 # Extended visibility during migration
```

**Tropical/equatorial regions** (custom wet/dry seasons):

```yaml
speciesTracking:
  seasonalTracking:
    seasons:
      wet:
        startMonth: 4 # April - start of wet season
        startDay: 1
      dry:
        startMonth: 10 # October - start of dry season
        startDay: 1
```

#### Practical Benefits

**For Casual Birdwatchers**:

- Never miss when a new species visits your yard
- Easily spot seasonal patterns in bird activity
- Get excited about first-of-the-year sightings

**For Serious Birders**:

- Track migration timing and patterns
- Monitor range expansions and climate-related shifts
- Document seasonal abundance changes
- Build comprehensive species lists for your location

**For Researchers**:

- Collect systematic data on species occurrence patterns
- Monitor long-term changes in bird communities
- Track phenological shifts in migration and breeding timing

#### Tips for Best Results

1. **Give it Time**: The system becomes more useful after running for several weeks or months to build up historical data
2. **Stable Location**: Best results come from monitoring a consistent location over time
3. **Check Regularly**: Visit your dashboard daily during migration seasons to catch the most exciting discoveries
4. **Seasonal Awareness**: Pay extra attention during spring and fall migrations when new species are most likely to appear

The species tracking system transforms your BirdNET-Go installation from a simple detector into an intelligent monitoring system that helps you understand the changing patterns of bird life at your location throughout the year.

### Integration Options

The application offers several integration points:

- **Server-Sent Events (SSE) API** for real-time detection streaming.
  - Provides live bird detection data as it happens
  - Compatible with any programming language or platform that supports SSE
  - Includes species metadata, confidence scores, and thumbnail images
  - No authentication required for read-only access
  - Perfect for building custom dashboards, mobile apps, or integration with other systems

* MQTT support for IoT ecosystems.
  - The `retain` flag in MQTT settings is recommended for Home Assistant integration to ensure sensor states are preserved across restarts.
* Telemetry endpoint compatible with Prometheus.
* BirdWeather API integration for community data sharing.
  - **About BirdWeather:** [BirdWeather.com](https://www.birdweather.com/) is a citizen science platform that collects bird vocalizations from stations around the world. It uses the BirdNET model (developed by Cornell Lab of Ornithology and Chemnitz University of Technology) for identification. Uploading data helps contribute to this global library.
  - **Getting a BirdWeather ID/Token:** To upload data, you need an ID (also referred to as a Token). This process is now automated:
    1. Create an account at [app.birdweather.com/login](https://app.birdweather.com/login).
    2. Go to your account's station page: [app.birdweather.com/account/stations](https://app.birdweather.com/account/stations).
    3. Create a new station, ensuring the Latitude and Longitude match your BirdNET-Go configuration (`birdnet.latitude` and `birdnet.longitude`).
    4. Copy the generated station ID/Token into the `realtime.birdweather.id` field in your BirdNET-Go configuration.
  - **Data Sharing Consent:** By configuring and enabling BirdWeather uploads with your ID/Token, you consent to sharing your soundscape snippets and detection data with BirdWeather.
* Custom actions that can be triggered on species detection.
* Built-in connection testers (via Web UI) for BirdWeather and MQTT to verify configuration.
  - The testers perform multi-stage checks (connectivity, authentication, test uploads/publishes) and provide feedback, including troubleshooting hints and rate limit information (for BirdWeather).

## Real-time Detection API (Server-Sent Events)

BirdNET-Go provides a Server-Sent Events (SSE) API that streams bird detections in real-time as they happen. This allows you to build custom applications, dashboards, or integrations that react immediately to new bird detections.

### Authentication Policy

**The SSE API endpoints are intentionally designed as public APIs with no authentication requirement.** This design choice enables:

- Easy integration with third-party applications and services
- Simple development and testing of custom clients
- Compatibility with embedded systems and IoT devices
- Reduced complexity for read-only access to detection data

The endpoints include built-in rate limiting (10 requests per minute per IP) to prevent abuse while maintaining open access.

> **🔒 Need Authentication?** If you require password protection for the detection stream API, please file a feature request by creating a GitHub issue at [https://github.com/tphakala/birdnet-go/issues](https://github.com/tphakala/birdnet-go/issues). Include your specific use case and security requirements to help guide the implementation.

### API Endpoints

#### Detection Stream Endpoint

**URL:** `GET /api/v2/detections/stream`
**Authentication:** None required (public endpoint)
**Rate Limiting:** 10 connections per minute per IP address

The SSE stream sends different types of events:

#### 1. Connection Event

Sent immediately when a client connects to confirm the connection is established.

```json
{
  "clientId": "550e8400-e29b-41d4-a716-446655440000",
  "message": "Connected to detection stream"
}
```

#### 2. Detection Event

Sent when a new bird detection occurs and passes all filters.

```json
{
  "id": 12345,
  "date": "2024-01-15",
  "time": "08:30:45",
  "source": "USB Audio Device",
  "beginTime": "2024-01-15T08:30:45Z",
  "endTime": "2024-01-15T08:31:00Z",
  "speciesCode": "EABL1",
  "scientificName": "Turdus merula",
  "commonName": "Eurasian Blackbird",
  "confidence": 0.87,
  "verified": "unverified",
  "locked": false,
  "latitude": 60.1699,
  "longitude": 24.9384,
  "clipName": "eurasian_blackbird_87p_20240115T083045Z.wav",
  "birdImage": {
    "url": "https://example.com/bird-image.jpg",
    "attribution": "Image by Photographer Name",
    "license": "CC BY-SA 4.0",
    "licenseUrl": "https://creativecommons.org/licenses/by-sa/4.0/"
  },
  "timestamp": "2024-01-15T08:30:45.123Z",
  "eventType": "new_detection"
}
```

#### 3. Heartbeat Event

Sent every 30 seconds to keep the connection alive and provide connection status.

```json
{
  "timestamp": 1705312245,
  "clients": 3
}
```

#### Connection Status Endpoint

**URL:** `GET /api/v2/sse/status`
**Authentication:** None required (public endpoint)
**Rate Limiting:** Standard API rate limits apply

Returns information about the current SSE connection status:

```json
{
  "connected_clients": 3,
  "status": "active"
}
```

### Integration Examples

#### JavaScript/HTML

Perfect for web dashboards or browser-based applications:

```html
<!DOCTYPE html>
<html>
  <head>
    <title>BirdNET-Go Live Detections</title>
  </head>
  <body>
    <div id="detections"></div>

    <script>
      const eventSource = new EventSource(
        "http://localhost:8080/api/v2/detections/stream",
      );
      const detectionsDiv = document.getElementById("detections");

      eventSource.addEventListener("connected", function (event) {
        const data = JSON.parse(event.data);
        console.log("Connected:", data.message);
      });

      eventSource.addEventListener("detection", function (event) {
        const detection = JSON.parse(event.data);

        // Create detection element
        const detectionElement = document.createElement("div");
        detectionElement.className = "detection";
        detectionElement.innerHTML = `
                 <h3>${detection.commonName}</h3>
                 <p><em>${detection.scientificName}</em></p>
                 <p>Confidence: ${(detection.confidence * 100).toFixed(1)}%</p>
                 <p>Time: ${detection.time}</p>
                 <p>Source: ${detection.source}</p>
                 ${detection.birdImage?.url ? `<img src="${detection.birdImage.url}" alt="${detection.commonName}" style="max-width: 200px;">` : ""}
             `;

        // Add to top of list
        detectionsDiv.insertBefore(detectionElement, detectionsDiv.firstChild);

        // Keep only last 10 detections
        while (detectionsDiv.children.length > 10) {
          detectionsDiv.removeChild(detectionsDiv.lastChild);
        }
      });

      eventSource.addEventListener("heartbeat", function (event) {
        const data = JSON.parse(event.data);
        console.log(`Heartbeat - ${data.clients} clients connected`);
      });

      eventSource.onerror = function (event) {
        console.error("SSE connection error:", event);
      };
    </script>
  </body>
</html>
```

#### Python

Great for data processing, logging, or integration with other Python applications:

```python
#!/usr/bin/env python3
import sseclient
import json
import requests

def listen_to_detections(base_url="http://localhost:8080"):
    """
    Listen to BirdNET-Go detection stream and process detections.

    Requires: pip install sseclient-py requests
    """
    url = f"{base_url}/api/v2/detections/stream"

    try:
        response = requests.get(url, stream=True, headers={'Accept': 'text/event-stream'})
        client = sseclient.SSEClient(response)

        print("Connected to BirdNET-Go detection stream...")

        for event in client.events():
            if event.event == 'connected':
                data = json.loads(event.data)
                print(f"✅ Connected: {data['message']}")

            elif event.event == 'detection':
                detection = json.loads(event.data)

                # Process the detection
                print(f"🐦 {detection['commonName']} detected!")
                print(f"   Scientific: {detection['scientificName']}")
                print(f"   Confidence: {detection['confidence']:.2f}")
                print(f"   Time: {detection['time']}")
                print(f"   Source: {detection['source']}")

                # Your custom processing here
                process_detection(detection)

            elif event.event == 'heartbeat':
                data = json.loads(event.data)
                print(f"💓 Heartbeat - {data['clients']} clients connected")

    except KeyboardInterrupt:
        print("\n👋 Disconnecting from stream...")
    except Exception as e:
        print(f"❌ Error: {e}")

def process_detection(detection):
    """
    Custom processing function for detections.
    Add your own logic here.
    """
    # Example: Save to file
    with open('detections.log', 'a') as f:
        f.write(f"{detection['time']},{detection['commonName']},{detection['confidence']}\n")

    # Example: Send notification for high confidence detections
    if detection['confidence'] > 0.9:
        send_notification(f"High confidence detection: {detection['commonName']}")

    # Example: Store in database
    # store_in_database(detection)

def send_notification(message):
    """Example notification function"""
    print(f"🔔 Notification: {message}")

if __name__ == "__main__":
    listen_to_detections()
```

#### Node.js

Ideal for server-side applications or building APIs on top of BirdNET-Go:

```javascript
const EventSource = require("eventsource");

class BirdNetGoClient {
  constructor(baseUrl = "http://localhost:8080") {
    this.baseUrl = baseUrl;
    this.eventSource = null;
  }

  connect() {
    const url = `${this.baseUrl}/api/v2/detections/stream`;
    this.eventSource = new EventSource(url);

    this.eventSource.addEventListener("connected", (event) => {
      const data = JSON.parse(event.data);
      console.log("✅ Connected:", data.message);
    });

    this.eventSource.addEventListener("detection", (event) => {
      const detection = JSON.parse(event.data);
      this.onDetection(detection);
    });

    this.eventSource.addEventListener("heartbeat", (event) => {
      const data = JSON.parse(event.data);
      console.log(`💓 Heartbeat - ${data.clients} clients connected`);
    });

    this.eventSource.onerror = (error) => {
      console.error("❌ SSE Error:", error);
    };

    console.log("🔗 Connecting to BirdNET-Go detection stream...");
  }

  onDetection(detection) {
    console.log(`🐦 ${detection.commonName} detected!`);
    console.log(`   Confidence: ${(detection.confidence * 100).toFixed(1)}%`);
    console.log(`   Time: ${detection.time}`);

    // Your custom logic here
    this.processDetection(detection);
  }

  processDetection(detection) {
    // Example: Send to webhook
    // this.sendWebhook(detection);
    // Example: Store in external database
    // this.storeInDatabase(detection);
    // Example: Send push notification
    // this.sendPushNotification(detection);
  }

  disconnect() {
    if (this.eventSource) {
      this.eventSource.close();
      console.log("👋 Disconnected from stream");
    }
  }
}

// Usage
const client = new BirdNetGoClient();
client.connect();

// Graceful shutdown
process.on("SIGINT", () => {
  client.disconnect();
  process.exit(0);
});
```

#### curl (Command Line)

For testing or simple scripting:

```bash
#!/bin/bash

# Simple detection monitor using curl
curl -N -H "Accept: text/event-stream" \
     "http://localhost:8080/api/v2/detections/stream" | \
while IFS= read -r line; do
    if [[ $line == data:* ]]; then
        # Extract JSON data
        json_data="${line#data: }"

        # Parse with jq if available
        if command -v jq &> /dev/null; then
            # Check if it's a detection event
            if echo "$json_data" | jq -e '.commonName' &> /dev/null; then
                species=$(echo "$json_data" | jq -r '.commonName')
                confidence=$(echo "$json_data" | jq -r '.confidence')
                time=$(echo "$json_data" | jq -r '.time')

                echo "🐦 $time: $species (${confidence})"

                # Example: Log to file
                echo "$time,$species,$confidence" >> detections.csv

                # Example: Send desktop notification (Linux)
                # notify-send "Bird Detected" "$species detected with ${confidence} confidence"
            fi
        else
            echo "Raw data: $json_data"
        fi
    fi
done
```

### Connection Management

#### Automatic Reconnection

The SSE connection can be lost due to network issues or server restarts. Most SSE clients automatically handle reconnection, but you can implement custom reconnection logic:

```javascript
function connectWithRetry(baseUrl, maxRetries = 5) {
  let retryCount = 0;

  function connect() {
    const eventSource = new EventSource(`${baseUrl}/api/v2/detections/stream`);

    eventSource.onopen = function () {
      console.log("✅ Connected to detection stream");
      retryCount = 0; // Reset retry count on successful connection
    };

    eventSource.onerror = function (event) {
      if (retryCount < maxRetries) {
        retryCount++;
        const delay = Math.min(1000 * Math.pow(2, retryCount), 30000); // Exponential backoff, max 30s
        console.log(
          `❌ Connection lost. Retrying in ${delay}ms... (attempt ${retryCount}/${maxRetries})`,
        );

        setTimeout(() => {
          eventSource.close();
          connect();
        }, delay);
      } else {
        console.error("❌ Max retries reached. Please check your connection.");
      }
    };

    return eventSource;
  }

  return connect();
}
```

### Performance Considerations

- **Connection Limits**: The server can handle multiple concurrent SSE connections, but each connection consumes server resources
- **Network Bandwidth**: Each connected client receives all detection events, so bandwidth usage scales with the number of clients
- **Client Processing**: Ensure your client application can process events fast enough to avoid missing detections
- **Heartbeat Monitoring**: Use heartbeat events to detect connection issues and implement automatic reconnection

### Use Cases

#### Real-time Dashboards

Create web-based dashboards that display live bird activity, species counts, and detection trends in real-time.

#### Mobile Applications

Build mobile apps that notify users immediately when interesting species are detected in their area.

#### Data Integration

Stream detection data into time-series databases, data lakes, or analytics platforms for advanced analysis.

#### Automation Systems

Trigger actions in home automation systems, cameras, or other IoT devices based on specific bird detections.

#### Research Applications

Collect real-time data for ornithological research, citizen science projects, or ecological monitoring.

#### Alert Systems

Send notifications via email, SMS, push notifications, or other channels when rare or specific species are detected.

## Species-Specific Settings

BirdNET-Go allows for fine-grained control over how individual species are handled through the `realtime.species` configuration section:

- **Include List (`include`):** A list of species names (matching the labels used by your BirdNET model/locale) that should _always_ be processed and trigger actions if their confidence meets the required threshold. These species bypass any location-based range filtering.
- **Exclude List (`exclude`):** A list of species names that should _always_ be ignored, regardless of their detection confidence. This is useful for filtering out consistently problematic species or non-bird sounds that might be misidentified.
- **Custom Configuration (`config`):** This section allows you to define specific settings for individual species:
  - **Custom Threshold:** You can set a unique `threshold` for a species, overriding the global `birdnet.threshold`. This is useful if you want to be more or less strict for specific birds.
  - **Custom Interval:** You can set a species-specific `interval` (in seconds) to control how frequently detections for that particular species are allowed. Useful for limiting overly vocal species without affecting detection rates for other birds. When set to 0 or omitted, the global `realtime.interval` value is used.
  - **Custom Actions (`actions`):** You can define a custom action to be triggered when a specific species is detected above its threshold. Currently, only one action per species is supported.
    - **Type:** The only supported type is `ExecuteCommand`.
    - **Command:** The full path to the script or executable to run.
    - **Parameters:** A list of values to pass as arguments to the command. Available values are:
      - `CommonName`: The common name of the detected species.
      - `ScientificName`: The scientific name of the detected species.
      - `Confidence`: The detection confidence score (0.0 to 1.0). Note: This is passed as a float; multiply by 100 in your script if you need a percentage.
      - `Time`: The time of the detection (format: HH:MM:SS).
      - `Source`: The audio source identifier (e.g., sound card name or RTSP stream URL).
    - **ExecuteDefaults:** A boolean value (`true` or `false`).
      - If `true` (default), BirdNET-Go will execute **both** your custom command **and** all other configured default actions (like saving to the database, uploading to BirdWeather, sending MQTT messages, etc.).
      - If `false`, BirdNET-Go will **only** execute your custom command for this specific species detection and will _skip_ all default actions.

Example `config` entry:

```yaml
realtime:
  interval: 15 # Default interval for most birds (15 seconds)
  species:
    config:
      "Great Tit":
        threshold: 0.65
        interval: 30 # 30 seconds between detections for this species
      "California Towhee":
        interval: 300 # Limit detections to once every 5 minutes
      "Eurasian Magpie":
        threshold: 0.80
        interval: 120 # 2 minutes between detections
        actions:
          - type: ExecuteCommand
            command: "/home/user/scripts/magpie_alert.sh"
            parameters: ["CommonName", "Time"]
            executedefaults: false # Only run the script, don't save to DB etc.
```

## Log Rotation

The application supports several log rotation strategies:

- Daily rotation
- Weekly rotation (on a specified day)
- Size-based rotation (with configurable maximum size)

This helps manage log files for long-running installations.
