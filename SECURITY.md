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
