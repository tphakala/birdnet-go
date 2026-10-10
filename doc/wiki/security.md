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

If you are running BirdNET-Go on a trusted network, you can bypass authentication for clients connecting from a trusted subnet.

This can be configured through the web interface or in the `config.yaml` file:

```yaml
security:
  allowsubnetbypass:
    enabled: true
    subnet: "192.168.1.0/24,10.0.0.0/8"
```

> **Note**: Earlier versions of BirdNET-Go could validate Cloudflare Access JWT tokens (`allowcftunnelbypass` / `allowcloudflarebypass`). That integration was removed and these settings no longer have any effect. If you expose BirdNET-Go through a Cloudflare Tunnel, use Cloudflare Access as a separate authentication layer in front of the tunnel and/or BirdNET-Go's built-in authentication — see the [Cloudflare Tunnel guide](cloudflare_tunnel_guide.md).

### Subnet-based Authentication Bypass

When enabled, BirdNET-Go will allow access to the application without any authentication if the client's IP address is within the specified subnet. Home routers typically use `192.168.1.0/24`, `192.168.0.0/24` or `172.16.0.0/24`.

The bypass reaches further than the web UI. If you have an authentication provider configured and also turn profiling on (`diagnostics.profiling.enabled`), every host on that subnet reaches `/debug/pprof/` with no credential, and no profiling token stands in the way, because a token is only generated when no authentication provider is configured. Unlike the settings pages, which redact their secrets on read, `/debug/pprof/cmdline` returns the process command line verbatim and `/debug/pprof/goroutine?debug=2` returns live goroutine stacks. (With no auth provider configured, the token is the gate, and a subnet host still needs it.) Keep the subnet narrow, and switch profiling back off when you are done with it; see the [profiling guide](../PROFILING.md#security-note) for the full picture.

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
