# DBA AI Skill (Super DBA)

A comprehensive, engine-aware Database Administration (DBA) toolkit designed for AI CLI agents (like Claude Code, Gemini CLI, OpenCode).

## 🎯 Intention & Goal

The goal of this skill is to transform your AI assistant into a Senior Database Administrator. Instead of relying on generic or hallucinated SQL advice, this skill provides the AI with strict, battle-tested rules for schema design, query optimization, indexing strategies, and zero-downtime migrations.

It is structured using an **80/20 architecture**:

- `SKILL.md` (Root): Contains the 80% universal rules for all relational databases (SARGability, Normalization, Leftmost Prefix).
- `references/` (Folder): Contains the 20% engine-specific deep dives (Execution Plans, advanced DDL gotchas, and diagnostic scripts) that the AI reads dynamically when needed.

## 🗄️ Supported Databases

This skill is highly specialized and handles the historical quirks, breaking changes, and performance tuning for the world's Top 5 Relational Database Management Systems (RDBMS):

- **MySQL**: From 5.7 legacy traps (missing CTEs, ignored `CHECK` constraints) up to modern 8.0/8.4 LTS gotchas (implicit sorting removal, `ONLY_FULL_GROUP_BY` enforcement).
- **PostgreSQL**: Deep knowledge of PG 11 through 15+ breaking changes (the death of `OIDs`, the `public` schema security trap, CTE materialization changes, and B-Tree deduplication).
- **Microsoft SQL Server (T-SQL)**: Advanced insights on DMVs, Execution Plans, and version traps (like the `STRING_SPLIT` ordering glitch or `NOLOCK` abuse).
- **Oracle**: Mastery of PL/SQL collections, 12c Identity columns (escaping Sequence-Trigger hell), the `ROWNUM` pagination nightmare, and the 23ai `BOOLEAN` miracle.
- **SQLite**: Navigating the transition from legacy "Flexible Typing" to Modern `STRICT` tables, the addition of `DROP COLUMN` support, and JSONB.

## ⚡ Core Capabilities

1. **SQL Auditing & Review**: A strict workflow for reviewing schemas and queries, classifying findings by severity (CRITICAL, WARNING, SUGGESTION, INFO).
2. **Schema Design**: Universal normalization rules, right-sizing data types, and engine-specific traps (like never using UUIDs as Primary Keys in InnoDB).
3. **Query Optimization**: Enforcing SARGability, Covering Indexes, and rewriting subqueries.
4. **Execution Plan Analysis**: Deciphering `EXPLAIN FORMAT=JSON` (MySQL), `EXPLAIN (ANALYZE, BUFFERS)` (Postgres), SSMS Execution Plans, and `DBMS_XPLAN` (Oracle).
5. **Zero-Downtime Migrations**: Safe DDL operations for massive production tables using the "Shadow Table" approach and online index creation.
6. **Diagnostics**: Health-check scripts to find unused indexes, calculate table bloat (dead tuples), and identify slow queries across engines.

## 🚀 Installation & Usage

To use this skill with your favorite AI CLI agent (like OpenCode, Claude Code, or Gemini CLI), you simply need to place it in the agent's designated `skills` directory.

1. **Clone the repository**:

   ```bash
   git clone https://github.com/u1pns/skill-dba.git
   ```

2. **Move to your agent's skills folder**:
   ```bash
   # For OpenCode:
   mv skill-dba ~/.config/opencode/skills/dba
   ```

> **💡 Pro Tip (Symlinking for Multiple Agents):**
> If you use multiple AI CLI tools and want them all to share this DBA skill without duplicating files, keep the cloned repository in a central location (e.g., `~/ai-skills/`) and create a **symlink** to each agent's skill directory:
>
> ```bash
> # 1. Clone to a central location
> git clone https://github.com/u1pns/skill-dba.git ~/ai-skills/dba
>
> # 2. Create symlinks for your agents
> ln -s ~/ai-skills/dba ~/.config/opencode/skills/dba
> ln -s ~/ai-skills/dba ~/.claude/skills/dba
> ```

## 📄 License

MIT License

Copyright (c) 2026 u1pns

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
