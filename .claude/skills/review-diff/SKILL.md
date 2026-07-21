---
name: review-diff
description: Reviews the current Git diff before commit.
---

Review the current Git diff.

Prioritize:

- Defects.
- Regressions.
- Security.
- Data integrity.
- Concurrency.
- Failure behavior.
- Missing validation.
- Missing tests.
- Deployment risk.
- Unnecessary complexity.

Ignore cosmetic preferences unless they hide a real maintenance problem.

Return findings ordered by severity and an overall commit recommendation.
Do not edit files.
