# Denial of Service (DoS) Protection for Push Notifications

## Overview

BirdNET-Go's push notification system is designed to be a **well-behaved internet citizen** that will never overwhelm external messaging APIs, even in the event of application malfunctions, configuration errors, or unexpected behavior.

## Multi-Layer Protection Strategy

### 1. Circuit Breaker Pattern (Primary Protection)

**Purpose**: Immediately stop requests to failing services

**How it works**:

```text
Normal Operation → Failures Detected → Circuit Opens → Service Protected
     (Closed)         (Threshold: 5)      (Block All)     (Recovery Test)
```

**Configuration** (`config.yaml`):

```yaml
circuit_breaker:
  enabled: true
  max_failures: 5 # Stop after 5 consecutive failures
  timeout: 30s # Wait 30s before testing recovery
  half_open_max_requests: 1 # Only 1 test request during recovery
```

**Protection Scenarios**:

- ✅ API endpoint down → Stops after 5 attempts, waits 30s
- ✅ Network timeout → Prevents retry storms
- ✅ Authentication failure → Blocks requests until manual intervention
- ✅ Rate limit response from API → Circuit opens, prevents further violations

**Example**:

```text
Telegram API returns 429 (Too Many Requests)
→ Circuit breaker opens after 5 failures
→ ALL subsequent notifications blocked for 30 seconds
→ After 30s, sends 1 test notification
→ If successful: Circuit closes, normal operation resumes
→ If failed: Circuit stays open for another 30s
```

### 2. Rate Limiting (Secondary Protection)

**Purpose**: Prevent bursts even when circuit is closed

**How it works**: Token bucket algorithm

- Tokens refill at a steady rate (e.g., 60/minute = 1/second)
- Each request consumes 1 token
- Burst capacity allows temporary spikes (default: 10 tokens)

**Configuration** (coming in config update):

```yaml
rate_limiting:
  enabled: true
  requests_per_minute: 60 # Average 1 req/sec
  burst_size: 10 # Allow bursts up to 10
```

**Protection Scenarios**:

- ✅ Rapid detection events → Smoothed to 1/second average
- ✅ Bug causes notification loop → Limited to 60/minute
- ✅ Multiple concurrent detections → Burst handled, then rate-limited

### 3. Retry Backoff (Tertiary Protection)

**Purpose**: Gradual retry with increasing delays

**Configuration**:

```yaml
push:
  max_retries: 3
  retry_delay: 5s
```

**Behavior**:

```text
Attempt 1: Immediate
Attempt 2: Wait 5s
Attempt 3: Wait 5s
Attempt 4: Wait 5s
Then: Give up or circuit breaker opens
```

### 4. Timeout Protection

**Purpose**: Prevent hanging connections

**Configuration**:

```yaml
push:
  default_timeout: 30s
```

**Protection**:

- No request hangs forever
- Frees resources promptly
- Counts as failure for circuit breaker

### 5. Per-Provider Isolation

**Purpose**: One failed provider doesn't affect others

**How it works**:

- Each provider has its own circuit breaker
- Each provider has its own rate limiter
- Each provider has its own health check

**Example**:

```text
Telegram circuit opens (API down)
→ Discord notifications continue working
→ Email notifications continue working
→ Only Telegram is blocked
```

## Real-World DoS Prevention Examples

### Example 1: API Endpoint Goes Down

**Without Protection**:

```text
App generates 1000 detections/hour
→ Each tries to send to Telegram (down)
→ Each hangs for 30s timeout
→ 1000 × 30s = 8.3 hours of wasted API calls
→ Telegram rate limiter sees flood, blocks your IP
```

**With BirdNET-Go Protection**:

```text
App generates 1000 detections/hour
→ First 5 fail to Telegram
→ Circuit breaker opens
→ Remaining 995 detections are blocked immediately
→ After 30s, 1 test request sent
→ If still down, wait another 30s
→ Total API load: 5-10 requests instead of 1000
→ Your IP stays in good standing
```

### Example 2: Bug Causes Notification Loop

**Scenario**: Bug causes same detection to trigger 100 times/second

**Without Protection**:

```text
100 notifications/second × 60 seconds = 6000 requests
→ Telegram API rate limit: 30 requests/second
→ Instant ban or IP block
```

**With BirdNET-Go Protection**:

```text
Rate limiter allows 10 burst requests
→ Then limits to 1 request/second (60/minute)
→ After 5 failures, circuit breaker opens
→ Total API load: ~15 requests instead of 6000
→ No API ban
```

### Example 3: Network Flakiness

**Scenario**: Your internet connection drops every 5 minutes

**Without Protection**:

```text
Every notification fails during outage
→ Retries hammer the API
→ Wasted bandwidth and CPU
→ Delays other notifications
```

**With BirdNET-Go Protection**:

```text
First 5 failures during outage
→ Circuit breaker opens
→ No further requests attempted
→ Network recovers
→ Circuit tests with 1 request after 30s
→ Resumes normal operation
```

## Safety Guarantees

### Hard Limits

1. **Maximum failure attempts**: 5 per circuit breaker cycle
2. **Maximum retries per notification**: 3 attempts
3. **Maximum concurrent requests**: Rate limited (60/minute default)
4. **Maximum hang time**: 30 seconds (configurable timeout)

### Conservative Defaults

All defaults are chosen to be **extremely safe** for external APIs:

| Setting             | Default | Reasoning                  |
| ------------------- | ------- | -------------------------- |
| max_failures        | 5       | Quick failure detection    |
| timeout             | 30s     | Most APIs complete in <10s |
| retry_delay         | 5s      | Gentle backoff             |
| max_retries         | 3       | Reasonable attempts        |
| circuit_timeout     | 30s     | Quick recovery test        |
| requests_per_minute | 60      | Well below most API limits |

### API Compliance

**Common API Rate Limits**:

- Telegram Bot API: 30 messages/second (BirdNET-Go: 1/second)
- Discord: 50 requests/second (BirdNET-Go: 1/second)
- Pushover: 10,000/month (BirdNET-Go: ~43,000/month worst case @ 1/sec)

**BirdNET-Go is 30-50x more conservative than API limits**.

## Monitoring and Observability

### Prometheus Metrics

Track DoS protection effectiveness:

```prometheus
# Circuit breaker state (0=closed, 1=half-open, 2=open)
notification_provider_circuit_breaker_state{provider="telegram"} 0

# Consecutive failures
notification_provider_consecutive_failures{provider="telegram"} 0

# Rate limit rejections (if rate limiter integrated)
notification_provider_rate_limited_total{provider="telegram"} 42

# Health status (1=healthy, 0=unhealthy)
notification_provider_health_status{provider="telegram"} 1
```

### Health Check Dashboard

```text
Provider: Telegram
Status: Healthy ✅
Circuit Breaker: Closed
Consecutive Failures: 0
Last Success: 2 seconds ago
Total Deliveries: 1,234
Success Rate: 99.2%
```

## Configuration Best Practices

### For High-Volume Applications

If you expect >100 detections/hour:

```yaml
push:
  circuit_breaker:
    max_failures: 3 # Fail faster
    timeout: 60s # Wait longer before recovery
  rate_limiting:
    requests_per_minute: 30 # More conservative
    burst_size: 5 # Smaller bursts
```

### For Rare/Critical Alerts Only

If you only send 1-5 notifications/day:

```yaml
push:
  circuit_breaker:
    max_failures: 10 # More tolerant of transient issues
    timeout: 15s # Recover faster
  rate_limiting:
    requests_per_minute: 120 # Less restrictive
    burst_size: 20 # Allow larger bursts
```

### For Testing/Development

```yaml
push:
  circuit_breaker:
    enabled: false # Disable for testing (NOT recommended for production)
  max_retries: 0 # No retries
  default_timeout: 5s # Faster timeouts
```

## Emergency Manual Intervention

If you suspect your instance is misbehaving:

### 1. Check Circuit Breaker Status

```bash
curl http://localhost:8080/metrics | grep circuit_breaker_state
```

### 2. Disable Push Notifications Temporarily

```yaml
notification:
  push:
    enabled: false
```

Then restart or reload config.

### 3. Review Logs

```bash
grep "push sent" /var/log/birdnet-go.log | wc -l  # Count sent notifications
grep "circuit breaker" /var/log/birdnet-go.log    # Check circuit breaker activity
```

## Conclusion

**BirdNET-Go is designed to be a respectful internet citizen**. The multi-layer DoS protection ensures that:

✅ **External APIs are never overwhelmed**, even if BirdNET-Go has a bug
✅ **Your IP address stays in good standing** with messaging services
✅ **Network resources are not wasted** on failed attempts
✅ **Other system functions continue** even if notifications fail
✅ **Recovery is automatic** when services come back online
✅ **Monitoring is comprehensive** so you can see what's happening

**Default settings are extremely conservative** - you're far more likely to send too few notifications than too many.
