# DBA Skill: Advanced Diagnostics & Health Checks

When the user requests a database health check, an audit of schema sizes, or help finding performance bottlenecks that aren't tied to a single query, use the following engine-specific diagnostic scripts.

## 1. MySQL 5.7+ / 8.0+ Diagnostics

The `sys` schema and `information_schema` are your best friends in MySQL.

### 1.1 Unused and Redundant Indexes

Indexes that are never used for `SELECT`s are pure overhead for `INSERT`/`UPDATE`/`DELETE` operations.

```sql
-- Find unused indexes (since last server restart)
SELECT * FROM sys.schema_unused_indexes
WHERE object_schema NOT IN ('mysql', 'sys', 'information_schema', 'performance_schema');

-- Find redundant indexes (e.g., an index on (A) when (A,B) already exists)
SELECT * FROM sys.schema_redundant_indexes
WHERE table_schema NOT IN ('mysql', 'sys', 'information_schema', 'performance_schema');
```

### 1.2 Database and Table Sizes

Identify the largest tables consuming disk space. Note: `index_length` can sometimes be larger than `data_length` if over-indexed.

```sql
-- Show table sizes and index sizes (in MB)
SELECT table_name, table_rows,
  ROUND(data_length/1024/1024, 2) AS data_mb,
  ROUND(index_length/1024/1024, 2) AS index_mb
FROM information_schema.tables
WHERE table_schema = 'your_database_name'
ORDER BY data_length + index_length DESC;
```

### 1.3 Optimizer Settings

Check the current optimizer flags, which can drastically change how `EXPLAIN` plans are generated.

```sql
-- View current optimizer settings
SELECT @@optimizer_switch\G
```

### 1.4 Index Cardinality

To see if an index is actually selective (a low cardinality means the index isn't very useful because it only filters down to a huge chunk of rows).

```sql
-- Show index cardinality
SHOW INDEX FROM table_name;
```

_Note: If cardinality seems wrong, run `ANALYZE TABLE table_name;` to update the statistics._

## 2. PostgreSQL Diagnostics

PostgreSQL provides a wealth of statistics via its `pg_stat_*` views.

### 2.1 Unused Indexes

```sql
-- Find indexes that have never been scanned (or rarely scanned)
SELECT schemaname, relname AS table_name, indexrelname AS index_name, idx_scan, pg_size_pretty(pg_relation_size(indexrelid)) AS index_size
FROM pg_stat_user_indexes
WHERE idx_scan = 0 AND idx_scan < 50
ORDER BY pg_relation_size(indexrelid) DESC;
```

### 2.2 Table and Index Sizes

```sql
-- Get total size of a table (including its indexes and TOAST data)
SELECT relname AS "table_name", pg_size_pretty(pg_total_relation_size(relid)) AS "total_size"
FROM pg_catalog.pg_statio_user_tables
ORDER BY pg_total_relation_size(relid) DESC;
```

### 2.3 Vacuum and Dead Tuples (Bloat)

PostgreSQL uses MVCC, meaning `UPDATE` and `DELETE` operations leave "dead tuples" behind until autovacuum cleans them up. Too many dead tuples cause "bloat" and severe performance degradation.

```sql
-- Check percentage of dead tuples per table
SELECT relname AS table_name,
       n_live_tup AS live_tuples,
       n_dead_tup AS dead_tuples,
       ROUND((n_dead_tup::numeric / NULLIF(n_live_tup + n_dead_tup, 0)) * 100, 2) AS dead_tuple_percentage,
       last_autovacuum
FROM pg_stat_user_tables
ORDER BY dead_tuple_percentage DESC;
```

_Note: If `dead_tuple_percentage` is consistently high (e.g., > 20%), autovacuum settings likely need tuning._

### 2.4 Slow Queries (Requires `pg_stat_statements`)

```sql
-- Top 5 queries consuming the most cumulative time
SELECT query, calls, total_exec_time, mean_exec_time
FROM pg_stat_statements
ORDER BY total_exec_time DESC
LIMIT 5;
```

## 3. Microsoft SQL Server Diagnostics

SQL Server provides Dynamic Management Views (DMVs) for deep diagnostics.

### 3.1 Unused Indexes

```sql
SELECT
    objects.name AS Table_name,
    indexes.name AS Index_name,
    user_seeks, user_scans, user_lookups, user_updates
FROM sys.dm_db_index_usage_stats AS stats
JOIN sys.indexes AS indexes
    ON stats.object_id = indexes.object_id AND stats.index_id = indexes.index_id
JOIN sys.objects AS objects
    ON indexes.object_id = objects.object_id
WHERE database_id = DB_ID()
  AND user_seeks = 0
  AND user_scans = 0
  AND user_lookups = 0
ORDER BY user_updates DESC;
```

### 3.2 Missing Indexes (Suggested by the Engine)

```sql
SELECT
    migs.avg_user_impact * (migs.user_seeks + migs.user_scans) AS Improvement_Measure,
    'CREATE INDEX [missing_index] ON ' + statement + ' (' + ISNULL(equality_columns,'') +
    CASE WHEN equality_columns IS NOT NULL AND inequality_columns IS NOT NULL THEN ',' ELSE '' END +
    ISNULL(inequality_columns, '') + ')' +
    ISNULL(' INCLUDE (' + included_columns + ')', '') AS Create_Index_Statement
FROM sys.dm_db_missing_index_groups mig
JOIN sys.dm_db_missing_index_group_stats migs ON migs.group_handle = mig.index_group_handle
JOIN sys.dm_db_missing_index_details mid ON mig.index_handle = mid.index_handle
ORDER BY Improvement_Measure DESC;
```

## 4. Oracle Diagnostics

Oracle uses the Data Dictionary (`DBA_*` and `V$*` views) for health checks.

### 4.1 Index Monitoring

In Oracle, you must explicitly enable monitoring on an index before checking if it's used.

```sql
-- Step 1: Turn on monitoring for an index
ALTER INDEX my_index MONITORING USAGE;

-- Step 2: Check if it was used
SELECT index_name, used, start_monitoring, end_monitoring
FROM v$object_usage
WHERE index_name = 'MY_INDEX';
```

### 4.2 Top Slowest Queries (AWR-like)

```sql
SELECT sql_text, executions, elapsed_time/1000000 as elapsed_sec,
       (elapsed_time/executions)/1000000 as avg_sec_per_exec
FROM v$sql
WHERE executions > 0
ORDER BY elapsed_time DESC
FETCH FIRST 10 ROWS ONLY;
```

## 5. SQLite Diagnostics

SQLite is a local file database, so diagnostics are limited to integrity checks and file size analysis.

### 4.1 Integrity Checks

```sql
-- Perform a thorough check of the entire database file for corruption
PRAGMA integrity_check;

-- Perform a faster, less thorough check
PRAGMA quick_check;

-- Check for foreign key violations across the entire database
PRAGMA foreign_key_check;
```

### 4.2 Database Size and Fragmentation

```sql
-- Get the page size and count to calculate total DB size (page_size * page_count)
PRAGMA page_size;
PRAGMA page_count;

-- Check the amount of free space (freelist count * page_size)
PRAGMA freelist_count;
```

_Note: If `freelist_count` is massive, it means a lot of data was deleted but the file size hasn't shrunk. You can reclaim this space by running `VACUUM;`, but warn the user that this locks the database completely while it rewrites the file._
