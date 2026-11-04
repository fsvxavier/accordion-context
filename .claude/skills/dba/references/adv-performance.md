# DBA Skill: Advanced Performance & Execution Plans

When optimizing queries or diagnosing performance issues, you must act as an expert indexer and execution plan analyst. The rules for interpreting how a database executes a query vary wildly by engine.

## 1. Execution Plan Analysis (`EXPLAIN`)

Always use `EXPLAIN` before suggesting an index. If the user hasn't provided the plan, ask them to generate it or execute the command yourself using `bash`.

### 1.1 MySQL / InnoDB (`EXPLAIN FORMAT=JSON`)

Analyze the output focusing on `type`, `Extra`, `rows` estimate, and `filtered` percentage.

#### Access Types (from best to worst):

- **type: `const` / `system`**: PK or UNIQUE lookup returning a single row. Optimal.
- **type: `eq_ref`**: PK or UNIQUE join returning one row per combination. Optimal for JOINs.
- **type: `ref`**: Non-unique index equality lookup. Good.
- **type: `range`**: Index range scan (e.g., `BETWEEN`, `>`, `<`). Good for bounded queries.
- **type: `index`**: Full index scan. Better than `ALL` but still reads the whole index tree.
- **type: `ALL`**: Full table scan. **CRITICAL FLAG**. Must add an appropriate index immediately.

#### Key Extra Values:

- **Extra: `Using index`**: A **Covering Index**. The query reads only the index tree, never touching the table data pages. Excellent.
- **Extra: `Using index condition`**: Index Condition Pushdown (ICP) to the storage engine. Good.
- **Extra: `Using where`**: The server-level filtering is applied after the storage engine retrieves the rows. Normal, but check the `rows` estimate.
- **Extra: `Using filesort`**: The database is sorting the result set in memory or disk because the index doesn't support the `ORDER BY`. **WARNING FLAG**. Add the sort column to your index.
- **Extra: `Using temporary`**: The database created a temporary table to process the query (e.g., mismatched `GROUP BY` and `ORDER BY`). **WARNING FLAG**. Align the clauses or add an index.
- **Extra: `Using join buffer (Block Nested Loop)`**: The database is buffering rows because there is NO index for the join condition. **CRITICAL FLAG**. Add an index on the join column immediately.

#### Red Flags in EXPLAIN:

- `type: ALL` on tables with > 1000 rows.
- `key: NULL` when `possible_keys` lists indexes (the optimizer rejected them because a full scan seemed cheaper).
- `rows` product across joined tables is massively larger than the actual expected result set.
- `filtered` < 20% (meaning 80% of the rows the engine examined were discarded by the `WHERE` clause).
- Multiple `Using filesort` + `Using temporary` combined.

### 1.2 PostgreSQL (`EXPLAIN (ANALYZE, BUFFERS)`)

The gold standard command is `EXPLAIN (ANALYZE, BUFFERS)`. `ANALYZE` actually executes the query to give real timings (not just estimates). `BUFFERS` shows memory hits and disk I/O.

#### Access Types:

- **Seq Scan**: Full table scan. **CRITICAL FLAG** on large tables. Needs an index.
- **Index Scan**: Reads the index to find the row location, then fetches the row from the table (Heap). Good.
- **Index Only Scan**: A **Covering Index**. Reads only the index, avoiding the table completely (relies on the Visibility Map being up to date). Excellent.
- **Bitmap Index Scan & Bitmap Heap Scan**: Often used together. The DB scans an index to build a memory bitmap of which data pages to fetch, then fetches those pages sequentially. Great for fetching many rows efficiently.

#### Join Types:

- **Nested Loop**: Good for small datasets. For large datasets, a missing index will cause massive performance drops here (similar to MySQL's Block Nested Loop).
- **Hash Join**: Creates a hash table in memory for one side of the join. Excellent for large, unsorted datasets.
- **Merge Join**: Requires both sides to be sorted. Excellent for large, pre-sorted datasets (e.g., joining on indexed columns).

#### Red Flags in EXPLAIN:

- High `Buffers: shared hit` combined with `read` indicates disk thrashing and insufficient memory (`shared_buffers`).
- `Filter: (...)` removing a massive percentage of rows from a `Seq Scan` means an index is desperately needed.

### 1.3 Microsoft SQL Server (`SET STATISTICS PROFILE ON` or `SET SHOWPLAN_ALL ON`)

SQL Server Execution Plans are best viewed visually in SSMS, but as a CLI agent, you rely on text plans.

#### Key Operators:

- **Table Scan / Clustered Index Scan**: Reading the entire table. **CRITICAL FLAG** on large tables. Needs a non-clustered index on the `WHERE` clause.
- **Index Seek / Clustered Index Seek**: Fast traversal of a B-Tree structure to find a specific range of rows. Optimal.
- **Key Lookup (Bookmark Lookup)**: The query uses a non-clustered index to find rows, but that index is NOT a "Covering Index." The engine must jump to the Clustered Index to fetch the rest of the columns. **WARNING FLAG**. If it happens thousands of times, add the missing columns to the non-clustered index using the `INCLUDE` clause.

#### Joins and Sorting:

- **Nested Loops Join**: Excellent for joining a small result set to a large indexed table.
- **Hash Match Join**: Excellent for joining two large, unsorted datasets. However, it requires significant memory (RAM) to build the hash table.
- **Sort Operator**: **WARNING FLAG**. Sorting a massive dataset is extremely expensive in CPU and memory. Create an index that matches the `ORDER BY` clause.

### 1.4 Oracle (`EXPLAIN PLAN FOR ...` and `DBMS_XPLAN.DISPLAY`)

In Oracle, you generate the plan and then read it from a specialized table.

#### Access Paths:

- **TABLE ACCESS FULL**: A full table scan. Reads all blocks below the high-water mark. **CRITICAL FLAG** for OLTP systems unless retrieving a large percentage of the table.
- **INDEX UNIQUE SCAN**: Looks up a single ROWID via a primary key or unique constraint. Optimal.
- **INDEX RANGE SCAN**: Returns multiple ROWIDs matching a range condition. Very fast.
- **TABLE ACCESS BY INDEX ROWID**: A **WARNING FLAG** for covering indexes. Similar to SQL Server's Key Lookup, Oracle found the index but had to go back to the table block for the rest of the columns. Add the missing columns to the index if performance is critical.

#### Joins:

- **NESTED LOOPS**: Driven by the smaller table, using an index to look up rows in the inner table.
- **HASH JOIN**: Builds a memory table of the smaller dataset and hashes the larger dataset against it.
- **MERGE JOIN**: Both data sources must be sorted on the join key. Extremely efficient for large, pre-sorted data.

### 1.5 SQLite (`EXPLAIN QUERY PLAN`)

- **SCAN TABLE**: Full table scan. **CRITICAL FLAG**. Needs an index.
- **SEARCH TABLE ... USING INDEX**: Good. The query is using an index.
- **USE TEMP B-TREE FOR ORDER BY**: Sorting without an index. **WARNING FLAG**. Add the sorted column to an index.
- **USE TEMP B-TREE FOR GROUP BY**: Grouping without an index. Add an index.

## 2. Advanced Indexing Scenarios

### 2.1 The Subquery vs JOIN Trap

Avoid correlated subqueries where the inner query depends on the outer query. This forces the inner query to execute once _per row_ of the outer query (an O(N\*M) disaster).

- **BAD (O(N^2) complexity):**
  ```sql
  SELECT e.name, e.salary
  FROM employees e
  WHERE e.department_id IN (
      SELECT d.id FROM departments d WHERE d.name = 'Sales'
  );
  ```
- **GOOD (Set-based, index-friendly O(N+M) complexity):**
  ```sql
  SELECT e.name, e.salary
  FROM employees e
  JOIN departments d ON e.department_id = d.id
  WHERE d.name = 'Sales';
  ```

### 2.2 Deep Pagination Death (`OFFSET`)

Pagination with huge `OFFSET` values forces the engine to scan, count, and discard rows.

- **BAD (Reads and discards 50,000 rows):**
  ```sql
  SELECT id, title FROM posts
  ORDER BY created_at DESC
  LIMIT 10 OFFSET 50000;
  ```
- **GOOD (Keyset / Cursor Pagination):**
  ```sql
  SELECT id, title FROM posts
  WHERE created_at < '2024-03-15 10:00:00'
  ORDER BY created_at DESC
  LIMIT 10;
  ```

### 2.3 `OR` Conditions Killing Indexes

`OR` conditions (`WHERE a = 1 OR b = 2`) often prevent the use of a single index.

- **BAD (Usually causes a full table scan):**
  ```sql
  SELECT * FROM users
  WHERE status = 'banned' OR last_login < '2020-01-01';
  ```
- **GOOD (Uses two indexes and merges results in memory):**
  ```sql
  SELECT * FROM users WHERE status = 'banned'
  UNION ALL
  SELECT * FROM users WHERE last_login < '2020-01-01' AND status != 'banned';
  ```

### 2.3 Prefix Indexes (MySQL Specific)

In MySQL, you can index just the first N characters of a large `VARCHAR` or `TEXT` column (e.g., `INDEX name(20)`). This saves space and memory.

- **Limitation**: Prefix indexes CANNOT be used for `ORDER BY`, `GROUP BY`, or as Covering Indexes (`Using index`). Only use them for basic filtering.

### 2.5 Engine-Specific Query Gotchas (MySQL 5.7 vs 8.0+)

Be hyper-aware of the database version, particularly for MySQL, which underwent massive changes between 5.7 and 8.0:

- **The `ONLY_FULL_GROUP_BY` Trap (MySQL 5.7 & 8.0+)**: Enabled by default. If a `SELECT` list includes a non-aggregated column that isn't in the `GROUP BY` clause, the query will crash. Legacy apps (pre-5.7) often rely on this broken behavior. Do NOT turn off the flag; fix the query with `ANY_VALUE()` or properly aggregate the column.
- **No CTEs (`WITH`) in MySQL 5.7**: You must rewrite Common Table Expressions using Derived Tables (subqueries in the `FROM` clause) or temporary tables. (MySQL 8.0+ supports CTEs perfectly).
- **No Window Functions in MySQL 5.7**: Functions like `ROW_NUMBER()`, `RANK()`, or `OVER()` do not exist. You must rewrite these using complex user-variable hacks (`@row_num := @row_num + 1`) or self-joins. (MySQL 8.0+ supports Window Functions).
- **JSON Indexing (MySQL 5.7)**: You cannot index a JSON column directly. You MUST create a Virtual Generated Column that extracts the JSON value, and then index that generated column: `ALTER TABLE t ADD v_col VARCHAR(50) AS (JSON_UNQUOTE(json_col->'$.key')), ADD INDEX (v_col);`
  - _(In MySQL 8.0+, you can use Multi-Valued Indexes to index JSON arrays directly using `MEMBER OF()`)._

### 2.6 Engine-Specific Query Gotchas (PostgreSQL 11 vs 12+)

- **[PostgreSQL 12] CTE Materialization Changes**: In PG 11 and earlier, Common Table Expressions (`WITH` clauses) were ALWAYS evaluated independently and materialized in memory. They acted as strict "optimization fences." In PG 12+, CTEs are NOT materialized by default if referenced only once; the optimizer folds them into the main query, massively improving performance.
  - **The Trap**: If you upgrade a legacy system that intentionally used CTEs as optimization fences to force a specific execution plan, performance may suddenly drop. To restore the old behavior in PG 12+, you must explicitly write `WITH cte_name AS MATERIALIZED (...)`.

### 2.7 Engine-Specific Query Gotchas (Oracle & SQL Server)

- **[Oracle] The Pagination Nightmare (`ROWNUM` vs `FETCH FIRST`)**: Prior to Oracle 12c, implementing query pagination required a horrific, double-nested subquery using the pseudo-column `ROWNUM` because `ROWNUM` evaluates _before_ `ORDER BY`.
  - **Legacy (11g and older)**: `SELECT * FROM (SELECT a.*, ROWNUM rnum FROM (SELECT * FROM my_table ORDER BY my_col) a WHERE ROWNUM <= 20) WHERE rnum >= 10;`
  - **Modern (12c+)**: Always refactor legacy pagination to use the ISO standard: `SELECT * FROM my_table ORDER BY my_col OFFSET 10 ROWS FETCH NEXT 10 ROWS ONLY;`
- **[SQL Server] The `NOLOCK` Abuse**: A massive anti-pattern in T-SQL. Developers often slap `WITH (NOLOCK)` on every `SELECT` statement to avoid blocking writers.
  - **The Trap**: `NOLOCK` reads uncommitted, "dirty" data. It can even read the same row twice or skip rows entirely during a page split. If you see `NOLOCK` on financial or critical reports, flag it as a **CRITICAL** bug. The correct solution to reader/writer blocking in SQL Server is enabling **Read Committed Snapshot Isolation (RCSI)** at the database level.
