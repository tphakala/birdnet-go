# BirdNET-Go Docker Compose Guide

This guide provides instructions for setting up and running BirdNET-Go using Docker Compose, which is an alternative to the `install.sh` script and systemd service method.

## Container Registry Options

BirdNET-Go Docker images are available from two registries:

- **GitHub Container Registry (Primary)**: `ghcr.io/tphakala/birdnet-go`
- **Docker Hub (Mirror)**: `tphakala/birdnet-go`

Both registries contain identical images and can be used interchangeably. The examples below use GitHub Container Registry, but you can substitute `tphakala/birdnet-go` if you prefer Docker Hub.

## Choosing a Network Mode

Two Compose files are provided:

- **Host networking, recommended for new Linux installs** ([`docker-compose.host.yml`](https://github.com/tphakala/birdnet-go/blob/main/Docker/docker-compose.host.yml)). The container shares the host network stack, so multicast (mDNS/DNS-SD) reaches the app directly. There is no port mapping: the app listens on `WEB_PORT` itself.
- **Bridge networking** ([`docker-compose.yml`](https://github.com/tphakala/birdnet-go/blob/main/Docker/docker-compose.yml)). Remains supported. Existing setups are not changed and keep working as they are.

Notes:

- Host networking needs rootful Docker, or rootless Docker Engine 29.5 or later. Earlier rootless versions isolate host networking inside RootlessKit, so ports are not reachable from the host.
- AutoTLS stays on [`docker-compose.autotls.yml`](https://github.com/tphakala/birdnet-go/blob/main/Docker/docker-compose.autotls.yml). It needs ports 80 and 443, which the non-root app cannot bind in host mode.
- `install.sh` sets up bridge networking. That is unchanged.
- The same choice applies to Portainer (see [Using Portainer](#using-portainer)), to plain `docker run` (see [Manual Docker Installation](installation.md#manual-docker-installation-advanced-linux-only)) and to Unraid (see the [Unraid README](https://github.com/tphakala/birdnet-go/blob/main/Unraid/README.md)). There is no host networking variant of the Podman files yet.

## Prerequisites

- Docker and Docker Compose installed on your system
- Basic understanding of Docker and command line interfaces
- A compatible audio device if using sound card input

## Setup Instructions

1. **Create a new directory for BirdNET-Go:**

   ```bash
   mkdir -p ~/birdnet-go-app
   cd ~/birdnet-go-app
   ```

2. **Create the docker-compose.yml file:**
   Create a file named `docker-compose.yml` in this directory and copy the content from the [premade docker-compose.host.yml](https://github.com/tphakala/birdnet-go/blob/main/Docker/docker-compose.host.yml) file in the repository (host networking, recommended). The bridge alternative is the [premade docker-compose.yml](https://github.com/tphakala/birdnet-go/blob/main/Docker/docker-compose.yml).

3. **Create config and data directories:**

   ```bash
   mkdir -p config data/clips
   ```

4. **Set up environment variables (optional):**
   You can create a `.env` file in the same directory to set environment variables:

   ```bash
   # .env example
   WEB_PORT=8080
   TZ=Europe/London
   BIRDNET_UID=1000
   BIRDNET_GID=1000
   ```

5. **Start BirdNET-Go:**

   ```bash
   docker compose up -d
   ```

   With host networking, open `WEB_PORT` in the host firewall (see [Host Networking Notes](#host-networking-notes)).

### Using Portainer

Portainer can deploy the same Compose files as a stack:

- **App template:** set Portainer's App Templates URL (in its settings) to `https://raw.githubusercontent.com/tphakala/birdnet-go/main/Docker/portainer-template.json` and choose **BirdNET-Go (host network, recommended)**. The **BirdNET-Go** template uses bridge networking and remains supported; **BirdNET-Go (AutoTLS)** is for AutoTLS.
- **Stack from the repository:** use `https://github.com/tphakala/birdnet-go` as the repository and `Docker/docker-compose.host.yml` as the Compose path (or `Docker/docker-compose.yml` for bridge networking).

The [Host Networking Notes](#host-networking-notes) apply to Portainer stacks too, and an existing bridge stack can be switched as described in [Switching an Existing Install](#switching-an-existing-install-optional).

## Configuration Options

### Audio Input Options

#### Using Sound Card (default)

The default configuration maps `/dev/snd` to use your local sound card for audio capture.

#### Using RTSP Stream

If you prefer to use an RTSP stream instead of a sound card:

1. You don't need to modify the Compose file, except on a host without a sound card: remove the `/dev/snd` line there, or `docker compose up` fails
2. After the container is running, edit the config file at `./config/config.yaml`
3. Comment out the sound card source and uncomment the RTSP section
4. Add your RTSP URL(s)

### HLS Streaming Performance Optimization

The configuration includes a RAM disk (tmpfs) mount for the HLS streaming segments directory:

- The `/config/hls` directory is mounted as a 50MB RAM disk
- This improves streaming performance by storing temporary stream segments in memory
- The RAM disk is automatically configured with the same UID/GID as your BirdNET-Go user
- This temporary storage is cleared on container restart (which is expected for stream segments)

## Internet Access Using Cloudflare Tunnel

The Docker Compose configuration includes an option to use Cloudflare Tunnel (cloudflared) to securely expose your BirdNET-Go instance to the internet without opening ports on your router/firewall.

**For comprehensive instructions and security best practices, see the dedicated [Cloudflare Tunnel Guide](cloudflare_tunnel_guide.md).**

Key benefits of using Cloudflare Tunnel:

- Enhanced security with no open ports on your network
- End-to-end encryption for all traffic
- Performance optimization through Cloudflare's content caching
- Protection against DDoS and other attacks

### Quick Setup Overview

1. **Prerequisites:**
   - A Cloudflare account
   - A domain added to your Cloudflare account

2. **Create a tunnel in the Cloudflare dashboard:**
   - Go to the [Cloudflare Zero Trust dashboard](https://dash.teams.cloudflare.com/)
   - Navigate to Access > Tunnels
   - Click "Create a tunnel"
   - Copy the provided tunnel token

3. **Configure Docker Compose:**
   - In your `.env` file, add: `CLOUDFLARE_TUNNEL_TOKEN=your-tunnel-token`
   - Uncomment the cloudflared service in your Compose file
   - In the tunnel's public hostname settings, use the service URL `http://localhost:<WEB_PORT>` with the host networking file (its cloudflared service already uses `network_mode: host`), or `http://birdnet-go:8080` with the bridge file

4. **Start the services:**

   ```bash
   docker compose up -d
   ```

> **IMPORTANT**: When exposing BirdNET-Go to the internet, always enable authentication to prevent unauthorized access. See the [Cloudflare Tunnel Guide](cloudflare_tunnel_guide.md#enabling-authentication) for details on security implications and configuration.

### Port Configuration

By default, the web interface is accessible on port 8080.

With the host networking file, `WEB_PORT` sets the port the app itself listens on (through `BIRDNET_WEBSERVER_PORT`). Use 1024 or higher, because the app runs as a non-root user. It overrides `webserver.port` in `config.yaml`, and the value is written to `config.yaml` when you save settings in the web interface.

With the bridge file, you can change the port by:

- Setting the `WEB_PORT` environment variable in your `.env` file
- Or directly editing the port mapping in the `docker-compose.yml` file

### Host Networking Notes

- **Host firewall:** Docker no longer inserts firewall rules for a published port, so ufw or firewalld now apply. Open `WEB_PORT` (for example `sudo ufw allow 8080/tcp`).
- **Prometheus telemetry:** if you enable it, it listens on every host interface on port 8090 (in bridge mode it stayed inside the container, because the Compose file does not publish it). Set its listen address to `127.0.0.1:8090` or a LAN IP in Settings.
- **No localhost-only web interface:** the web interface always listens on all interfaces in host mode. To limit it to localhost, use the bridge file with a `127.0.0.1:` port mapping.
- **Reverse proxy:** a proxy on the same host reaches the app on `localhost:<WEB_PORT>` and should send `X-Forwarded-For` and `X-Forwarded-Proto`. Subnet bypass honors the client address it forwards only when the address the app sees the proxy connect from, `127.0.0.1` or `::1` here, is listed in `security.trustedproxies`.
- **Subnet bypass and IPv6:** with subnet bypass enabled, IPv6 clients are now seen by their real address. If you rely on bypass for IPv6 clients, add your IPv6 prefix to the subnet list.
- **Sound card:** on a host without one (RTSP streams only), remove the `/dev/snd` line from the Compose file, or `docker compose up` fails.
- **Cloudflare Tunnel:** the cloudflared service must also use `network_mode: host`, and the tunnel's service URL is `http://localhost:<WEB_PORT>`.

### Switching an Existing Install (Optional)

You do not need to switch. Bridge networking keeps working. If you want host networking for an existing Compose or Portainer stack:

1. If Prometheus telemetry is enabled, set its listen address to `127.0.0.1:8090` or a LAN IP first (see above).
2. In the same directory, replace `docker-compose.yml` with `docker-compose.host.yml` (or point Portainer at `Docker/docker-compose.host.yml`). Keep your `.env`, `config` and `data`.
3. Run `docker compose up -d`. Compose recreates the one service and keeps your data.
4. Open `WEB_PORT` in the host firewall.
5. If you copied a `/run/dbus` line into your own file, remove it. Host networking does not use it.

To go back, replace the file again. If `WEB_PORT` was not 8080, first set `webserver.port: 8080` in `config/config.yaml`: the custom port is saved there when you save settings, and in bridge mode the container maps host port `WEB_PORT` to container port 8080, so the web interface is unreachable until the setting is reset.

Users who installed with `install.sh` keep using it: its systemd unit removes and recreates a container named `birdnet-go` on every start, which would replace a Compose container.

### User Permissions

The container runs with the following permissions by default:

- UID: 1000
- GID: 1000

To match your user's permissions:

- Set `BIRDNET_UID` to your user ID (find with `id -u`)
- Set `BIRDNET_GID` to your group ID (find with `id -g`)

## Common Commands

- **Start BirdNET-Go:**

  ```bash
  docker-compose up -d
  ```

- **Stop BirdNET-Go:**

  ```bash
  docker-compose down
  ```

- **View logs:**

  ```bash
  docker-compose logs -f
  ```

- **Update to latest version:**
  ```bash
  docker-compose pull
  docker-compose up -d
  ```

## Accessing the Web Interface

Once running, you can access the BirdNET-Go web interface at:

- http://localhost:8080 (replace 8080 with your configured port)
- Or using your machine's IP address: http://YOUR_IP:8080
- If avahi-daemon/mDNS is configured: http://HOSTNAME.local:8080

## Troubleshooting

- **Audio device issues:** Make sure your user has permission to access `/dev/snd` (usually by being in the `audio` group)
- **Port conflicts:** If port 8080 is already in use, change `WEB_PORT` (host networking) or the port mapping in your docker-compose file (bridge)
- **Permission errors:** Set the correct UID/GID for your user with the environment variables

## Securing BirdNET-Go for Internet Access

When exposing BirdNET-Go to the internet (using Cloudflare Tunnel or other methods), it's **strongly recommended** to enable authentication to prevent unauthorized access to your data and settings.

### Authentication Options

BirdNET-Go supports several authentication methods that can be configured in the `config.yaml` file:

1. **Basic Authentication**:

   ```yaml
   security:
     basicauth:
       enabled: true # Enable basic authentication
       password: "your_password" # Password hash (will be auto-hashed)
   ```

2. **Google OAuth2**:

   ```yaml
   security:
     host: "yourdomain.com" # Your domain for the auth system
     googleauth:
       enabled: true # Enable Google authentication
       clientid: "your_id" # From Google Cloud Console
       clientsecret: "secret" # From Google Cloud Console
       userid: "your_email" # Your Google account email
   ```

3. **GitHub OAuth2**:
   ```yaml
   security:
     host: "yourdomain.com" # Your domain for the auth system
     githubauth:
       enabled: true # Enable GitHub authentication
       clientid: "your_id" # From GitHub Developer settings
       clientsecret: "secret" # From GitHub Developer settings
       userid: "username" # Your GitHub username
   ```

### Setting Up Authentication with Docker Compose

1. **Create or edit your configuration**:

   ```bash
   nano config/config.yaml
   ```

2. **Add your security configuration** as shown above.

3. **Restart your container**:
   ```bash
   docker-compose down
   docker-compose up -d
   ```

### Allowing Subnet Bypass (Optional)

If you want to disable authentication for your local network while keeping it enabled for external access:

```yaml
security:
  allowsubnetbypass:
    enabled: true
    subnet: "192.168.1.0/24,10.0.0.0/8" # Your local network CIDR ranges
```

In a container (bridge or host networking), subnet bypass applies only to the ranges listed in `subnet`. The automatic local-network check compares the client with the Docker bridge network (from `host.docker.internal`), not with your LAN, so list your LAN range (for example `192.168.1.0/24`) there. Keep the `extra_hosts: host.docker.internal:host-gateway` line in the Compose file: without it, under host networking, the automatic check falls back to your LAN gateway and every device on that /24 skips login.

### Using TLS

For additional security, consider enabling TLS:

```yaml
security:
  host: "yourdomain.com" # Your domain
  autotls: true # Enable automatic TLS certificate
  redirecttohttps: true # Redirect HTTP to HTTPS
```

Note: When using Cloudflare Tunnel, the connection between Cloudflare and your server is already encrypted, but enabling TLS provides end-to-end encryption.

## Additional Configuration

For more advanced configuration options, refer to the BirdNET-Go documentation. After initial setup, you can modify the configuration file at `./config/config.yaml`.
