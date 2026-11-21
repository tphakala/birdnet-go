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

#### Allowed users: prefer user IDs

The allowed users list accepts user IDs and email addresses. Use the user ID the provider issues where you can (for an OIDC provider, the `sub` claim). An email address is not a stable identifier: a provider may let users change their address or reuse it for another account, and OpenID Connect Core 1.0 (section 5.7) advises against using it to identify a user. BirdNET-Go matches the address the provider sends and does not check whether the provider verified it. With Microsoft sign-in this matters most, because the address comes from an account attribute that the administrator of any Microsoft Entra tenant can set, so allow Microsoft users by user ID.

### OIDC identity provider with a private certificate authority

BirdNET-Go checks the certificate of an OIDC identity provider against the system certificate store, like other outgoing TLS connections that have no CA setting of their own. When the provider's certificate is signed by a private certificate authority, the console and `logs/security.log` show an `OIDC discovery failed` line with a certificate error. Each request to the identity provider times out after ten seconds.

- **Docker:** mount the CA certificate (PEM) into the container, for example under `/config`, and set `SSL_CERT_FILE` to its path inside the container (`SSL_CERT_FILE=/config/my-ca.pem`). Public certificate authorities stay trusted, because the system certificate directories are still read. `SSL_CERT_DIR` replaces the directory list instead, so include `/etc/ssl/certs` when you use it. The variables are read once at startup, so restart the container after changing them. A path that does not exist is ignored without an error.
- **Linux without Docker:** add the CA to the system store (`sudo update-ca-certificates` on Debian, Ubuntu and Raspberry Pi OS), or set `SSL_CERT_FILE` in the service environment.
- **Windows and macOS:** add the CA to the system certificate store. Do not use the variables there, since they replace the system store.

These settings apply to every outgoing TLS connection of BirdNET-Go that uses the system certificate store, not only to OIDC. A connection with its own CA setting, such as MQTT with a CA certificate configured, uses that certificate instead.

### OIDC sign-out

Signing out of BirdNET-Go also signs the user out at the identity provider when the provider publishes an end-session endpoint in its discovery document. The end-session URL must use HTTPS, except when the configured issuer URL itself uses `http://`, in which case an HTTP end-session URL is accepted.

By default the user stays on the identity provider's page after that sign-out. To send the browser back to BirdNET-Go, enable the post-logout return option of the OIDC provider (`postLogoutRedirect: true` in its `oauthProviders` entry). The return URL is `<base URL>/ui/`, built from the Base URL or Host setting, and it is sent only when the session holds an ID token. Register that exact URL as a post-logout redirect URI at the identity provider first. A provider that does not know the URL may refuse the redirect or show an error page, which is why the option is off by default.

```yaml
security:
  oauthProviders:
    - provider: oidc
      enabled: true
      issuerUrl: "https://auth.example.com/application/o/birdnet-go/"
      postLogoutRedirect: true
```

## Staying signed in

The sign-in dialog has a "Remember this browser" box, ticked by default. Ticked, the browser stays signed in for 30 days after its last visit; the days are counted from the last renewal, which happens at most once a day, so a browser that is not used can be signed out up to a day sooner. Unticked, the browser is signed out after 8 hours without a request, counted from the last renewal (at most every 15 minutes), and usually when it is closed; a browser that restores its previous session on start keeps the cookie, so the 8 hour limit is the guarantee. The choice applies to password and social sign-ins alike. Leave it unticked on a shared computer.

Signing out ends the session at once. Changing the password (or the basic authentication client ID) signs every browser out, including the one that made the change. Removing a user from a provider's allowed list blocks that user's browser on its next request; the session stays until it expires, so adding the user back lets that browser in again. After an upgrade to a version with this feature, each browser signs in once. The `sessionduration` and `accesstokenexp` settings in `config.yaml` no longer control how long a browser stays signed in.

## Authentication Bypass

On a trusted network you can let clients from a given subnet skip authentication entirely.

Set it in the web interface, or in `config.yaml`:

```yaml
security:
  allowsubnetbypass:
    enabled: true
    subnet: "192.168.1.0/24,10.0.0.0/8"
```

> **Note**: BirdNET-Go used to check Cloudflare Access tokens itself, through the `allowcftunnelbypass` and `allowcloudflarebypass` settings. That support is gone, and those keys are now ignored. When BirdNET-Go sits behind a Cloudflare Tunnel, protect it with Cloudflare Access on the Cloudflare side, with BirdNET-Go's own login, or with both. The [Cloudflare Tunnel guide](cloudflare_tunnel_guide.md) describes the tunnel setup.

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
