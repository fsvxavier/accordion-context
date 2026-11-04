# DBA Skill: Advanced Logic (DB vs Application)

When determining where business rules, constraints, and data integrity logic should reside, you must balance the application layer (Python, Node, PHP) with the database layer (MySQL, SQLite, PostgreSQL, Oracle).

## 1. Database Layer (The "Fortress")

The database is the ultimate source of truth. It must protect itself from bad data, regardless of the application's bugs or oversights.

### 1.1 Constraints (First Line of Defense)

Always prefer DB-level constraints over application validation for data integrity:

- `NOT NULL`: Never allow `NULL` unless the absence of a value has a specific business meaning.
- `UNIQUE`: Prevent duplicates across single or multiple columns (e.g., `UNIQUE(user_id, product_id)`).
- `CHECK`: In modern engines (MySQL 8.0.16+, PostgreSQL, Oracle, SQLite), use `CHECK` for domain validation (e.g., `CHECK (age >= 18 AND age <= 120)` or `CHECK (status IN ('active', 'inactive'))`).
  - **[MySQL 5.7 Gotcha]**: MySQL 5.7 parses `CHECK` constraints but silently IGNORES them. They provide zero protection. Use triggers instead if stuck on 5.7.
  - **[MySQL 8.0.16+ Gotcha]**: `CHECK` constraints are finally enforced, but they CANNOT contain subqueries or reference variables/functions that are non-deterministic (like `NOW()`).
- `FOREIGN KEY`: With `ON DELETE RESTRICT` to prevent orphan records, or `ON DELETE CASCADE` for automatic cleanup of child records.

### 1.2 Triggers (Automated Internal Maintenance)

Use triggers strictly for internal, data-focused automation:

- **Audit Trails**: Automatically recording `created_at` or `updated_at` timestamps. (e.g., in SQLite `CREATE TRIGGER update_timestamp BEFORE UPDATE ON my_table BEGIN UPDATE my_table SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id; END;`).
- **History/Shadow Tables**: Logging old values to a history table `ON UPDATE` or `ON DELETE` for compliance.
- **Computed Columns (If unsupported natively)**: Calculating and storing a value derived from other columns in the same row.

**Engine-Specific Trigger Rules:**

- **[MySQL] The Mutating Table Error**: A trigger CANNOT modify the same table it fires on. This will cause an immediate execution error.
- **[MySQL] Performance**: Triggers fire row-by-row. Do not put heavy logic or complex `JOIN` queries inside triggers.
- **[PostgreSQL] No Mutating Table Error**: Unlike MySQL, PostgreSQL triggers can safely query or modify the table that fired them (using `BEFORE` or `AFTER` triggers). Trigger logic is written in `PL/pgSQL` functions.
- **[Oracle]**: _(Placeholder for `MUTATING TABLE` exceptions and `PRAGMA AUTONOMOUS_TRANSACTION`)._

### 1.3 Events (MySQL & Oracle)

Use the built-in Event Scheduler for routine maintenance inside the DB:

- **Data Archiving/Purging**: Automatically deleting rows older than 30 days (`DELETE FROM logs WHERE created_at < NOW() - INTERVAL 30 DAY`).
- **Aggregation**: Summarizing detailed logs into a daily summary table every night.
- **[PostgreSQL/SQLite]**: Neither has a native built-in event scheduler enabled by default. For Postgres, use the `pg_cron` extension. For SQLite, rely on external OS-level cron jobs.

### 1.4 Stored Procedures / Functions

Use sparingly. They are appropriate when:

- **Complex Transactional Logic**: A sequence of inserts/updates must happen together, and doing it in the application requires excessive network round-trips.
- **Legacy Integration**: Multiple applications (or languages) access the same DB and need a unified API.

**Engine-Specific Procedure Rules:**

- **[MySQL] DEFINER vs INVOKER**: Always specify the security context explicitly (`SQL SECURITY DEFINER` vs `INVOKER`) to avoid permission escalation risks.
- **[MySQL] Cursors are Evil**: Avoid cursors whenever set-based operations are possible. Cursors process row-by-row and are painfully slow. Rewrite logic to use `INSERT ... SELECT` instead.
  - **BAD (Cursor)**: Looping through rows to calculate a discount and updating one by one.
  - **GOOD (Set-based)**: `UPDATE orders SET total = total * 0.9 WHERE status = 'gold';`
- **[PostgreSQL] Functions vs Procedures**: Historically used Functions (`RETURNS void`), but Postgres 11+ supports true Procedures (`CALL`) which can manage their own transactions (`COMMIT`/`ROLLBACK` inside the procedure).
- **[PostgreSQL] STRICT / RETURNS NULL ON NULL INPUT**: Add this to a function definition so it instantly returns null if any argument is null, saving execution time.
- **[SQL Server] SET NOCOUNT ON**: Always start stored procedures with `SET NOCOUNT ON`. It stops the server from sending the "x rows affected" message back to the client for every internal operation, drastically reducing network overhead.
- **[Oracle] PL/SQL Collections**: For bulk operations in Oracle, always use `BULK COLLECT` and `FORALL` instead of row-by-row cursors to minimize context switches between the SQL and PL/SQL engines.

## 2. Application Layer (The "Agile Logic")

Business rules that change frequently or require external context belong in the application code.

### 2.1 External Systems & Integrations

Never use the DB to trigger emails, API calls, or external notifications. The application must handle this after a successful DB commit.

### 2.2 Complex Validations

If validation requires checking external APIs, complex string parsing (e.g., validating a credit card format beyond a simple regex), or comparing data across many disjointed tables, do it in the application.

### 2.3 Business Workflow Logic

If a "Status" change (e.g., `Pending` -> `Shipped`) requires sending an email, charging a card, and updating a counter, orchestrate this in the application layer. Do not bury this workflow in a DB trigger, as it becomes invisible and hard to debug.
