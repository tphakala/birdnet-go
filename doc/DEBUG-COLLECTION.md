# BirdNET-Go Debug Data Collection Guide

This guide explains how to collect and analyze debug data from BirdNET-Go for performance troubleshooting.

## Prerequisites

1. BirdNET-Go running with `debug: true` in config.yaml
2. `go` tool installed for profile analysis (optional - scripts will offer automatic installation)
3. `curl` command available
4. Python 3.x (for advanced analysis, optional)

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
- Verify debug mode is enabled
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

```bash
# Specify custom host/port
BIRDNET_HOST=192.168.1.100 BIRDNET_PORT=8443 ./scripts/collect-debug-data.sh

# Longer CPU profile (60 seconds)
PROFILE_DURATION=60 ./scripts/collect-debug-data.sh
```

### Real-time Monitoring

While BirdNET-Go is running:

```bash
# Watch memory usage in real-time
watch -n 5 'curl -s http://localhost:8080/debug/pprof/heap | go tool pprof -top -unit=mb'

# Monitor goroutine count
watch -n 10 'curl -s http://localhost:8080/debug/pprof/goroutine?debug=1 | grep "goroutine profile:" | head -1'
```

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
- Disable debug mode in production after troubleshooting

## Troubleshooting Collection

### For Native Installation

If collection fails:

1. Verify debug mode: Check `debug: true` in config.yaml
2. Check connectivity: `curl http://localhost:8080/`
3. Verify authentication: If using auth, the script may need credentials
4. Check permissions: Ensure write access to current directory

### For Docker Installation

If collection fails:

1. Verify container is running: `docker ps | grep birdnet`
2. Check debug mode in container:
   ```bash
   docker exec <container-name> grep "debug:" /path/to/config.yaml
   ```
3. Verify port mapping: `docker port <container-name>`
4. Check container logs: `docker logs <container-name>`
5. Ensure debug mode is enabled and container was restarted:
   ```bash
   # Edit config.yaml to set debug: true
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
