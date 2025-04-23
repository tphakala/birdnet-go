# Security & Access Control

## Authentication Methods

BirdNET-Go provides three authentication methods that can be used independently or in combination. You can configure the security settings in the BirdNET-Go security settings or directly in the `config.yaml` file.

### Basic Password Authentication

Basic password authentication follows the OAuth2.0 specification. It uses merely a password to authenticate a user. If no client id or secret is provided, they will be created automatically.

```yaml
security:
  host: "https://your.domain.com"
  basicauth:
    enabled: true
    password: "your-password"
    redirecturi: "https://your.domain.com"
    clientid: "your-client-id"
    clientsecret: "your-client-secret"
```

### Social Authentication

BirdNET-Go supports OAuth authentication through Google and GitHub identity providers. To implement either provider, you'll need to generate the corresponding client ID and secret, then configure them through the Security settings or in the configuration file. Remember to set the Redirect URI parameter in your Google or GitHub developer console to match the value configured in `redirecturi`. The `userid` is a list of accepted authenticated user emails.

```yaml
security:
  googleauth:
    enabled: true
    clientid: "your-google-client-id"
    clientsecret: "your-google-client-secret"
    userid: "allowed@gmail.com,another@gmail.com"
    redirecturi: "https://your.domain.com/auth/google/callback"
```

Similarly, GitHub authentication can be enabled:

```yaml
security:
  githubauth:
    enabled: true
    clientid: "your-github-client-id"
    clientsecret: "your-github-client-secret"
    userid: "user@example.com"
    redirecturi: "https://your.domain.com/auth/github/callback"
```

## Authentication Bypass

If you are running BirdNET-Go on a trusted network, you can bypass authentication either by configuring a Cloudflare Tunnel with Cloudflare Access enabled, or by specifying a trusted subnet. Both options will allow access to the application without any authentication.

Both options can be configured through the web interface or in the `config.yaml` file:

```yaml
security:
  allowcftunnelbypass: true
  allowsubnetbypass:
    enabled: true
    subnet: "192.168.1.0/24,10.0.0.0/8"
```

### Cloudflare Access Authentication Bypass

Cloudflare Access provides an authentication layer that uses your existing identity providers, such as Google or GitHub accounts, to control access to your applications. When using Cloudflare Access for authentication, you can configure BirdNET-Go to trust traffic coming through the Cloudflare tunnel. The system authenticates requests by validating the `Cf-Access-Jwt-Assertion` header containing a JWT token from Cloudflare.

To add even more security, you can also require that the Cloudflare Team Domain Name and Policy audience are valid in the JWT token. Enable these by defining them in the `config.yaml` file:

```yaml
security:
  allowcloudflarebypass:
          enabled: true
          teamdomain: "your-subdomain-of-cloudflareaccess.com"
          audience: "your-policy-auddience"
```	

See the following links for more information on Cloudflare Access:
- [Cloudflare tunnels](https://developers.cloudflare.com/cloudflare-one/connections/connect-apps/)
- [Create a remotely-managed tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/get-started/create-remote-tunnel/)
- [Self-hosted applications](https://developers.cloudflare.com/cloudflare-one/applications/configure-apps/self-hosted-apps/)

### Subnet-based Authentication Bypass

When enabled, BirdNET-Go will allow access to the application without any authentication if the client's IP address is within the specified subnet. Home routers typically use `192.168.1.0/24`, `192.168.0.0/24` or `172.16.0.0/24`.

## Authentication Recovery

If you end up locking yourself out, authentication can be turned off with the following command:

```bash
# For host system installations
./reset_auth.sh [path/to/config.yaml]

# For Docker deployments
docker exec $(docker ps | grep birdnet-go | awk '{print $1}') reset_auth.sh

# For a devcontainer
docker exec $(docker ps | grep birdnet-go | awk '{print $1}') ./reset_auth.sh
```

The script automatically creates a timestamped backup of your current configuration before disabling the authentication.
