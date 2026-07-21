---
name: optimize-query
description: Diagnoses and optimizes a SQL or NoSQL query without blind indexing.
---

Optimize the query from evidence.

Inspect:

- Query shape.
- Filters.
- Sort.
- Pagination.
- Projection.
- Cardinality.
- Existing indexes.
- Execution plan when available.
- Locking and transaction scope.
- Returned data volume.
- Application-side loops or N+1 behavior.

Do not add an index without explaining:

- Exact key order.
- Query supported.
- Selectivity.
- Expected plan change.
- Write and storage cost.
- Redundancy.

Return the smallest safe optimization first.
