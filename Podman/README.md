# BirdNET-Go Podman Configuration Files

This directory contains Podman-specific configuration files for running BirdNET-Go with the Podman container runtime.

## Compose Files

### Production Files

- **`podman-compose.yml`** - Standard configuration for running BirdNET-Go with Podman
  - Includes audio device mounting for real-time bird detection
  - Uses standard HTTP on port 8080
  - Suitable for most home/local deployments

- **`podman-compose.autotls.yml`** - HTTPS configuration with automatic TLS certificates
  - Includes Let's Encrypt integration for automatic SSL certificates
  - Requires ports 80 and 443 for ACME challenges
  - Requires a valid domain name

## Quadlet Integration

The `quadlet/` subdirectory contains systemd Quadlet unit files for native systemd integration:

- **`birdnet-go.container`** - Standard Quadlet container unit
- **`birdnet-go-autotls.container`** - HTTPS/AutoTLS Quadlet container unit
- **`birdnet-go.network`** - Bridge network configuration for Quadlet

## Environment Files

- **`.env.example`** - Template environment file for standard deployment
- **`.env.autotls.example`** - Template environment file for HTTPS/AutoTLS deployment

Copy the appropriate example file to `.env` and customize for your setup.

## Entrypoint Script

- **`entrypoint-podman.sh`** - Podman-optimized entrypoint script
  - Handles rootless container execution
  - Manages user/group permissions without requiring gosu
  - Optimized for Podman's user namespace handling

## Installation

Use the `podman-install.sh` script in the repository root to install BirdNET-Go with Podman:

```bash
bash podman-install.sh
```

The script will:

1. Check system compatibility (requires Debian 13+, Ubuntu 25.04+, or compatible)
2. Install Podman if not present
3. Detect and handle any existing Docker installations
4. Set up Quadlet systemd integration
5. Configure and start BirdNET-Go

## Compatibility

BirdNET-Go container images are built following the OCI (Open Container Initiative) standard, making them compatible with both Docker and Podman runtimes. The same image works with both tools - the `podman-*` prefixed tags are provided for easier discovery by Podman users.

### System Requirements

- **Podman 5.4+** for full feature support (including Quadlet)
- **Debian 13+**, **Ubuntu 25.04+**, or compatible distributions
- Audio device access for real-time bird detection (optional for file processing)
