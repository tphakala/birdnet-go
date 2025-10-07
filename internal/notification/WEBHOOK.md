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
      token: "${WEBHOOK_API_TOKEN}"  # Use environment variables for secrets
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
        "content": "**{{.Title}}**\n{{.Message}}",
        "embeds": [{
          "title": "{{.Type | title}}",
          "description": "{{.Message}}",
          "color": 15258703,
          "fields": [
            {"name": "Priority", "value": "{{.Priority}}", "inline": true},
            {"name": "Component", "value": "{{.Component}}", "inline": true}
          ],
          "timestamp": "{{.Timestamp}}"
        }]
      }

    filter:
      types: [error, detection]
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
