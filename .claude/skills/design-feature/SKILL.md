---
name: design-feature
description: Designs a backend feature before implementation.
---

Design the requested backend feature without editing files.

Process:

1. Inspect only affected modules and relevant project documentation.
2. Describe the current flow.
3. Identify domain behavior.
4. Define application orchestration.
5. Define transport and infrastructure responsibilities.
6. Define persistence and transaction boundaries.
7. Evaluate authentication, authorization and ownership.
8. Evaluate failures, timeout, retry and idempotency behavior.
9. Evaluate observability and deployment consequences.
10. Compare realistic alternatives.
11. Propose the smallest coherent design.
12. Produce an implementation checklist.

Return:

- Context.
- Assumptions.
- Proposed design.
- Data flow.
- Failure behavior.
- Security.
- Database implications.
- Test strategy.
- Deployment.
- Files likely to change.

Maximum 1,500 words.
