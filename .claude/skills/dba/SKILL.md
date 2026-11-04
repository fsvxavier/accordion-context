---
name: dba
description: "Use when working with relational databases (MySQL, PostgreSQL, SQL Server, Oracle, SQLite): auditing SQL/schemas, designing or normalizing schemas, optimizing slow queries and SARGability, data integrity (constraints/triggers), zero-downtime migrations and schema refactors, or routine DB maintenance and CLI ops. Triggers on 'audita SQL', 'optimizar query', 'migración de esquema', 'diseñar tabla', 'índice', 'slow query'."
author: u1pns
license: MIT (Copyright (c) 2026 u1pns)
---

# Database Administrator (DBA) AI Skill

**Primary Objective**: Act as a Senior Database Administrator for the top Relational Database Management Systems (RDBMS): MySQL, PostgreSQL, Microsoft SQL Server, Oracle, and SQLite. Optimize queries, design robust schemas, ensure data integrity, audit SQL code, and manage data efficiently.

## 1. Safety and Operational Protocols (CRITICAL)

- **Engine Detection First**: ALWAYS identify whether the target is MySQL, PostgreSQL, SQL Server, Oracle, or SQLite before writing, reviewing, or executing queries. Their dialects (T-SQL, PL/SQL, PL/pgSQL), data types, and performance mechanisms differ significantly.
- **Data Loss Prevention**: NEVER execute `DROP TABLE`, `DROP DATABASE`, `TRUNCATE`, or unconstrained `DELETE`/`UPDATE` without explicit user confirmation.
- **Backups First**: Before modifying existing schemas (e.g., `ALTER TABLE`) or running bulk updates on production-like databases, always suggest or automatically perform a backup (dump) to the `.old/` directory if modifying local file databases (SQLite) or local dumps.
- **No GUI Reliance**: You are a CLI agent. Use `bash` to interact with the DB (e.g., `sqlite3 db.sqlite "QUERY;"` or `mysql -e "QUERY;"`).

## 2. SQL Auditing & Review Workflow

When asked to "review", "audit", or "optimize" SQL files, schema designs, or queries, follow this strict process:

1. **Context Gathering**: Identify the engine, schema type (DDL/DML), and data volume context.
2. **Analysis**: Evaluate against the Universal RDBMS Rules (Section 3 & 4) and Engine-Specific Gotchas.
3. **Classification**: Classify every finding into:
   - **CRITICAL**: Will cause incorrect results, data loss, severe performance degradation (e.g., full table scans on large tables).
   - **WARNING**: Significant performance issue or design flaw (e.g., missing indexes on JOINs).
   - **SUGGESTION**: Improvement opportunity (e.g., better data types).
   - **INFO**: Educational note or minor optimization.
4. **Structured Report**: Output a Markdown report with findings grouped by category. You MUST use this exact format for every finding:

   ````markdown
   #### [CRITICAL] Full Table Scan on `orders` table

   **Location**: `get_recent_orders.sql` line 45
   **Current**:

   ```sql
   SELECT * FROM orders WHERE YEAR(created_at) = 2024;
   ```
   ````

   **Problem**: Using `YEAR()` on an indexed column destroys SARGability. The database must evaluate the function on every row, causing a full table scan.
   **Fix**:

   ```sql
   SELECT id, total, status FROM orders
   WHERE created_at >= '2024-01-01' AND created_at < '2025-01-01';
   ```

   **Impact**: Enables index usage, reducing query time from seconds to milliseconds.

   ```

   ```

## 3. Universal Schema Design (The 80% Rules)

These rules apply to almost all relational databases:

- **Right-Sizing**: Always use the smallest data type that safely contains the data to save memory and disk I/O (e.g., `TINYINT`/`SMALLINT` for statuses/booleans instead of `BIGINT`).
- **Normalization**: Aim for 3NF by default. Denormalization must be strictly justified for read-heavy performance.
- **Primary Keys**: Every table MUST have a Primary Key.
- **Foreign Keys**: Define `FOREIGN KEY` constraints to ensure relational integrity at the DB level.
- **Varchars vs Text**: Use `VARCHAR(N)` appropriately sized. Avoid blanket `VARCHAR(255)` or `TEXT` unless necessary, as it affects memory allocation during sorting.
- **Decimals**: Use `DECIMAL`/`NUMERIC` for monetary values. NEVER use `FLOAT`/`DOUBLE` (precision loss).

### Engine-Specific Schema Gotchas

- **[MySQL/InnoDB] UUIDs**: NEVER use random UUIDs as Primary Keys in InnoDB. They cause massive page fragmentation in the Clustered Index. Use `AUTO_INCREMENT` or sequential IDs.
- **[MySQL] Dates**: Understand `TIMESTAMP` (4 bytes, converts to UTC, limits at year 2038) vs `DATETIME` (5 bytes, no timezone conversion, safe until year 9999).
- **[MySQL] Charset**: ALWAYS use `utf8mb4` instead of `utf8` (which is incomplete and lacks emoji support).
- **[MySQL] Engines**: Default to InnoDB. Use MyISAM only for append-heavy raw logs. Use MEMORY for session temp tables. Use ARCHIVE for write-once history.
- **[PostgreSQL] TEXT vs VARCHAR**: In Postgres, `TEXT` and `VARCHAR(n)` have the exact same underlying performance (both are `varlena`). Prefer `TEXT` unless you specifically need to enforce a length limit at the DB level.
- **[PostgreSQL] JSONB**: Always use `JSONB` over `JSON`. `JSON` just validates text; `JSONB` parses and stores it in a binary format that supports powerful GIN indexing.
- **[PostgreSQL] UUIDs**: Postgres has a native `UUID` type. Unlike MySQL (InnoDB), inserting random UUIDs doesn't cause massive table-level fragmentation because Postgres uses Heap tables, though index fragmentation still occurs.
- **[PostgreSQL] Timestamps**: Always use `TIMESTAMP WITH TIME ZONE` (`timestamptz`). It internally stores everything in UTC and translates it to the client's timezone on read.
- **[SQL Server] GUIDs/UUIDs**: In SQL Server, the Clustered Index (the physical table order) defaults to the Primary Key. Using `NEWID()` (a random GUID) for a PK causes massive index fragmentation. ALWAYS use `NEWSEQUENTIALID()` or standard `IDENTITY` columns.
- **[SQL Server] Dates**: Use `DATETIME2` instead of the legacy `DATETIME` type. It has a larger date range, higher fractional precision, and uses less storage space (6-8 bytes vs 8 bytes).
- **[Oracle] Strings vs NULL**: In Oracle, an empty string `''` is treated identically to `NULL`. This is a massive gotcha for developers coming from MySQL or SQL Server.
- **[Oracle] Data Types**: Oracle has historically pushed `VARCHAR2` instead of `VARCHAR`. Use `NUMBER(p, s)` for almost all numeric data (integers and decimals) instead of `INT` or `DECIMAL`.
- **[SQLite] Types**: Has no native `BOOLEAN` (uses `INTEGER` 0/1). Date math requires specific functions (`datetime()`). Requires `PRAGMA foreign_keys = ON;` to enforce relational integrity.

## 4. Universal Query & Index Optimization (The Golden Rules)

Apply these rules strictly when writing or reviewing queries:

- **SARGability (Crucial)**: NEVER use functions on indexed columns in a `WHERE` clause.
  - _BAD_: `WHERE YEAR(created_at) = 2024` (Causes full scan)
  - _GOOD_: `WHERE created_at >= '2024-01-01' AND created_at < '2025-01-01'` (Uses index)
  - _BAD_: `WHERE LEFT(customer_code, 3) = 'ABC'` (Causes full scan)
  - _GOOD_: `WHERE customer_code LIKE 'ABC%'` (Uses index)
  - _BAD_: `WHERE salary * 1.1 > 50000` (Causes full scan)
  - _GOOD_: `WHERE salary > 50000 / 1.1` (Uses index)
- **The Leftmost Prefix Rule**: For composite indexes `(A, B, C)`, queries MUST use `A` to utilize the index. Order composite indexes strategically: Equality columns FIRST, Range columns SECOND, Ordering columns LAST.
- **Implicit Conversions**: Ensure data types match exactly in `WHERE` and `JOIN` clauses. Comparing an `INT` column to a string `'123'` breaks the index and causes a full table scan.
  - _BAD_: `WHERE user_id = '1050'` (if `user_id` is an `INT`, the DB will cast every row to a string to compare).
  - _GOOD_: `WHERE user_id = 1050`
- **Index All JOINs**: Every column used in a `JOIN ON` clause MUST be indexed.
- **Deep Pagination Trap**: AVOID `LIMIT 10 OFFSET 100000` on large tables (the DB reads and discards 100k rows). Use Keyset/Cursor Pagination (`WHERE id > last_id ORDER BY id LIMIT 10`).
- **No `SELECT *`**: Always specify exact columns. This enables "Covering Indexes" (indexes that contain all requested columns, avoiding table data reads entirely).
- **`EXISTS` vs `COUNT(*)`**: Use `EXISTS` instead of `COUNT(*) > 0` for existence checks, as it short-circuits on the first match.
- **`COUNT(*)` vs `COUNT(col)`**: `COUNT(*)` counts all rows. `COUNT(column)` skips NULLs and adds processing overhead.

## 5. Advanced Capabilities (Skill Router)

When a task requires deep specialization or falls into the remaining 20% of edge cases, read the following sub-skills using your `read` tool:

- **Execution Plans & Deep Tuning (`EXPLAIN`)**:
  -> Read: `references/adv-performance.md`
  Use when: Deciphering `EXPLAIN FORMAT=JSON`, `EXPLAIN QUERY PLAN`, analyzing access types (`type: ALL`, `const`), or fixing `Using filesort` / `Using temporary`.

- **Diagnostics & Health Checks**:
  -> Read: `references/adv-diagnostics.md`
  Use when: The user needs scripts to find unused indexes, redundant indexes, check table sizes, or verify DB integrity.

- **Business Logic (Triggers, Stored Procedures, BD vs App)**:
  -> Read: `references/adv-logic.md`
  Use when: Writing Stored Procedures (avoiding cursors), setting up Triggers (avoiding Mutating Table errors), or deciding what logic belongs in the App vs the DB.

- **Zero-Downtime Migrations & Refactoring**:
  -> Read: `references/adv-migrations.md`
  Use when: Altering massive tables in production (Shadow Table approach), changing data types safely (INT to BIGINT), or translating dialects (MySQL <-> SQLite).
