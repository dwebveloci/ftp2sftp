---
name: model-database
description: Designs SQL or NoSQL models from access patterns and integrity requirements.
---

Model the requested data capability.

Begin with:

- Required invariants.
- Reads.
- Writes.
- Cardinality.
- Growth.
- Consistency.
- Transaction boundaries.
- Retention.

Then propose:

- Entities, tables, documents or aggregates.
- Constraints.
- Unique rules.
- Indexes.
- Concurrency strategy.
- Migration.
- Rollback.
- Backup implications.

For each index explain supported queries and write/storage cost.

Do not generate ORM code until the logical model is coherent.
