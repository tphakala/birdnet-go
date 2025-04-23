# Exposing BirdNET-Go to the Internet with Cloudflare Tunnel

This guide explains how to securely expose your BirdNET-Go installation to the internet using Cloudflare Tunnel (cloudflared), which is the recommended method for remote access.

## Why Use Cloudflare Tunnel?

Cloudflare Tunnel provides several significant advantages over traditional port forwarding:

- **Enhanced Security**: No need to open ports on your router/firewall, eliminating attack vectors
- **End-to-End Encryption**: Traffic is encrypted from your server to Cloudflare, and from Cloudflare to users' browsers
- **DDoS Protection**: Cloudflare's infrastructure shields your server from direct attacks
- **Performance Optimization**: Static content like spectrograms and audio clips are cached at Cloudflare's edge network
- **Simple Setup**: No need for complex networking configuration or dynamic DNS solutions
- **Free Tier Available**: Basic functionality is available on Cloudflare's free plan

## Prerequisites

- A Cloudflare account (free tier is sufficient)
- A domain name added to your Cloudflare account
- BirdNET-Go running on your system (any installation method is compatible)

The cloudflared client can be found in the [official cloudflared GitHub repository](https://github.com/cloudflare/cloudflared).

## Setup Instructions

### Using Docker Compose (Recommended for Docker installations)

If you're using Docker Compose with BirdNET-Go, setting up Cloudflare Tunnel is straightforward:

1. **Create a Cloudflare Tunnel**:
   - Log in to the [Cloudflare Zero Trust dashboard](https://dash.teams.cloudflare.com/)
   - Navigate to Access > Tunnels
   - Click "Create a tunnel"
   - Give your tunnel a name (e.g., "BirdNET-Go")
   - Copy the provided tunnel token

2. **Configure Environment Variables**:
   - In your `.env` file (in the same directory as your docker-compose.yml), add:
     ```
     CLOUDFLARE_TUNNEL_TOKEN=your-tunnel-token
     ```

3. **Update Docker Compose Configuration**:
   - Edit your docker-compose.yml to include the cloudflared service:
     ```yaml
     cloudflared:
       image: cloudflare/cloudflared:latest
       container_name: birdnet-cloudflared
       restart: unless-stopped
       command: tunnel run
       environment:
         - TUNNEL_TOKEN=${CLOUDFLARE_TUNNEL_TOKEN}
       depends_on:
         - birdnet-go
     ```
   - Alternatively, use the [premade docker-compose.yml](../Docker/docker-compose.yml) which already includes this configuration (commented out)

4. **Start the Services**:
   ```bash
   docker-compose up -d
   ```

5. **Configure Public Hostname in Cloudflare Dashboard**:
   - Go back to your tunnel in the Cloudflare Zero Trust dashboard
   - Add a public hostname (e.g., `birdnet.yourdomain.com`)
   - Set the service to `http://birdnet-go:8080`
   - Save the configuration

Your BirdNET-Go instance will now be accessible at `https://birdnet.yourdomain.com` from anywhere.

### Using Standard Docker Install

If you're using the standard Docker installation method:

1. **Create a Cloudflare Tunnel** (same as above)

2. **Download and Run Cloudflared**:
   ```bash
   # Create a directory for configuration
   mkdir -p ~/cloudflared

   # Run cloudflared in a container
   docker run -d --name cloudflared \
     --restart unless-stopped \
     -e TUNNEL_TOKEN=your-tunnel-token \
     cloudflare/cloudflared:latest \
     tunnel run
   ```

3. **Configure Public Hostname** (same as above)

### Using Binary Installation (Non-Docker)

If you're running BirdNET-Go directly on your system (not using Docker):

1. **Install cloudflared**:
   - Download the appropriate binary for your system from the [cloudflared GitHub releases page](https://github.com/cloudflare/cloudflared/releases)
   - Or use your system's package manager:
     ```bash
     # Debian/Ubuntu
     curl -L https://pkg.cloudflare.com/cloudflare-main.gpg | sudo apt-key add -
     echo "deb [signed-by=/usr/share/keyrings/cloudflare-main.gpg] https://pkg.cloudflare.com/cloudflared $(lsb_release -cs) main" | sudo tee /etc/apt/sources.list.d/cloudflared.list
     sudo apt update
     sudo apt install cloudflared
     
     # macOS
     brew install cloudflare/cloudflare/cloudflared
     
     # Windows (using Scoop)
     scoop install cloudflared
     ```

2. **Create a Cloudflare Tunnel**:
   - Log in to the Cloudflare Zero Trust dashboard
   - Create a tunnel and copy the token

3. **Run cloudflared**:
   ```bash
   cloudflared tunnel run --token your-tunnel-token
   ```

4. **Configure Public Hostname** in the Cloudflare dashboard (same as above)

## Quick Tunnels with TryCloudflare

If you want to quickly test exposing your BirdNET-Go instance without configuring a domain or creating a permanent tunnel, you can use Cloudflare's "quick tunnels" feature:

```bash
# For Docker installations
docker run --name cloudflared \
  --network=host \
  cloudflare/cloudflared:latest \
  tunnel --no-autoupdate run --url http://localhost:8080

# For binary installations
cloudflared tunnel --no-autoupdate run --url http://localhost:8080
```

This will create a temporary public URL that you can use to access your BirdNET-Go instance. The output will show the random hostname assigned to your tunnel, such as `https://randomly-generated-hostname.trycloudflare.com`.

**Important limitations of quick tunnels**:
- The hostname is randomly generated each time you start the tunnel
- The tunnel is temporary and will be deleted when the cloudflared process stops
- Quick tunnels do not support custom domains or advanced configuration
- Not recommended for permanent installations, but useful for testing

For more information on quick tunnels, see the [TryCloudflare documentation](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/do-more-with-tunnels/trycloudflare/).

## Enabling Authentication

When exposing BirdNET-Go to the internet, it's **strongly recommended** to enable authentication. 

### Security Implications of Not Enabling Authentication

Without authentication, your BirdNET-Go instance is completely open to anyone who has the URL. This creates significant security and privacy risks:

- **Full Administrative Access**: Anyone can access all administrative functions, including:
  - Changing configuration settings (audio sources, detection parameters, etc.)
  - Deleting detection records from the database
  - Viewing your configured location coordinates (privacy concern)
  - Accessing all audio recordings of detected birds
  
- **Potential for Abuse**:
  - Malicious users could deliberately misconfigure your system
  - Someone could wipe your detection history and audio clips
  - Your data could be scraped or downloaded without your knowledge
  - An attacker could use your system as an entry point to other services
  
- **Privacy Concerns**:
  - Your exact location is visible in the settings
  - Personal information might be inferred from detection patterns and setup
  - Audio clips could potentially contain background human voices

Even with Cloudflare Tunnel's security benefits, exposing an unauthenticated BirdNET-Go instance to the internet is equivalent to leaving your front door wide open. The tunnel provides secure access, but without authentication, anyone who finds your URL has complete control over your BirdNET-Go installation.

### Setting Up Authentication

There are several authentication methods available in BirdNET-Go:

1. **Edit your config.yaml file** to enable one of the authentication methods:

   ```yaml
   security:
     # Required for OAuth methods
     host: "birdnet.yourdomain.com"  # Your Cloudflare domain
     
     # Option 1: Basic Password Authentication
     basicauth:
       enabled: true
       password: "your_password"  # Will be automatically hashed
     
     # Option 2: Google OAuth (requires Google Cloud account)
     googleauth:
       enabled: true
       clientid: "your_client_id"  # From Google Cloud Console
       clientsecret: "your_client_secret"
       userid: "your_email@gmail.com"
     
     # Option 3: GitHub OAuth
     githubauth:
       enabled: true
       clientid: "your_client_id"  # From GitHub developer settings
       clientsecret: "your_client_secret"
       userid: "your_github_username"
     
     # Optional: Allow local network access without auth
     allowsubnetbypass:
       enabled: true
       subnet: "192.168.1.0/24,10.0.0.0/8"  # Your local network ranges
   ```

2. **Restart BirdNET-Go** for the changes to take effect

### Authentication Method Comparison

- **Basic Password Authentication**: Simplest to set up, but offers the least security. Good for personal use.
- **OAuth (Google/GitHub)**: More secure, requires fewer passwords to remember, and offers better protection against brute force attacks. Recommended for most users.
- **Subnet Bypass**: Optional feature that allows unauthenticated access from your local network while requiring authentication from the internet. Useful for mixed home/remote usage.

If you're sharing your BirdNET-Go instance with family or friends, consider using OAuth which makes it easier to control who has access without sharing passwords.

## Advanced Configuration

### Using a Configuration File (Alternative to Token)

For more advanced setups, you can use a config file instead of a token:

1. **Create configuration files**:
   ```bash
   mkdir -p ~/cloudflared
   ```

2. **Add config.yml**:
   ```yaml
   # ~/cloudflared/config.yml
   tunnel: your-tunnel-id
   credentials-file: /etc/cloudflared/credentials.json
   ingress:
     - hostname: birdnet.yourdomain.com
       service: http://birdnet-go:8080
     - service: http_status:404
   ```

3. **Add credentials file** obtained from Cloudflare dashboard to `~/cloudflared/credentials.json`

4. **Run with Docker Compose**:
   ```yaml
   cloudflared:
     image: cloudflare/cloudflared:latest
     container_name: birdnet-cloudflared
     restart: unless-stopped
     command: tunnel --config /etc/cloudflared/config.yml run
     volumes:
       - ./cloudflared:/etc/cloudflared
     depends_on:
       - birdnet-go
   ```

### Securing Multiple Services

If you run multiple services on your network, you can expose them all through a single Cloudflare Tunnel:

1. **Expand your ingress rules**:
   ```yaml
   ingress:
     - hostname: birdnet.yourdomain.com
       service: http://birdnet-go:8080
     - hostname: otherservice.yourdomain.com
       service: http://otherservice:80
     - service: http_status:404
   ```

## Troubleshooting

- **Connection refused errors**: Verify that the BirdNET-Go container is accessible from the cloudflared container. They should be on the same Docker network.
- **Tunnel not connecting**: Check logs with `docker logs birdnet-cloudflared` to ensure the token is valid.
- **Cannot access web interface**: Verify that your DNS settings in Cloudflare are properly configured for your domain.
- **Authentication issues**: If using OAuth, ensure the `security.host` matches your public domain exactly.

For more help, see the [Cloudflare Tunnel documentation](https://developers.cloudflare.com/cloudflare-one/connections/connect-apps/). 