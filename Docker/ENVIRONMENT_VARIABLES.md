# BirdNET-Go Container Environment Variables

This document describes environment variables that control container startup behavior and configuration.

## Container Startup Variables

These variables affect how the container initializes and handles errors during startup.

### `BIRDNET_UID` / `BIRDNET_GID`

**Purpose:** Set the user and group ID for file ownership inside the container.

**Default:** `1000`

**Usage:**
```yaml
environment:
  - BIRDNET_UID=1000
  - BIRDNET_GID=1000
```

**Description:**
- Controls ownership of `/config` and `/data` directories
- Important for permission compatibility between host and container
- Use `id -u` and `id -g` on your host to find your user/group IDs
- Required for rootful containers (running as root)
- Ignored in rootless container mode

**Example:**
```bash
# Find your user ID
id -u  # Output: 1000

# Find your group ID
id -g  # Output: 1000

# Use in docker-compose.yml
environment:
  - BIRDNET_UID=1000
  - BIRDNET_GID=1000
```

---

### `BIRDNET_STARTUP_FAIL_DELAY`

**Purpose:** Configure how long the container waits before exiting after a startup error.

**Default:** `10` (seconds)

**Usage:**
```yaml
environment:
  - BIRDNET_STARTUP_FAIL_DELAY=10
```

**Description:**
- Ensures error messages are visible in logs before container exits
- Useful in orchestrated environments (Kubernetes, Docker Swarm)
- Prevents rapid restart loops from hiding error messages
- Applies to disk space errors, permission errors, and config issues

**When to adjust:**
- **Increase (20-30s):** For slow log collection systems or manual debugging
- **Decrease (5s):** For fast automated restart policies with external monitoring
- **Keep default (10s):** For most use cases

**Example scenarios:**

```yaml
# Fast restart in Kubernetes with external monitoring
environment:
  - BIRDNET_STARTUP_FAIL_DELAY=5

# Manual debugging, want time to check logs
environment:
  - BIRDNET_STARTUP_FAIL_DELAY=30
```

---

### `TZ`

**Purpose:** Set the container's timezone.

**Default:** `UTC`

**Usage:**
```yaml
environment:
  - TZ=America/Denver
```

**Description:**
- Affects timestamps in logs and detection records
- Uses standard IANA timezone database names
- Validates timezone exists, falls back to UTC if invalid
- Warns about legacy timezone formats (US/*, Etc/*)

**Common timezones:**
- `America/New_York` - Eastern Time
- `America/Chicago` - Central Time
- `America/Denver` - Mountain Time
- `America/Los_Angeles` - Pacific Time
- `Europe/London` - UK Time
- `Europe/Paris` - Central European Time

**Find your timezone:**
```bash
# List available timezones
ls /usr/share/zoneinfo/

# Or use timedatectl on systemd systems
timedatectl list-timezones
```

---

### `BIRDNET_MODELPATH`

**Purpose:** Override the default BirdNET model file path.

**Default:** `/data/models/BirdNET_GLOBAL_6K_V2.4_Model_FP32.tflite`

**Usage:**
```yaml
environment:
  - BIRDNET_MODELPATH=/data/models/custom_model.tflite
```

**Description:**
- Allows using custom or alternative BirdNET models
- Path must be accessible inside the container
- Model file must be compatible with BirdNET-Go
- Useful for testing new models or regional variants

---

## Container Health Check

The container includes a built-in health check that monitors the application's web interface.

**Configuration:**
- **Interval:** 30 seconds between checks
- **Timeout:** 10 seconds per check
- **Start period:** 120 seconds (extended for Raspberry Pi compatibility)
- **Retries:** 3 failed checks before marking unhealthy

**Check command:**
```bash
curl -f http://localhost:8080/ || exit 1
```

**Note:** The health check assumes the application runs on port 8080 (the default). If you've changed the port in `config.yaml`, the health check will fail, but the application will still work.

**View health status:**
```bash
docker inspect --format '{{json .State.Health}}' birdnet-go | jq
```

---

## Startup Execution Chain

Understanding the container startup sequence helps with troubleshooting:

1. **entrypoint.sh**
   - Sets up user permissions (UID/GID)
   - Configures timezone (TZ)
   - Creates necessary directories
   - Performs pre-flight checks:
     - Disk space (minimum 1GB on /data)
     - Config directory writability
   - Exits with clear error message if checks fail

2. **startup-wrapper.sh**
   - Wraps the application process
   - Captures stdout/stderr to log file
   - Forwards signals (SIGTERM, SIGINT) for graceful shutdown
   - Detects and reports common startup errors
   - Delays exit on failure (BIRDNET_STARTUP_FAIL_DELAY)

3. **birdnet-go**
   - The actual BirdNET-Go application
   - Inherits environment from previous scripts
   - Configuration from `/config/config.yaml`

---

## Troubleshooting

### Container exits immediately

Check logs for error messages:
```bash
docker logs birdnet-go
```

Common causes:
- Insufficient disk space on `/data` volume
- `/config` directory not writable
- Invalid timezone in `TZ` variable

### Permission errors

Ensure UID/GID match your host user:
```bash
# Check file ownership on host
ls -la ./config ./data

# Set matching UID/GID in docker-compose.yml
environment:
  - BIRDNET_UID=1000
  - BIRDNET_GID=1000
```

### Health check failing

The health check requires the web interface to be accessible on port 8080. If you've changed the port in `config.yaml`, this is expected and can be ignored as long as the application works.

### Viewing detailed startup logs

The startup wrapper saves detailed logs to `/tmp/birdnet-startup.log` inside the container:

```bash
docker exec birdnet-go cat /tmp/birdnet-startup.log
```

---

## Example docker-compose.yml

```yaml
version: '3.8'

services:
  birdnet-go:
    image: ghcr.io/tphakala/birdnet-go:latest
    container_name: birdnet-go
    restart: unless-stopped

    environment:
      # User/Group IDs for file permissions
      - BIRDNET_UID=1000
      - BIRDNET_GID=1000

      # Timezone configuration
      - TZ=America/Denver

      # Startup error delay (optional)
      - BIRDNET_STARTUP_FAIL_DELAY=10

      # Custom model path (optional)
      # - BIRDNET_MODELPATH=/data/models/custom_model.tflite

    volumes:
      - ./config:/config
      - ./data:/data
      - /dev/snd:/dev/snd  # For audio capture

    ports:
      - "8080:8080"

    devices:
      - /dev/snd:/dev/snd  # Audio device access
```

---

## See Also

- [Docker Compose Guide](../doc/wiki/docker_compose_guide.md)
- [Dockerfile](../Dockerfile) - Container build configuration
- [entrypoint.sh](entrypoint.sh) - Container initialization script
- [startup-wrapper.sh](startup-wrapper.sh) - Application wrapper script
