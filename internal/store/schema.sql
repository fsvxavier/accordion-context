-- Projetos (§18): um por pasta raiz detectada ou configurada
CREATE TABLE projects (
  id           TEXT PRIMARY KEY,         -- estável: ver §18.2
  root_path    TEXT NOT NULL,            -- caminho absoluto canônico atual
  storage      TEXT NOT NULL CHECK (storage IN ('global','local')),
  created_at   INTEGER NOT NULL,
  last_seen_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX idx_projects_root ON projects(root_path);

-- Sessões
CREATE TABLE sessions (
  id             TEXT PRIMARY KEY,
  project_id     TEXT NOT NULL REFERENCES projects(id),
  created_at     INTEGER NOT NULL,
  model          TEXT NOT NULL,
  context_limit  INTEGER NOT NULL,
  mode           TEXT NOT NULL CHECK (mode IN ('proxy','plugin','library')),  -- quem executa compress (§19)
  integration    TEXT NOT NULL CHECK (integration IN ('url','launcher','attach','mitm','system_proxy','transparent','plugin','mcp','middleware','library'))
);

-- Conteúdo endereçado por hash (deduplicado, comprimido com zstd)
CREATE TABLE contents (
  hash       TEXT PRIMARY KEY,          -- sha256 do conteúdo bruto
  size_bytes INTEGER NOT NULL,
  data       BLOB NOT NULL               -- zstd
);

-- Mensagens: append-only, nunca apagadas
CREATE TABLE messages (
  session_id   TEXT NOT NULL REFERENCES sessions(id),
  seq          INTEGER NOT NULL,         -- posição no ledger (monotônica)
  ref          TEXT NOT NULL,            -- 'm00042'
  role         TEXT NOT NULL CHECK (role IN ('system','user','assistant','tool')),
  kind         TEXT NOT NULL CHECK (kind IN ('text','tool_call','tool_result','reasoning')),
  tool_name    TEXT,
  tool_call_id TEXT,                     -- liga call ↔ result
  content_hash TEXT NOT NULL REFERENCES contents(hash),
  tokens       INTEGER NOT NULL,
  ccr_stored   INTEGER NOT NULL DEFAULT 0, -- 1 = visível como placeholder (§14)
  created_at   INTEGER NOT NULL,
  PRIMARY KEY (session_id, seq),
  UNIQUE (session_id, ref)
);

-- Blocos de compressão
CREATE TABLE blocks (
  session_id   TEXT NOT NULL REFERENCES sessions(id),
  block_id     TEXT NOT NULL,            -- 'b17'
  tier         INTEGER NOT NULL CHECK (tier IN (1,2,3)),
  topic        TEXT,
  summary      TEXT NOT NULL,
  start_seq    INTEGER NOT NULL,         -- intervalo coberto no ledger
  end_seq      INTEGER NOT NULL,
  src_tokens   INTEGER NOT NULL,         -- tokens dobrados
  sum_tokens   INTEGER NOT NULL,         -- tokens do resumo
  active       INTEGER NOT NULL CHECK (active IN (0,1)),
  parent_block TEXT,                     -- bloco que o consumiu (tier superior)
  created_at   INTEGER NOT NULL,
  PRIMARY KEY (session_id, block_id),
  CHECK (end_seq >= start_seq),
  CHECK (sum_tokens < src_tokens)        -- compressão precisa encolher
);

-- Linhagem: mensagens diretamente cobertas por blocos T1
CREATE TABLE block_messages (
  session_id TEXT NOT NULL,
  block_id   TEXT NOT NULL,
  seq        INTEGER NOT NULL,
  PRIMARY KEY (session_id, block_id, seq),
  FOREIGN KEY (session_id, block_id) REFERENCES blocks(session_id, block_id),
  FOREIGN KEY (session_id, seq)      REFERENCES messages(session_id, seq)
);

-- Linhagem: blocos consumidos por blocos T2/T3
CREATE TABLE block_sources (
  session_id TEXT NOT NULL,
  block_id   TEXT NOT NULL,              -- bloco de tier superior
  source_id  TEXT NOT NULL,              -- bloco consumido
  PRIMARY KEY (session_id, block_id, source_id)
);

-- Ledger de intenções do usuário (§11.3)
CREATE TABLE intents (
  session_id TEXT NOT NULL,
  intent_id  INTEGER NOT NULL,
  origin_seq INTEGER NOT NULL,           -- mensagem do usuário de origem
  text       TEXT NOT NULL,              -- literal
  status     TEXT NOT NULL CHECK (status IN ('open','done','superseded')),
  closed_seq INTEGER,
  PRIMARY KEY (session_id, intent_id)
);

-- Estado do gatilho (uma linha por sessão)
CREATE TABLE trigger_state (
  session_id       TEXT PRIMARY KEY REFERENCES sessions(id),
  low_water_tokens INTEGER NOT NULL,     -- vale mais recente (§8.2)
  nudge_pending    INTEGER NOT NULL,     -- 0/1
  nudge_shown_at   INTEGER NOT NULL,     -- tokens quando o nudge foi mostrado
  last_shown_t2    INTEGER NOT NULL DEFAULT 0,
  last_shown_t3    INTEGER NOT NULL DEFAULT 0,
  refusals         INTEGER NOT NULL DEFAULT 0
);

-- Log de eventos append-only (auditoria + replay)
CREATE TABLE events (
  session_id TEXT NOT NULL,
  event_seq  INTEGER NOT NULL,
  at         INTEGER NOT NULL,
  type       TEXT NOT NULL,              -- 'turn','nudge','compress','decompress','refuse','recall_hint',...
  payload    TEXT NOT NULL,              -- JSON
  PRIMARY KEY (session_id, event_seq)
);

-- Ledger de cache por chamada (§9.4)
CREATE TABLE cache_ledger (
  session_id     TEXT NOT NULL,
  call_seq       INTEGER NOT NULL,
  at             INTEGER NOT NULL,
  input_tokens   INTEGER NOT NULL,       -- reportado pelo provedor
  cached_tokens  INTEGER NOT NULL,
  est_tokens     INTEGER NOT NULL,       -- nossa estimativa (calibração §15)
  after_compress INTEGER NOT NULL,       -- 1 se é a 1ª chamada após um fold
  PRIMARY KEY (session_id, call_seq)
);

-- Busca textual sobre resumos e mensagens
CREATE VIRTUAL TABLE search_idx USING fts5(
  session_id UNINDEXED, kind UNINDEXED, key UNINDEXED, body,
  tokenize = 'unicode61 remove_diacritics 2'
);

CREATE INDEX idx_sessions_project ON sessions(project_id, created_at);
CREATE INDEX idx_blocks_active ON blocks(session_id, active, start_seq);
CREATE INDEX idx_msgs_tool     ON messages(session_id, tool_call_id);
