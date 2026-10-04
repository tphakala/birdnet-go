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
- If `BIRDNET_UID` is `0`, remove the `/run/dbus` line from the compose file: uid 0 on the host system bus is host root

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
- Warns about legacy timezone formats (US/_, Etc/_)

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

### `BIRDNET_WEBSERVER_PORT`

**Purpose:** Set the port the web interface listens on.

**Default:** `8080` (the `webserver.port` setting)

**Usage:**

```yaml
environment:
  - BIRDNET_WEBSERVER_PORT=8080
```

**Description:**

- Used by `Docker/docker-compose.host.yml` (host networking), where there is no port mapping and the app listens on this port directly. The Compose file sets it from `WEB_PORT`
- Use 1024 or higher: the app runs as a non-root user
- Takes precedence over `webserver.port` in `config.yaml`
- The value is written to `config.yaml` when settings are saved in the web interface. Going back to a bridge networking file with a custom port therefore needs `webserver.port: 8080` restored in `config.yaml`
- An invalid value (not a number from 1 to 65535) is ignored with a warning in the log, and the configured or default port is used
- The container health check follows this port (see below)

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

**Check command:** the `/health` endpoint must answer with JSON status `healthy`. The probes run in this order, and the first success wins:

1. `http://localhost:<port>/health`, where `<port>` is `BIRDNET_WEBSERVER_PORT` when it holds a valid port (1 to 65535) and 8080 otherwise. With a custom port, 8080 is not probed, so under host networking another service on the host cannot answer for the app.
2. `https://localhost:8443/health`
3. `https://localhost:443/health`

**Note:** The port follows `BIRDNET_WEBSERVER_PORT`. If you changed the port only in `config.yaml` (not through that variable) to something other than 8080, 8443 or 443, the health check fails, but the application still works.

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

The health check probes the port in `BIRDNET_WEBSERVER_PORT`, then 8080, 8443 and 443. If you changed the port only in `config.yaml` to another value, this is expected and can be ignored as long as the application works. Set `BIRDNET_WEBSERVER_PORT` to make the health check follow the port.

### `.local` hostnames do not resolve

The bridge compose files mount the host's `/run/avahi-daemon` (name resolution) and `/run/dbus` (DNS-SD service discovery) read-only. The host networking file (`docker-compose.host.yml`) mounts only `/run/avahi-daemon`. Without them the container resolves `.local` names only through unicast DNS (a router that serves them). Check with `docker exec birdnet-go getent hosts cam.local`. Rootless Docker without avahi or D-Bus on the host must remove the matching volume line. If the app runs as uid 0 (`BIRDNET_UID=0`), remove the `/run/dbus` line: uid 0 on the system bus is host root. Never add `:z` or `:Z`. Details, the D-Bus security trade-off and how to opt out: [RTSP troubleshooting](../doc/wiki/rtsp-troubleshooting.md#using-local-mdns-hostnames-in-containers).

### Subnet bypass does not skip login for LAN clients

In a container (bridge or host networking), subnet bypass applies only to the ranges listed in `security.allowsubnetbypass.subnet`. The automatic local-network check compares the client with the Docker bridge network (resolved from `host.docker.internal`), not with your LAN, so add your LAN range (for example `192.168.1.0/24`) to that list. Keep the `extra_hosts: host.docker.internal:host-gateway` line in the Compose files: without it, under host networking, the automatic check falls back to the LAN default gateway and every device on that /24 skips login.

### Viewing detailed startup logs

The startup wrapper saves detailed logs to `/tmp/birdnet-startup.log` inside the container:

```bash
docker exec birdnet-go cat /tmp/birdnet-startup.log
```

---

## Example docker-compose.yml

```yaml
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
      - /dev/snd:/dev/snd # For audio capture
      - /run/avahi-daemon:/run/avahi-daemon:ro # Host Avahi, for .local hostnames
      - /run/dbus:/run/dbus:ro # Host system D-Bus, for DNS-SD service discovery; remove it if BIRDNET_UID=0

    ports:
      - "8080:8080"

    devices:
      - /dev/snd:/dev/snd # Audio device access
```

---

## See Also

- [Docker Compose Guide](../doc/wiki/docker_compose_guide.md)
- [Dockerfile](../Dockerfile) - Container build configuration
- [entrypoint.sh](entrypoint.sh) - Container initialization script
- [startup-wrapper.sh](startup-wrapper.sh) - Application wrapper script
