---
name: implement-feature
description: Implements an approved backend design with focused verification.
---

Implement the approved design.

Rules:

- Reinspect only files directly involved.
- Do not redesign unless a blocking contradiction is found.
- Do not refactor unrelated code.
- Make the smallest coherent change.
- Follow project conventions.
- Validate external data.
- Preserve security boundaries.
- Handle errors, cancellation and shutdown.
- Avoid unbounded buffers, retries or concurrency.
- Do not add dependencies without justification.

Run relevant build, type checking, linting and tests.

Final response must contain only:

- Summary.
- Files changed.
- Important decisions.
- Commands executed.
- Failed checks.
- Residual risks.
