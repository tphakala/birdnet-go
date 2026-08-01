# BirdNET-Go Debug Data Collection Guide

This guide explains how to collect and analyze debug data from BirdNET-Go for performance troubleshooting.

## Prerequisites

1. BirdNET-Go running with `diagnostics.profiling.enabled: true` in config.yaml.
   Note this is not `debug: true`, which only controls logging verbosity.
2. On an instance with no authentication provider configured, the profiling token
   from `diagnostics.profiling.token`, exported as `BIRDNET_PROFILING_TOKEN`. The
   collection scripts pass it on every request. See
   [PROFILING.md](PROFILING.md#getting-the-token-on-an-instance-with-no-authentication)
   for how to read it, since the API deliberately will not return it.
3. `go` tool installed for profile analysis (optional - scripts will offer automatic installation)
4. `curl` command available
5. Python 3.x (for advanced analysis, optional)

The collection scripts support unauthenticated and token-gated instances. They
cannot log in to an instance behind basic auth or OAuth; collect those profiles
manually as described in [PROFILING.md](PROFILING.md).

## Installing Go (Optional)

The debug collection scripts will check for Go and offer automatic installation if needed. Go is required for analyzing the collected profiling data but not for collecting it.

### Automatic Installation

When running the collection scripts, if Go is not installed, you'll be prompted:

```
Would you like to install Go 1.24.4 automatically? (y/N):
```

Answering 'y' will download and install the latest Go version for your architecture.

### Manual Installation Options

For apt-based Linux (Ubuntu/Debian/Raspberry Pi OS):

**Option 1: Official Go Release (Recommended)**

```bash
wget https://go.dev/dl/go1.24.4.linux-$(dpkg --print-architecture).tar.gz
sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf go1.24.4.linux-$(dpkg --print-architecture).tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
source ~/.bashrc
```

**Option 2: From APT Repository**

```bash
sudo apt update && sudo apt install -y golang-go
```

**Option 3: Use Docker (No Installation Required)**

```bash
docker run --rm -v $PWD:/data -w /data golang:1.24 bash analyze.sh
```

## Quick Start

### For Native Installation

Run the collection script:

```bash
cd /path/to/birdnet-go
./scripts/collect-debug-data.sh
```

### For Docker Installation

Run the Docker-specific collection script:

```bash
cd /path/to/birdnet-go
./scripts/collect-debug-data-docker.sh

# Or specify a custom container name:
BIRDNET_CONTAINER=my-birdnet-container ./scripts/collect-debug-data-docker.sh
```

This will:

- Check for Go installation (and offer automatic installation if missing)
- Verify the profiling endpoints are reachable
- Collect all profiling data
- Create a timestamped archive
- Generate an analysis script

The collection takes about 2-3 minutes and creates:

- System information
- Memory profiles (heap, allocations)
- CPU profile (30 seconds)
- Goroutine snapshots
- Mutex/blocking profiles
- Execution trace
- Time-series heap samples

### 2. Analyze Locally

```bash
cd debug-data-YYYYMMDD-HHMMSS/
./analyze.sh
```

Or use the Python analyzer for a comprehensive report:

```bash
python3 scripts/analyze-debug-data.py debug-data-YYYYMMDD-HHMMSS/
```

### 3. Share for Analysis

Upload the generated `.tar.gz` file to a file sharing service and share the link.

## Docker-Specific Features

The Docker collection script additionally collects:

- Container configuration and resource limits
- Container statistics (CPU, memory, network, disk I/O)
- Container processes
- Recent container logs
- Attempts to copy and sanitize config.yaml from the container

### Analyzing Without Go Installed

If you don't have Go installed locally, you can use Docker to analyze the profiles:

```bash
cd debug-data-docker-*/
docker run --rm -v $PWD:/data -w /data golang:1.24 bash analyze-docker.sh

# For interactive web UI:
docker run --rm -v $PWD:/data -w /data -p 8081:8081 golang:1.24 \
  go tool pprof -http=:8081 heap.pprof
```

## Advanced Usage

### Custom Collection Options

The two collectors do not read the same environment variables, because they do
not find the server the same way. The native script builds its base URL from
`BIRDNET_HOST` and `BIRDNET_PORT`; the Docker one asks `docker port` where the
container's port is published and only falls back to the container IP, so it has
no use for a host name.

| Variable                  | `collect-debug-data.sh` | `collect-debug-data-docker.sh` | Default         |
| ------------------------- | ----------------------- | ------------------------------ | --------------- |
| `BIRDNET_HOST`            | yes                     | no                             | `localhost`     |
| `BIRDNET_PORT`            | yes                     | yes (container-side port)      | `8080`          |
| `BIRDNET_CONTAINER`       | no                      | yes                            | `birdnet-go`    |
| `BIRDNET_PROFILING_TOKEN` | yes                     | yes                            | empty           |
| `PROFILE_DURATION`        | yes                     | yes                            | `30` (seconds)  |
| `PROBE_CONNECT_TIMEOUT`   | yes                     | yes                            | `15` (seconds)  |
| `PROBE_MAX_TIME`          | yes                     | yes                            | `120` (seconds) |

```bash
# Specify custom host/port (native script)
BIRDNET_HOST=192.168.1.100 BIRDNET_PORT=8443 ./scripts/collect-debug-data.sh

# Name a different container (Docker script)
BIRDNET_CONTAINER=birdnet-go-test ./scripts/collect-debug-data-docker.sh

# Longer CPU profile (60 seconds)
PROFILE_DURATION=60 ./scripts/collect-debug-data.sh
```

### Real-time Monitoring

While BirdNET-Go is running:

These assume `BIRDNET_PROFILING_TOKEN` is exported; drop the `?token=...` on an
instance that has an authentication provider configured.

Two things about the form below. Pass the URL to `go tool pprof` directly, because
it does not read a profile from stdin, so piping `curl` into it does not work. And
note the single quotes: they keep the token out of the long-lived `watch`
process's own command line, since the shell `watch` spawns expands it from the
exported environment on each iteration. The short-lived child still carries it in
its argv, so this narrows the `ps` exposure rather than removing it.

```bash
# Watch memory usage in real-time
watch -n 30 'go tool pprof -top -unit=mb -nodecount=15 "http://localhost:8080/debug/pprof/heap?token=$BIRDNET_PROFILING_TOKEN"'

# Monitor goroutine count
watch -n 10 'curl -s "http://localhost:8080/debug/pprof/goroutine?token=$BIRDNET_PROFILING_TOKEN&debug=1" | head -1'
```

Note that `go tool pprof` saves a copy of every profile it fetches under
`~/pprof/`, so a tight `watch` interval fills that directory quickly. Use a
generous interval and clean up afterwards.

### Interactive Analysis

```bash
# Open interactive web UI for heap analysis
go tool pprof -http=:8081 debug-data-*/heap.pprof

# Analyze CPU profile
go tool pprof -http=:8081 debug-data-*/cpu.pprof

# View execution trace
go tool trace debug-data-*/trace.out
```

## Understanding the Data

### Memory Profiles

- **heap.pprof**: Current memory usage and allocations
- **allocs.pprof**: All memory allocations since program start
- **time-series/heap-\*.pprof**: Memory snapshots over time

Look for:

- Total memory usage (should be <500MB for typical usage)
- Memory growth between snapshots
- Large allocations by specific functions

### CPU Profile

Shows where CPU time is spent. Look for:

- Functions consuming >10% CPU
- High GC (garbage collection) overhead
- Excessive syscall time

### Goroutine Profile

Shows all running goroutines. Look for:

- Total count (should be <1000 for normal operation)
- Blocked goroutines
- Goroutine leaks (count growing over time)

### Mutex/Block Profiles

Shows contention and blocking. Look for:

- High contention on specific mutexes
- Long blocking operations

## Common Issues and Solutions

### High Memory Usage

Symptoms:

- Heap profile shows >1GB usage
- Memory growing over time

Common causes:

- Audio buffer accumulation
- Image cache not being cleaned
- Goroutine leaks

### High CPU Usage

Symptoms:

- CPU profile shows high usage
- System feels sluggish

Common causes:

- Excessive audio processing
- Too many concurrent operations
- Inefficient algorithms

### Goroutine Leaks

Symptoms:

- Goroutine count >1000 and growing
- Memory usage increasing

Common causes:

- Unclosed channels
- Infinite loops in goroutines
- Missing context cancellation

## Automated Monitoring

Set up a cron job for periodic collection:

```bash
# Add to crontab (every 6 hours)
0 */6 * * * /path/to/birdnet-go/scripts/collect-debug-data.sh >> /var/log/birdnet-debug.log 2>&1
```

## Security Considerations

- Debug data may contain sensitive information
- Only share data with trusted parties
- Consider sanitizing system information before sharing
- Set `diagnostics.profiling.enabled` back to `false` after troubleshooting, and
  reset `blockrate` and `mutexfraction` to `0` if you changed them, since those
  two cost CPU continuously
- Treat the profiling token as a credential. Rotate it, by deleting the `token:`
  line under `diagnostics.profiling` and restarting, if you shared a config file

## Troubleshooting Collection

When a profiling request fails, both collectors print the HTTP status code they
got, so the number the steps below key off is in the script's own output and you
do not have to reproduce the request by hand. The initial connectivity check is
the exception: it only reports whether any response arrived at all, because at
that point any status means the server is up.

### For Native Installation

If collection fails:

1. Verify profiling is enabled: check `diagnostics.profiling.enabled: true` in
   config.yaml, and that BirdNET-Go was restarted after the edit. `debug: true`
   does not enable profiling. A `404` from `/debug/pprof/` means it is off.
2. Check connectivity: `curl http://localhost:8080/`
3. Verify the token: a `403` on an instance with no authentication provider means
   `BIRDNET_PROFILING_TOKEN` is unset or stale. Re-read it from config.yaml.
4. Verify authentication: a `401`, or a `302` to `/login`, means an authentication
   provider is configured. The scripts cannot log in, and neither can
   `go tool pprof`. Collect those profiles with the login-then-fetch recipe in
   [PROFILING.md](PROFILING.md#on-an-instance-that-does-have-authentication-configured).
5. Check the port: a `410` means you are on the telemetry listener (default 8090).
   Profiling moved to the web server port (default 8080). A refused connection on
   8090 means the same thing on an instance where telemetry is switched off.
6. Check permissions: ensure write access to current directory

### For Docker Installation

If collection fails:

1. Verify container is running: `docker ps | grep birdnet`
2. Check profiling is enabled in the container:
   ```bash
   docker exec <container-name> awk 'NR==1{sub(/^\357\273\277/,"")} /^[^[:space:]#]/{d=($0~/^diagnostics:/);p=0;next} d&&p&&/[^[:space:]]/{i=match($0,/[^[:space:]]/);if(i<=pi&&substr($0,i,1)!="#")p=0} d&&!p&&/^[[:space:]]*profiling:/{p=1;pi=match($0,/[^[:space:]]/);next} d&&p&&/^[[:space:]]*enabled:/{print;exit}' /config/config.yaml
   ```
3. Read the profiling token, if the instance has no authentication provider:
   ```bash
   docker exec <container-name> awk 'NR==1{sub(/^\357\273\277/,"")} /^[^[:space:]#]/{d=($0~/^diagnostics:/);p=0;next} d&&p&&/[^[:space:]]/{i=match($0,/[^[:space:]]/);if(i<=pi&&substr($0,i,1)!="#")p=0} d&&!p&&/^[[:space:]]*profiling:/{p=1;pi=match($0,/[^[:space:]]/);next} d&&p&&/^[[:space:]]*token:/{sub(/\r$/,"");sub(/^[[:space:]]*token:[[:space:]]*/,"");sub(/[[:space:]]+#.*$/,"");sub(/[[:space:]]+$/,"");if($0~/^".*"$/||$0~/^\047.*\047$/)$0=substr($0,2,length($0)-2);print;exit}' /config/config.yaml
   ```
4. Verify port mapping: `docker port <container-name>`
5. Check container logs: `docker logs <container-name>`
6. Ensure profiling is enabled and the container was restarted:
   ```bash
   # Edit config.yaml to set diagnostics.profiling.enabled: true
   docker restart <container-name>
   ```

## Next Steps

After collecting and analyzing data:

1. Review the analysis report for issues
2. Check the GitHub issues for similar problems
3. Share findings with the development team
4. Implement recommended optimizations
5. Re-run collection to verify improvements

For help interpreting results, please include:

- The analysis report (analysis-report.md)
- System specifications
- BirdNET-Go configuration
- Description of the performance issue
