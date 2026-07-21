---
name: investigate-bug
description: Investigates a backend bug before changing code.
---

Investigate the reported bug without editing first.

Process:

1. Restate the observable failure.
2. Identify the smallest relevant execution path.
3. Inspect logs, errors and recent changes when available.
4. Form competing hypotheses.
5. Seek evidence that falsifies each hypothesis.
6. Identify root cause or remaining uncertainty.
7. Propose the smallest fix.
8. Define regression tests.
9. Evaluate production and data impact.

Avoid speculative refactors.
Do not hide uncertainty.
