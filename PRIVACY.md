# Privacy Statement

## ⚠️ Important Notice: Volunteer-Maintained Project

BirdNET-Go is a hobby project maintained by volunteers in their spare time. While we are committed to protecting your privacy and following best practices, our capacity to respond to requests and provide support is limited by volunteer availability. This privacy statement reflects our sincere efforts to be transparent about data practices, but please understand that responses may be delayed during busy periods or personal circumstances.

BirdNET-Go is committed to protecting your privacy while providing reliable bird sound identification software. This privacy statement explains what data we collect, how we protect it, and your rights regarding data collection.

## 🔒 Important: Completely Opt-In Data Collection

**BirdNET-Go collects ZERO data by default.** All telemetry and support data collection is:

- ✅ **Completely Optional** - Never enabled without your explicit consent
- ✅ **Opt-In Only** - You must actively choose to enable telemetry
- ✅ **No Hidden Collection** - Nothing is transmitted without your knowledge
- ✅ **User Controlled** - Enable or disable anytime with immediate effect

**By default, BirdNET-Go operates locally with minimal external data transmission.** The only default external connections are:

- **Weather data**: Read-only requests to YR.no (Norway's meteorological service) for weather information
- **Bird images**: Read-only requests to Wikimedia Commons and AviCommons for bird photos

**All other external integrations require explicit user configuration and are disabled by default.**

## Privacy Principles

BirdNET-Go follows **Privacy by Design** principles:

- **Data Minimization**: Only essential technical data is collected for debugging
- **Explicit Consent**: All telemetry requires explicit opt-in activation
- **Transparency**: Complete disclosure of data collection practices
- **User Control**: Users can enable/disable telemetry at any time
- **Data Protection**: All data is anonymized before transmission

## Data Collection and External Integrations

### Default External Connections (No Personal Data)

**Weather Services** (enabled by default):
- **YR.no API**: Read-only requests for weather data using your configured coordinates
- **Data sent**: Only HTTP requests with latitude/longitude parameters
- **Purpose**: Correlate weather patterns with bird detection for better insights
- **Privacy**: No registration required, no personal data, no tracking

**Image Services** (enabled by default):
- **Wikimedia Commons & AviCommons**: Download bird photos for web interface
- **Data sent**: Only HTTP GET requests for public images
- **Purpose**: Display bird photos in the web dashboard
- **Privacy**: Read-only requests, no personal data transmitted

### Optional External Integrations (Require User Configuration)

**BirdWeather Integration** (disabled by default):
- **Purpose**: Share bird detections with the citizen science platform
- **Data sent**: 3-second audio clips, species data, randomized location coordinates
- **Requires**: User account registration and station ID configuration
- **Privacy protection**: Location randomized within user-defined accuracy radius

**MQTT Broker Integration** (disabled by default):
- **Purpose**: Publish bird detection events to external MQTT brokers
- **Data sent**: Bird detection information (species, confidence, timestamp, optional audio)
- **Requires**: User-configured broker URL, credentials, and topics

**Backup Services** (disabled by default):
- **Purpose**: Store encrypted backups on external storage (FTP, SFTP, Google Drive, rsync)
- **Data sent**: Encrypted database and configuration backups
- **Requires**: User-configured storage credentials and settings

**Weather API Alternatives** (disabled by default):
- **OpenWeather**: Requires user API key for enhanced weather data
- **Data sent**: HTTP requests with coordinates and API key

### Telemetry Data Collection (Opt-In Only)

**IMPORTANT: The following telemetry data is ONLY collected when you explicitly enable it. By default, NO telemetry data is collected.**

### ✅ Technical Error Information (When Telemetry is Enabled)

**Error Messages & Context**
- Anonymized error messages for debugging software issues
- Component names where errors occur (e.g., "ffmpeg-process", "rtsp-connection")
- System resource errors (memory, disk space issues)
- Network connection patterns and retry attempts

**System Information**
- Operating system and architecture (Linux/Windows/macOS, ARM64/AMD64)
- Hardware specs (CPU count, memory amount)
- Container environment detection (Docker/Podman)
- Board model (only for ARM64 Linux systems like Raspberry Pi)

**Privacy Protection Applied**:
- URLs and credentials are automatically anonymized using SHA-256 hashing
- File paths and user directories are never transmitted
- Original RTSP URLs become anonymous identifiers like "url-abc123def456"

### ✅ Support Data Collection (When You Manually Create Support Packages)

**System Information**
- OS details, hardware specifications, runtime information
- Disk usage and container information for troubleshooting

**Log Files**
- Application logs with sensitive information automatically scrubbed
- Configuration data with passwords/tokens/secrets replaced with "[REDACTED]"
- System journal logs (Linux systemd environments)

**Privacy Protection Applied**:
- All sensitive configuration keys automatically redacted
- URLs anonymized in log files
- User control over what data to include in support packages

### ❌ Data We NEVER Collect (Telemetry)

**Note**: The following applies to telemetry data collection. Optional integrations like BirdWeather may transmit some of this data when explicitly configured by users.

- **Personal audio recordings** (except 3-second clips for BirdWeather when configured)
- **Continuous bird detection results** (except when shared via configured integrations)
- **Actual RTSP URLs, IP addresses, or hostnames** (anonymized in telemetry)
- **Usernames, passwords, or authentication credentials**
- **File paths** or directory structures  
- **Personal configuration settings** (except when included in support packages)
- **Precise location data** (coordinates used only for weather/BirdWeather when configured)
- **Device identifiers** or hardware serial numbers
- **Network topology** or internal IP addresses

### ❌ Data Never Transmitted by Default

- **No bird detection data** shared without explicit integration setup
- **No audio recordings** transmitted without BirdWeather configuration
- **No location information** sent without weather service or BirdWeather setup
- **No personal information** of any kind

## System Identification

BirdNET-Go uses a **unique System ID** for correlating error reports:

- **Format**: Random 12-character identifier (e.g., "A1B2-C3D4-E5F6")
- **Generation**: Created locally using cryptographically secure random numbers
- **Storage**: Stored only in your local configuration directory
- **Purpose**: Allows developers to correlate error reports without identifying users
- **Privacy**: Cannot be linked to you unless you explicitly share it

## Data Anonymization

### URL Protection Process

1. **Detection**: Automatic identification of URLs in error messages
2. **Component Analysis**: URLs are broken into scheme, host, port, and path
3. **Host Categorization**: 
   - `localhost` for local connections
   - `private-ip` for internal networks  
   - `public-ip` for internet addresses
   - `domain-com` for .com domains (TLD only)
4. **Consistent Hashing**: SHA-256 creates anonymous but consistent identifiers
5. **Structure Preservation**: Maintains debugging value without exposing sensitive data

### Example Anonymization

| Original URL | Anonymized Result | Information Preserved |
|-------------|------------------|---------------------|
| `rtsp://admin:pass@192.168.1.100:554/stream1` | `url-b0c823d0454e766694949834` | Protocol, private network, port, stream type |
| `http://camera.example.com/api/live` | `url-8a6d125351c079f9d0a27598` | Protocol, .com domain, API structure |

## Where Data Goes

### Default External Services (Read-Only)

**Weather Data** (YR.no):
- **Service**: Norwegian Meteorological Institute
- **Location**: Norway (GDPR compliant)
- **Data flow**: Inbound only (weather information)
- **Encryption**: HTTPS

**Image Services**:
- **Wikimedia Commons**: Wikimedia Foundation servers
- **AviCommons**: University of Arizona servers  
- **Data flow**: Inbound only (bird photos)
- **Encryption**: HTTPS

### User-Configured Integrations

**BirdWeather** (when enabled):
- **Service**: BirdWeather citizen science platform
- **Purpose**: Community bird detection sharing
- **Data**: 3-second audio clips, species data, randomized coordinates
- **User control**: Requires explicit registration and configuration

**MQTT Brokers** (when configured):
- **Service**: User-specified MQTT brokers
- **Purpose**: Real-time bird detection publishing
- **Data**: Bird detection events, optional audio clips
- **User control**: Complete control over broker and data format

**Backup Services** (when configured):
- **Service**: User-specified storage (FTP, SFTP, Google Drive, rsync)
- **Purpose**: Encrypted backup storage
- **Data**: Encrypted database and configuration backups
- **User control**: Complete control over storage location and credentials

### Error Telemetry (Opt-In Only)

**Sentry Error Tracking** (when enabled):
- **Service**: Sentry error tracking (SOC 2 Type II certified)
- **Location**: Frankfurt, Germany data center (GDPR compliant)
- **Encryption**: TLS 1.3 for all data transmission
- **Retention**: 90 days for active errors, 30 days for resolved errors

**Support Packages** (when you create them):
- **Storage**: Uploaded to Sentry as temporary attachments
- **Purpose**: Manual troubleshooting by developers
- **Retention**: Limited retention based on support needs

## User Rights & Control

### GDPR Compliance

- **Right to Consent**: Explicit opt-in required for all telemetry
- **Right to Withdraw**: Disable telemetry anytime in settings
- **Right to Information**: This privacy statement provides full transparency
- **Right to Erasure**: Disabling telemetry stops collection; existing data purged per retention policy

### Control Mechanisms

- **Settings Panel**: Easy telemetry toggle in web interface
- **Immediate Effect**: Changes apply instantly without restart
- **Visual Indicators**: Clear display of telemetry status
- **No Hidden Tracking**: Zero telemetry when disabled

## Legal Basis & Compliance

### Lawful Basis (GDPR Article 6)
- **Legitimate Interest**: Software improvement and bug fixing
- **Explicit Consent**: Required opt-in for all data collection
- **Proportionality**: Data collection limited to technical debugging needs

### Regional Compliance
- **GDPR** (European Union) - We strive to comply and follow privacy-by-design principles
- **CCPA** (California) - Privacy rights respected to the extent possible in a volunteer project
- **PIPEDA** (Canada) - Privacy principles followed where applicable
- **Other regional privacy laws** - Best effort compliance through privacy-by-design approach

## Technical Implementation

### Privacy Filters
All telemetry passes through multiple privacy protection layers:
- Automatic URL anonymization in all messages
- Sensitive context filtering before transmission
- Configuration data scrubbing for support packages
- Memory safety to prevent credential exposure

### Security Measures
- **Encryption**: TLS 1.3 for data transmission
- **Authentication**: Secure API keys for authorized data flow
- **Rate Limiting**: Prevents excessive data collection
- **Access Control**: Restricted developer access to telemetry data

## How to Enable/Disable Telemetry

### Enable Telemetry
1. Open BirdNET-Go web interface
2. Go to Settings → Support
3. Toggle "Enable Telemetry" to ON
4. Your System ID will be displayed for reference

### Disable Telemetry
1. Open Settings → Support
2. Toggle "Enable Telemetry" to OFF
3. All future data collection stops immediately

### Find Your System ID
- Located in Settings → Support → System ID
- Use this ID when reporting issues to help developers correlate error data
- The ID cannot identify you unless you explicitly share it

## Limitation of Liability & Volunteer Project Notice

### Project Nature
BirdNET-Go is provided as free, open-source software maintained by volunteers. While we implement strong privacy protections by design, users should understand:

- **Volunteer Capacity**: Support and response times depend on volunteer availability
- **Best Effort Basis**: All privacy commitments are made on a best-effort basis within volunteer constraints
- **No Warranties**: This software is provided "as is" without warranty of any kind
- **Limited Liability**: To the maximum extent permitted by law, project maintainers shall not be liable for any damages arising from software use

### Privacy Protection Strategy
Our primary privacy protection comes through technical design choices:
- **Minimal data collection** by default reduces privacy risks
- **Local operation** keeps your data on your devices
- **Opt-in only** puts control in your hands
- **Open source** allows community review of privacy practices

### Community Maintenance
As the project grows, multiple volunteers may contribute to maintenance and support. All contributors are expected to follow these same privacy principles.

## Contact & Data Protection

### Reporting Privacy Concerns

**Quick Resolution**: For most privacy concerns, simply disable telemetry in Settings → Support for immediate protection.

For other privacy questions, please report them through GitHub Issues:

**📋 How to Report Privacy Issues:**

1. **Go to**: [BirdNET-Go Issues](https://github.com/tphakala/birdnet-go/issues)
2. **Click**: "New Issue"
3. **Title Format**: Use one of these prefixes:
   - `[PRIVACY]` - General privacy questions or concerns
   - `[GDPR]` - GDPR-related requests (access, deletion, portability)
   - `[DATA-BREACH]` - Suspected privacy/security violations
   - `[TELEMETRY]` - Questions about telemetry data collection

**📝 Issue Template for Privacy Concerns:**
```markdown
**Privacy Concern Type**: [Select: General Question, GDPR Request, Data Breach, Telemetry Issue]

**Description**: 
[Describe your privacy concern or question]

**System ID** (if applicable): 
[Your system ID if relevant to the issue - found in Settings → Support]

**Telemetry Status**: 
[Enabled/Disabled - helps us understand the scope of concern]

**Specific Request** (for GDPR):
[e.g., "Please delete all telemetry data associated with my System ID"]
```

**⚡ Response Times:**
- **Privacy concerns**: Best effort based on volunteer availability
- **GDPR requests**: We aim for 30 days as required by law, subject to volunteer capacity
- **Suspected data breaches**: Best effort, as soon as volunteer maintainers are available
- **Note**: As a volunteer project, response times may vary significantly during busy periods

**🔒 Confidential Reports:**
For sensitive security or privacy issues that shouldn't be public, contact the project maintainers through GitHub Issues marked as confidential or use GitHub's private vulnerability reporting if available.

**📝 Note for Users:**
BirdNET-Go is a hobby project maintained by volunteers. We are genuinely committed to privacy protection, but our response capacity is limited by volunteer availability. Most privacy concerns can be immediately addressed by disabling telemetry in Settings → Support. For complex requests, please be patient as responses depend on volunteer schedules and may take longer than commercial services.

### Data Subject Rights (GDPR)
**Note**: While we respect GDPR rights, please remember this is a volunteer-maintained hobby project. Most privacy concerns can be immediately addressed by disabling telemetry.

- **Right to Access**: Request information about data we have collected (limited to System ID correlation)
- **Right to Deletion**: Request deletion of your telemetry data using your System ID
- **Right to Object**: Object to data processing by disabling telemetry in settings
- **Right to Portability**: Since we only collect anonymized technical data, there's typically no personal data to export
- **Right to Rectification**: Since data is anonymized and technical, correction requests are rarely applicable

## Changes to This Privacy Statement

This privacy statement may be updated to reflect changes in our data practices or legal requirements. Significant changes will be announced through:
- GitHub repository releases
- Documentation updates
- In-application notifications (if applicable)

---

**Last Updated**: June 2025  
**Effective Date**: June 2025

*This privacy statement covers BirdNET-Go software as maintained by volunteers. For questions about the privacy practices of third-party services (Sentry, BirdWeather, etc.), please consult their respective privacy policies. BirdNET-Go is provided "AS IS" without warranty of any kind, express or implied, including but not limited to the warranties of merchantability, fitness for a particular purpose and non-infringement. In no event shall the authors or copyright holders be liable for any claim, damages or other liability arising from the use of this software.*