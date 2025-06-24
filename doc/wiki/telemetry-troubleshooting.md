# Telemetry Troubleshooting

This guide helps you diagnose and resolve issues with BirdNET-Go's error tracking system.

## Common Issues

### Telemetry Not Enabling

#### Problem: Checkbox doesn't stay checked
**Symptoms:**
- Check "Enable Error Tracking" but it becomes unchecked after saving
- Settings don't persist between browser sessions

**Causes & Solutions:**

1. **Browser cache issues**
   ```bash
   # Clear browser cache and cookies for BirdNET-Go
   # Or try in an incognito/private browser window
   ```

2. **Permission issues**
   ```bash
   # Check file permissions for config file
   ls -la config.yaml
   
   # Fix if needed (adjust ownership as appropriate)
   chown user:group config.yaml
   chmod 644 config.yaml
   ```

3. **Configuration file corruption**
   ```bash
   # Backup current config
   cp config.yaml config.yaml.backup
   
   # Validate YAML syntax
   cat config.yaml | python -c "import yaml, sys; yaml.safe_load(sys.stdin)"
   ```

#### Problem: "Sentry initialization failed" in logs
**Log message:** `❌ Sentry initialization failed: [error details]`

**Solutions:**

1. **Check network connectivity**
   ```bash
   # Test HTTPS connectivity to Sentry
   curl -I https://sentry.io
   
   # Should return HTTP 200 OK or similar
   ```

2. **Verify proxy configuration**
   ```bash
   # Check proxy environment variables
   echo $HTTP_PROXY
   echo $HTTPS_PROXY
   echo $NO_PROXY
   
   # Test with proxy
   curl -I https://sentry.io --proxy $HTTP_PROXY
   ```

3. **Firewall blocking outbound connections**
   - Ensure outbound HTTPS (port 443) is allowed
   - Whitelist `*.sentry.io` domains if using domain filtering

### Error Reports Not Being Sent

#### Problem: Telemetry enabled but no errors being tracked
**Symptoms:**
- Telemetry shows as enabled
- Errors occur in BirdNET-Go
- No improvement in error patterns over time

**Diagnosis:**

1. **Check if errors are actually occurring**
   ```bash
   # Review recent logs for error messages
   tail -100 birdnet.log | grep -i error
   
   # Look for connection failures
   tail -100 birdnet.log | grep -i "connection\|timeout\|failed"
   ```

2. **Verify telemetry initialization**
   ```bash
   # Look for successful initialization
   grep "Sentry telemetry initialized successfully" birdnet.log
   
   # Check for initialization errors
   grep "Sentry.*failed" birdnet.log
   ```

3. **Test network connectivity**
   ```bash
   # Test DNS resolution
   nslookup sentry.io
   
   # Test HTTPS connection
   timeout 10 openssl s_client -connect sentry.io:443 -servername sentry.io
   ```

**Solutions:**

1. **Network connectivity issues**
   - Configure proxy settings if behind corporate firewall
   - Check DNS resolution for sentry.io
   - Verify outbound HTTPS is allowed

2. **Silent failures**
   - Restart BirdNET-Go to reinitialize telemetry
   - Check system clock is correct (certificate validation)
   - Review any custom network configurations

### Performance Issues

#### Problem: BirdNET-Go slower after enabling telemetry
**Symptoms:**
- Increased response times
- Higher CPU or memory usage
- Audio processing delays

**Diagnosis:**
1. **Check resource usage**
   ```bash
   # Monitor CPU and memory
   top -p $(pgrep birdnet-go)
   
   # Check network usage
   iftop -i interface_name
   ```

2. **Review error frequency**
   ```bash
   # Count recent errors
   grep -c "ERROR\|WARN" birdnet.log | tail -20
   ```

**Solutions:**
1. **High error frequency**
   - Address underlying issues causing frequent errors
   - Check RTSP stream stability
   - Verify system resources (memory, disk space)

2. **Network congestion**
   - Ensure adequate bandwidth for error reporting
   - Consider rate limiting if in containerized environment

### Configuration Issues

#### Problem: Settings not saving correctly
**Log messages:** Various configuration-related errors

**Solutions:**

1. **File permission issues**
   ```bash
   # Check config file permissions
   ls -la config.yaml
   
   # Ensure BirdNET-Go can write to config
   sudo chown $(whoami):$(whoami) config.yaml
   ```

2. **YAML syntax errors**
   ```bash
   # Validate YAML syntax
   python3 -c "import yaml; yaml.safe_load(open('config.yaml'))"
   ```

3. **Disk space issues**
   ```bash
   # Check available disk space
   df -h .
   
   # Clean up if needed
   sudo apt clean  # Ubuntu/Debian
   # or
   sudo yum clean all  # CentOS/RHEL
   ```

## Network Troubleshooting

### Corporate Firewalls

#### Problem: Corporate network blocking telemetry
**Symptoms:**
- Telemetry timeouts
- "Connection refused" errors
- Works from home but not at office

**Solutions:**

1. **Whitelist domains**
   Add these domains to firewall whitelist:
   ```
   *.sentry.io
   *.ingest.sentry.io
   *.ingest.de.sentry.io
   ```

2. **Configure proxy**
   ```bash
   # Set proxy environment variables
   export HTTP_PROXY=http://proxy.company.com:8080
   export HTTPS_PROXY=http://proxy.company.com:8080
   
   # Restart BirdNET-Go
   sudo systemctl restart birdnet-go
   ```

3. **Request IT support**
   Provide your IT team with:
   - Destination: `*.sentry.io` (Germany region)
   - Protocol: HTTPS (port 443)
   - Purpose: Error tracking for open-source bird detection software
   - Data: Anonymous technical errors only (no personal data)

### Docker Networking

#### Problem: Container cannot reach external services
**Symptoms:**
- Telemetry works on host but not in container
- Network timeouts from container

**Solutions:**

1. **Check Docker networking**
   ```bash
   # Test connectivity from container
   docker exec container_name curl -I https://sentry.io
   
   # Check DNS resolution
   docker exec container_name nslookup sentry.io
   ```

2. **Review Docker configuration**
   ```yaml
   # In docker-compose.yml
   services:
     birdnet-go:
       # Ensure no network isolation
       network_mode: "bridge"  # or "host" if needed
       
       # Or specify custom network with internet access
       networks:
         - default
   ```

3. **Proxy in containers**
   ```yaml
   # In docker-compose.yml
   services:
     birdnet-go:
       environment:
         - HTTP_PROXY=http://proxy.company.com:8080
         - HTTPS_PROXY=http://proxy.company.com:8080
   ```

## Log Analysis

### Useful Log Commands

1. **Find telemetry-related logs**
   ```bash
   # All telemetry messages
   grep -i "sentry\|telemetry" birdnet.log
   
   # Initialization messages
   grep "telemetry.*initialized\|telemetry.*disabled" birdnet.log
   
   # Error transmission
   grep "CaptureMessage\|CaptureError" birdnet.log
   ```

2. **Check for network issues**
   ```bash
   # Connection problems
   grep -i "connection.*failed\|timeout\|refused" birdnet.log
   
   # DNS issues
   grep -i "dns\|resolve\|lookup" birdnet.log
   ```

3. **Monitor real-time telemetry**
   ```bash
   # Follow telemetry activity
   tail -f birdnet.log | grep -i "sentry\|telemetry"
   ```

### Log Message Reference

| Log Message | Meaning | Action |
|-------------|---------|---------|
| `Sentry telemetry initialized successfully` | ✅ Working correctly | None needed |
| `Sentry telemetry is disabled (opt-in required)` | ℹ️ Normal when disabled | Enable in settings if desired |
| `sentry initialization failed: [error]` | ❌ Setup problem | Check network, config, permissions |
| `Failed to capture telemetry: [error]` | ⚠️ Transmission issue | Check network connectivity |

## Getting Help

### Before Reporting Issues

1. **Gather information**
   ```bash
   # System information
   uname -a
   
   # BirdNET-Go version
   ./birdnet-go --version
   
   # Recent relevant logs
   tail -50 birdnet.log | grep -i "sentry\|telemetry\|error"
   
   # Network connectivity test
   curl -I https://sentry.io
   ```

2. **Try basic fixes**
   - Restart BirdNET-Go
   - Disable and re-enable telemetry
   - Check network connectivity
   - Review firewall settings

3. **Check known issues**
   - Review this troubleshooting guide
   - Search existing GitHub issues
   - Check the main [telemetry documentation](telemetry.md)

### Reporting Problems

When reporting telemetry issues, include:

1. **System information**
   - Operating system and version
   - BirdNET-Go version
   - Deployment method (binary, Docker, etc.)

2. **Problem description**
   - What you were trying to do
   - What happened instead
   - Error messages (if any)

3. **Logs (sanitized)**
   ```bash
   # Get relevant logs (remove any sensitive URLs manually)
   grep -i "sentry\|telemetry" birdnet.log | tail -20
   ```

4. **Network environment**
   - Behind corporate firewall?
   - Using proxy?
   - Any custom network configuration?

### Where to Get Help

- **GitHub Issues**: [BirdNET-Go Issues](https://github.com/tphakala/birdnet-go/issues)
- **Documentation**: [Main Documentation](index.md)
- **Community**: Check existing discussions and issues

## Privacy & Security Notes

### Troubleshooting Data
When troubleshooting telemetry issues:
- ✅ **Safe to share**: Log messages about telemetry initialization
- ✅ **Safe to share**: Network connectivity test results
- ❌ **Never share**: Actual RTSP URLs or credentials
- ❌ **Never share**: Configuration files containing sensitive data

### Sensitive Information
If you need to share logs for troubleshooting:
1. **Anonymize URLs**: Replace actual URLs with placeholders
2. **Remove credentials**: Strip any usernames/passwords
3. **Check twice**: Review logs before sharing

Example of safe log sharing:
```bash
# Replace sensitive info before sharing
sed 's/rtsp:\/\/[^[:space:]]*/rtsp:\/\/[ANONYMIZED]/g' birdnet.log
```

---

*For additional help, see the main [Telemetry Documentation](telemetry.md) or [Privacy Information](telemetry-privacy.md).*

*Last updated: June 2025*