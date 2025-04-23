# BirdNET-Go

BirdNET-Go is an application inspired by BirdNET-Pi and BirdNET Analyzer. It aims to be a high-performance and easy-to-deploy alternative to both of these.

## BirdNET-Go Features

* Audio analysis based on the BirdNET 2.4 tflite model
* 24/7 real-time analysis of soundcard capture
* Real-time analysis output compatible with OBS chat log input for wildlife streams
* BirdWeather API support for real-time analysis
* File analysis of WAV files
* Analysis output options: Raven table, CSV file, SQLite, or MySQL database
* Localized species labels, with extensive language support (over 30 languages)
* Runs on Windows, Linux (including Raspberry Pi), and macOS
* Minimal runtime dependencies; the BirdNET TensorFlow Lite model and other supporting files are embedded in the executable
* Web dashboard with visualization capabilities
* Weather integration through OpenWeather or Yr.no
* MQTT support for IoT integration
* Advanced audio processing with equalizer filters
* Privacy and dog bark filtering capabilities
* Dynamic threshold adjustment for better detection
* OAuth2 authentication options for security
* Telemetry support with Prometheus-compatible endpoint

## Supported Platforms

BirdNET-Go has been successfully tested on:

* Raspberry Pi 3B+ with 512MB RAM
* Raspberry Pi 4B with 4GB RAM
* Raspberry Pi 5 with 4GB RAM
* Intel NUC running Windows 10
* Intel desktop PC running Windows 11
* Intel MacBook Pro

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

The script will:
- Check system prerequisites and install required packages
- Pull the latest BirdNET-Go Docker image
- Guide you through configuration (audio input, location, language, etc.)
- Create a systemd service for automatic start on boot
- Set up directories for configuration and data persistence

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

* **TensorFlow Lite C library**: Required for the core audio analysis functionality
* **FFmpeg**: Required for RTSP stream capture, audio export to formats other than WAV (MP3, AAC, FLAC, Opus), and for the HLS live stream feature in the web interface
* **SoX**: Required for rendering spectrograms in the web interface

> **Note**: When using the Docker installation method, all these dependencies are already included in the Docker image, so you don't need to install them separately. This is one of the major advantages of using the Docker-based installation.

For manual installations, you'll need to install these dependencies separately on your system.

#### TensorFlow Lite C library

In addition to BirdNET-Go executable you also need TensorFlow Lite C library which is available for download at https://github.com/tphakala/tflite_c/releases. Download library for your target platform and install it in proper library path for your system:

* On Linux copy **libtensorflowlite_c.so** to **/usr/local/lib** and run "**sudo ldconfig**"
* On macOS **libtensorflowlite_c.dylib** to **/usr/local/lib**
* On Windows copy **libtensorflowlite_c.so** to BirdNET-Go executable directory or any other directory in system PATH

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

debug: false  # Enable debug messages for troubleshooting

# Main application settings
main:
  name: BirdNET-Go  # Name of this node, used to identify the source of notes
  timeas24h: true   # true for 24-hour time format, false for 12-hour time format
  log:
    enabled: false  # Enable main application logging
    path: logs/birdnet.log  # Path to log file
    rotation: daily  # Log rotation type: daily, weekly, or size
    maxsize: 10485760  # Maximum log size in bytes for size rotation (10MB)
    rotationday: Sunday  # Day of the week for weekly rotation

# BirdNET model specific settings
birdnet:
  debug: false  # Enable debug mode for BirdNET functionality
  sensitivity: 1.0  # Sigmoid sensitivity, 0.1 to 1.5
  threshold: 0.8  # Threshold for prediction confidence to report, 0.0 to 1.0
  overlap: 0.0  # Overlap between chunks, 0.0 to 2.9
  latitude: 00.000  # Latitude of recording location for prediction filtering
  longitude: 00.000  # Longitude of recording location for prediction filtering
  threads: 0  # Number of CPU threads to use (0 = use all available, automatically optimized for P-cores if detected)
  locale: en-uk  # Language to use for labels
  modelpath: ""  # Path to external model file (empty for embedded)
  labelpath: ""  # Path to external label file (empty for embedded)
  usexnnpack: true  # Use XNNPACK delegate for inference acceleration
  rangefilter:
    debug: false  # Enable debug mode for range filter
    model: ""  # Range filter model to use. "" (default) uses V2, "legacy" uses V1.
    threshold: 0.05  # Range filter species occurrence threshold

# Realtime processing settings
realtime:
  interval: 15  # Minimum interval between repeating detections in seconds
  processingtime: false  # Report processing time for each prediction
  
  # Audio settings
  audio:
    source: ""  # Audio source to use for analysis
    ffmpegpath: ""  # Path to ffmpeg (runtime value)
    soxpath: ""  # Path to sox (runtime value)
    streamtransport: auto  # Preferred transport for audio streaming: auto, sse, or ws
    export:
      debug: false  # Enable audio export debug
      enabled: false  # Export audio clips containing identified bird calls
      path: clips/  # Path to audio clip export directory
      type: wav  # Audio file type: wav, mp3, or flac
      bitrate: 192k  # Bitrate for audio export
      retention:
        debug: false  # Enable retention debug
        policy: none  # Retention policy: none, age, or usage
        maxage: 30d  # Maximum age of audio clips to keep
        maxusage: 85%  # Maximum disk usage percentage before cleanup
        minclips: 5  # Minimum number of clips per species to keep
    equalizer:
      enabled: false  # Enable equalizer filters
      filters:
        - type: LowPass  # Filter type: LowPass, HighPass, BandPass, etc.
          frequency: 10000  # Filter frequency in Hz
          q: 0.7  # Filter Q factor
          gain: 0  # Filter gain (only for certain types)
          width: 0  # Filter width (only for BandPass and BandReject)
          passes: 1  # Filter passes for added attenuation or gain
  
  # Web dashboard settings
  dashboard:
    thumbnails:
      debug: false  # Enable debug mode for thumbnails
      summary: true  # Show thumbnails on summary table
      recent: true  # Show thumbnails on recent table
    summarylimit: 20  # Limit for the number of species shown in the summary table
  
  # Dynamic threshold adjustment
  dynamicthreshold:
    enabled: false  # Enable dynamic threshold adjustment
    debug: false  # Enable debug mode for dynamic threshold
    trigger: 0.5  # Trigger threshold for dynamic adjustment
    min: 0.3  # Minimum threshold for dynamic adjustment
    validhours: 24  # Number of hours to consider for dynamic threshold
  
  # OBS chat log settings
  log:
    enabled: false  # Enable OBS chat log
    path: birdnet.txt  # Path to OBS chat log
  
  # BirdWeather API integration
  birdweather:
    enabled: false  # Enable BirdWeather uploads
    debug: false  # Enable debug mode for BirdWeather API
    id: "00000"  # BirdWeather ID / Token
    threshold: 0.9  # Threshold of prediction confidence for uploads
    locationaccuracy: 10  # Accuracy of location in meters
    retrysettings:
      enabled: true  # Enable retry mechanism
      maxretries: 5  # Maximum number of retry attempts
      initialdelay: 5  # Initial delay before first retry in seconds
      maxdelay: 300  # Maximum delay between retries in seconds
      backoffmultiplier: 2.0  # Multiplier for exponential backoff
  
  # Weather integration settings
  weather:
    provider: "yrno"  # Weather provider: none, yrno, or openweather
    pollinterval: 30  # Weather data polling interval in minutes
    debug: false  # Enable debug mode for weather integration
    openweather:
      enabled: false  # Enable OpenWeather integration (legacy setting, use 'provider' above)
      apikey: ""  # OpenWeather API key
      endpoint: "https://api.openweathermap.org/data/2.5/weather"  # OpenWeather API endpoint
      units: "metric"  # Units of measurement: standard, metric, or imperial
      language: "en"  # Language code for the response
  
  # Privacy and filtering settings
  privacyfilter:
    debug: false  # Enable debug mode for privacy filter
    enabled: false  # Enable privacy filter
    confidence: 0.8  # Confidence threshold for human detection
  
  dogbarkfilter:
    debug: false  # Enable debug mode for dog bark filter
    enabled: false  # Enable dog bark filter
    confidence: 0.8  # Confidence threshold for dog bark detection
    remember: 60  # How long to remember barks for filtering (in seconds)
    species: []  # Species list for filtering
  
  # RTSP streaming settings
  rtsp:
    transport: "tcp"  # RTSP Transport Protocol: tcp or udp
    urls: []  # RTSP stream URLs
  
  # MQTT integration
  mqtt:
    enabled: false  # Enable MQTT
    broker: "localhost:1883"  # MQTT broker URL (e.g., mqtt://host:port or mqtts://host:port)
    topic: "birdnet/detections"  # MQTT topic
    username: ""  # MQTT username
    password: ""  # MQTT password
    retain: false  # Retain messages (useful for Home Assistant)
    retrysettings:
      enabled: true  # Enable retry mechanism
      maxretries: 5  # Maximum number of retry attempts
      initialdelay: 5  # Initial delay before first retry in seconds
      maxdelay: 300  # Maximum delay between retries in seconds
      backoffmultiplier: 2.0  # Multiplier for exponential backoff
  
  # Telemetry settings
  telemetry:
    enabled: false  # Enable Prometheus compatible telemetry endpoint
    listen: "localhost:9090"  # IP address and port to listen on (e.g., 0.0.0.0:9090)
  
  # Species-specific settings
  species:
    include: []  # Always include these species, bypassing range/occurrence filters
    exclude: []  # Always exclude these species, regardless of confidence
    config:  # Per-species configuration overrides
      "European Robin": # Use the exact species name from BirdNET labels
        threshold: 0.75  # Custom confidence threshold for this species
        actions:  # List of actions to execute on detection (currently only one action per species supported)
          - type: ExecuteCommand # Action type (only ExecuteCommand supported currently)
            command: "/path/to/notify_script.sh" # Full path to the script/command
            parameters: ["CommonName", "Confidence"] # Parameters to pass to the command
            executedefaults: true  # true: run default actions (DB, MQTT, etc.) AND this command. false: run ONLY this command.

# Web server settings
webserver:
  debug: false  # Enable debug mode for web server
  enabled: true  # Enable web server
  port: "8080"  # Port for web server
  log:
    enabled: false  # Enable web server logging
    path: logs/webserver.log  # Path to log file
    rotation: daily  # Log rotation type: daily, weekly, or size
    maxsize: 10485760  # Maximum log size in bytes for size rotation (10MB)
    rotationday: Sunday  # Day of the week for weekly rotation

# Security settings
security:
  debug: false  # Enable debug mode for security features
  host: ""  # Primary hostname used for TLS certificates and OAuth redirect URLs
  autotls: false  # Enable automatic TLS certificate management using Let's Encrypt
  redirecttohttps: true  # Redirect HTTP to HTTPS
  allowsubnetbypass:
    enabled: false  # Enable subnet bypass for authentication
    subnet: "192.168.1.0/24"  # Subnet to bypass authentication
  basicauth:
    enabled: false  # Enable password authentication
    password: ""  # Password for admin interface
    clientid: ""  # Client ID for OAuth2
    clientsecret: ""  # Client secret for OAuth2
    redirecturi: ""  # Redirect URI for OAuth2
    authcodeexp: 10m  # Duration for authorization code
    accesstokenexp: 1h  # Duration for access token
  googleauth:
    enabled: false  # Enable Google OAuth2
    clientid: ""  # Google client ID
    clientsecret: ""  # Google client secret
    redirecturi: ""  # Google redirect URI
    userid: ""  # Valid Google user ID
  githubauth:
    enabled: false  # Enable GitHub OAuth2
    clientid: ""  # GitHub client ID
    clientsecret: ""  # GitHub client secret
    redirecturi: ""  # GitHub redirect URI
    userid: ""  # Valid GitHub user ID
  sessionsecret: ""  # Secret for session cookie

# Output settings
output:
  # SQLite database output settings
  sqlite:
    enabled: false  # Enable SQLite output
    path: birdnet.db  # Path to SQLite database
  
  # MySQL database output settings
  mysql:
    enabled: false  # Enable MySQL output
    username: birdnet  # MySQL database username
    password: secret  # MySQL database user password
    database: birdnet  # MySQL database name
    host: localhost  # MySQL database host
    port: 3306  # MySQL database port
```

### Command Line Interface

While the primary configuration is done via `config.yaml`, BirdNET-Go also offers several command-line operations:

```bash
birdnet [command] [flags]
```

**Available Commands:**

*   `realtime`: (Default) Starts the real-time analysis using the configuration file.
*   `file`: Analyzes a single audio file. Requires `-i <filepath>`.
*   `directory`: Analyzes all audio files in a directory. Requires `-i <dirpath>`. Can optionally use `--recursive` and `--watch`.
*   `benchmark`: Runs a performance benchmark on the current system.
*   `rangefilter`: Manages the range filter database (used for location-based species filtering).
    *   `rangefilter update`: Downloads or updates the range filter database.
    *   `rangefilter info`: Displays information about the current range filter database.
*   `support`: Generates a support bundle containing logs and configuration (with sensitive data masked) for troubleshooting.
*   `authors`: Displays author information.
*   `license`: Displays software license information.
*   `help`: Shows help for any command.

**Global Flags (can be used with most commands):**

Many configuration options can be overridden via command-line flags (e.g., `--threshold 0.7`, `--locale fr`). Run `birdnet [command] --help` to see all available flags for a specific command. Some common global flags include:

*   `-d, --debug`: Enable debug output.
*   `-s, --sensitivity`: Set sigmoid sensitivity (0.0 to 1.5).
*   `-t, --threshold`: Set confidence threshold (0.1 to 1.0).
*   `-j, --threads`: Set number of CPU threads (0 for auto).
*   `--locale`: Set language for labels (e.g., `en-us`, `de`).
*   `--latitude`, `--longitude`: Set location coordinates.
*   `--overlap`: Set analysis overlap (0.0 to 2.9).

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
   - Check if audio device is working properly: `arecord -d 5 -f S16_LE -r 48000 test.wav`
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

The cleanup task runs periodically (every few minutes) to check if clips need to be deleted based on the selected policy.

Configure these options under `realtime.audio.export.retention` in your `config.yaml`:

*   **`policy`**: Sets the retention strategy. Options are:
    *   **`none`** (Default): No automatic deletion. You are responsible for managing the clip files.
    *   **`age`**: Deletes clips older than the specified `maxage`.
    *   **`usage`**: Deletes the oldest clips *only when* the disk usage of the partition containing the clips directory exceeds the `maxusage` percentage. This policy tries to keep at least `minclips` per species, deleting the oldest clips first when cleanup is needed.
*   **`maxage`**: (Used with `policy: age`) Maximum age for clips (e.g., `30d` for 30 days, `7d` for 7 days, `24h` for 24 hours). Clips older than this will be deleted.
*   **`maxusage`**: (Used with `policy: usage`) The target maximum disk usage percentage (e.g., `85%`). Cleanup triggers when usage exceeds this threshold.
*   **`minclips`**: (Used with `policy: usage`) The minimum number of clips to keep for each species, even when cleaning up based on disk usage. This ensures you retain at least some recent examples per species.

### Security Features

The application includes several security options:
- Basic authentication with password protection
- OAuth2 authentication through Google or GitHub
- Automatic TLS certificate management via Let's Encrypt
- IP subnet-based authentication bypass for local networks

### Filtering Capabilities

BirdNET-Go includes intelligent filtering mechanisms:
- Privacy filter to ignore human voices
- Dog bark filter to reduce false positives from canine sounds
- Species-specific inclusion and exclusion lists
- Dynamic threshold adjustment based on detection patterns

### Deep Detection

BirdNET-Go includes a "Deep Detection" feature designed to improve detection reliability and reduce false positives, especially when using lower confidence thresholds.

*   **How it works:** Instead of analyzing standard 3-second chunks with a 1.5-second step, Deep Detection significantly increases the analysis frequency by reducing the step size (e.g., to 300ms when overlap is 2.7). BirdNET-Go then requires multiple consecutive positive results (e.g., 4 or more) for the same species within a short time window before confirming a detection.
*   **Benefits:** This method acts as an additional filter, making it less likely that a single spurious result triggers a detection. It can allow for using lower `birdnet.threshold` values (e.g., 0.3) while maintaining good accuracy.
*   **Requirements:** This feature significantly increases CPU load as the AI model runs more frequently. It is recommended for Raspberry Pi 4/5 or more powerful systems. If the system cannot keep up, you will see `WARNING: BirdNET processing time exceeded buffer length` messages in the log (see Troubleshooting).
*   **Enabling/Disabling:**
    *   When using the recommended `install.sh` script for Docker installations, Deep Detection is **enabled by default**. The script automatically benchmarks your hardware and sets an appropriate high `birdnet.overlap` value.
    *   For manual installations or to manually adjust, set the `birdnet.overlap` value in your `config.yaml`. High values (e.g., 2.7) enable the feature, while lower values (e.g., 0.0) disable it.
*   **Reference:** [[GitHub Discussion #302](https://github.com/tphakala/birdnet-go/discussions/302)]

### Live Audio Streaming

BirdNET-Go allows you to listen to the live audio feed directly from the web interface. This is useful for monitoring the audio quality, checking microphone placement, or simply listening to the ambient sounds.

*   **How to Use:**
    1.  Locate the microphone icon / audio level indicator in the web interface header.
    2.  Click the icon to open the audio source dropdown.
    3.  If you have multiple audio sources configured (e.g., a sound card and RTSP streams), select the source you wish to listen to.
    4.  Click the play icon (▶️) next to the source name.
    5.  Audio playback will begin using your browser's audio capabilities.
    6.  Click the stop icon (⏹️) to end the stream.
*   **Technology:** The live stream uses HLS (HTTP Live Streaming) for broad browser compatibility and efficient delivery.
*   **Dependency:** This feature requires **FFmpeg** to be installed and accessible by BirdNET-Go. If FFmpeg is not found, the play button may not appear or function.
*   **Server Interaction:** Starting the live stream initiates audio encoding on the server. The stream uses a heartbeat mechanism to stay active while you are listening. Stopping the stream or closing the browser tab/window signals the server to stop the encoding process, conserving server resources.

### Integration Options

The application offers several integration points:
- MQTT support for IoT ecosystems.
  - The `retain` flag in MQTT settings is recommended for Home Assistant integration to ensure sensor states are preserved across restarts.
- Telemetry endpoint compatible with Prometheus.
- BirdWeather API integration for community data sharing.
  - **About BirdWeather:** [BirdWeather.com](https://www.birdweather.com/) is a citizen science platform that collects bird vocalizations from stations around the world. It uses the BirdNET model (developed by Cornell Lab of Ornithology and Chemnitz University of Technology) for identification. Uploading data helps contribute to this global library.
  - **Getting a BirdWeather ID/Token:** To upload data, you need an ID (also referred to as a Token). This process is now automated:
    1. Create an account at [app.birdweather.com/login](https://app.birdweather.com/login).
    2. Go to your account's station page: [app.birdweather.com/account/stations](https://app.birdweather.com/account/stations).
    3. Create a new station, ensuring the Latitude and Longitude match your BirdNET-Go configuration (`birdnet.latitude` and `birdnet.longitude`).
    4. Copy the generated station ID/Token into the `realtime.birdweather.id` field in your BirdNET-Go configuration.
  - **Data Sharing Consent:** By configuring and enabling BirdWeather uploads with your ID/Token, you consent to sharing your soundscape snippets and detection data with BirdWeather.
- Custom actions that can be triggered on species detection.
- Built-in connection testers (via Web UI) for BirdWeather and MQTT to verify configuration.
  - The testers perform multi-stage checks (connectivity, authentication, test uploads/publishes) and provide feedback, including troubleshooting hints and rate limit information (for BirdWeather).

## Species-Specific Settings

BirdNET-Go allows for fine-grained control over how individual species are handled through the `realtime.species` configuration section:

*   **Include List (`include`):** A list of species names (matching the labels used by your BirdNET model/locale) that should *always* be processed and trigger actions if their confidence meets the required threshold. These species bypass any location-based range filtering.
*   **Exclude List (`exclude`):** A list of species names that should *always* be ignored, regardless of their detection confidence. This is useful for filtering out consistently problematic species or non-bird sounds that might be misidentified.
*   **Custom Configuration (`config`):** This section allows you to define specific settings for individual species:
    *   **Custom Threshold:** You can set a unique `threshold` for a species, overriding the global `birdnet.threshold`. This is useful if you want to be more or less strict for specific birds.
    *   **Custom Actions (`actions`):** You can define a custom action to be triggered when a specific species is detected above its threshold. Currently, only one action per species is supported.
        *   **Type:** The only supported type is `ExecuteCommand`.
        *   **Command:** The full path to the script or executable to run.
        *   **Parameters:** A list of values to pass as arguments to the command. Available values are:
            *   `CommonName`: The common name of the detected species.
            *   `ScientificName`: The scientific name of the detected species.
            *   `Confidence`: The detection confidence score (0.0 to 1.0). Note: This is passed as a float; multiply by 100 in your script if you need a percentage.
            *   `Time`: The time of the detection (format: HH:MM:SS).
            *   `Source`: The audio source identifier (e.g., sound card name or RTSP stream URL).
        *   **ExecuteDefaults:** A boolean value (`true` or `false`).
            *   If `true` (default), BirdNET-Go will execute **both** your custom command **and** all other configured default actions (like saving to the database, uploading to BirdWeather, sending MQTT messages, etc.).
            *   If `false`, BirdNET-Go will **only** execute your custom command for this specific species detection and will *skip* all default actions.

Example `config` entry:

```yaml
realtime:
  species:
    config:
      "Great Tit":
        threshold: 0.65
      "Eurasian Magpie":
        threshold: 0.80
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