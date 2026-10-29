# Security policy

This project gives software access to a personal WhatsApp account, so security reports are welcome and taken seriously.

## Reporting a vulnerability

Report privately through GitHub: **Security → Report a vulnerability** on this repository ([private vulnerability reporting](https://github.com/Roman-Andr/whatsapp-mcp/security/advisories/new)). Please do not open a public issue for a vulnerability.

Include what an attacker can do, the steps or a proof of concept, and the affected component (bridge, MCP server) and commit.

## Scope

In scope, for example:

- Bypassing the send or read allowlist, read-only mode, the send rate limit or the media roots.
- Sending files from outside the media roots, or the store's own files (session database, API token).
- Reaching the bridge API or the HTTP MCP transport without the token.
- Forged or replayable webhook deliveries.
- Rewriting stored history through another user's messages.

Out of scope: WhatsApp's own rules for unofficial clients (account bans), anyone with direct access to the `store/` directory, and prompt injection in general (see the Safety section of the README); a concrete way for message content to defeat one of the limits above is in scope.

Only the latest `main` is supported.
