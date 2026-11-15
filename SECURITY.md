# Security Policy

## Reporting a vulnerability

Please report security issues privately to the repository maintainers through
GitHub's private vulnerability reporting (Security → Report a vulnerability).
Do not open a public issue for an undisclosed vulnerability.

Include the affected version, a minimal reproduction, and the impact you
observed. Do not include real credentials, DSNs, PII or knowledge bodies in the
report. Maintainers will acknowledge within a few business days and coordinate
a fix and disclosure timeline with you.

## Supported versions

Security fixes are applied to the current release line. Older tags are not
patched; upgrade to the latest release.

## Baseline configuration

agentmem is fail-closed by design. The following settings are required for any
deployment reachable beyond the local machine:

- `AGENTMEM_MODE=production` — validation rejects a production start without
  authentication, without at least one token, and with a DSN that uses
  `sslmode=disable`.
- `AGENTMEM_AUTH_ENABLED=true` and `AGENTMEM_AUTH_TOKENS` — the file stores only
  SHA-256 digests of bearer tokens, never the raw values. Tokens map to a
  subject and a scope set; the subject is injected by the server and cannot be
  asserted by a request. Generate a digest with
  `printf %s 'your-token' | sha256sum`.
- `AGENTMEM_EGRESS_ALLOWED_HOSTS` — required whenever a non-empty HTTP endpoint
  (System One or a non-CLI adapter) is configured. Private, loopback and
  link-local literals must be named explicitly. Redirects are re-checked per
  hop and TLS downgrade is refused.
- `AGENTMEM_ALLOWED_ORIGINS` — list every cross-origin console that may write.
- `AGENTMEM_RATE_RPS` / `AGENTMEM_RATE_BURST` — per-client in-process limit.
  Multi-instance deployments need a gateway-level limit as well.
- Put a TLS-terminating reverse proxy in front of the service. Compose binds
  `8080` and `5432` to loopback only; the container listens on `0.0.0.0`
  because that is required by container networking.
- Keep `.env` untracked and out of images/logs. The container runs as an
  unprivileged user and only `/app/data` is writable.

See `docs/security.md` for the permission matrix, token rotation and the
threat model.

## CLI trust boundary

The bearer-token system described above protects the HTTP surface only. The CLI
is a **local trusted operator surface**, not a second authenticated transport:

- There is no bearer channel on the command line. A subject is self-asserted:
  `agentmem put --subject alice` runs as `alice` without any token, digest or
  scope check.
- Authorisation semantics are still enforced underneath. The storage layer
  rejects anonymous writes (`router: anonymous write rejected`), matches the
  owner on updates and filters reads by ACL, so the CLI cannot bypass *what* may
  be written or read — only *who* it claims to be.
- Therefore any local process that can read `.env` (which holds token digests,
  the DSN and the bundle root) and write the OKF bundle already has equivalent
  or greater authority. Treat that filesystem access as the real boundary.
- Mitigations: run the CLI only on the host or through a controlled jump box;
  never expose it to untrusted callers or wrap it in a shell service; use the
  HTTP API with tokens for any multi-tenant or remote access; audit subjects in
  `internal/meta` rather than trusting CLI output; keep `.env` untracked and
  `0600`.

Do not read this as "the CLI is protected by the token system": a local
operator can assert any subject, and that is a deliberate design choice for a
single-tenant operator console.

## Trust stores

Go 1.27.3 carries the `crypto/x509` blocklist for the eight weak eMudhra
(emSign) intermediate CAs. Rebuild the container image to refresh the Alpine
CA bundle after the 2026-10 incident; site certificates are unaffected.
Verify offline with:

```bash
make check-trust-store
docker run --rm --entrypoint cat <image> /etc/ssl/certs/ca-certificates.crt > /tmp/image-ca.crt
scripts/check-emudhra.sh /tmp/image-ca.crt
```

Online probes are deliberately avoided: forward and intercepting proxies can
return false positives.

## Deliberate future work

The following are out of scope for the current single-binary design and are
tracked as follow-ups rather than silently approximated:

- distributed rate limiting, OIDC/SSO and mTLS;
- hot token rotation and central revocation (current tokens are static and
  require a restart);
- pinning base images and GitHub Actions to full commit SHAs (SHAs must be
  verified before pinning; floating tags are currently combined with
  least-privilege `permissions`, `persist-credentials: false` and dependabot).

## Residual risk: release credentials

Release assets are uploaded with the workflow's own `GITHUB_TOKEN`. Image
publishing to GHCR uses the `ARIACOMPUTE_TOKEN` repository secret — a personal
access token granted no scope beyond `write:packages`. It is long-lived by
nature, so:

- keep it scoped to `write:packages` only and bound to a dedicated account;
- rotate it on a fixed schedule and immediately after any suspected exposure;
- rotate by writing a new value into the repository secret — no workflow change
  is required;
- never print it: the workflow passes it only to `docker/login-action`, and no
  step echoes the login configuration.

Replacing it with a short-lived OIDC federation (`id-token: write` plus
`docker/login-action`'s cloud providers) is the preferred end state and remains
future work.
