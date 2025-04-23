# BirdNET-Go Docker Compose Guide

This guide provides instructions for setting up and running BirdNET-Go using Docker Compose, which is an alternative to the `install.sh` script and systemd service method.

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
   Create a file named `docker-compose.yml` in this directory and copy the content from the [premade docker-compose.yml](https://github.com/tphakala/birdnet-go/blob/main/Docker/docker-compose.yml) file in the repository, or the example below.

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
   docker-compose up -d
   ```

## Configuration Options

### Audio Input Options

#### Using Sound Card (default)
The default configuration maps `/dev/snd` to use your local sound card for audio capture.

#### Using RTSP Stream
If you prefer to use an RTSP stream instead of a sound card:

1. You don't need to modify the docker-compose.yml file
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
   - Uncomment the cloudflared service in docker-compose.yml

4. **Start the services:**
   ```bash
   docker-compose up -d
   ```

> **IMPORTANT**: When exposing BirdNET-Go to the internet, always enable authentication to prevent unauthorized access. See the [Cloudflare Tunnel Guide](cloudflare_tunnel_guide.md#enabling-authentication) for details on security implications and configuration.

### Port Configuration

By default, the web interface is accessible on port 8080. You can change this by:
- Setting the `WEB_PORT` environment variable in your `.env` file
- Or directly editing the port mapping in the `docker-compose.yml` file

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
- **Port conflicts:** If port 8080 is already in use, change the port mapping in your docker-compose file
- **Permission errors:** Set the correct UID/GID for your user with the environment variables

## Securing BirdNET-Go for Internet Access

When exposing BirdNET-Go to the internet (using Cloudflare Tunnel or other methods), it's **strongly recommended** to enable authentication to prevent unauthorized access to your data and settings.

### Authentication Options

BirdNET-Go supports several authentication methods that can be configured in the `config.yaml` file:

1. **Basic Authentication**:
   ```yaml
   security:
     basicauth:
       enabled: true              # Enable basic authentication
       password: "your_password"  # Password hash (will be auto-hashed)
   ```

2. **Google OAuth2**:
   ```yaml
   security:
     host: "yourdomain.com"     # Your domain for the auth system
     googleauth:
       enabled: true            # Enable Google authentication
       clientid: "your_id"      # From Google Cloud Console
       clientsecret: "secret"   # From Google Cloud Console
       userid: "your_email"     # Your Google account email
   ```

3. **GitHub OAuth2**:
   ```yaml
   security:
     host: "yourdomain.com"     # Your domain for the auth system
     githubauth:
       enabled: true            # Enable GitHub authentication
       clientid: "your_id"      # From GitHub Developer settings
       clientsecret: "secret"   # From GitHub Developer settings
       userid: "username"       # Your GitHub username
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
    subnet: "192.168.1.0/24,10.0.0.0/8"  # Your local network CIDR ranges
```

### Using TLS

For additional security, consider enabling TLS:

```yaml
security:
  host: "yourdomain.com"  # Your domain
  autotls: true           # Enable automatic TLS certificate
  redirecttohttps: true   # Redirect HTTP to HTTPS
```

Note: When using Cloudflare Tunnel, the connection between Cloudflare and your server is already encrypted, but enabling TLS provides end-to-end encryption.

## Additional Configuration

For more advanced configuration options, refer to the BirdNET-Go documentation. After initial setup, you can modify the configuration file at `./config/config.yaml`.