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

The published container images use the shared entrypoint baked in from `Docker/entrypoint.sh`, which handles both rootful and rootless execution under Docker and Podman (user/group setup, device permissions, timezone, and pre-flight checks for disk space and config writability).

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

## Rootless Audio Access

Rootless Podman runs the container inside a user namespace. Host-owned `/dev/snd` nodes appear as `nobody:nogroup` inside that namespace, and the container's "root" is an unprivileged host user that cannot change their permissions. Older images tried anyway and printed `chmod: changing permissions of '/dev/snd/...': Operation not permitted` for every node. The entrypoint now detects the rootless user namespace, skips that futile fixup, and prints a short note instead of the error spam.

To actually capture audio in rootless Podman, keep your host user identity and its supplementary groups inside the container with `--userns=keep-id --group-add keep-groups`. Your host user must be a member of the host `audio` group. With `keep-id` the app runs directly as your host UID (not as a namespaced fake root), so the entrypoint takes its rootless path and does not drop privileges, which preserves the `audio` group membership that `keep-groups` provides:

```bash
podman run -d --name birdnet-go \
  --userns=keep-id --group-add keep-groups \
  --device /dev/snd \
  -p 8080:8080 \
  -v ./config:/config -v ./data:/data \
  ghcr.io/tphakala/birdnet-go:nightly
```

Note that `--group-add keep-groups` on its own is not enough with the default configuration: the entrypoint drops privileges with `gosu`, which clears the inherited `audio` group, so the sound card becomes inaccessible after the drop. Adding `--userns=keep-id` (as above) avoids the privilege drop and is the reliable recipe.

`--group-add keep-groups` requires the `crun` OCI runtime: it works by telling the runtime to skip the `setgroups` call so the container keeps the host's supplementary groups, and `runc` does not implement that annotation (under `runc` the container still starts, but the sound card stays inaccessible). Some distributions ship `podman` with `runc` and without `crun` (Debian 13 for example), so install `crun` if it is missing and Podman will then prefer it. Check your active runtime with `podman info --format '{{.Host.OCIRuntime.Name}}'`.

Set `SKIP_DEVICE_PERMS=true` to make the entrypoint skip all `/dev/snd` and `/dev/dri` permission handling, for example when your runtime configuration already grants device access or when you want to skip the device permission fixes entirely. The entrypoint still prints a one-line confirmation that the flag took effect, mirroring `SKIP_CHOWN`.

### Enabling audio in the shipped compose and Quadlet files

`podman-install.sh` enables this automatically on a fresh install: it installs `crun` if it is missing and uncomments `UserNS=keep-id` and `GroupAdd=keep-groups` in the generated Quadlet unit. It deliberately does NOT touch an existing install, because switching an already-running deployment to `keep-id` changes the app from a subuid to your host UID and would strand the `/config` and `/data` files it wrote earlier.

The static `Podman/quadlet/*.container` and `Podman/podman-compose*.yml` files ship with the same options commented out. Uncomment them to enable audio for a manual deployment, and make sure `crun` is installed (the compose files also need `x-podman: { in_pod: false }` at the top level, because podman-compose otherwise puts services in a pod, which conflicts with a per-container user namespace).

If you enable `keep-id` on an EXISTING install whose volumes were written under the old mapping, re-own them once so the host-UID process can read them. From the directory holding `config/` and `data/`:

```bash
podman unshare chown -R 0:0 ./config ./data
```

## Compatibility

BirdNET-Go container images are built following the OCI (Open Container Initiative) standard, making them compatible with both Docker and Podman runtimes. The same image works with both tools - the `podman-*` prefixed tags are provided for easier discovery by Podman users.

### System Requirements

- **Podman 5.4+** for full feature support (including Quadlet)
- **Debian 13+**, **Ubuntu 25.04+**, or compatible distributions
- Audio device access for real-time bird detection (optional for file processing)
