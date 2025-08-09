# Privacy & Data Collection

This document provides detailed information about BirdNET-Go's privacy-compliant telemetry system, explaining exactly what data is collected, how it's protected, and where it's sent.

## Privacy Principles

BirdNET-Go's telemetry system is built on these core privacy principles:

### 🛡️ Privacy by Design

- **Data minimization**: Only essential technical error data is collected
- **Purpose limitation**: Data is used solely for debugging and improving reliability
- **Storage limitation**: Error data is automatically purged after analysis
- **Transparency**: Complete disclosure of what data is collected and how it's used

### 🔒 GDPR Compliance

- **Lawful basis**: Legitimate interest for software improvement, with clear opt-in consent
- **Data subject rights**: Users can enable/disable tracking at any time
- **Data protection**: All data is processed securely and anonymized before transmission
- **Data minimization**: Only the minimum necessary data for debugging is collected

### 🌍 Global Privacy Standards

The system meets privacy requirements for:

- **GDPR** (European Union)
- **CCPA** (California, USA)
- **PIPEDA** (Canada)
- **Other regional privacy laws**

## Data Collection Breakdown

### Technical Error Information ✅

**What**: Error messages, types, and debugging context
**Why**: Essential for identifying and fixing software bugs
**Example**: "Connection timeout after 30 seconds"
**Privacy protection**: URLs and credentials are automatically anonymized

**What**: Component names where errors occur
**Why**: Helps developers know which part of the system needs attention
**Example**: "ffmpeg-process-start", "rtsp-connection", "audio-buffer"
**Privacy protection**: No user-specific information in component names

**What**: Anonymous stream identifiers
**Why**: Allows tracking of issues with the same stream without revealing private URLs
**Example**: "url-abc123def456" (instead of actual RTSP URL)
**Privacy protection**: Original URLs are never transmitted

### System Resource Errors ✅

**What**: Memory usage, disk space, CPU load errors
**Why**: Helps identify hardware compatibility issues and resource optimization needs
**Example**: "Insufficient memory for audio buffer allocation"
**Privacy protection**: No file paths, user directories, or system-specific information

### Network Connection Patterns ✅

**What**: Connection attempt patterns, retry counts, timeout values
**Why**: Helps improve connection reliability and retry logic
**Example**: "Connection failed after 3 retries with 5-second intervals"
**Privacy protection**: No actual network addresses or credentials

### Personal Data ❌ NEVER COLLECTED

- **Audio recordings** or any sound files
- **Bird detection results** or species information
- **RTSP URLs, IP addresses, hostnames** (anonymized before transmission)
- **Usernames, passwords** or authentication credentials
- **File paths** or directory structures
- **User preferences** or configuration settings
- **Location data** or GPS coordinates
- **Device identifiers** or hardware serial numbers
- **Network topology** or internal IP ranges

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

## Anonymization Process

### URL Anonymization Algorithm

BirdNET-Go uses a sophisticated anonymization process that protects privacy while preserving debugging value:

#### Step 1: URL Detection

```regex
Pattern: \b(?:https?|rtsp|rtmp)://[^\s]+
```

Automatically finds URLs in error messages using regex patterns.

#### Step 2: Component Analysis

The URL is broken down into components:

- **Scheme**: rtsp, http, https (preserved for debugging)
- **Credentials**: username:password (completely removed)
- **Host**: IP address or hostname (categorized, not preserved)
- **Port**: port number (preserved if relevant for debugging)
- **Path**: stream path (structure preserved, content anonymized)

#### Step 3: Host Categorization

Instead of preserving actual hosts, they are categorized as:

- `localhost` for local connections
- `private-ip` for internal network addresses
- `public-ip` for internet addresses
- `domain-com` for .com domains (TLD only)

#### Step 4: Path Structure Preservation

Stream paths are analyzed to preserve structure while anonymizing content:

- Common stream names (`stream`, `live`, `cam`) → preserved as categories
- Numeric identifiers → preserved as `numeric`
- Custom names → hashed to `seg-abc123`

#### Step 5: Consistent Hashing

The anonymized components are combined and hashed using SHA-256 to create a consistent identifier that:

- ✅ Always produces the same result for the same URL
- ✅ Cannot be reverse-engineered to reveal the original
- ✅ Maintains reasonable uniqueness for debugging
- ❌ Contains no recoverable sensitive information

### Anonymization Examples

| Original URL                                  | Anonymized Identifier          | Information Preserved                             |
| --------------------------------------------- | ------------------------------ | ------------------------------------------------- |
| `rtsp://admin:pass@192.168.1.100:554/stream1` | `url-b0c823d0454e766694949834` | Protocol, private network, port, stream structure |
| `rtsp://user@10.0.0.50/cam/live`              | `url-942848b0732d3a2288ca6516` | Protocol, private network, camera/live pattern    |
| `http://public-server.com/api/stream`         | `url-8a6d125351c079f9d0a27598` | Protocol, public domain (.com), API structure     |
| `rtsp://localhost:8554/mystream`              | `url-c46a4ffb7c9a74b9ddc0a603` | Protocol, localhost, port, single stream          |

## Data Transmission

### Where Data Goes

Error reports are transmitted to:

- **Service**: Sentry (industry-standard error tracking)
- **Region**: Europe (Frankfurt, Germany data center)
- **Operator**: BirdNET-Go development team
- **Purpose**: Software debugging and improvement only

### Transmission Security

- **Encryption**: TLS 1.3 for all data in transit
- **Authentication**: Secure API keys for authorized transmission
- **Rate limiting**: Prevents excessive data transmission
- **Filtering**: All data passes through privacy filters before leaving your system

### Data Retention

- **Active errors**: Kept for 90 days for debugging purposes
- **Resolved errors**: Automatically purged after 30 days
- **Trend data**: Anonymized statistics may be retained longer for development planning
- **User control**: Disabling telemetry stops all future data collection immediately

## Technical Implementation

### Privacy Filters

All telemetry data passes through multiple privacy filters before transmission:

```go
// Automatic URL anonymization in all messages
scrubbedMessage := ScrubMessage(originalMessage)

// Error context filtering
filteredContext := FilterSensitiveContext(errorContext)

// Consistent anonymization
anonymizedID := anonymizeURL(originalURL)
```

### Error Processing Pipeline

1. **Error occurs** in BirdNET-Go
2. **Privacy filters applied** automatically
3. **URLs anonymized** using consistent hashing
4. **Context sanitized** to remove sensitive data
5. **Telemetry transmitted** (if enabled)
6. **Data received** by development team for debugging

### Local Privacy Protection

Even before transmission, BirdNET-Go protects your privacy:

- **No logging** of sensitive URLs in local log files
- **Memory safety**: Credentials never stored in memory longer than necessary
- **Configuration protection**: Sensitive settings are not included in telemetry

## User Rights & Control

### Data Subject Rights (GDPR)

- **Right to consent**: Explicit opt-in required for any data collection
- **Right to withdraw**: Can disable telemetry at any time in settings
- **Right to information**: This documentation provides complete transparency
- **Right to erasure**: Disabling telemetry stops collection; existing data is purged per retention policy
- **Right to portability**: Technical error data is not personal, but users can request information about their contributions

### Control Mechanisms

- **Settings panel**: Easy on/off toggle in web interface
- **Immediate effect**: Changes take effect without restart
- **Visual indicators**: Clear status display of telemetry state
- **No hidden tracking**: No telemetry occurs when disabled

### Using Your System ID

Your unique System ID provides a privacy-preserving way to help developers debug issues:

1. **Find your System ID**: Go to Settings → Support → Your System ID
2. **Copy it**: Use the convenient copy button next to your ID
3. **When reporting issues**: Include your System ID in GitHub issues if you want developers to correlate your error reports
4. **Privacy preserved**: The System ID cannot be linked to you without your explicit action

**Example GitHub issue**:

```markdown
I'm experiencing connection issues with my RTSP stream.
System ID: A1B2-C3D4-E5F6 (telemetry enabled)
```

This allows developers to:

- ✅ Find related error reports in telemetry data
- ✅ Better understand your specific issue
- ✅ Provide more targeted solutions
- ❌ Cannot identify you unless you share the ID

## Compliance & Auditing

### Regular Reviews

The BirdNET-Go development team regularly reviews:

- **Data collection practices** to ensure minimal necessary collection
- **Privacy filter effectiveness** to prevent sensitive data leakage
- **Anonymization quality** to maintain privacy while preserving debugging value
- **Security measures** to protect data in transit and at rest

### Third-Party Auditing

- **Sentry compliance**: The error tracking service is SOC 2 Type II certified
- **Data processing agreements**: Formal agreements ensure GDPR compliance
- **Regular security audits** of the entire data pipeline

## Contact & Questions

### Privacy Questions

For questions about privacy and data collection:

- **GitHub Issues**: [BirdNET-Go Privacy Issues](https://github.com/tphakala/birdnet-go/issues)
- **Email**: Include "Privacy" in the subject line for faster routing

### Data Protection Officer

For GDPR-related requests or concerns, contact the project maintainers through the official GitHub repository.

---

_Last updated: June 2025_
