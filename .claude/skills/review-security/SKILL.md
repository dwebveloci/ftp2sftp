---
name: review-security
description: Runs a focused defensive security review.
---

Perform a defensive security review of the requested scope.

Use the security reviewer agent when available.

Focus on:

- Trust boundaries.
- Authentication.
- Authorization.
- Ownership.
- Secrets.
- Validation.
- Injection.
- Path traversal.
- SSRF.
- Replay.
- Brute force.
- Rate limits.
- TLS.
- SSH host keys.
- Sensitive logs.
- Dependency risk.
- Resource exhaustion.

Return findings with severity, evidence, impact, remediation and verification.
Do not edit files.
