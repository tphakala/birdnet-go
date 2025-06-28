# Error Tracking & Telemetry

BirdNET-Go includes an optional, privacy-first error tracking system designed to help developers identify and fix issues that affect system reliability and performance. This system is **completely opt-in** and follows privacy-by-design principles.

📋 **For comprehensive privacy information, please see our [Privacy Statement](../../PRIVACY.md)**

## Quick Start

1. Navigate to **Settings → Support** in the BirdNET-Go web interface
2. Read the privacy notice and data collection information
3. Check **"Enable Error Tracking (Opt-in)"** if you want to help improve BirdNET-Go
4. Save your settings

That's it! No additional configuration is required. Error reports will be automatically sent to the BirdNET-Go development team for analysis.

## What is Telemetry?

**Note**: BirdNET-Go has multiple external integrations beyond telemetry. For complete information about all data collection and external services, see our [Privacy Statement](../../PRIVACY.md).

Telemetry in BirdNET-Go refers to the automatic collection and transmission of technical error information to help developers:

- **Identify crashes and system errors** that affect application stability
- **Debug issues** that occur across different configurations and environments
- **Understand failures** across various hardware and software setups
- **Prioritize bug fixes** based on real-world usage patterns
- **Improve reliability** and compatibility with different systems

## Privacy-First Design

### 🔒 Completely Optional
- Error tracking is **disabled by default**
- Requires **explicit user consent** to enable
- Can be **disabled at any time** in settings
- No tracking without permission

### 🛡️ Data Protection
- **No personal data** is collected or transmitted
- **No audio recordings** or bird detection data is sent
- **RTSP URLs are anonymized** to protect private network information
- **Usernames and passwords** are completely removed from error reports
- All data is **filtered before transmission** to ensure privacy

### 🎯 Technical Focus
The system only collects essential technical information needed for debugging:
- Error types and categories
- Component names where errors occur
- Anonymous resource identifiers (not actual URLs or file paths)
- System resource errors
- Platform and compatibility information
- Installation and update events

## Data Collection Details

### What IS Collected ✅
- **Error messages** (with URLs and credentials anonymized)
- **Error types and categories** (network errors, validation errors, system resource errors, etc.)
- **Component names** (component where error occurred, such as datastore, audio processing, etc.)
- **Anonymous identifiers** (hashed URLs and resource identifiers instead of actual values)
- **Technical error context** (timeout values, retry counts, operation names, etc.)
- **Platform information** (operating system, architecture, container status, hardware details for debugging compatibility issues)

### What is NOT Collected ❌

**Note**: The following applies specifically to telemetry. Other optional integrations (BirdWeather, MQTT, backups) may transmit different data when explicitly configured. See [Privacy Statement](../../PRIVACY.md) for complete details.

- **Personal audio recordings** (except 3-second clips for BirdWeather when configured)
- **Continuous bird detection results** (except when shared via configured integrations)
- **Actual RTSP URLs, IP addresses, or hostnames** (anonymized in telemetry)
- **Usernames, passwords, or authentication credentials**
- **Personal information or user data**
- **File paths or directory structures**
- **Precise location data** (coordinates used only for weather/BirdWeather when configured)
- **Any sensitive application data**

### System Identification 🔑

BirdNET-Go uses a **unique system ID** for telemetry purposes:

**What**: A randomly generated identifier (format: XXXX-XXXX-XXXX)
**Why**: Allows tracking errors from the same system without revealing identity
**Example**: "A1B2-C3D4-E5F6"
**Privacy protection**: 
- Generated locally using cryptographically secure random numbers
- No connection to hardware, network, or user information
- Stored only in your local configuration directory
- You control when and if to share this ID

### Data Anonymization Example

When an error occurs with sensitive information, the system automatically anonymizes it:

**Original error (never sent):**
```
failed to connect to rtsp://admin:password123@192.168.1.100:554/cam/stream1
```

**Anonymized version (what gets sent):**
```
failed to connect to url-b0c823d0454e766694949834
```

The anonymized identifier:
- ✅ **Allows tracking** of issues with the same resource
- ✅ **Preserves error patterns** for debugging
- ❌ **Cannot be reverse-engineered** to reveal the original data
- ❌ **Contains no sensitive information**

## Technical Implementation

### Enhanced Error System
BirdNET-Go uses an advanced error handling system that automatically integrates with telemetry:

1. **Component Detection**: Automatically identifies which part of the system generated the error
2. **Error Categorization**: Classifies errors by type (network, validation, database, etc.) for better grouping
3. **Context Enrichment**: Adds relevant technical context while preserving privacy
4. **Meaningful Titles**: Generates descriptive error titles in telemetry instead of generic error types

### Automatic Privacy Protection
All telemetry data is automatically processed through privacy filters before transmission:

1. **URL Detection**: Regex patterns identify URLs, file paths, and other sensitive data in error messages
2. **Anonymization**: Sensitive data is replaced with consistent, privacy-safe identifiers using SHA-256 hashing
3. **Credential Removal**: Any embedded usernames, passwords, or API keys are completely stripped
4. **Context Preservation**: Technical error context is maintained for debugging while removing personal information

### Consistent Identifiers
The same sensitive data always produces the same anonymized identifier, allowing developers to:
- Track recurring issues with the same resources without exposing sensitive information
- Identify patterns in system failures across different installations
- Understand which configurations are most problematic
- Prioritize fixes for commonly affected setups

## Benefits

### For Users
- **Better reliability**: Bugs get fixed faster based on real-world usage data
- **Improved compatibility**: Stream connection issues are identified and resolved
- **Enhanced stability**: System crashes and resource problems are tracked and fixed
- **Privacy protected**: No sensitive information leaves your system

### For Developers
- **Real-world insights**: Understanding of how BirdNET-Go performs in different environments
- **Prioritized development**: Focus on issues that affect the most users
- **Faster debugging**: Anonymous error patterns help identify root causes
- **Quality assurance**: Continuous monitoring of system reliability

## Related Documentation

- **[Privacy Statement](../../PRIVACY.md)** - **⭐ COMPREHENSIVE** privacy information covering all data collection and external integrations
- **[Privacy & Data Collection](telemetry-privacy.md)** - Detailed privacy information and data handling (telemetry-specific)
- **[Setup & Configuration](telemetry-setup.md)** - Step-by-step configuration guide
- **[Troubleshooting](telemetry-troubleshooting.md)** - Common issues and solutions

**Important**: The main [Privacy Statement](../../PRIVACY.md) contains complete information about all external services including BirdWeather, MQTT, backup services, weather APIs, and image services in addition to telemetry.

## Frequently Asked Questions

### Q: Is telemetry enabled by default?
**A:** No. Error tracking is completely disabled by default and requires explicit opt-in through the web interface.

### Q: Can I see what data is being sent?
**A:** While you cannot see the exact transmitted data, all URLs and sensitive information are automatically anonymized using the privacy protection system described above.

### Q: How do I disable telemetry?
**A:** Go to Settings → Support and uncheck "Enable Error Tracking". Changes take effect immediately.

### Q: Where is the data sent?
**A:** Error reports are sent to Sentry (SOC 2 Type II certified error tracking service) in Frankfurt, Germany. See our [Privacy Statement](../../PRIVACY.md) for complete information about all external services.

### Q: What about other external services?
**A:** BirdNET-Go may connect to other external services when explicitly configured (BirdWeather, MQTT brokers, backup services, weather APIs). All require user configuration and are disabled by default. See [Privacy Statement](../../PRIVACY.md) for details.

### Q: Does this affect performance?
**A:** No. The telemetry system has minimal performance impact and only activates when errors occur.

### Q: Is this GDPR compliant?
**A:** We strive to comply with GDPR and follow privacy-by-design principles. As a volunteer-maintained project, all privacy commitments are made on a best-effort basis. See our [Privacy Statement](../../PRIVACY.md) for detailed compliance information.

## External Integrations Beyond Telemetry

**Important**: This document focuses on telemetry (error tracking). BirdNET-Go has several other external integrations that may transmit data:

### Default External Connections (No Personal Data)
- **Weather Services**: YR.no for weather data (read-only)
- **Image Services**: Wikimedia Commons & AviCommons for bird photos (read-only)

### Optional External Integrations (Require User Configuration)
- **BirdWeather**: Citizen science platform (audio clips, species data)
- **MQTT Brokers**: Real-time detection publishing
- **Backup Services**: External storage (FTP, SFTP, Google Drive, rsync)
- **OpenWeather API**: Enhanced weather data (requires API key)

📋 **For complete information about all external services and data collection, see our [Privacy Statement](../../PRIVACY.md)**

## Volunteer Project Notice

BirdNET-Go is provided as free, open-source software maintained by volunteers. While we implement strong privacy protections by design:

- **Support capacity** depends on volunteer availability
- **Privacy commitments** are made on a best-effort basis
- **Response times** may vary significantly during busy periods
- **No warranties** - software provided "as is"

For immediate privacy protection, simply disable telemetry in Settings → Support.

---

*Last updated: June 2025*

*This document covers telemetry specifically. For comprehensive privacy information including all external integrations, see [Privacy Statement](../../PRIVACY.md). BirdNET-Go is provided "AS IS" without warranty of any kind.*
