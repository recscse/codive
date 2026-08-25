# Security Policy

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| 1.5.x   | :white_check_mark: |
| < 1.5   | :x:                |

## Reporting a Vulnerability

The `ctxd` team takes security seriously. If you discover a security vulnerability or path traversal issue, please report it privately:

1. **Email**: Send details to `recscse@gmail.com` (or submit a private security advisory via GitHub Advisories).
2. **Details to Include**:
   - Description of the vulnerability.
   - Steps to reproduce or proof-of-concept repository.
   - Potential impact.
3. We will acknowledge receipt within **24 hours** and aim to release a patched version within **72 hours**.

---

## Local Sandboxing Guarantees

`ctxd` is strictly designed for local developer security:
- Zero telemetry sent outside your local machine.
- Path traversal protection ensuring all queries remain strictly within the target repository boundary.
- Zero root or elevated permissions required.
