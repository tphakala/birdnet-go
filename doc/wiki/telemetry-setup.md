# Telemetry Setup & Configuration

This guide walks you through enabling and configuring BirdNET-Go's optional error tracking system.

## Quick Setup (Recommended)

### Step 1: Access Settings
1. Open your BirdNET-Go web interface
2. Navigate to **Settings** in the sidebar
3. Click on **Support** in the settings menu

### Step 2: Review Privacy Information
Before enabling telemetry, please review:
- The **Privacy Notice** explaining what data is collected
- The **"What Data is Collected"** section showing specific information types
- The **anonymization examples** demonstrating URL protection

### Step 3: Enable Telemetry
1. Check the box **"Enable Error Tracking (Opt-in)"**
2. Click **Save Settings** at the bottom of the page
3. You'll see a confirmation message that telemetry is now active

### Step 4: Verification
Once enabled, you should see:
- ✅ **"Error Tracking Enabled"** status message
- 📊 **Automatic error reporting** begins immediately
- 🔒 **Privacy protection** automatically applied to all reports

That's it! No additional configuration is required.

## Understanding the Interface

### Settings Page Layout

The Support settings page contains several sections:

#### Privacy Notice
- **Purpose**: Clear explanation of the telemetry system
- **What to read**: Privacy commitments and data collection practices
- **Key points**: Opt-in required, no personal data, automatic anonymization

#### Enable/Disable Toggle
- **Control**: Single checkbox to turn telemetry on/off
- **Default**: Disabled (opt-in required)
- **Effect**: Immediate - no restart needed

#### Status Display
When enabled, you'll see:
```
✅ Error Tracking Enabled
BirdNET-Go will now automatically report errors to help developers 
identify and fix issues with BirdNET-Go.

No additional configuration required. Error reports are automatically 
sent to the BirdNET-Go development team for analysis.
```

#### Data Collection Information
- **"What Data is Collected"**: Technical error information, component names, anonymous identifiers
- **"What is NOT Collected"**: Audio recordings, personal data, actual URLs, credentials

## Configuration Options

### Simple Configuration (Current)
BirdNET-Go uses a simplified configuration approach:

```yaml
# In config.yaml
sentry:
  enabled: false  # Change to true to enable telemetry
```

### Web Interface Configuration
The web interface automatically manages the configuration:
- **Enable/Disable**: Checkbox in Settings → Support
- **Status**: Visual indicators show current state
- **Changes**: Take effect immediately without restart

## Advanced Information

### Behind the Scenes
When you enable telemetry:

1. **Sentry SDK initialization**: Error tracking service starts
2. **Privacy filters activated**: URL anonymization begins
3. **Error monitoring begins**: System starts capturing relevant errors
4. **Automatic reporting**: Errors are automatically sent (if telemetry is enabled)

### Configuration File Details
The telemetry setting is stored in your main configuration:

```yaml
# Location: config.yaml or your custom config file
sentry:
  enabled: true   # or false to disable
```

**Note**: You should use the web interface instead of manually editing this file.

## Docker & Container Setup

### Docker Compose
If running BirdNET-Go in Docker, telemetry settings are managed the same way:

1. Access the web interface through your mapped port
2. Navigate to Settings → Support
3. Enable telemetry as described above
4. Settings persist in your mounted config volume

### Environment Variables
Currently, telemetry cannot be configured via environment variables. Use the web interface or configuration file.

### Container Networking
Telemetry works correctly in containerized environments:
- **Outbound HTTPS**: Requires port 443 access for error reporting
- **Proxy compatibility**: Works with most HTTP proxies
- **Firewall considerations**: Ensure outbound HTTPS is allowed

## Network Requirements

### Firewall Configuration
For telemetry to work, ensure your system can make outbound HTTPS connections:

- **Protocol**: HTTPS (port 443)
- **Destination**: `*.sentry.io` (Frankfurt, Germany region)
- **Frequency**: Only when errors occur (not continuous)
- **Fallback**: If network is unavailable, telemetry fails silently

### Proxy Support
BirdNET-Go telemetry works with standard HTTP proxies:
- **HTTP_PROXY**: Automatically detected and used
- **HTTPS_PROXY**: Used for telemetry transmission
- **NO_PROXY**: Can exclude telemetry if needed

### Offline Operation
If your BirdNET-Go installation cannot reach the internet:
- **Telemetry fails silently**: No impact on normal operation
- **Local logging continues**: System logs are unaffected
- **Error handling unchanged**: Errors are still handled locally

## Security Considerations

### Data in Transit
- **Encryption**: All telemetry uses TLS 1.3
- **Authentication**: Secure API keys prevent unauthorized access
- **Validation**: Data is validated before transmission

### Local Security
- **No credential storage**: Sensitive information is never stored locally for telemetry
- **Memory safety**: URLs and credentials are scrubbed from memory
- **Log protection**: Anonymization applies to log files as well

### Network Security
- **Rate limiting**: Prevents excessive network usage
- **Error batching**: Multiple errors may be sent together efficiently
- **Graceful failure**: Network issues don't affect BirdNET-Go operation

## Performance Impact

### Resource Usage
Telemetry has minimal impact on system performance:

- **CPU**: Less than 0.1% additional usage
- **Memory**: Under 1MB additional RAM usage
- **Network**: Only during errors (typically <1KB per error)
- **Disk**: No additional disk usage

### When Telemetry Activates
The telemetry system only becomes active when:
- ❌ **Errors occur**: Connection failures, resource issues, etc.
- ❌ **System problems**: Memory issues, disk problems, etc.
- ✅ **Normal operation**: Zero telemetry activity during normal operation

## Verification & Testing

### Confirm Telemetry is Working
To verify telemetry is properly configured:

1. **Check settings**: Ensure "Error Tracking Enabled" appears in Settings → Support
2. **Review logs**: Look for "Sentry telemetry initialized successfully" in system logs
3. **Network test**: Verify outbound HTTPS connectivity to sentry.io

### Test Error Reporting
You can test the system by temporarily causing a harmless error:
1. Configure an invalid RTSP URL
2. Check that connection errors are handled normally
3. Verify the system continues operating correctly

**Note**: You cannot see the actual telemetry data being sent, as it's anonymized and sent directly to the development team.

## Frequently Asked Questions

### Q: Do I need to restart BirdNET-Go after enabling telemetry?
**A:** No. Changes take effect immediately.

### Q: Can I see what data is being sent?
**A:** While you cannot see the exact transmitted data, all URLs and sensitive information are automatically anonymized as described in the [Privacy Documentation](telemetry-privacy.md).

### Q: Will telemetry work with my firewall/proxy?
**A:** Yes, as long as outbound HTTPS (port 443) connections are allowed. Standard HTTP proxies are automatically detected and used.

### Q: What happens if my internet connection is down?
**A:** Telemetry fails silently without affecting BirdNET-Go operation. Error tracking resumes when connectivity is restored.

### Q: Can I configure telemetry for my organization's internal error tracking?
**A:** Currently, no. Telemetry is configured to send reports to the BirdNET-Go development team only. This ensures consistent debugging information and prevents configuration errors.

### Q: How do I disable telemetry temporarily?
**A:** Go to Settings → Support and uncheck "Enable Error Tracking". Changes take effect immediately.

## Troubleshooting

For telemetry-specific issues, see the [Troubleshooting Guide](telemetry-troubleshooting.md).

---

*Last updated: June 2025*