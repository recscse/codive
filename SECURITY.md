# Security Policy

## Supported Versions

Only the latest [tagged release](https://github.com/recscse/codive/releases) is supported. Security fixes are not backported to older versions — please upgrade with `codive upgrade` before reporting an issue you haven't reproduced on the latest release.

## Reporting a Vulnerability

The `codive` team takes security seriously. If you discover a security vulnerability or path traversal issue, please report it privately:

1. **Email**: Send details to `recscse@gmail.com` (or submit a private security advisory via GitHub Advisories).
2. **Details to Include**:
   - Description of the vulnerability.
   - Steps to reproduce or proof-of-concept repository.
   - Potential impact.
3. We will acknowledge receipt within **24 hours** and aim to release a patched version within **72 hours**.

---

## Local Sandboxing Guarantees

`codive` is strictly designed for local developer security:
- Zero telemetry sent outside your local machine.
- Path traversal protection ensuring all queries remain strictly within the target repository boundary.
- Zero root or elevated permissions required.
