# Webhook Provider Implementation

## Overview

The webhook provider enables BirdNET-Go to send notifications to custom HTTP/HTTPS endpoints with support for:

- **Multiple endpoints** with automatic failover
- **Flexible authentication** (Bearer, Basic, Custom headers)
- **Custom JSON templates** for payload customization
- **Per-endpoint timeouts** and retry logic
- **Context-aware cancellation** for proper resource cleanup

## Modern Go Features Used

### Go 1.24+ Features

- **`http.NoBody`**: Proper HTTP semantics for GET requests
- **`omitzero` JSON tag**: Cleaner payloads by omitting only true zero values
- **Enhanced `net/http`**: Improved connection pooling and protocol configuration

### Go 1.25 Features

- **`encoding/json/v2`** ready: Code structured for future performance improvements
- **`sync.WaitGroup.Go()`** ready: Architecture supports modern goroutine patterns

## Configuration

### Basic Configuration

```yaml
notification:
  push:
    enabled: true
    providers:
      - type: webhook
        enabled: true
        name: "custom-api"

        endpoints:
          - url: "https://api.example.com/webhooks/birdnet"
            method: POST
            timeout: 10s

        filter:
          types: [error, warning, detection]
          priorities: [high, critical]
```

### Multiple Endpoints with Failover

```yaml
notification:
  push:
    providers:
      - type: webhook
        enabled: true
        name: "production-webhooks"

        endpoints:
          # Primary endpoint
          - url: "https://primary.example.com/webhook"
            method: POST
            timeout: 5s

          # Backup endpoint (called if primary fails)
          - url: "https://backup.example.com/webhook"
            method: POST
            timeout: 10s

        filter:
          types: [error, detection]
```

### Authentication Examples

#### Bearer Token Authentication

```yaml
endpoints:
  - url: "https://api.example.com/webhook"
    method: POST
    auth:
      type: bearer
      token: "${WEBHOOK_API_TOKEN}" # Use environment variables for secrets
```

#### Basic Authentication

```yaml
endpoints:
  - url: "https://internal-api.example.com/webhook"
    method: POST
    auth:
      type: basic
      user: "${WEBHOOK_USER}"
      pass: "${WEBHOOK_PASS}"
```

#### Custom Header Authentication

```yaml
endpoints:
  - url: "https://custom-api.example.com/webhook"
    method: POST
    auth:
      type: custom
      header: "X-API-Key"
      value: "${API_KEY}"
```

### Custom Headers

```yaml
endpoints:
  - url: "https://api.example.com/webhook"
    method: POST
    headers:
      X-Custom-Header: "custom-value"
      X-Request-ID: "birdnet-go"
      User-Agent: "BirdNET-Go/1.0"
```

### Custom JSON Template

```yaml
providers:
  - type: webhook
    enabled: true
    name: "custom-format"

    # Custom Go template for JSON payload
    template: |
      {
        "event": "{{.Type}}",
        "severity": "{{.Priority}}",
        "title": "{{.Title}}",
        "description": "{{.Message}}",
        "timestamp": "{{.Timestamp}}",
        "component": "{{.Component}}",
        "metadata": {{.MetadataJSON}}
      }

    endpoints:
      - url: "https://api.example.com/events"
        method: POST
```

## Security Best Practices

### Secret Management

The webhook provider supports three methods for managing secrets, listed from most to least secure:

#### 1. File-Based Secrets (Recommended for Production)

Best for Kubernetes, Docker Swarm, and other orchestration platforms that support secret mounting.

```yaml
endpoints:
  - url: "https://api.example.com/webhook"
    auth:
      type: bearer
      token_file: "/run/secrets/api_token" # Read from mounted secret file
```

**Docker Swarm Example:**

```yaml
services:
  birdnet:
    image: ghcr.io/tphakala/birdnet-go:latest
    secrets:
      - webhook_token
secrets:
  webhook_token:
    file: ./secrets/webhook_token.txt
```

**Kubernetes Example:**

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: webhook-secrets
type: Opaque
data:
  token: <base64-encoded-token>
---
apiVersion: v1
kind: Pod
spec:
  containers:
    - name: birdnet-go
      volumeMounts:
        - name: secrets
          mountPath: "/run/secrets"
          readOnly: true
  volumes:
    - name: secrets
      secret:
        secretName: webhook-secrets
```

**File Permissions:** Set restrictive permissions on secret files:

```bash
chmod 400 /run/secrets/webhook_token  # Read-only for owner
```

#### 2. Environment Variables (Recommended for Docker)

Best for Docker containers and simple deployments.

```yaml
endpoints:
  - url: "https://api.example.com/webhook"
    auth:
      type: bearer
      token: "${WEBHOOK_TOKEN}" # Expands from environment variable
```

**Docker Example:**

```bash
docker run -e WEBHOOK_TOKEN=your-secret-token ghcr.io/tphakala/birdnet-go:latest
```

**Docker Compose Example:**

```yaml
services:
  birdnet:
    image: ghcr.io/tphakala/birdnet-go:latest
    environment:
      WEBHOOK_TOKEN: ${WEBHOOK_TOKEN} # From host environment
```

**Systemd Service Example:**

```ini
[Service]
Environment="WEBHOOK_TOKEN=your-secret-token"
ExecStart=/usr/local/bin/birdnet-go
```

**Default Values:** Use `${VAR:-default}` syntax for optional variables:

```yaml
token: "${WEBHOOK_TOKEN:-fallback-token}"
```

#### 3. Direct Values (Development Only)

**⚠️ WARNING:** Only use for local development. Never commit secrets to version control.

```yaml
endpoints:
  - url: "http://localhost:8080/webhook"
    auth:
      type: bearer
      token: "dev-token-123" # Literal value - NOT for production!
```

### Security Checklist

- [ ] **Never commit secrets to git**
  - Add `config.yaml` to `.gitignore` if it contains secrets
  - Use `.env` files for development (also add to `.gitignore`)
  - Use secret management for production

- [ ] **Use HTTPS in production**
  - Webhook URLs should use `https://` not `http://`
  - Validate SSL certificates (BirdNET-Go does this by default)

- [ ] **Rotate secrets regularly**
  - Change tokens/passwords periodically
  - Revoke old tokens after rotation

- [ ] **Limit token permissions**
  - Use webhook-specific tokens, not admin/root tokens
  - Grant minimum required permissions

- [ ] **Monitor for leaks**
  - Check logs don't contain tokens
  - BirdNET-Go never logs secret values

- [ ] **Secure your endpoints**
  - Validate webhook signatures if your API supports them
  - Implement rate limiting on receiving endpoints
  - Use IP allowlists if possible

### Mixing Secret Sources

You can mix environment variables and file references in the same configuration:

```yaml
endpoints:
  - url: "https://api.example.com/webhook"
    auth:
      type: basic
      user: "${API_USER}" # From environment
      pass_file: "/run/secrets/api_pass" # From file
```

**Precedence:** File references always take precedence over value fields when both are provided.

### Multi-Platform Secret Management

| Platform           | Recommended Method     | Example                           |
| ------------------ | ---------------------- | --------------------------------- |
| **Docker**         | Environment variables  | `-e TOKEN=xyz`                    |
| **Docker Compose** | Environment + secrets  | `secrets:` + `environment:`       |
| **Kubernetes**     | Mounted secrets        | `volumeMounts` from `Secret`      |
| **Systemd**        | Environment in service | `Environment=` in `.service` file |
| **Binary**         | Environment or files   | `export TOKEN=xyz` or config file |
| **Development**    | `.env` file            | Load with `source .env`           |

## Default Payload Structure

When no custom template is specified, the webhook provider sends:

```json
{
  "id": "notification-uuid",
  "type": "error",
  "priority": "critical",
  "title": "RTSP Stream Failed",
  "message": "Connection timeout to rtsp://...",
  "component": "rtsp",
  "timestamp": "2024-01-20T10:30:00Z",
  "metadata": {
    "url": "rtsp://...",
    "error_code": "TIMEOUT",
    "confidence": 0.95
  }
}
```

**Note**: Fields with zero values are omitted (`omitzero` tag) for cleaner payloads.

## Detection Metadata Fields

Detection notifications include additional metadata fields with the `bg_` prefix (BirdNET-Go specific fields). These fields are available in custom templates for webhook providers.

### Template Safety and Error Handling

Go templates used in webhook providers follow these rules:

- **Accessing undefined fields**: Produces empty strings (no error thrown)
- **Type safety**: Metadata values are type-asserted when accessed
- **Nil safety**: Missing metadata keys return empty values

**Best Practices**:
```yaml
# Always use conditionals for optional fields
{{if .Metadata.bg_latitude}}
  "location": {"lat": {{.Metadata.bg_latitude}}, "lon": {{.Metadata.bg_longitude}}}
{{end}}

# Check for non-zero GPS coordinates
{{if ne .Metadata.bg_latitude 0.0}}
  "gps": "{{.Metadata.bg_latitude}}, {{.Metadata.bg_longitude}}"
{{end}}

# Provide fallback values
{{.Metadata.bg_detection_url | default "N/A"}}
```

**Testing Templates**: Test your templates with various scenarios:
- Detections with GPS configured
- Detections without GPS (bg_latitude/bg_longitude will be 0)
- Different confidence levels
- Various time formats (24h vs 12h)

### Available Metadata Fields

| Field | Type | Example | Description |
|-------|------|---------|-------------|
| `{{.Metadata.bg_detection_url}}` | string | `http://host/ui/detections/123` | Link to detection details page |
| `{{.Metadata.bg_image_url}}` | string | `http://host/api/v2/media/...` | Species image URL |
| `{{.Metadata.bg_confidence_percent}}` | string | "95" | Confidence percentage (without % sign) |
| `{{.Metadata.bg_detection_time}}` | string | "15:04:05" | Time of detection (24h or 12h format) |
| `{{.Metadata.bg_detection_date}}` | string | "2025-10-27" | Date of detection (YYYY-MM-DD) |
| `{{.Metadata.bg_latitude}}` | float64 | 45.123456 | GPS latitude (0 if not configured) |
| `{{.Metadata.bg_longitude}}` | float64 | -122.987654 | GPS longitude (0 if not configured) |

### Type Safety in Templates

Metadata fields are stored in a `map[string]interface{}` and require type awareness when used in templates:

**Important Type Information:**
- `bg_latitude` and `bg_longitude` are `float64` (numeric values)
- `bg_confidence_percent` is `string` (text, not numeric!)
- All other `bg_*` fields are strings
- Missing fields return zero values (0 for numbers, "" for strings)
- No errors are thrown for undefined fields

**Safe Usage Patterns:**

```yaml
# Numeric comparison for GPS (use numeric 0.0, not string "0")
{{if ne .Metadata.bg_latitude 0.0}}
  "location": {
    "lat": {{.Metadata.bg_latitude}},
    "lon": {{.Metadata.bg_longitude}}
  }
{{end}}

# String fields can be checked with empty string
{{if .Metadata.bg_detection_url}}
  "url": "{{.Metadata.bg_detection_url}}"
{{end}}

# Confidence is STRING, not number - don't use numeric comparisons
{{if .Metadata.bg_confidence_percent}}
  "confidence": "{{.Metadata.bg_confidence_percent}}%"
{{end}}
```

**Conditional GPS**: Use template conditionals to include GPS only when available:

```yaml
template: |
  {
    "species": "{{.Title}}",
    {{if ne .Metadata.bg_latitude 0.0}}
    "location": {
      "lat": {{.Metadata.bg_latitude}},
      "lon": {{.Metadata.bg_longitude}}
    },
    {{end}}
    "confidence": "{{.Metadata.bg_confidence_percent}}%"
  }
```

### Privacy Considerations

**⚠️ Important**: Detection notifications may include sensitive data when sent to external services:

- **GPS Coordinates**: Exact location (latitude/longitude) if configured
- **Detection URLs**: May expose internal network information
- **Species Data**: What birds were detected and when

**Recommendations**:
- Use external webhooks only with trusted services
- Prefer self-hosted or local services for sensitive deployments
- Consider using VPN or SSH tunnels for external webhooks
- Review what data is exposed in your custom templates

**Setting Host URLs**: Configure proper base URLs to avoid exposing `localhost`:
```yaml
# In config.yaml
security:
  host: "birdnet.example.com"  # Your public or VPN hostname
```

Or set environment variable:
```bash
export BIRDNET_HOST="birdnet.example.com"
```

When localhost URLs are used with external webhooks, BirdNET-Go logs an informational warning at startup.

## Use Cases

### Use Case 1: Integration with IFTTT/Zapier

```yaml
providers:
  - type: webhook
    enabled: true
    name: "ifttt"

    endpoints:
      - url: "https://maker.ifttt.com/trigger/birdnet_detection/with/key/${IFTTT_KEY}"
        method: POST

    filter:
      types: [detection]
      metadata_filters:
        species_list: "interesting"
        confidence: ">0.9"
```

### Use Case 2: Custom Logging Service

```yaml
providers:
  - type: webhook
    enabled: true
    name: "log-aggregator"

    endpoints:
      - url: "https://logs.example.com/api/v1/events"
        method: POST
        auth:
          type: bearer
          token: "${LOG_API_TOKEN}"

    template: |
      {
        "source": "birdnet-go",
        "level": "{{.Priority}}",
        "message": "{{.Title}}: {{.Message}}",
        "fields": {
          "type": "{{.Type}}",
          "component": "{{.Component}}"
        }
      }
```

### Use Case 3: Slack/Discord Integration

**Note**: Discord embed color `5814783` is green (hex `0x58B05F`). Adjust for your needs:
- Red: `15158332` (0xE74C3C)
- Blue: `3447003` (0x3498DB)
- Yellow: `16776960` (0xFFFF00)

```yaml
providers:
  - type: webhook
    enabled: true
    name: "discord"

    endpoints:
      - url: "${DISCORD_WEBHOOK_URL}"
        method: POST

    template: |
      {
        "embeds": [{
          "title": "{{.Title}}",
          "url": "{{.Metadata.bg_detection_url}}",
          "description": "{{.Message}}",
          "color": 5814783,
          "thumbnail": {
            "url": "{{.Metadata.bg_image_url}}"
          },
          "fields": [
            {"name": "Confidence", "value": "{{.Metadata.bg_confidence_percent}}%", "inline": true},
            {"name": "Time", "value": "{{.Metadata.bg_detection_date}} {{.Metadata.bg_detection_time}}", "inline": true}{{if ne .Metadata.bg_latitude 0.0}},
            {"name": "Location", "value": "{{.Metadata.bg_latitude}}, {{.Metadata.bg_longitude}}", "inline": true}{{end}}
          ],
          "timestamp": "{{.Timestamp}}"
        }]
      }

    filter:
      types: [detection]
      priorities: [high, critical]
```

### Use Case 4: Home Assistant Integration

```yaml
providers:
  - type: webhook
    enabled: true
    name: "home-assistant"

    endpoints:
      - url: "http://homeassistant.local:8123/api/webhook/${HA_WEBHOOK_ID}"
        method: POST
        timeout: 5s

    template: |
      {
        "event_type": "birdnet_{{.Type}}",
        "data": {
          "title": "{{.Title}}",
          "message": "{{.Message}}",
          "priority": "{{.Priority}}",
          "timestamp": "{{.Timestamp}}"
        }
      }
```

## Advanced Configuration

### Complete Example with All Features

```yaml
notification:
  push:
    enabled: true
    default_timeout: 30s
    max_retries: 3
    retry_delay: 5s

    # Circuit breaker configuration
    circuit_breaker:
      enabled: true
      max_failures: 5
      timeout: 30s
      half_open_max_requests: 1

    # Health check configuration
    health_check:
      enabled: true
      interval: 60s
      timeout: 10s

    # Rate limiting configuration
    rate_limiting:
      enabled: true
      requests_per_minute: 60
      burst_size: 10

    providers:
      - type: webhook
        enabled: true
        name: "production-api"

        # Multiple endpoints with failover
        endpoints:
          # Primary endpoint with short timeout
          - url: "https://api-primary.example.com/webhook"
            method: POST
            timeout: 5s
            headers:
              X-Environment: "production"
              X-Source: "birdnet-go"
            auth:
              type: bearer
              token: "${PRIMARY_API_TOKEN}"

          # Backup endpoint with longer timeout
          - url: "https://api-backup.example.com/webhook"
            method: PUT
            timeout: 15s
            auth:
              type: basic
              user: "${BACKUP_USER}"
              pass: "${BACKUP_PASS}"

        # Custom JSON template
        template: |
          {
            "event": "{{.Type}}",
            "severity": "{{.Priority}}",
            "summary": "{{.Title}}",
            "details": "{{.Message}}",
            "source": {
              "component": "{{.Component}}",
              "timestamp": "{{.Timestamp}}"
            },
            "context": {{.MetadataJSON}}
          }

        # Filter configuration
        filter:
          types: [error, warning, detection]
          priorities: [medium, high, critical]
          components: [rtsp, mqtt, detection]
          metadata_filters:
            confidence: ">0.85"
            species_list: "interesting"
```

## Testing Your Webhook

Use the BirdNET-Go notification test command:

```bash
# Test webhook with a test notification
birdnet-go notify --type=error --title="Test Webhook" --message="Testing webhook integration"
```

## Monitoring and Metrics

The webhook provider integrates with BirdNET-Go's Prometheus metrics:

- `notification_provider_deliveries_total{provider="webhook",status="success"}`
- `notification_provider_delivery_duration_seconds{provider="webhook"}`
- `notification_provider_delivery_errors_total{provider="webhook",category="timeout"}`
- `notification_provider_circuit_breaker_state{provider="webhook"}`
- `notification_provider_health_status{provider="webhook"}`

Access metrics at: `http://localhost:8080/metrics`

## Troubleshooting

### Common Issues

**Issue**: Webhook not sending

- Check: `notification.push.enabled: true`
- Check: Provider `enabled: true`
- Verify: Endpoint URL is correct
- Test: Run `birdnet-go notify` command

**Issue**: Authentication failures

- Verify: Environment variables are set
- Check: Token/credentials are valid
- Test: Use `curl` to test endpoint manually

**Issue**: Timeouts

- Increase: `endpoint.timeout` value
- Check: Network connectivity to endpoint
- Verify: Endpoint is responding within timeout

**Issue**: All endpoints fail

- Check: Circuit breaker hasn't opened
- Verify: Rate limiting isn't blocking requests
- Review: Logs for specific error messages

### Debug Logging

Enable debug logging in `config.yaml`:

```yaml
debug: true
```

Check logs at `~/.config/birdnet-go/birdnet-go.log`

## Architecture Notes

### Reusable HTTP Client

The webhook provider uses a shared `httpclient` package that can be reused for:

- Other webhook integrations
- External API calls (weather, bird databases)
- Health check endpoints
- Future HTTP-based features

### Context Management

All webhook calls properly handle context cancellation:

- Immediate cleanup on application shutdown
- Timeout enforcement at multiple levels
- No hanging connections or goroutine leaks

### Performance Optimizations

- Connection pooling (100 max idle connections)
- Struct pointer passing (avoids 144-byte copies)
- Bounded concurrency (semaphore-based dispatch limiting)
- Exponential backoff with jitter for retries

## Future Enhancements

Potential features for future releases:

1. **Retry Strategies**: Configurable retry policies per endpoint
2. **Response Validation**: Validate webhook responses against schema
3. **Notification Batching**: Group multiple notifications into single webhook call
4. **Webhook Templates Library**: Pre-built templates for popular services
5. **Dynamic Endpoint Selection**: Route based on notification content
6. **Two-Way Webhooks**: Handle responses and callbacks

## Contributing

When modifying the webhook provider:

1. **Run tests**: `go test -v -race ./internal/notification/push_webhook_test.go`
2. **Run linter**: `golangci-lint run ./internal/notification/push_webhook.go`
3. **Update docs**: Keep this file and code comments in sync
4. **Follow patterns**: Use existing Go 1.24/1.25 patterns in the code

## References

- [Push Notification System Documentation](./METRICS_AND_HEALTH_CHECKS.md)
- [DoS Protection Guide](./DOS_PROTECTION.md)
- [GitHub Issue #882](https://github.com/tphakala/birdnet-go/issues/882)
