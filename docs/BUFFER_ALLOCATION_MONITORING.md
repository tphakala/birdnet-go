# Buffer Allocation Monitoring Guide

## Overview

BirdNET-Go uses metrics to track buffer allocations and detect potential repeated allocation issues. This replaces the previous allocation tracking system with lightweight Prometheus metrics.

## Key Metrics

### `myaudio_buffer_allocation_attempts_total`

This counter tracks all buffer allocation attempts with the following labels:
- `buffer_type`: Type of buffer (e.g., "capture", "analysis")
- `source`: Source identifier (e.g., "rtsp://camera1")
- `result`: Outcome of the allocation attempt
  - `"attempted"`: Initial attempt (always recorded first)
  - `"first_allocation"`: Successful first allocation
  - `"repeated_blocked"`: Allocation blocked due to existing buffer
  - `"error"`: Allocation failed due to validation or system errors

## Monitoring Repeated Allocations

### Prometheus Queries

1. **Find sources with repeated allocation attempts:**
```promql
sum by (source) (rate(myaudio_buffer_allocation_attempts_total{result="repeated_blocked"}[5m])) > 0
```

2. **Count total repeated allocations per source:**
```promql
myaudio_buffer_allocation_attempts_total{result="repeated_blocked"}
```

3. **Ratio of blocked to successful allocations:**
```promql
sum by (source) (myaudio_buffer_allocation_attempts_total{result="repeated_blocked"}) 
/ 
sum by (source) (myaudio_buffer_allocation_attempts_total{result="first_allocation"})
```

4. **Alert on high repeated allocation rate:**
```yaml
groups:
- name: buffer_allocation_alerts
  rules:
  - alert: HighRepeatedBufferAllocations
    expr: rate(myaudio_buffer_allocation_attempts_total{result="repeated_blocked"}[5m]) > 0.1
    for: 5m
    labels:
      severity: warning
    annotations:
      summary: "High rate of repeated buffer allocation attempts"
      description: "Source {{ $labels.source }} is experiencing {{ $value }} repeated allocation attempts per second"
```

## Grafana Dashboard Example

```json
{
  "panels": [
    {
      "title": "Buffer Allocation Attempts by Result",
      "targets": [
        {
          "expr": "sum by (result) (rate(myaudio_buffer_allocation_attempts_total[5m]))"
        }
      ]
    },
    {
      "title": "Repeated Allocations by Source",
      "targets": [
        {
          "expr": "topk(10, sum by (source) (myaudio_buffer_allocation_attempts_total{result=\"repeated_blocked\"}))"
        }
      ]
    }
  ]
}
```

## Troubleshooting

If you see high rates of `repeated_blocked` allocations:

1. **Check RTSP stream stability** - Unstable streams may cause frequent reconnection attempts
2. **Review configuration changes** - Dynamic reconfiguration may trigger allocation attempts
3. **Monitor FFmpeg process lifecycle** - Frequent process restarts can cause allocation attempts
4. **Check for race conditions** - Multiple goroutines trying to allocate the same buffer

## Implementation Details

The metrics are recorded at these points in the code:

1. **At allocation start**: Records an "attempted" event
2. **On successful allocation**: Records "first_allocation" 
3. **When buffer already exists**: Records "repeated_blocked"
4. **On validation/system errors**: Records "error"

This approach provides visibility into allocation patterns without the overhead of the previous tracking system.