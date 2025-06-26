# Error Tracking & Telemetry

BirdNET-Go includes an optional, privacy-first error tracking system designed to help developers identify and fix issues that affect system reliability and performance. This system is **completely opt-in** and respects EU privacy laws including GDPR.

## Quick Start

1. Navigate to **Settings → Support** in the BirdNET-Go web interface
2. Read the privacy notice and data collection information
3. Check **"Enable Error Tracking (Opt-in)"** if you want to help improve BirdNET-Go
4. Save your settings

That's it! No additional configuration is required. Error reports will be automatically sent to the BirdNET-Go development team for analysis.

## What is Telemetry?

Telemetry in BirdNET-Go refers to the automatic collection and transmission of technical error information to help developers:

- **Identify common failure patterns** in RTSP stream connections
- **Debug audio processing issues** that affect detection accuracy
- **Understand system stability** across different hardware configurations
- **Prioritize bug fixes** based on real-world usage patterns
- **Improve compatibility** with various audio sources and network setups

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
- Anonymous stream identifiers (not actual URLs)
- System resource errors
- Network connection patterns

## Data Collection Details

### What IS Collected ✅
- **Error messages** (with URLs and credentials anonymized)
- **Error types and categories** (connection errors, buffer overflows, etc.)
- **Component names** (ffmpeg, rtsp-connection, audio-buffer, etc.)
- **Anonymous stream identifiers** (url-abc123def456 instead of actual URLs)
- **Technical error context** (timeout values, retry counts, etc.)

### What is NOT Collected ❌
- Audio recordings or sound files
- Bird detection results or species data
- Actual RTSP URLs, IP addresses, or hostnames
- Usernames, passwords, or authentication credentials
- Personal information or user data
- File paths or directory structures
- Location data or GPS coordinates
- Any sensitive application data

### URL Anonymization Example

When an error occurs with an RTSP stream, the system automatically anonymizes the URL:

**Original URL (never sent):**
```
rtsp://admin:password123@192.168.1.100:554/cam/stream1
```

**Anonymized identifier (what gets sent):**
```
url-b0c823d0454e766694949834
```

The anonymized identifier:
- ✅ **Allows tracking** of issues with the same stream
- ✅ **Preserves error patterns** for debugging
- ❌ **Cannot be reverse-engineered** to reveal the original URL
- ❌ **Contains no sensitive information**

## Technical Implementation

### Automatic Privacy Protection
All telemetry data is automatically processed through privacy filters before transmission:

1. **URL Detection**: Regex patterns identify RTSP, HTTP, and other URLs in error messages
2. **Anonymization**: URLs are replaced with consistent, privacy-safe identifiers
3. **Credential Removal**: Any embedded usernames/passwords are completely stripped
4. **Context Preservation**: Technical error context is maintained for debugging

### Consistent Identifiers
The same URL always produces the same anonymized identifier, allowing developers to:
- Track recurring issues with specific streams
- Identify patterns in connection failures
- Understand which stream configurations are most problematic
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

- **[Privacy & Data Collection](telemetry-privacy.md)** - Detailed privacy information and data handling
- **[Setup & Configuration](telemetry-setup.md)** - Step-by-step configuration guide
- **[Troubleshooting](telemetry-troubleshooting.md)** - Common issues and solutions

## Frequently Asked Questions

### Q: Is telemetry enabled by default?
**A:** No. Error tracking is completely disabled by default and requires explicit opt-in through the web interface.

### Q: Can I see what data is being sent?
**A:** While you cannot see the exact transmitted data, all URLs and sensitive information are automatically anonymized using the privacy protection system described above.

### Q: How do I disable telemetry?
**A:** Go to Settings → Support and uncheck "Enable Error Tracking". Changes take effect immediately.

### Q: Where is the data sent?
**A:** Error reports are sent to Sentry (a privacy-compliant error tracking service) operated by the BirdNET-Go development team.

### Q: Does this affect performance?
**A:** No. The telemetry system has minimal performance impact and only activates when errors occur.

### Q: Is this GDPR compliant?
**A:** Yes. The system is designed to be fully compliant with GDPR and other privacy regulations through opt-in consent and data minimization.

---

*Last updated: June 2025*