# Security Policy

OnionForge sits in front of services that people often choose to run as onion services for privacy reasons, so security reports are taken seriously.

## Supported versions

| Version | Supported |
|---|---|
| latest release (`0.x`) | ✅ |
| older releases | ❌ — please upgrade |

## Reporting a vulnerability

**Do not open a public issue for security problems.**

Report privately through GitHub's [private vulnerability reporting](https://github.com/emon5122/onionforge/security/advisories/new). Please include:

- the OnionForge version (`docker run --rm emon5122/onionforge version`)
- your `onionforge.yml` (with real targets and onion addresses removed) and relevant logs
- steps to reproduce and the impact you observed

You can expect an acknowledgement within a few days. Fixes are released as a new patch version and credited in the advisory unless you prefer otherwise.

## Scope

In scope, for example:

- leaking onion private keys (`hs_ed25519_secret_key`) through logs, generated files, HTTP or file permissions
- using OnionForge as an open proxy, or steering requests to an upstream that isn't configured
- bypassing the target SSRF policy (private, loopback or link-local destinations)
- an onion identity being replaced or destroyed without explicit operator action
- privilege-escalation issues in the container

Out of scope:

- vulnerabilities in the upstream applications that OnionForge proxies to
- weaknesses in the Tor network itself (report those to the [Tor Project](https://support.torproject.org/misc/bug-or-feedback/))
- deanonymization caused by the published application (e.g. it leaking its clearnet IP)
