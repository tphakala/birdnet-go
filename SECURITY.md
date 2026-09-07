# Security Policy

## Reporting a vulnerability

Please report security issues privately through GitHub's private vulnerability reporting. Open the [Security tab](https://github.com/tphakala/birdnet-go/security/advisories) of this repository and choose "Report a vulnerability". Do not open a public issue or discussion for anything you believe is a security problem.

I read new reports within a few days and aim to confirm or decline within two weeks. Fixes ship in the next release, and I publish the advisory, with credit to the reporter, once a release containing the fix is out. If a fix needs longer, I will say so in the advisory thread.

BirdNET-Go is a hobby project with no bug bounty, but responsible reports are genuinely appreciated and every reporter is credited in the published advisory.

## Scope

In scope: the BirdNET-Go server and its web interface, including authentication, Private Mode, the HTTP API, and any outbound request the server makes on a user's behalf (webhooks, notification tests, weather providers).

Out of scope: vulnerabilities in third-party dependencies that are not exploitable through BirdNET-Go, issues that require physical access to the host, and instances deliberately exposed to an untrusted network with authentication disabled.

## Supported versions

Only the latest release receives security fixes. Stable releases are date-tagged (YYYYMMDD); upgrade to the newest one. The `nightly` and `latest` container images track the default branch and receive fixes as they merge.
