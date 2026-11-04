# Accordion Context — Compactação de Contexto Incremental e Reversível em Go + SQLite

> *Fold and unfold your agent's context — infinite sessions, finite window.*

| Item | Nome |
|---|---|
| Projeto | **Accordion Context** |
| Repositório | `accordion-context` |
| Módulo Go | `github.com/fsvxavier/accordion-context` |
| Comando | `acd-ctx` |
| Pasta local do projeto | `.acd-ctx/` |
| Banco SQLite | `acd-ctx.db` |
| Variáveis de ambiente | `ACD_CTX_*` (hífen não é permitido em nomes de variáveis) |
| Cabeçalhos HTTP | `X-Acd-Ctx-*` |
| Endpoints internos | `/__acd-ctx/*` |
| Métricas | `acd_ctx_*` |

> Reimplementação do modelo de compactação do projeto [billion-context](https://github.com/ranxianglei/billion-context) (kernel `acp-kernel`), em **Go** com **SQLite** como fonte de verdade, corrigindo os pontos críticos identificados na análise do projeto original.

**Status:** proposta de design · **Versão:** 0.1 · **Data:** 2026-11-03

---

## Sumário

1. [Objetivo e escopo](#1-objetivo-e-escopo)
2. [Resumo do modelo original](#2-resumo-do-modelo-original)
3. [Pontos críticos e como este design os corrige](#3-pontos-críticos-e-como-este-design-os-corrige)
4. [Arquitetura](#4-arquitetura)
5. [Modelo de dados (SQLite)](#5-modelo-de-dados-sqlite)
6. [Tipos de domínio em Go](#6-tipos-de-domínio-em-go)
7. [Pipeline por turno](#7-pipeline-por-turno)
8. [Gatilho de compressão (especificação única)](#8-gatilho-de-compressão-especificação-única)
9. [Modelo de custo de cache e agendamento](#9-modelo-de-custo-de-cache-e-agendamento)
10. [Aplicação da compressão (transação)](#10-aplicação-da-compressão-transação)
11. [Renderização da janela e enquadramento dos resumos](#11-renderização-da-janela-e-enquadramento-dos-resumos)
12. [Tiers (destilação hierárquica)](#12-tiers-destilação-hierárquica)
13. [Recuperação: decompress, retrieve e recall assistido](#13-recuperação-decompress-retrieve-e-recall-assistido)
14. [Saídas de ferramenta grandes (CCR + crush)](#14-saídas-de-ferramenta-grandes-ccr--crush)
15. [Contagem de tokens calibrada](#15-contagem-de-tokens-calibrada)
16. [Ferramentas expostas ao modelo](#16-ferramentas-expostas-ao-modelo)
17. [Invariantes e estratégia de testes](#17-invariantes-e-estratégia-de-testes)
18. [Configuração e escopo por projeto](#18-configuração-e-escopo-por-projeto)
19. [Modelos de integração](#19-modelos-de-integração)
20. [Observabilidade](#20-observabilidade)
21. [Plano de implementação](#21-plano-de-implementação)
22. [Riscos e limites conhecidos](#22-riscos-e-limites-conhecidos)

---

## 1. Objetivo e escopo

Permitir que um agente de código (Claude Code, Codex, OpenCode etc.) rode sessões de **dias ou semanas** dentro de uma janela de contexto limitada (100K–200K tokens), sem perder o histórico de forma irreversível.

**Princípios mantidos do original:**

- O **modelo da sessão escreve os resumos**; o sistema decide *quando* pedir, *o que* oferecer e *como* reconstruir a janela.
- A compressão é **incremental**: só se dobra o conteúdo novo e já consumido, nunca o histórico inteiro.
- A compressão é **reversível**: o original nunca é apagado, apenas deixa de ser renderizado.
- Os resumos são **hierárquicos** (tiers 1→2→3).

**O que muda:**

- Go no lugar de TypeScript; **kernel puro** sem I/O, como no original.
- **SQLite como fonte de verdade**, com log de eventos append-only, invariantes em constraints SQL e replay determinístico.
- Correções explícitas para os seis pontos críticos (seção 3).

**Fora de escopo:** compressão treinada (fine-tuning), memória vetorial externa, tarefas que exigem histórico literal completo (auditoria, forense) — nestes casos, use `decompress` explícito ou desligue a compressão.

---

## 2. Resumo do modelo original

| Conceito | Como funciona no billion-context |
|---|---|
| Proxy | Fica entre o agente e a API do modelo; reescreve requisições Anthropic/OpenAI/Responses |
| Kernel | `processTurn(state, messages, config) → (state', messages', nudge)`, função pura |
| Referências | Cada mensagem recebe um ID estável `mNNNNN` para endereçamento |
| Nudge | Pedido injetado ao modelo quando o contexto cresceu ≥ 22,5K desde a última referência e há ≥ 50K comprimíveis |
| `compress` | O modelo envia faixas `startRef..endRef` + resumo; o kernel cria um bloco e esconde as mensagens cobertas |
| Blocos | `tier ∈ {1,2,3}`, linhagem (`effectiveMessageIds`, `directBlockIds`), `active` |
| Tiers | T2 resume blocos T1; T3 resume blocos T2 |
| Proteções | 1ª mensagem do usuário fixada; zona recente (3 mensagens / 5K tokens); pares tool-call/result atômicos |
| CCR / crush / absorb | Tratamento de saídas de ferramenta gigantes (armazenar, encolher deterministicamente, destilar) |
| Recuperação | `decompress`, `decompress-to-file`, `search_context`, `retrieve` |

---

## 3. Pontos críticos e como este design os corrige

| # | Problema no original | Correção neste design | Seção |
|---|---|---|---|
| P1 | **Paper e código divergem no gatilho**: o paper diz "uso ≥ 45% **e** crescimento"; o código só aplica o piso de uso em caminhos especiais | Especificação única em tabela, implementada como função pura com **testes de tabela que são a especificação**; piso de uso explícito e configurável | §8, §17 |
| P2 | **"Cache nunca invalidado" é otimista**: inserir o resumo na posição da faixa reescreve o wire dali em diante | Modelo de custo explícito (benefício × custo de cache-miss), **aplicação oportunista em cache frio** e medição real via ledger de cache | §9 |
| P3 | **Modelos não usam `decompress` espontaneamente** | **Recall assistido**: detecção determinística de re-leituras e de referências a conteúdo dobrado, com dica automática apontando o bloco | §13 |
| P4 | **Resumos lidos como instruções novas** em modelos de atenção fraca | Envelope histórico explícito, regra de redação no passado, **ledger de intenções do usuário** com status, e reposicionamento do último turno do usuário depois dos resumos (ligado por padrão) | §11 |
| P5 | **Compressões muito frequentes** em sessões com muita leitura (passo fixo de 50K) | CCR ligado por padrão (saídas grandes não inflam o crescimento), **passo adaptativo** pela densidade de leitura e histerese | §8.4, §14 |
| P6 | **Complexidade alta e difícil de auditar** (~113K linhas, correções numeradas por issue) | SQLite como fonte única, log de eventos, invariantes nomeadas verificadas em SQL, replay determinístico e testes de propriedade | §5, §17 |

Correções adicionais:

- **Marcadores falsos de compressão** (o modelo escreve "📦 Compressed…" sem chamar a ferramenta): a confirmação vem de `compression_events` no banco, nunca do texto; marcadores no texto do modelo são removidos.
- **Estimativa de tokens com deriva**: calibração contínua com o `usage` devolvido pelo provedor (§15).

---

## 4. Arquitetura

```
┌──────────────────────────┐
│ Agente (Claude Code,     │
│ Codex, OpenCode, ...)    │
└────────────┬─────────────┘
             │ base URL → proxy
┌────────────▼─────────────────────────────────────────────┐
│ cmd/acd-ctx                                              │
│  ┌──────────────┐   ┌────────────────┐   ┌─────────────┐  │
│  │ wire/        │──▶│ session/       │──▶│ kernel/     │  │
│  │ anthropic    │   │ (orquestra o   │   │ (puro, sem  │  │
│  │ openai       │◀──│  turno, tools, │◀──│  I/O)       │  │
│  │ responses    │   │  loop compress)│   └─────────────┘  │
│  └──────────────┘   └───────┬────────┘                    │
│                             │                             │
│                     ┌───────▼────────┐                    │
│                     │ store/ (SQLite)│  WAL, FTS5         │
│                     └────────────────┘                    │
└────────────┬──────────────────────────────────────────────┘
             │
┌────────────▼─────────────┐
│ API do modelo            │
└──────────────────────────┘
```

### Layout de pacotes

```
accordion-context/
├── cmd/acd-ctx/            # main: HTTP server, config, graceful shutdown
├── internal/
│   ├── kernel/              # PURO: tipos, pipeline, decide, apply, render
│   │   ├── types.go
│   │   ├── pipeline.go
│   │   ├── trigger.go       # DecideNudge (§8)
│   │   ├── cachecost.go     # modelo de custo (§9)
│   │   ├── apply.go         # validação + plano de compressão (§10)
│   │   ├── render.go        # janela visível (§11)
│   │   ├── recall.go        # recall assistido (§13)
│   │   └── tiers.go
│   ├── store/               # SQLite: schema, migrações, repositórios
│   │   ├── schema.sql
│   │   ├── store.go
│   │   └── invariants.go
│   ├── wire/                # adaptadores de protocolo
│   │   ├── anthropic/
│   │   ├── openai/
│   │   ├── responses/
│   │   └── google/
│   ├── plugin/              # protocolo de plugin HTTP + servidor MCP (§19)
│   ├── launcher/            # launchers por cliente + MITM (§19)
│   ├── session/             # orquestração do turno e do loop de compress
│   ├── project/             # resolução do projeto, escopos de config (§18)
│   ├── crush/               # encolhimento determinístico (json/log/code)
│   └── tokens/              # tokenizador + calibração
└── testdata/traces/         # traces gravados para replay
```

**Regra de dependência:** `kernel` não importa nada de `store`, `wire` ou `session`. O kernel recebe um *snapshot* e devolve um *plano*; quem grava é o `session` via `store`.

---

## 5. Modelo de dados (SQLite)

### 5.1 Configuração do banco

- Driver: `modernc.org/sqlite` (Go puro, sem CGO, FTS5 incluído). Alternativa: `github.com/mattn/go-sqlite3` com build tag `sqlite_fts5`.
- Pragmas na abertura:

```sql
PRAGMA journal_mode = WAL;        -- leitores não bloqueiam o escritor
PRAGMA synchronous = NORMAL;      -- seguro com WAL
PRAGMA foreign_keys = ON;
PRAGMA busy_timeout = 5000;
```

- Por padrão, um banco global com os projetos separados por `project_id`; opcionalmente, um banco próprio dentro da pasta do projeto (§18.3). Um único escritor por sessão (mutex por sessão no processo).

### 5.2 Schema

```sql
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
```

### 5.3 Por que SQLite

- **Fonte única de verdade**: o original mantém o estado em JSON e várias cópias (proxy, agente, arquivos de estado), o que gerou dezenas de correções de sincronização e de "single-writer". Aqui, toda mudança é uma transação.
- **Reversibilidade garantida**: `messages` e `contents` são append-only; nenhuma operação de compressão faz `DELETE`.
- **Auditoria**: `events` permite reconstruir qualquer estado e explicar qualquer decisão.
- **Busca**: FTS5 resolve `search_context` sem dependência externa.
- **Invariantes no banco**: `CHECK`, `FOREIGN KEY` e consultas de verificação (§17) pegam corrupção cedo.

---

## 6. Tipos de domínio em Go

```go
package kernel

type Role string
type Kind string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"

	KindText       Kind = "text"
	KindToolCall   Kind = "tool_call"
	KindToolResult Kind = "tool_result"
	KindReasoning  Kind = "reasoning"
)

// Message é a visão normalizada, independente de protocolo.
type Message struct {
	Seq        int64
	Ref        string // "m00042"
	Role       Role
	Kind       Kind
	ToolName   string
	ToolCallID string
	Text       string
	Tokens     int
	CCRStored  bool
}

type Block struct {
	ID        string // "b17"
	Tier      int    // 1..3
	Topic     string
	Summary   string
	StartSeq  int64
	EndSeq    int64
	SrcTokens int
	SumTokens int
	Active    bool
	Sources   []string // blocos consumidos (T2/T3)
}

type Intent struct {
	ID        int64
	OriginSeq int64
	Text      string
	Status    string // open | done | superseded
}

type TriggerState struct {
	LowWater     int  // vale mais recente de tokens
	NudgePending bool
	NudgeShownAt int
	LastShownT2  int
	LastShownT3  int
	Refusals     int
}

// Snapshot é tudo que o kernel precisa para decidir um turno.
// O kernel NÃO lê nem escreve no banco.
type Snapshot struct {
	SessionID    string
	ContextLimit int
	Messages     []Message // ledger completo (ou janela relevante)
	Blocks       []Block
	Intents      []Intent
	Trigger      TriggerState
	TokenCount   int       // tokens do contexto visível atual (calibrado, §15)
	IdleSeconds  int       // tempo desde a última chamada (§9.3)
	Cache        CacheStats
}

// TurnPlan é o que o kernel devolve; o session aplica no banco.
type TurnPlan struct {
	Visible   []Message     // janela renderizada para o modelo
	Nudge     *Nudge        // nil = não injetar
	Hints     []RecallHint  // dicas de recuperação (§13)
	Trigger   TriggerState  // novo estado do gatilho
	Events    []Event       // eventos a anexar no log
}
```

---

## 7. Pipeline por turno

Cada nó é uma função pura `func(io NodeIO, ctx Ctx) NodeIO`, testada isoladamente.

```go
var pipeline = []Node{
	assignRefs,        // garante refs estáveis para mensagens novas
	ccrPlaceholders,   // saídas gigantes já viram placeholder (§14)
	syncBlocks,        // projeta blocos ativos sobre o ledger
	computeProtected,  // 1ª msg do usuário, zona recente, pares atômicos
	recommend,         // faixas comprimíveis validadas (§10.2)
	decideTrigger,     // §8
	cacheGate,         // §9 — pode adiar um nudge válido
	recallAssist,      // §13
	render,            // §11
	emergencyTruncate, // última válvula, ≥ 95%
}

func ProcessTurn(s Snapshot, cfg Config) TurnPlan {
	io := NodeIO{Snapshot: s}
	ctx := Ctx{Cfg: cfg}
	for _, n := range pipeline {
		io = n.Run(io, ctx)
	}
	return io.Plan()
}
```

Diferenças em relação ao original:

- `cacheGate` e `recallAssist` são nós novos (P2, P3).
- A ordem é fixa e documentada aqui; qualquer mudança exige atualizar este documento e os testes de tabela (§17).

---

## 8. Gatilho de compressão (especificação única)

**Correção de P1.** Esta tabela é a especificação. O código em `trigger.go` a implementa literalmente, e `trigger_test.go` contém um caso para cada linha.

### 8.1 Variáveis

| Símbolo | Definição | Padrão |
|---|---|---|
| `L` | limite da janela do modelo | do modelo |
| `U` | uso = `TokenCount / L` | — |
| `S` | passo de crescimento (adaptativo, §8.4) | 50K (faixa 30K–100K) |
| `F` | piso de crescimento = `max(20K, 0,45 × S)` | 22,5K |
| `G` | crescimento = `TokenCount − LowWater` | — |
| `P1` | tokens comprimíveis efetivos em T1 (faixas validadas) | — |
| `UFloor` | piso de uso para o modo normal | **0,30** |
| `UHigh` | faixa de pressão | 0,75 |
| `UEmerg` | emergência | 0,95 |
| `Bmin` | ganho mínimo em pressão = `max(5K, 1% de L)` | — |

### 8.2 Regra do vale (low-water mark)

`LowWater` só desce: `LowWater = min(LowWater, TokenCount)` em todo turno, e é zerado para `TokenCount` após uma compressão bem-sucedida. Assim, `G` sempre mede a subida desde o vale mais recente (comportamento correto do original, mantido).

### 8.3 Tabela de decisão

Avaliada **de cima para baixo**; a primeira linha que casa decide.

| # | Condição | Ação | Voz |
|---|---|---|---|
| 1 | `U ≥ UEmerg` e `max(P1,P2,P3) ≥ Bmin` | Nudge no tier com maior pendente | emergência |
| 2 | `U ≥ UEmerg` e nada comprimível | Sem nudge; `emergencyTruncate` atua | — |
| 3 | `U ≥ UHigh` e `max(P1,P2,P3) ≥ Bmin` | Nudge no tier com maior pendente | firme |
| 4 | `U < UFloor` | Sem nudge | — |
| 5 | `G < F` | Sem nudge | — |
| 6 | `P1 ≥ S` | Nudge T1 | suave |
| 7 | `P2 ≥ 1,5·S` e `P2 > P1` e cadência T2 cumprida | Nudge T2 | suave |
| 8 | `P3 ≥ 1,5·S` e `P3 > P2` e `P3 > P1` e cadência T3 cumprida | Nudge T3 | suave |
| 9 | caso contrário | Sem nudge | — |

**Sobre o piso de uso (linha 4):** é uma decisão explícita. Abaixo de 30% da janela não há benefício de atenção nem de custo que compense o cache-miss (§9). Quem quiser o comportamento do código original (sem piso) configura `UFloor = 0`. O que não pode acontecer é documentação e código discordarem.

**Recusa do modelo:** se o nudge foi mostrado e o modelo não chamou `compress`, o próximo nudge exige `G ≥ F/2` a partir de `NudgeShownAt` (metade do piso, como no original). Após 3 recusas consecutivas, o nudge é suspenso até `U ≥ UHigh`. Recusas são registradas em `events` como `refuse`.

### 8.4 Passo adaptativo (correção de P5)

Sessões com muita leitura de logs ou arquivos atingem `S` em poucas chamadas. Em vez de um `S` fixo:

```go
// readDensity = fração do crescimento recente que veio de tool_result,
// medida nas últimas N chamadas (exclui conteúdo já armazenado via CCR).
func adaptiveStep(cfg Config, readDensity float64) int {
	s := float64(cfg.StepBase) * (1 + cfg.StepReadGain*readDensity)
	return clamp(int(s), cfg.StepMin, cfg.StepMax) // 30K..100K
}
```

Com `StepReadGain = 1,0`, uma sessão 100% de leitura usa passo de 100K; uma sessão de diálogo puro, 50K. Combinado com o CCR ligado por padrão (§14), saídas gigantes deixam de inflar `G`.

### 8.5 Implementação

```go
func DecideNudge(in TriggerInput, cfg Config) Decision {
	U := float64(in.TokenCount) / float64(in.ContextLimit)
	S := adaptiveStep(cfg, in.ReadDensity)
	F := max(cfg.MinGrowthFloor, int(0.45*float64(S)))
	G := in.TokenCount - in.Trigger.LowWater
	Bmin := max(5000, in.ContextLimit/100)
	best, bestP := argmaxTier(in.Pending)

	switch {
	case U >= cfg.UEmerg && bestP >= Bmin:
		return nudge(best, VoiceEmergency, "row1")
	case U >= cfg.UEmerg:
		return none("row2: emergency, nothing compressible")
	case U >= cfg.UHigh && bestP >= Bmin:
		return nudge(best, VoiceFirm, "row3")
	case U < cfg.UFloor:
		return none("row4: below usage floor")
	}

	need := F
	if in.Trigger.NudgePending {
		need = F / 2
	}
	if in.Trigger.Refusals >= 3 {
		return none("refusals suspended until high band")
	}
	if G < need {
		return none(fmt.Sprintf("row5: growth %d < floor %d", G, need))
	}

	p1, p2, p3 := in.Pending[1], in.Pending[2], in.Pending[3]
	t23 := int(1.5 * float64(S))
	switch {
	case p1 >= S:
		return nudge(1, VoiceGentle, "row6")
	case cfg.TiersEnabled && p2 >= t23 && p2 > p1 &&
		in.TokenCount-in.Trigger.LastShownT2 >= F:
		return nudge(2, VoiceGentle, "row7")
	case cfg.TiersEnabled && p3 >= t23 && p3 > p2 && p3 > p1 &&
		in.TokenCount-in.Trigger.LastShownT3 >= F:
		return nudge(3, VoiceGentle, "row8")
	}
	return none("row9")
}
```

Toda decisão carrega o rótulo da linha (`row1`…`row9`), que vai para `events`. Isso torna qualquer comportamento explicável a partir do log.

---

## 9. Modelo de custo de cache e agendamento

**Correção de P2.** O original afirma que a compressão não invalida o prefixo de cache. Na prática, o resumo é inserido na posição da faixa dobrada, então **tudo depois desse ponto (o anchor) muda no wire** e é relido sem cache na chamada seguinte. O custo é pequeno, mas real. Este design o modela e o minimiza.

### 9.1 Custo e benefício

Sejam:

- `R` = tokens recuperados = `src_tokens − sum_tokens`
- `T` = tokens após o anchor na nova janela (a "cauda" que perde cache)
- `p_u`, `p_c` = preço por token sem cache e com cache
- `K` = número esperado de chamadas restantes na sessão (estimado pelo histórico: mediana de chamadas entre folds × fator, limitado)

```
custo_unico   = T × (p_u − p_c)            # releitura da cauda sem cache
beneficio     = R × p_c × K                # R tokens a menos em cada chamada futura
compensa      = beneficio ≥ custo_unico × margem   (margem padrão 1,5)
```

Na prática, como o anchor costuma ficar logo após os resumos anteriores, `T` ≈ tamanho da zona bruta recente. Com `R = 50K`, `T = 60K`, `p_c = 0,1·p_u` e `K = 20`, o benefício é `50K × 0,1 × 20 = 100K` contra custo `60K × 0,9 = 54K` → compensa. Com `K = 5`, não compensa e o nudge é adiado (exceto nas linhas 1–3 da tabela, em que a pressão manda).

### 9.2 Escolha do anchor

O kernel só **recomenda** faixas que começam na fronteira do fold (a primeira mensagem bruta após o último bloco ativo). Isso garante que:

- a parte com cache antes do anchor (sistema + resumos) é preservada;
- não surgem "buracos" no meio do histórico que multiplicariam as invalidações.

O modelo ainda pode pedir outra faixa; o kernel aceita, mas registra o custo maior em `events`.

### 9.3 Aplicação oportunista em cache frio

Os provedores expiram o cache após alguns minutos de inatividade. Se a última chamada foi há mais tempo do que o TTL do provedor (`IdleSeconds > cfg.CacheTTL`), **o cache já está frio**: compressão nesse momento custa zero em cache.

Regra do nó `cacheGate`:

1. Se a decisão do §8 é "nudge" nas linhas 6–8 e o custo não compensa (§9.1) → adia e marca `deferred`.
2. Se há `deferred` e `IdleSeconds > CacheTTL` → injeta o nudge agora.
3. Linhas 1–3 (pressão) nunca são adiadas.

### 9.4 Medição real

O `cache_ledger` grava, por chamada, `input_tokens` e `cached_tokens` reportados pelo provedor. O painel de status mostra:

- taxa de leitura de cache global e por fase (ativa vs. após idle);
- **custo real medido de cada fold** (tokens não cacheados na 1ª chamada após o fold, menos a linha de base);
- comparação com a previsão de §9.1, para calibrar `margem` e `K`.

Assim, a afirmação "a compressão custa ≤ 2% de cache" vira um número medido por sessão, e não uma premissa.

---

## 10. Aplicação da compressão (transação)

### 10.1 Fluxo

```
modelo chama compress({ranges:[{startRef,endRef,summary,topic}]})
        │
        ▼
kernel.ValidateCompress(snapshot, call)   ← puro, devolve CompressPlan ou erros
        │
        ▼
store.ApplyCompressPlan(tx, plan)          ← uma transação SQLite
        │
        ▼
events += 'compress' ; trigger_state reset ; FTS5 atualizado
        │
        ▼
resposta à ferramenta: confirmação com IDs dos blocos (vinda do banco)
```

### 10.2 Validação (pura)

Uma faixa é rejeitada se:

- `startRef`/`endRef` não existem ou estão invertidas;
- toca a **zona protegida** (1ª mensagem do usuário, últimas `PreserveRecentMessages` mensagens ou `PreserveRecentTokens` tokens, chamadas de compressão, ferramentas em `ProtectedTools`);
- **divide um par** tool-call/tool-result (o par é atômico);
- cobre menos de `MinCompressRange` tokens (5K);
- o resumo tem menos de `MinSummaryChars` ou não é menor que a fonte (`sum_tokens < src_tokens`);
- o resumo contém um **marcador de confirmação forjado** (ex.: `📦 [acd-ctx] Compressed`) — removido antes de validar;
- falha na verificação de enquadramento (§11.2), quando habilitada.

A recomendação (`recommend`) usa **a mesma função de validação**, então o kernel nunca oferece uma faixa que depois rejeitaria (correção de um loop "anuncia/rejeita" que o original precisou tratar).

### 10.3 Transação

```go
func (s *Store) ApplyCompressPlan(ctx context.Context, sid string, p kernel.CompressPlan) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, b := range p.NewBlocks {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO blocks(session_id, block_id, tier, topic, summary,
			                   start_seq, end_seq, src_tokens, sum_tokens,
			                   active, created_at)
			VALUES (?,?,?,?,?,?,?,?,?,1,?)`,
			sid, b.ID, b.Tier, b.Topic, b.Summary,
			b.StartSeq, b.EndSeq, b.SrcTokens, b.SumTokens, p.Now); err != nil {
			return fmt.Errorf("insert block %s: %w", b.ID, err)
		}
		if b.Tier == 1 {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO block_messages(session_id, block_id, seq)
				SELECT session_id, ?, seq FROM messages
				WHERE session_id = ? AND seq BETWEEN ? AND ?`,
				b.ID, sid, b.StartSeq, b.EndSeq); err != nil {
				return err
			}
		}
		for _, src := range b.Sources {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO block_sources(session_id, block_id, source_id)
				VALUES (?,?,?)`, sid, b.ID, src); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO search_idx(session_id, kind, key, body)
			VALUES (?, 'block', ?, ?)`, sid, b.ID, b.Topic+"\n"+b.Summary); err != nil {
			return err
		}
	}

	for _, id := range p.Deactivate {
		res, err := tx.ExecContext(ctx, `
			UPDATE blocks SET active = 0, parent_block = ?
			WHERE session_id = ? AND block_id = ? AND active = 1`,
			p.ParentOf[id], sid, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return fmt.Errorf("block %s not active (concurrent change?)", id)
		}
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE trigger_state
		SET low_water_tokens = ?, nudge_pending = 0, nudge_shown_at = 0, refusals = 0
		WHERE session_id = ?`, p.TokensAfter, sid); err != nil {
		return err
	}

	if err := appendEvent(ctx, tx, sid, "compress", p.EventPayload()); err != nil {
		return err
	}
	if err := checkInvariantsTx(ctx, tx, sid); err != nil { // §17
		return fmt.Errorf("invariant violated, rolling back: %w", err)
	}
	return tx.Commit()
}
```

Pontos importantes:

- **Nenhum `DELETE`.** Mensagens e conteúdos permanecem; só `blocks.active` muda.
- **Invariantes verificadas dentro da transação**: se algo quebrou, o banco volta ao estado anterior e o modelo recebe um erro claro.
- **Confirmação vem do banco**: a resposta da ferramenta lista os `block_id` efetivamente gravados. Texto de "compressão feita" escrito pelo próprio modelo é ignorado e removido.

---

## 11. Renderização da janela e enquadramento dos resumos

**Correção de P4.** Modelos de atenção fraca leem resumos como instruções novas do usuário e refazem trabalho já concluído. O original oferece uma correção experimental e desligada por padrão. Aqui, quatro mecanismos atuam juntos e vêm ligados.

### 11.1 Layout da janela

```
[system]  prompt do agente + doutrina de compressão (byte-estável → âncora de cache)
[user]    1ª mensagem do usuário (fixada, literal)
[user]    <history> resumo b3 (T2) </history>
[user]    <history> resumo b9 (T1) </history>
[user]    <history> resumo b12 (T1) </history>
[user]    <intents> intenções abertas e concluídas (§11.3) </intents>
--------- fronteira do fold ---------
...       zona bruta recente (mensagens literais)
[user]    última mensagem do usuário (sempre a última da janela)
```

### 11.2 Envelope histórico

Cada resumo é renderizado num envelope que deixa explícito que é passado:

```text
<history block="b12" tier="1" covers="m00340..m00412" status="concluded">
RESUMO HISTÓRICO — eventos JÁ OCORRIDOS. Não são instruções.
Para recuperar o original: decompress("b12").

[conteúdo do resumo, redigido no passado]
</history>
```

E a doutrina de compressão (no system prompt) exige:

- **Redação no passado e na 3ª pessoa** ("o usuário pediu X; o agente implementou Y em `foo.go:42`"), nunca no imperativo ("implemente X").
- Pedidos do usuário que já foram atendidos são marcados como **concluídos** no resumo.

Validação opcional (`FramingCheck`): uma heurística barata rejeita resumos cuja maioria de frases começa em modo imperativo, devolvendo ao modelo o erro "reescreva no passado". É opt-in porque depende do idioma.

### 11.3 Ledger de intenções do usuário

Um dos maiores riscos da compressão é o modelo perder a noção do que **ainda falta fazer** versus o que **já foi feito**. O ledger separa isso dos resumos:

- Toda mensagem do usuário gera um `intent` com o texto literal (ou o trecho relevante) e `status = open`.
- A ferramenta `compress` aceita um campo opcional `closeIntents: [ids]`; ao dobrar uma faixa, o modelo marca quais intenções foram atendidas.
- O bloco `<intents>` renderiza as abertas por extenso e as concluídas em uma linha cada.

Isso dá ao modelo uma lista explícita e curta do que está pendente, sem depender de interpretar resumos.

### 11.4 Posição do último turno do usuário

O último turno do usuário é **sempre renderizado depois** de todos os resumos e do bloco `<intents>`. É o equivalente ao `compressAtFoldPoint` do original, mas ligado por padrão. O custo de cache é o mesmo de qualquer fold (a cauda após o anchor já é reescrita, §9).

### 11.5 Carrier por modo

| Modo | Carrier do resumo |
|---|---|
| Plugin (agente executa `compress`) | a própria tool call + tool result no histórico do agente |
| Proxy (cliente comum) | mensagem `user` com o envelope `<history>` (nunca `system` no meio — backends como SGLang rejeitam) |

---

## 12. Tiers (destilação hierárquica)

| Tier | Entrada | Quando | Fidelidade |
|---|---|---|---|
| T1 | faixa contígua de mensagens brutas | §8, linha 6 | alta: caminhos, linhas, erros literais, decisões com motivo |
| T2 | blocos T1 ativos contíguos | §8, linha 7 | média: mantém decisões, artefatos, conclusões |
| T3 | blocos T2 ativos contíguos | §8, linha 8 | baixa: objetivo, decisões-chave, mapa de onde está o quê |

Regras:

- Um bloco de tier superior **só consome blocos contíguos** (mesma justificativa de cache do §9.2).
- A linhagem é transitiva: `effective_seqs(b)` = união das mensagens de todos os T1 alcançáveis por `block_sources`. Calculada com CTE recursiva:

```sql
WITH RECURSIVE lineage(block_id) AS (
  SELECT ?2
  UNION ALL
  SELECT bs.source_id FROM block_sources bs
  JOIN lineage l ON bs.block_id = l.block_id
  WHERE bs.session_id = ?1
)
SELECT DISTINCT bm.seq
FROM lineage l
JOIN block_messages bm ON bm.block_id = l.block_id AND bm.session_id = ?1
ORDER BY bm.seq;
```

- Sem coleta de lixo por idade: blocos nunca são desativados por tempo (o original removeu isso porque causava perda de memória).

---

## 13. Recuperação: decompress, retrieve e recall assistido

### 13.1 Operações

| Operação | Efeito | Cache |
|---|---|---|
| `decompress(b, mode="context")` | reativa as fontes do bloco e desativa o bloco | invalida a partir do anchor |
| `decompress(b, mode="file")` | escreve as mensagens originais num arquivo e devolve o caminho | preserva o cache |
| `decompress(b, mode="peek", query=...)` | devolve só os trechos originais que casam com a consulta | preserva o cache |
| `retrieve(ref)` | devolve o conteúdo original de uma mensagem CCR (§14) | preserva o cache |
| `search_context(q)` | FTS5 sobre resumos + mensagens visíveis | preserva o cache |

O modo `peek` é novo: devolve apenas as linhas relevantes do original (via FTS5 no conteúdo das mensagens do bloco), o que reduz o custo de recuperar um detalhe pontual.

### 13.2 Recall assistido (correção de P3)

O original admite que os modelos não chamam `decompress` sozinhos. Em vez de depender disso, o nó `recallAssist` detecta, de forma determinística, situações em que o modelo provavelmente precisa de algo que foi dobrado:

| Sinal | Detecção | Dica injetada |
|---|---|---|
| **Re-leitura** | o modelo chama `Read`/`cat`/`grep` em um caminho que aparece numa mensagem coberta por bloco ativo | "O arquivo `x.go` já foi lido em m00340 (bloco b12). Use `decompress("b12", mode="peek", query="x.go")` se precisar do conteúdo exato." |
| **Referência a ref dobrada** | o texto do modelo cita `m00340` coberta por bloco | "m00340 está no bloco b12; recupere com `decompress`." |
| **Erro repetido** | uma string de erro de um tool_result atual casa (FTS5) com um tool_result dobrado | "Este erro já ocorreu em m00377 (b12). O resumo registra a causa; detalhes via `decompress`." |
| **Pergunta sobre o passado** | a última mensagem do usuário contém termos que casam fortemente com um bloco e fracamente com a janela visível | lista dos 1–3 blocos mais relevantes com seus IDs |

Implementação: um índice por sessão `caminho → [seq]` e `assinatura de erro → [seq]`, mantido no banco, e consultas FTS5 limitadas a top-3. As dicas são curtas, saem em um único `<recall_hint>` no fim da janela e são **efêmeras** (não entram no histórico).

A eficácia é medida: `events` registra `recall_hint` e se o modelo chamou `decompress`/`retrieve` em seguida. Uma taxa de aceitação baixa para um tipo de sinal desliga aquele sinal automaticamente (limiar configurável).

---

## 14. Saídas de ferramenta grandes (CCR + crush)

**Contribui para a correção de P5.** No original, CCR e absorb ficam atrás de configuração. Aqui, o CCR é o caminho padrão para saídas grandes.

### 14.1 CCR (armazenamento sem perda)

Ao chegar um `tool_result` com mais de `CCRThresholdTokens` (padrão 4K):

1. O conteúdo integral vai para `contents` (deduplicado por hash).
2. A mensagem visível vira um **placeholder determinístico**:

```text
[stored m00451 · tool=bash · 38.2K tokens · exit=1]
cmd: go test ./...
head:
  --- FAIL: TestReconcile (0.02s)
      reconcile_test.go:88: expected 3 matches, got 2
  ...
tail:
  FAIL github.com/x/acerto/internal/match 0.412s
errors (2): reconcile_test.go:88, matcher.go:141
→ retrieve("m00451") para o conteúdo completo
```

3. O placeholder é gerado **uma única vez** e nunca muda (estável para o cache).

Como só o placeholder entra na janela, o crescimento `G` reflete o que o modelo realmente lê, e não o tamanho bruto das saídas. Isso reduz drasticamente a frequência de nudges em sessões com muita leitura.

### 14.2 crush (encolhimento determinístico)

Antes de decidir entre exibir inteiro ou virar placeholder, a saída passa por estratégias puras:

| Estratégia | Tipo | Garantia |
|---|---|---|
| `json-fold` | JSON com arrays de objetos | **sem perda** (campos constantes extraídos) |
| `log-select` | logs de build/teste | preserva **toda** linha ERROR/FAIL/panic literalmente; caso contrário, o resultado é descartado |
| `code-trim` | código-fonte | com perda (comentários/docstrings), marcada explicitamente |

Se após o crush a saída ficar abaixo do limiar, ela é exibida inteira; senão, vira placeholder CCR.

---

## 15. Contagem de tokens calibrada

O gatilho depende de `TokenCount`. Estimativas locais divergem dos tokenizadores reais (especialmente com CJK, código e JSON). O original tem várias correções de deriva.

Solução:

1. Estimativa local rápida (`tokens/estimate.go`): heurística por classe de caractere.
2. A cada resposta do provedor, grava-se `input_tokens` reportado e a estimativa (`cache_ledger.est_tokens`).
3. Um fator de calibração por sessão e modelo é mantido por média móvel exponencial:

```go
// alpha = 0.2; aplicado só se a amostra for "limpa" (sem imagens, sem falhas)
func (c *Calib) Update(estimated, reported int) {
	if estimated <= 0 || reported <= 0 {
		return
	}
	r := float64(reported) / float64(estimated)
	c.Factor = c.Factor*(1-c.Alpha) + r*c.Alpha
}

func (c *Calib) Tokens(estimated int) int { return int(float64(estimated) * c.Factor) }
```

4. `TokenCount` usado no gatilho = estimativa × fator. O fator e o erro médio aparecem no status.

---

## 16. Ferramentas expostas ao modelo

| Ferramenta | Parâmetros | Observações |
|---|---|---|
| `compress` | `ranges[{startRef,endRef,summary,topic}]`, `closeIntents?[]` | resposta lista os blocos gravados (do banco) |
| `decompress` | `blockId`, `mode: context\|file\|peek`, `query?` | `peek` devolve trechos relevantes |
| `retrieve` | `ref` | conteúdo original de mensagens CCR |
| `search_context` | `query`, `limit?` | FTS5 sobre resumos e mensagens |
| `ctx_status` | — | uso, composição, blocos ativos, faixas comprimíveis, intenções abertas, custo de cache medido |

Regras de exposição:

- **O nudge nunca informa a porcentagem de uso** ao modelo (mantido do original: modelos ancoram em "20% usado" e recusam). O `ctx_status` informa, porque é consultado sob demanda.
- O nudge lista as faixas recomendadas com tamanho real, número de mensagens e composição (tool/texto), além das intenções abertas.
- Chamadas de compressão consumidas são escondidas das janelas seguintes no modo proxy.

Exemplo de nudge (voz suave):

```text
<context_nudge>
Nudge de eficiência — não é alerta de estouro. Comprima o que a tarefa atual
já não precisa; recuse se estiver no meio de algo crítico.

Faixas recomendadas:
  m00340..m00412  51.3K tokens  73 msgs  (tool 88% / texto 12%)
  m00413..m00460  22.0K tokens  31 msgs  (tool 64% / texto 36%)

Intenções abertas: #7 "corrigir o matcher de taxas MDR", #9 "adicionar teste p/ chargeback"
Regras: redija no passado; preserve caminhos com linha, erros literais,
decisões com motivo e valores exatos; marque intenções concluídas em closeIntents.
</context_nudge>
```

---

## 17. Invariantes e estratégia de testes

**Correção de P6.** Em vez de dezenas de correções pontuais numeradas por issue, o comportamento é amarrado a invariantes nomeadas, verificadas em código e em SQL.

### 17.1 Invariantes

| ID | Invariante | Verificação |
|---|---|---|
| I1 | Nenhuma mensagem é coberta por mais de um bloco **ativo** | SQL (abaixo) |
| I2 | Blocos ativos não se sobrepõem em `[start_seq, end_seq]` | SQL |
| I3 | Nenhum bloco cobre um tool_call sem seu tool_result (ou vice-versa) | SQL |
| I4 | A 1ª mensagem do usuário está sempre na janela renderizada | teste de render |
| I5 | `sum_tokens < src_tokens` para todo bloco | `CHECK` no schema |
| I6 | Todo bloco T2/T3 tem fontes inativas cujo `parent_block` aponta para ele | SQL |
| I7 | Toda mensagem do ledger é recuperável (direta, via bloco ativo ou via CCR) | SQL |
| I8 | A janela renderizada nunca excede o limite após `emergencyTruncate` | teste de propriedade |
| I9 | O system prompt é byte-idêntico entre turnos sem mudança de config | teste de render |

Exemplos em SQL:

```sql
-- I1: mensagem coberta por mais de um bloco T1 ativo
SELECT bm.seq FROM block_messages bm
JOIN blocks b ON b.session_id = bm.session_id AND b.block_id = bm.block_id
WHERE bm.session_id = ?1 AND b.active = 1
GROUP BY bm.seq HAVING COUNT(*) > 1;

-- I2: sobreposição de intervalos ativos
SELECT a.block_id, b.block_id FROM blocks a JOIN blocks b
  ON a.session_id = b.session_id AND a.block_id < b.block_id
WHERE a.session_id = ?1 AND a.active = 1 AND b.active = 1
  AND a.start_seq <= b.end_seq AND b.start_seq <= a.end_seq;

-- I3: par tool dividido por um bloco ativo
SELECT m1.tool_call_id FROM messages m1
JOIN messages m2 ON m1.session_id = m2.session_id
  AND m1.tool_call_id = m2.tool_call_id AND m1.seq < m2.seq
JOIN blocks b ON b.session_id = m1.session_id AND b.active = 1
WHERE m1.session_id = ?1
  AND ((m1.seq BETWEEN b.start_seq AND b.end_seq)
       <> (m2.seq BETWEEN b.start_seq AND b.end_seq));
```

Todas as consultas devem retornar zero linhas; `checkInvariantsTx` roda I1–I3, I6 e I7 dentro de cada transação de compressão.

### 17.2 Camadas de teste

1. **Testes de tabela do gatilho** (`trigger_test.go`): um caso por linha da tabela do §8.3, mais as fronteiras (`G = F − 1`, `G = F`, `U = UFloor` etc.). **Esta é a especificação executável**; documentação e código não podem divergir sem quebrar o teste.
2. **Testes de propriedade** (`testing/quick` ou `pgregory.net/rapid`): sequências aleatórias de turnos, compressões válidas e inválidas, descompressões; após cada passo, I1–I9 valem.
3. **Fuzzing** (`go test -fuzz`) no parser de argumentos de `compress` (JSON malformado, cercas de markdown, arrays truncados, nomes de campo alternativos).
4. **Replay de traces** (`testdata/traces/*.jsonl`): sessões reais anonimizadas reproduzidas contra o kernel; o resultado (eventos e janela final) é comparado com um *golden file*. Qualquer mudança de comportamento aparece como diff revisável.
5. **Testes de integração com SQLite em memória** (`file::memory:?cache=shared`) para o `store`.

---

## 18. Configuração e escopo por projeto

O sistema funciona com **uma configuração geral** (global), mas **separa automaticamente os dados por projeto**, identificando o projeto pela pasta em que o agente está rodando. Opcionalmente, um projeto pode ter **configuração e dados locais** numa pasta própria (`.acd-ctx/`) dentro do projeto.

### 18.1 Três modos de uso

| Modo | Configuração | Dados (SQLite) | Quando usar |
|---|---|---|---|
| **Global** (padrão) | `~/.config/acd-ctx/config.yaml` | banco global, projetos separados por `project_id` | uso geral; nada a configurar por projeto |
| **Global com override por projeto** | config global + bloco `projects:` com ajustes por caminho | banco global | ajustar um projeto sem criar arquivos dentro dele |
| **Local no projeto** | `<raiz>/.acd-ctx/config.yaml` | opcionalmente `<raiz>/.acd-ctx/acd-ctx.db` | projeto com necessidades próprias, ou dados que devem ficar junto do projeto |

Locais globais (seguindo XDG no Linux, equivalentes no macOS e Windows):

| Item | Linux | macOS | Windows |
|---|---|---|---|
| Config | `~/.config/acd-ctx/config.yaml` | `~/Library/Application Support/acd-ctx/config.yaml` | `%APPDATA%\acd-ctx\config.yaml` |
| Banco | `~/.local/share/acd-ctx/acd-ctx.db` | `~/Library/Application Support/acd-ctx/acd-ctx.db` | `%LOCALAPPDATA%\acd-ctx\acd-ctx.db` |

Em Go, `os.UserConfigDir()` resolve o diretório de config de cada sistema; o diretório de dados segue `XDG_DATA_HOME` quando definido.

### 18.2 Separação automática por pasta

Mesmo sem nenhuma configuração por projeto, sessões de projetos diferentes **nunca se misturam**: cada sessão pertence a exatamente um projeto, e blocos, busca (FTS5), ledger de cache e estatísticas de recall são sempre filtrados por projeto.

**Detecção da raiz do projeto.** A partir do diretório de trabalho do agente, sobe-se na árvore de pastas e a primeira regra que casar define a raiz:

1. existe `.acd-ctx/` → raiz configurada explicitamente (modo local);
2. existe `.git` (pasta ou arquivo, cobrindo worktrees e submódulos) → raiz do repositório;
3. existe um marcador de projeto (`go.mod`, `package.json`, `pyproject.toml`, `composer.json`, `Cargo.toml`) → raiz desse módulo;
4. nenhum marcador → o próprio diretório de trabalho.

A lista de marcadores é configurável (`project.markers`).

**Como o proxy descobre o diretório de trabalho**, em ordem de prioridade:

| Fonte | Modo | Observação |
|---|---|---|
| Launcher (`acd-ctx claude`, `acd-ctx codex` …) | launcher | o processo conhece o `cwd` e o envia em todas as requisições |
| Cabeçalho `X-Acd-Ctx-Project-Dir` | proxy | para clientes que permitem cabeçalhos customizados |
| Prefixo de URL `/p/<project-id>/` | proxy | para clientes que só permitem trocar a base URL |
| Inferência pelo system prompt (ex.: linha "Working directory: …" que alguns agentes incluem) | proxy | heurística; registrada em `events` como `project_inferred` |
| Nenhuma das anteriores | proxy | projeto `default`, com aviso no status |

**Identificador estável.** O `project_id` é derivado assim:

- modo local: lido de `<raiz>/.acd-ctx/project-id` (gerado no `init`), então **sobrevive a mover ou renomear a pasta**;
- modo global: `sha256(caminho absoluto canônico)` truncado em 16 caracteres hexadecimais, com symlinks resolvidos (`filepath.EvalSymlinks`).

Se uma pasta for movida no modo global, o histórico pode ser reassociado com `acd-ctx project move <antigo> <novo>`, que atualiza `projects.root_path` sem alterar o `id`.

```go
package project

type Resolved struct {
	ID      string
	Root    string
	Storage string // "global" | "local"
	Source  string // "local-dir" | "git" | "marker" | "cwd" | "header" | "inferred" | "default"
}

func Resolve(cwd string, markers []string) (Resolved, error) {
	dir, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return Resolved{}, err
	}
	dir, _ = filepath.Abs(dir)

	for d := dir; ; d = filepath.Dir(d) {
		if fileExists(filepath.Join(d, ".acd-ctx")) {
			id, err := readOrCreateID(filepath.Join(d, ".acd-ctx", "project-id"))
			return Resolved{ID: id, Root: d, Storage: localOrGlobal(d), Source: "local-dir"}, err
		}
		if fileExists(filepath.Join(d, ".git")) {
			return globalProject(d, "git"), nil
		}
		for _, m := range markers {
			if fileExists(filepath.Join(d, m)) {
				return globalProject(d, "marker"), nil
			}
		}
		if parent := filepath.Dir(d); parent == d {
			break // chegou na raiz do sistema de arquivos
		}
	}
	return globalProject(dir, "cwd"), nil
}

func globalProject(root, source string) Resolved {
	sum := sha256.Sum256([]byte(root))
	return Resolved{ID: hex.EncodeToString(sum[:8]), Root: root, Storage: "global", Source: source}
}
```

### 18.3 Configuração local no projeto (`.acd-ctx/`)

Criada com `acd-ctx project init` na raiz do projeto:

```
meu-projeto/
├── .acd-ctx/
│   ├── config.yaml      # ajustes deste projeto (pode ir para o git)
│   ├── project-id       # identificador estável (pode ir para o git)
│   ├── acd-ctx.db       # só quando storage: local (NÃO versionar)
│   └── .gitignore       # gerado: ignora acd-ctx.db*, exports/
└── ...
```

Exemplo de `config.yaml` local:

```yaml
# .acd-ctx/config.yaml
storage: local            # global (padrão) | local — onde ficam os dados deste projeto
trigger:
  step_base: 80000        # projeto com muita leitura de logs (§8.4)
  u_floor: 0.25
compress:
  protected_tools: ["db_migrate"]
ccr:
  threshold_tokens: 3000
recall:
  assist: true
```

Com `storage: local`, o banco do projeto fica em `.acd-ctx/acd-ctx.db`. Isso é útil quando:

- os dados de contexto devem acompanhar o projeto (backup junto, exclusão junto);
- o projeto é sensível e não deve ter histórico misturado num banco global;
- o mesmo projeto é aberto em máquinas diferentes via disco compartilhado.

O `.gitignore` gerado impede que o banco (que contém o histórico literal das sessões, possivelmente com segredos lidos de arquivos) seja versionado por engano. `config.yaml` e `project-id` podem ser versionados para compartilhar ajustes com o time.

### 18.4 Override por projeto na config global

Para ajustar um projeto **sem criar arquivos dentro dele**:

```yaml
# ~/.config/acd-ctx/config.yaml
trigger:
  step_base: 50000
cache:
  ttl: 5m

projects:
  "/home/fabricio/src/acerto":
    trigger:
      step_base: 90000
  "/home/fabricio/src/legado-*":   # glob permitido
    storage: local
```

### 18.5 Precedência

Da maior para a menor prioridade; cada chave é resolvida individualmente (merge profundo):

1. flags de linha de comando
2. variáveis de ambiente `ACD_CTX_*`
3. `<raiz>/.acd-ctx/config.yaml` (local do projeto)
4. bloco `projects:` da config global que casar com a raiz
5. config global
6. padrões embutidos

`acd-ctx config show --effective` mostra a configuração final do projeto atual e, para cada chave, de qual camada ela veio.

### 18.6 Restrição de segurança na config local

Uma config local pode chegar ao disco junto com um repositório clonado de terceiros. Por isso, a camada local (item 3) **só pode alterar parâmetros de compressão**. Chaves sensíveis são **exclusivas da config global** e ignoradas, com aviso, se aparecerem localmente:

| Somente global | Motivo |
|---|---|
| `upstream.*` (URLs de API, proxies) | um repositório malicioso poderia redirecionar o tráfego e as credenciais |
| `credentials.*`, chaves de API | idem |
| `listen.*` (porta, interface) | evitar expor o proxy na rede |
| `storage.path` absoluto fora da pasta do projeto | evitar escrita em locais arbitrários |
| `tools.external_summary.*` | evitar envio do histórico a serviços externos |

### 18.7 Comandos

| Comando | Efeito |
|---|---|
| `acd-ctx project init [--local-storage]` | cria `.acd-ctx/` com `config.yaml`, `project-id` e `.gitignore` |
| `acd-ctx project show` | projeto detectado, raiz, fonte da detecção, modo de storage |
| `acd-ctx project list` | projetos conhecidos no banco global, com última atividade e tamanho |
| `acd-ctx project move <antigo> <novo>` | reassocia o histórico após mover a pasta (modo global) |
| `acd-ctx project migrate --to local\|global` | copia as sessões do projeto entre o banco global e o local, numa transação, e verifica as invariantes (§17) no destino |
| `acd-ctx project purge <id>` | apaga todos os dados de um projeto (pede confirmação) |
| `acd-ctx config show --effective` | configuração final com a origem de cada chave |

### 18.8 Estrutura da configuração

```go
type Config struct {
	// Gatilho (§8)
	UFloor         float64 // 0.30
	UHigh          float64 // 0.75
	UEmerg         float64 // 0.95
	StepBase       int     // 50_000
	StepMin        int     // 30_000
	StepMax        int     // 100_000
	StepReadGain   float64 // 1.0
	MinGrowthFloor int     // 20_000
	MaxRefusals    int     // 3
	TiersEnabled   bool    // true

	// Cache (§9)
	CacheTTL       time.Duration // 5 * time.Minute (ajustar por provedor)
	CacheMargin    float64       // 1.5
	PriceUncached  float64       // por 1M tokens
	PriceCached    float64

	// Proteção e validação (§10)
	PreserveRecentMessages int // 3
	PreserveRecentTokens   int // 5_000
	MinCompressRange       int // 5_000
	MinSummaryChars        int // 50
	MaxSummaryChars        int // 20_000
	ProtectedTools         []string
	FramingCheck           bool // false (depende do idioma)

	// Renderização (§11)
	UserTurnAfterSummaries bool // true
	IntentLedger           bool // true

	// CCR / crush (§14)
	CCRThresholdTokens int  // 4_000
	CrushEnabled       bool // true

	// Recall (§13)
	RecallAssist        bool    // true
	RecallMinAcceptRate float64 // 0.05 — abaixo disso, o sinal é desligado
}
```

Validação na inicialização (falha cedo): `UFloor ≤ UHigh ≤ UEmerg`, `StepMin ≤ StepBase ≤ StepMax`, preços não negativos. A validação roda sobre a configuração **já mesclada** de cada projeto (§18.5), então um override local inválido é rejeitado com a camada de origem indicada no erro.

---

## 19. Modelos de integração

O projeto original suporta vários modos de se acoplar a agentes, porque cada cliente oferece pontos de extensão diferentes: alguns permitem trocar a base URL, outros têm sistema de plugins, outros são binários fechados que só respeitam proxy HTTP. Este design mantém os mesmos modos, implementados sobre um único núcleo (`kernel` + `store`), e acrescenta o uso como biblioteca Go.

**Requisito:** a integração precisa funcionar com **qualquer IDE, modelo, harness e console**. Os modos abaixo são as peças; a §19.9 descreve como eles se combinam numa escada automática que garante essa cobertura, com perfis declarativos para clientes e modelos novos e os limites que nenhum mecanismo local consegue contornar.

### 19.1 Protocolos de API suportados

| Protocolo | Endpoints | Streaming | Pacote |
|---|---|---|---|
| Anthropic Messages | `POST /v1/messages` | SSE | `wire/anthropic` |
| OpenAI Chat Completions | `POST /v1/chat/completions` | SSE | `wire/openai` |
| OpenAI Responses | `POST /v1/responses` | SSE e WebSocket | `wire/responses` |
| Google Gemini | `:generateContent`, `:streamGenerateContent` | SSE | `wire/google` |

Cada adaptador converte a requisição para `[]kernel.Message`, aplica o `TurnPlan` e converte de volta, incluindo a injeção das ferramentas (§16) no formato do protocolo. Requisições a endpoints não reconhecidos passam **sem alteração** (fail-open) e geram um aviso no status.

### 19.2 Os seis modos

| # | Modo | Como o agente chega ao proxy | Quem executa `compress` | Carrier do resumo | Indicado para |
|---|---|---|---|---|---|
| 1 | **Base URL** | o usuário troca a base URL do cliente para `http://127.0.0.1:8787/ctx/<upstream>` | o proxy (loop server-side) | mensagem `user` com `<history>` | qualquer cliente com base URL configurável |
| 2 | **Launcher** | `acd-ctx <cliente> [args]` sobe ou reaproveita o proxy e inicia o cliente com variáveis de ambiente | o proxy | mensagem `user` com `<history>` | uso diário sem editar config do cliente |
| 3 | **MITM com certificado** | `HTTPS_PROXY` + CA própria confiada só pelo processo do cliente | o proxy | mensagem `user` com `<history>` | binários fechados sem base URL configurável |
| 4 | **Plugin nativo** | o plugin dentro do agente fala com o proxy pelo protocolo de plugin (§19.4) | o **agente** | a própria tool call no histórico do agente | agentes com sistema de plugins/extensões |
| 5 | **Companheiro MCP** | o cliente carrega um servidor MCP (stdio) do Accordion Context | o proxy (compress); o MCP expõe recuperação | mensagem `user` com `<history>` | clientes com suporte a MCP que já passam pelo proxy (modos 1–3) |
| 6 | **Biblioteca Go** | o agente importa `accordion-context/kernel` e `accordion-context/store` | o próprio agente | definido pelo agente | agentes escritos em Go |

O modo de execução é gravado em `sessions.mode` e é **fixo por sessão** (§19.6).

### 19.3 Detalhes de cada modo

**1. Base URL.** O prefixo carrega o upstream real, então um único proxy atende vários provedores:

```
http://127.0.0.1:8787/ctx/https://api.anthropic.com/v1/messages
http://127.0.0.1:8787/ctx/https://api.openai.com/v1/chat/completions
http://127.0.0.1:8787/p/<project-id>/ctx/https://api.anthropic.com/v1/messages   # com projeto explícito (§18.2)
```

O upstream extraído da URL passa por uma allowlist (`upstream.allow`, só na config global) para evitar que o proxy vire um relay aberto.

**2. Launcher.** O comando:

1. resolve o projeto a partir do `cwd` (§18.2);
2. reaproveita um proxy já rodando (verifica `GET /__acd-ctx/health` e a versão) ou sobe um novo numa porta livre;
3. lê da config do cliente a janela e o limite de saída do modelo, quando disponíveis;
4. inicia o cliente com as variáveis de ambiente adequadas, **sem alterar arquivos de configuração do usuário**;
5. repassa sinais e o código de saída do cliente.

Variáveis típicas por família de cliente (a confirmar na documentação de cada cliente, pois mudam entre versões):

| Família | Variável |
|---|---|
| Clientes Anthropic (ex.: Claude Code) | `ANTHROPIC_BASE_URL` |
| Clientes OpenAI | `OPENAI_BASE_URL` |
| Gemini CLI | `GOOGLE_GEMINI_BASE_URL` |
| Clientes sem variável de base URL | cai para o modo 3 |

Prioridade de injeção: variáveis de ambiente → flags de linha de comando do cliente → arquivo de config temporário (apagado ao sair), nessa ordem.

**3. MITM com certificado.** Para binários fechados:

- o Accordion Context gera uma CA própria por instalação (`~/.local/share/acd-ctx/ca/`), com chave privada legível só pelo usuário;
- o launcher exporta `HTTPS_PROXY` e a variável de confiança da linguagem do cliente (`SSL_CERT_FILE`, `NODE_EXTRA_CA_CERTS`, `REQUESTS_CA_BUNDLE`), **apenas para o processo do cliente**: a CA nunca é instalada no sistema;
- somente os domínios de API listados em `mitm.domains` são interceptados; todo o resto passa por túnel `CONNECT` sem inspeção;
- clientes com TLS que ignora CAs adicionais (ex.: algumas builds com rustls e raízes embutidas) não funcionam neste modo e são reportados no `acd-ctx doctor`.

**4. Plugin nativo.** O agente registra as ferramentas e executa `compress` localmente, chamando o proxy para as decisões. O resumo fica no histórico do próprio agente como tool call, o que é o carrier mais natural. O proxy continua responsável pelo nudge, porque a maioria dos agentes não tem um canal próprio para isso. Protocolo em §19.4.

**5. Companheiro MCP.** Para clientes que suportam MCP mas não têm plugins capazes de interceptar requisições, um servidor MCP via stdio (`acd-ctx mcp`) expõe `decompress`, `retrieve`, `search_context` e `ctx_status` como ferramentas nativas do cliente. A compressão em si continua no proxy (o tráfego precisa passar por um dos modos 1–3). O servidor MCP identifica a sessão pelo projeto (§18.2) e pela sessão mais recente ativa nele. Implementação com o SDK oficial de MCP para Go ou com `mark3labs/mcp-go`.

**6. Biblioteca Go.** Para agentes próprios em Go, sem proxy:

```go
import (
	"accordion-context/kernel"
	"accordion-context/store"
)

st, _ := store.Open(ctx, store.Options{Path: dbPath})
sess, _ := st.OpenSession(ctx, projectID, "claude-opus", 200_000)

snap, _ := st.Snapshot(ctx, sess.ID, history, tokenCount)
plan := kernel.ProcessTurn(snap, cfg)
_ = st.ApplyTurn(ctx, sess.ID, plan)          // grava trigger_state e events

// plan.Visible → mensagens para o modelo; plan.Nudge → texto a injetar
// quando o modelo chamar compress:
cp, err := kernel.ValidateCompress(snap, call, cfg)
if err == nil {
	_ = st.ApplyCompressPlan(ctx, sess.ID, cp)
}
```

### 19.4 Protocolo de plugin (HTTP loopback)

Inspirado no protocolo do original (`PLUGIN.md`), com autenticação explícita:

| Endpoint | Uso |
|---|---|
| `GET /__acd-ctx/plugin/manifest` | versão do protocolo, ferramentas e esquemas por protocolo de API, textos da doutrina |
| `POST /__acd-ctx/plugin/runtime-info` | o plugin informa modelo, janela, limite de saída, `cwd` e id de conversa; o proxy **prefere** esses valores aos da tabela interna |
| `POST /__acd-ctx/plugin/turn` | envia o histórico normalizado e recebe o `TurnPlan` (janela, nudge, dicas de recall) |
| `POST /__acd-ctx/plugin/tool` | executa `compress`, `decompress`, `retrieve`, `search_context`, `ctx_status` e devolve o resultado do banco |
| `GET /__acd-ctx/plugin/status?conversationId=…` | estado da sessão para painéis do cliente |
| `POST /__acd-ctx/plugin/compact` | compressão forçada sob comando do usuário |

Cabeçalhos por requisição de modelo, quando o plugin também roteia o tráfego pelo proxy:

| Cabeçalho | Conteúdo |
|---|---|
| `X-Acd-Ctx-Plugin` | nome e versão do plugin (ativa o modo plugin) |
| `X-Acd-Ctx-Conversation` | id de conversa do agente (vincula a sessão) |
| `X-Acd-Ctx-Project-Dir` | diretório de trabalho (§18.2) |
| `X-Acd-Ctx-Context-Window` / `X-Acd-Ctx-Max-Output` | limites do modelo em uso |
| `Authorization: Bearer <token>` | token de sessão do proxy (§19.7) |

Ordem de resolução da janela do modelo: negociação explícita na requisição (ex.: cabeçalho beta de contexto estendido) → cabeçalho do plugin → `runtime-info` → valor passado pelo launcher → config → tabela embutida.

### 19.5 Comparação dos carriers

| Aspecto | Proxy executa (modos 1, 2, 3, 5) | Agente executa (modos 4, 6) |
|---|---|---|
| Tool call de `compress` no histórico do cliente | não (tráfego efêmero do proxy) | sim |
| Resumo chega ao modelo como | mensagem `user` com `<history>` | resultado da tool call |
| Mensagens `system` no wire | exatamente uma (cliente + doutrina) | exatamente uma |
| Nudge | injetado pelo proxy | injetado pelo proxy (via `/turn`) |
| Compressão de emergência sem tool call | sim (preflight) | sim, como último recurso |
| Precisa de algo no cliente | só base URL, env ou proxy HTTP | plugin ou código |

### 19.6 Regras de convivência

- **Um modo por sessão.** `sessions.mode` é gravado na criação e não muda. Uma sessão em modo plugin nunca passa a modo proxy, porque os resumos do proxy não estão no histórico do agente e ficariam órfãos.
- **Um proxy, vários modos.** A mesma instância atende clientes em modos diferentes; o modo é decidido por requisição (presença de `X-Acd-Ctx-Plugin`) e fixado na sessão.
- **Sem compressão dupla.** Se um plugin nativo e uma extensão independente estiverem ativos no mesmo cliente, o plugin define `ACD_CTX_NATIVE=<host>` no processo e o proxy rejeita a segunda fonte de nudges para a mesma conversa, com aviso.
- **Reuso de instância.** Launchers e plugins procuram uma instância saudável do mesmo usuário antes de subir outra; portas automáticas usam uma faixa separada da porta manual (8787), e cada instância registra porta e PID num arquivo de lock por usuário.

### 19.7 Segurança da integração

- O proxy escuta **somente em loopback** por padrão; expor em outra interface exige `listen.*` na config global e token obrigatório.
- Os endpoints `/__acd-ctx/plugin/*` exigem o token gerado na subida da instância e gravado num arquivo com permissão `0600`.
- Credenciais do usuário (chaves de API) apenas **passam** pelo proxy; nunca são gravadas no SQLite nem em logs (mascaramento em `slog`).
- O MITM só intercepta domínios da allowlist; a CA é confiada apenas pelo processo lançado.
- **Fail-open com anúncio:** se o kernel ou o banco falharem num turno, a requisição segue para o upstream sem compressão, e o status da sessão e o log registram o modo degradado. O agente nunca fica parado por falha do Accordion Context.

### 19.8 Diagnóstico

`acd-ctx doctor [cliente]` verifica, para o cliente e o projeto atuais:

- qual modo de integração é possível e qual está ativo;
- se o tráfego realmente passa pelo proxy (requisição de teste e registro em `cache_ledger`);
- se a CA está sendo aceita (modo 3);
- se a janela do modelo foi resolvida e por qual fonte;
- se há compressão dupla (plugin + extensão).

### 19.9 Cobertura universal: qualquer IDE, modelo, harness e console

#### Princípio

O único ponto comum a todos os clientes é a **requisição HTTP para a API do modelo**. IDEs, extensões, CLIs, harnesses de agente e SDKs variam em tudo, menos nisso. Por isso o núcleo da integração é a interceptação dessa requisição, de forma independente do cliente. Plugins, MCP e biblioteca são **melhorias opcionais** sobre esse núcleo, nunca pré-requisitos.

Consequência de projeto: nenhuma funcionalidade essencial (gatilho, compressão, renderização, recuperação, separação por projeto) depende de recursos específicos de um cliente. Tudo funciona apenas com o tráfego passando pelo proxy.

#### Escada automática de integração

`acd-ctx attach` (ou o launcher do cliente) testa os mecanismos do mais limpo para o mais invasivo e usa o primeiro que funcionar. A verificação é real: o Accordion Context dispara o cliente ou uma requisição de teste e confirma que ela chegou ao proxy (registro em `cache_ledger`), em vez de supor.

| Degrau | Mecanismo | Requer do cliente | Invasividade |
|---|---|---|---|
| 1 | Biblioteca Go / middleware HTTP embutido | código próprio | nenhuma |
| 2 | Plugin nativo | sistema de plugins com acesso às requisições | baixa |
| 3 | Variável de ambiente de base URL | ler `*_BASE_URL` | baixa |
| 4 | Flag ou config temporária de base URL | opção de base URL na CLI ou num arquivo | baixa |
| 5 | Proxy HTTP com CA, escopo de processo | respeitar `HTTPS_PROXY` e uma variável de CA | média |
| 6 | Proxy do sistema ou PAC, escopo de usuário | respeitar o proxy do sistema operacional | alta |
| 7 | Redirecionamento transparente (opcional, Linux com `iptables`/nftables por UID ou cgroup) | nada | muito alta; exige privilégio e consentimento explícito |

Os degraus 6 e 7 vêm **desligados** por padrão e só são ativados por comando explícito (`acd-ctx attach --system-proxy`, `--transparent`), porque afetam outros programas.

**Console genérico.** Para qualquer comando de terminal sem perfil conhecido:

```bash
acd-ctx attach -- <comando> [args]
```

Esse comando exporta **ao mesmo tempo** todas as variáveis de base URL conhecidas (`ANTHROPIC_BASE_URL`, `OPENAI_BASE_URL`, `OPENAI_API_BASE`, `GOOGLE_GEMINI_BASE_URL` …), o `HTTPS_PROXY` com a CA restrita ao processo e as variáveis de CA de cada runtime (`SSL_CERT_FILE`, `NODE_EXTRA_CA_CERTS`, `REQUESTS_CA_BUNDLE`, `CURL_CA_BUNDLE`). O cliente usa o que reconhecer, e o resultado aparece no `doctor`. Assim, um console desconhecido funciona sem que alguém tenha escrito um perfil para ele.

#### Perfis declarativos de cliente

Clientes conhecidos são descritos em YAML, não em código. Para suportar um cliente novo, basta um arquivo, que pode ser contribuído pela comunidade ou mantido localmente em `~/.config/acd-ctx/clients/`:

```yaml
# clients/exemplo-cli.yaml
name: exemplo-cli
detect:
  binaries: ["exemplo"]                 # nome do executável
  config_paths: ["~/.exemplo/config.json"]
integration:
  prefer: [base_url_env, https_proxy]   # degraus a tentar, em ordem
  base_url_env:
    OPENAI_BASE_URL: "{proxy}/ctx/https://api.openai.com/v1"
  https_proxy:
    ca_env: [NODE_EXTRA_CA_CERTS]
    domains: ["api.openai.com"]
model_info:                             # de onde ler janela e limite de saída
  file: "~/.exemplo/config.json"
  context_window: "$.model.contextWindow"
  max_output: "$.model.maxTokens"
quirks:
  single_system_message: true           # backends que rejeitam system no meio
```

Os perfis cobrem o que hoje exige código específico no projeto original (dezenas de launchers): detecção, variáveis, domínios para MITM, leitura da janela do modelo e peculiaridades do protocolo.

#### IDEs

| Tipo de IDE / extensão | Como integrar |
|---|---|
| Extensões com campo de base URL ou de provedor customizado | degrau 3/4: apontar a base URL para o proxy |
| Editores abertos pelo terminal | `acd-ctx attach -- code .` (ou o executável do editor); as extensões herdam o ambiente do processo |
| Editores com configuração de proxy HTTP própria (VS Code tem `http.proxy`; IDEs JetBrains têm proxy e certificados confiáveis nas configurações) | degrau 5 com a CA do Accordion Context registrada só no editor |
| IDEs sem nenhuma das opções acima | degrau 6 (proxy do sistema) ou 7 (transparente) |
| Extensões com plugin próprio para o Accordion Context | degrau 2, com status e painel dentro do editor |

O `acd-ctx doctor <ide>` mostra qual degrau foi usado e se o tráfego das extensões de fato passa pelo proxy.

#### Modelos

**Provedores.** Os quatro protocolos da §19.1 cobrem os provedores principais e, pelo formato compatível com OpenAI, também servidores de modelos locais e auto-hospedados (Ollama, LM Studio, vLLM, SGLang, servidor do llama.cpp) e gateways (OpenRouter, LiteLLM e similares). Upstreams novos são configurados por URL, sem código.

**Perfil de capacidade por modelo.** O comportamento do kernel se adapta ao que o modelo suporta, em vez de assumir o melhor caso:

| Capacidade | Se o modelo tem | Se o modelo não tem |
|---|---|---|
| Chamada de ferramentas nativa | `compress` e demais como ferramentas (§16) | **protocolo em texto**: o nudge pede um bloco `<ctx:compress start="m…" end="m…">resumo</ctx:compress>` na resposta, que o proxy extrai e remove antes de entregar ao cliente |
| Cache de prefixo | modelo de custo da §9 | §9 desligado: comprimir sempre compensa quando há massa |
| Múltiplas mensagens `system` | permitido | resumos como `user` (já é o padrão) |
| Janela pequena (< 32K) | passo e pisos proporcionais | idem, com CCR mais agressivo e zona recente menor |
| Qualidade suficiente para resumir | o próprio modelo resume | **resumidor externo**: um modelo configurado à parte escreve os resumos a partir da faixa recomendada (como o `external-summary` do original) |

Os perfis vêm de uma tabela embutida, de `runtime-info` (§19.4) e da config, e podem ser corrigidos pelo usuário. Um modelo desconhecido recebe um perfil conservador (sem cache, ferramentas nativas testadas na primeira chamada, texto como fallback).

#### Harnesses e SDKs

Frameworks de agente e SDKs quase sempre aceitam base URL ou um cliente HTTP customizado. Para quem prefere não rodar o proxy como processo separado, o Accordion Context publica middlewares finos que encaminham para um proxy local ou embutem o núcleo:

| Linguagem | Ponto de extensão |
|---|---|
| Go | `http.RoundTripper` (`acdctx.Transport`) ou a biblioteca completa (modo 6) |
| Python | transporte customizado do `httpx` (usado pelos SDKs oficiais da OpenAI e da Anthropic) ou apenas `base_url` |
| Node/TypeScript | `fetch` customizado passado ao construtor do SDK ou apenas `baseURL` |
| Qualquer outra | base URL ou `HTTPS_PROXY` |

Harnesses que **executam ferramentas** podem, além disso, usar o protocolo de plugin (§19.4) para que o próprio agente execute `compress`, ou o servidor MCP (modo 5) se falarem MCP.

#### Conformidade

Para que "funciona com tudo" seja verificável, e não uma promessa:

- **Suíte de conformidade por protocolo**: cada adaptador de wire passa no mesmo conjunto de casos (streaming, tool calls em paralelo, reasoning, imagens, erros do upstream, cancelamento no meio do stream, respostas truncadas por limite de saída).
- **Testes de contrato com tráfego gravado**: requisições e respostas reais de cada cliente suportado, anonimizadas, reproduzidas a cada versão.
- **Matriz de compatibilidade publicada**: cliente × versão × degrau usado × resultado do `doctor`, atualizada pela CI e por relatos de usuários (`acd-ctx doctor --report` gera um relatório sem conteúdo das sessões).

#### Limites que nenhum mecanismo local contorna

A cobertura universal vale para todo cliente **cujo tráfego com o modelo sai da máquina do usuário por HTTP e pode ser roteado**. Ficam de fora, e o `doctor` os identifica explicitamente:

- **Clientes cujo backend é do fornecedor**: quando o IDE ou app envia o histórico para o servidor do próprio fornecedor, que monta o prompt e chama o modelo do lado de lá, o proxy local só vê o tráfego para esse servidor, num protocolo proprietário. Isso inclui interfaces web de chat e alguns IDEs com IA embutida (verificar caso a caso, pois muda entre versões e planos). Nesses casos, a integração só é possível se o produto oferecer um modo "traga sua própria chave" que chame a API diretamente da máquina.
- **Certificate pinning ou repositório de raízes embutido**: o cliente rejeita qualquer CA adicional, então o degrau 5 falha. Restam os degraus 3/4, se o cliente tiver base URL configurável.
- **Protocolos não HTTP ou binários proprietários** entre o cliente e o modelo.

Nesses casos, o comportamento correto é **não interferir**: o tráfego segue direto, sem compressão, e o `doctor` explica o motivo e o que o fornecedor precisaria oferecer para a integração funcionar.

---

## 20. Observabilidade

- **Logs estruturados** (`log/slog`, JSON): cada turno registra a linha da tabela de decisão, `U`, `G`, `S`, `P1..P3`, custo/benefício de cache e dicas de recall.
- **Métricas** (Prometheus):
  - `acd_ctx_context_tokens` (histograma por sessão)
  - `acd_ctx_compress_total{tier,outcome}` (aceito, rejeitado, recusado)
  - `acd_ctx_compress_ratio` (src/sum)
  - `acd_ctx_cache_read_ratio` e `acd_ctx_fold_cache_cost_tokens`
  - `acd_ctx_recall_hint_total{signal,accepted}`
  - `acd_ctx_token_calib_factor`
- **Comando de status** (`/ctx` ou endpoint `GET /sessions/{id}/status`): composição da janela, blocos ativos por tier, intenções abertas, histórico de decisões recentes e custo de cache medido.
- **Exportação de trace**: `GET /sessions/{id}/trace.jsonl` gera o arquivo usado no replay (§17.2), com conteúdo anonimizável.

---

## 21. Plano de implementação

| Fase | Entrega | Critério de aceite |
|---|---|---|
| 1 | `store` (schema, migrações, invariantes SQL) + `kernel` (tipos, refs, render básico) + `project` (detecção da raiz, escopos de config, §18) | invariantes I1–I7 com testes; janela renderizada a partir do banco; projetos separados corretamente em testes com pastas aninhadas, worktrees e `.acd-ctx/` |
| 2 | Gatilho (§8) + validação/aplicação de `compress` (§10) | todos os casos da tabela passam; testes de propriedade sem violação em 10K sequências |
| 3 | Adaptador `wire/anthropic` + loop de compress no modo proxy + modos base URL e launcher (§19) | sessão real com Claude Code passa de 2× a janela sem estouro |
| 4 | CCR + crush (§14) + calibração de tokens (§15) | erro médio de estimativa < 5% após 20 chamadas |
| 5 | Modelo de custo de cache + agendamento em cache frio (§9) | custo medido por fold reportado; adiamentos registrados |
| 6 | Envelope histórico, ledger de intenções, último turno do usuário no fim (§11) | em traces com modelo de atenção fraca, taxa de retrabalho medida cai em relação ao baseline |
| 7 | Recall assistido (§13) + `peek` | taxa de aceitação de dicas medida por sinal |
| 8 | Adaptadores `openai`, `responses` (SSE + WebSocket) e `google`, tiers T2/T3 completos | replay de traces dos quatro protocolos igual ao golden |
| 9 | Protocolo de plugin (§19.4) + companheiro MCP + biblioteca Go | um plugin de referência e o servidor MCP passam nos testes de integração |
| 10 | Modo MITM + `acd-ctx doctor` | cliente fechado de teste comprimido via MITM; `doctor` detecta CA rejeitada e compressão dupla |
| 11 | `acd-ctx attach` com escada automática, perfis declarativos de cliente e perfis de capacidade de modelo (§19.9) | console desconhecido integrado sem perfil; modelo sem tool calling comprime pelo protocolo em texto |
| 12 | Middlewares (Go, Python, Node), suíte de conformidade e matriz de compatibilidade publicada | todos os adaptadores passam na mesma suíte; matriz gerada pela CI |

---

## 22. Riscos e limites conhecidos

- **Compressão continua sendo com perda.** Os resumos retêm por volta de 1/8 do texto original (dado do paper do projeto original). A reversibilidade cobre o caso em que o modelo *sabe* que precisa recuperar; o recall assistido aumenta essa chance, mas não a garante.
- **O custo de cache de um fold não é zero.** O §9 reduz e mede esse custo, mas não o elimina: remover conteúdo do meio da janela sempre reescreve a cauda.
- **Qualidade depende do modelo.** Modelos pequenos escrevem resumos piores e seguem pior a doutrina; o envelope histórico e o ledger de intenções mitigam, mas não substituem um modelo competente.
- **Heurísticas de recall e de enquadramento dependem do idioma** e de convenções de ferramentas (nomes como `Read`, `bash`). Precisam de adaptação por host.
- **Tarefas que exigem histórico literal completo** (auditoria, compliance, refatorações com muitas restrições simultâneas) estão fora do escopo de um fold com perda; para elas, desligue a compressão ou use `decompress` explícito.
- **SQLite é um escritor por vez.** Adequado para um proxy local ou por usuário; para um serviço multiusuário de alto volume, o `store` deve ser trocado por PostgreSQL mantendo a mesma interface (as invariantes SQL são portáveis com ajustes mínimos).

---

## Referências

- Repositório original: https://github.com/ranxianglei/billion-context
- Kernel: `kernel/DESIGN.md`, `kernel/src/compress.ts` (`decideNudge`), `kernel/src/config.ts` (padrões), `kernel/src/prune.ts`
- Notas técnicas: `TECHNICAL-NOTES.md` (modos de execução e carriers)
- Paper: `paper/model-driven-incremental-hierarchical-compression-...md`
- Blog do projeto: posts de 2026-11-01 (compressão frequente) e 2026-11-02 (`compressAtFoldPoint`)
