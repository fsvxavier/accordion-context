# Plano de testes de prontidão para produção: Accordion Context (`acd-ctx`)

| | |
|---|---|
| Repositório | https://github.com/fsvxavier/accordion-context |
| Commit avaliado | `e5c0234` (12/11/2026) |
| Data do plano | 12/11/2026 |
| Estado declarado pelo projeto | pré-1.0, sem tag publicada, spec v0.1 |

## 1. Resumo executivo

**Veredito preliminar: ainda não está pronto para produção.** A suíte automatizada é ampla (30 mil linhas de teste para 34 mil de código, testes de propriedade, fuzz, e2e com binário real), mas tudo roda contra upstreams falsos em loopback. O próprio `CLAUDE.md` declara que nada foi validado contra APIs reais dos provedores, o endpoint WebSocket real do Responses, binários reais dos clientes, desktops reais nem uma execução real do GitHub Actions ou de release.

Este plano cobre o que falta para fechar essa distância. Ele tem 11 frentes (A a K), 80 casos, e termina em critérios de go/no-go (seção 7).

As cinco lacunas que mais pesam na decisão:

1. **Nenhuma validação com provedores e clientes reais** (frente C). É o maior risco: um proxy que quebra ou altera requisições derruba a sessão do agente.
2. **Qualidade da compactação nunca medida** (frente D). A compressão é com perdas e não existe avaliação de fidelidade dos resumos nem de impacto na taxa de sucesso das tarefas.
3. **Zero benchmarks** (frente F). Não há nenhuma função `Benchmark*` no repositório, nem teste de carga ou soak.
4. **Um teste falha na linha de base** (A-02) e as ferramentas de lint e de vulnerabilidades não rodaram (A-04, A-05).
5. **CI e release nunca executados** (frente J).

## 2. Linha de base medida

Executado em 12/11/2026 em Linux amd64, Go 1.27.1, usuário root dentro de contêiner.

| Verificação | Resultado |
|---|---|
| `go vet ./...` | passou |
| `go test -race -count=1 -cover ./...` | 23 de 24 pacotes passaram |
| `internal/attach` → `TestSystemProxyEndToEnd` | **falhou** 3 vezes em 3 (`system proxy was never set`, timeout de 20 s). Causa não investigada; o ambiente (root, contêiner) pode ser o motivo |
| `golangci-lint` | não rodou: binário disponível compilado com Go 1.25, projeto exige 1.27.1 |
| `govulncheck` | não rodou: `@latest` compilado com Go 1.26 |
| Benchmarks | nenhum (`Benchmark*` = 0 ocorrências) |
| Alvos de fuzz | 13 nomes distintos (codecs, `compress`, frames do plugin, SNI, iptables) |

Cobertura por pacote:

| Pacote | Cobertura | Pacote | Cobertura |
|---|---|---|---|
| `wire/toolargs` | 93,6% | `launcher` | 81,4% |
| `kernel` | 91,0% | `cli` | 79,8% |
| `wire/openai` | 90,7% | `sysproxy` | 75,4% |
| `session` | 90,3% | `store` | 73,5% |
| `wire/google` | 90,3% | `attach` | 73,3% |
| `wire/responses` | 90,1% | `backup` | 73,2% |
| `project` | 88,5% | `cmd/acd-ctx` | 68,6% |
| `wire/anthropic` | 88,0% | **`obs`** | **34,3%** |
| `doctor` | 86,4% | `scripts/gen-compat-matrix` | 0% |
| `mitm` | 85,7% | | |
| `config` | 85,3% | | |
| `plugin` | 85,4% | | |
| `proxy` | 84,1% | | |
| `pkg/acdctx` | 81,9% | | |

`store` e `backup` guardam os dados do usuário e estão entre os pacotes com menor cobertura. `obs` está muito abaixo dos demais.

## 3. Escopo

**Dentro do escopo**

- Binário `acd-ctx`: `serve`, lançadores (`claude`, `codex`, `gemini`, `opencode`, `run`), `attach`, `backup`, `restore`, `clean`, `project`, `doctor`, `mcp`, `replay`.
- Os quatro dialetos: Anthropic Messages, OpenAI Chat Completions, OpenAI Responses (HTTP e WebSocket), Google Gemini.
- Armazenamento SQLite (WAL, FTS5, migração de esquema v4, invariantes I1 a I9).
- Biblioteca `pkg/acdctx`, protocolo de plugin e clientes Python e Node.
- Imagem Docker e pipeline de release.

**Fora do escopo**

- Qualidade dos modelos dos provedores em si.
- Clientes sem regra de detecção (Copilot CLI, Cursor, Windsurf), que o projeto lista como não implementados.

**Definição de "produção" assumida:** uso diário por desenvolvedores em máquina de usuário único, com o proxy em loopback, que é o cenário que o README descreve. Uso em servidor compartilhado ou multiusuário exigiria autenticação no listener, que não existe, e fica fora deste plano.

## 4. Estratégia

Prioridades: **P0** bloqueia a liberação, **P1** precisa passar ou ter exceção documentada, **P2** é desejável.

Os limites numéricos marcados com (proposta) são sugestões minhas e precisam da sua confirmação antes da execução.

### Frente A: linha de base, build e análise estática

| ID | Pri | Teste | Critério de aprovação |
|---|---|---|---|
| A-01 | P0 | `gofmt -l .`, `go vet ./...`, cross vet darwin e windows com `CGO_ENABLED=0` | saída vazia, exit 0 |
| A-02 | P0 | Diagnosticar `TestSystemProxyEndToEnd`: rodar como usuário comum, fora de contêiner, e como root | passa nos ambientes suportados, ou faz skip explícito com motivo quando o ambiente não serve |
| A-03 | P0 | `go test -race -count=10 ./...` para caçar testes instáveis | 10 de 10 execuções verdes |
| A-04 | P0 | `golangci-lint` compilado com Go ≥ 1.27, com `gosec`, `errcheck`, `staticcheck`, `bodyclose`, `sqlclosecheck` | zero achados de severidade alta; demais triados |
| A-05 | P0 | `govulncheck ./...` com Go ≥ 1.27 | zero vulnerabilidades alcançáveis |
| A-06 | P1 | Cobertura mínima por pacote | `store`, `backup`, `session`, `proxy` ≥ 85%; `obs` ≥ 70%; total ≥ 80% (proposta) |
| A-07 | P1 | Teste de mutação em `kernel` e `store/invariants.go` (por exemplo `gremlins`) | eficácia ≥ 75% (proposta) |
| A-08 | P1 | Regras de arquitetura: `go list -deps ./internal/kernel` e `TestNoDeleteOutsidePurge` | kernel sem imports internos; `DELETE FROM` só em `purge.go` |

### Frente B: integridade e durabilidade dos dados

A promessa central do produto é que os originais nunca se perdem. Esta frente tenta quebrá-la.

| ID | Pri | Teste | Critério de aprovação |
|---|---|---|---|
| B-01 | P0 | Ida e volta: `compress` seguido de `decompress` em 10 mil sessões aleatórias (`rapid`, com `-rapid.checks` elevado) | conteúdo restaurado idêntico byte a byte; I1 a I9 sem violação |
| B-02 | P0 | Injeção de falha: `kill -9` no processo em pontos aleatórios da transação de compressão, 500 repetições | banco reabre; `PRAGMA integrity_check` ok; `CheckInvariants` com zero linhas; nenhuma compressão parcial |
| B-03 | P0 | Mesmo teste de B-02 no modo `journal_mode = DELETE` (fallback de WAL) | mesmo critério |
| B-04 | P0 | Disco cheio e erro de I/O durante `Turn` e `Compress` (tmpfs pequeno ou `dm-flakey`) | requisição segue sem alteração (fail-open); banco íntegro; erro registrado |
| B-05 | P0 | Migração v1, v2 e v3 para v4 com bancos reais de cada versão; falha injetada no meio | sucesso preserva todos os dados; falha mantém a versão antiga intacta |
| B-06 | P0 | Binário antigo abrindo banco v4 | recusa com mensagem clara, sem escrever |
| B-07 | P1 | Dois processos `serve` no mesmo arquivo; `serve` e `mcp` simultâneos | sem corrupção nem `SQLITE_BUSY` não tratado; comportamento documentado |
| B-08 | P1 | Banco corrompido (bytes aleatórios sobrescritos, arquivo truncado, WAL órfão) | erro claro na abertura; proxy sobe em fail-open ou recusa de forma explícita |
| B-09 | P1 | `clean` e `restore --mode replace` com `serve` em execução | epoch incrementa; sessões em memória descartadas na requisição seguinte; sem dado fantasma |
| B-10 | P1 | Hash de conteúdo: alterar um `content` direto no SQLite | detectado por `InspectFile` e por `backup` |
| B-11 | P2 | Crescimento: sessão de 5 mil turnos, banco acima de 5 GB | sem degradação superlinear; `VACUUM` e `clean --older-than` funcionam |

### Frente C: validação com provedores e clientes reais

Nada disto foi feito até hoje. Requer chaves de API e orçamento.

| ID | Pri | Teste | Critério de aprovação |
|---|---|---|---|
| C-01 | P0 | Anthropic Messages real: sem streaming, com streaming, tool use, imagens, thinking, prompt caching | resposta equivalente à chamada direta; nenhum erro 4xx causado pelo proxy |
| C-02 | P0 | OpenAI Chat Completions real: streaming, tool calls paralelas, `response_format` | idem |
| C-03 | P0 | OpenAI Responses real por HTTP e por WebSocket, incluindo itens não modelados (opacos) | itens opacos devolvidos byte a byte; sessão WS estável |
| C-04 | P0 | Google Gemini real: streaming, function calling | idem C-01 |
| C-05 | P0 | Claude Code real via `acd-ctx claude`: sessão de trabalho de 2 horas em repositório de verdade | zero falhas atribuíveis ao proxy; pelo menos 3 compactações confirmadas por `compression_events` |
| C-06 | P0 | Codex CLI, Gemini CLI e OpenCode reais, mesma sessão de C-05 | idem |
| C-07 | P0 | Detecção de compactação do cliente (`/compact` e equivalentes) em tráfego real | passthrough sem alteração; métrica `acd_ctx_client_compactions_total` incrementa com o sinal correto |
| C-08 | P0 | Estabilidade do prefixo: comparar `cache_read_input_tokens` com e sem proxy em 50 turnos sem compactação | taxa de acerto de cache igual à da chamada direta (tolerância de 2 pontos, proposta) |
| C-09 | P1 | Modelo emite marcador falso "📦 Compressed…" | marcador removido; nenhuma compressão registrada sem linha em `compression_events` |
| C-10 | P1 | Contagem de tokens estimada contra `usage` real, 200 requisições por provedor | erro médio ≤ 5%, máximo ≤ 15% (proposta); I8 nunca violado no provedor |
| C-11 | P1 | Versões novas dos clientes: repetir C-05 a C-07 a cada release de cliente | matriz em `docs/compatibility.md` atualizada com versões testadas |

### Frente D: qualidade da compactação

| ID | Pri | Teste | Critério de aprovação |
|---|---|---|---|
| D-01 | P0 | Conjunto fixo de 30 tarefas longas de código, executadas com e sem proxy, 3 repetições cada | taxa de sucesso com proxy ≥ taxa sem proxy menos 5 pontos (proposta) |
| D-02 | P0 | Fidelidade dos resumos: 100 blocos comprimidos avaliados por perguntas factuais sobre o original (nomes de arquivo, decisões, erros) | ≥ 90% das respostas corretas a partir do resumo ou via `retrieve` (proposta) |
| D-03 | P1 | Recall: após compactação, perguntar algo que só está no original | o modelo usa `search_context`, `retrieve` ou `decompress` e responde certo em ≥ 85% dos casos (proposta) |
| D-04 | P1 | Economia líquida: tokens de entrada e custo total (incluindo invalidação de cache) com e sem proxy nas tarefas de D-01 | relatório com números; custo com proxy não superior ao sem proxy |
| D-05 | P1 | Hierarquia T1, T2 e T3 em sessão de 500 turnos | resumos de nível superior continuam recuperáveis até o original |
| D-06 | P2 | Pares `tool_call`/`tool_result` e primeira mensagem do usuário | nunca separados nem ocultados (I4), verificado no payload enviado ao provedor |

### Frente E: segurança

| ID | Pri | Teste | Critério de aprovação |
|---|---|---|---|
| E-01 | P0 | DNS rebinding com navegador real (Chrome e Firefox) contra `127.0.0.1:8787` | todas as requisições recebem 403; hoje só há teste com cabeçalho `Host` forjado |
| E-02 | P0 | Allowlist de upstream e SSRF: `/ctx/<url>/` com IP interno, `file://`, redirecionamento, host com userinfo, IDN, IPv6 mapeado | só origens em `--allow` são contatadas; redirecionamento devolvido ao cliente |
| E-03 | P0 | Vazamento de credenciais: varrer logs, `trace.jsonl`, `/metrics`, `/status` e o banco após tráfego com chaves marcadas | zero ocorrências; hoje os testes cobrem só a query string em mensagens de erro |
| E-04 | P0 | Plugin: sem token, token errado, comparação em tempo constante, `Origin` presente | 401 ou 403 em todos os casos |
| E-05 | P0 | `restore` com arquivos hostis: triggers, views, tabelas virtuais extras, esquema mais novo, hash divergente, arquivo acima de `--max-size`, bomba de páginas | recusado antes de qualquer escrita |
| E-06 | P0 | Fuzz prolongado: cada um dos 13 alvos por 1 hora, mais novos alvos para `route.go` e para `InspectFile` | zero crashes; corpus commitado |
| E-07 | P1 | Listener fora de loopback: sem `--allow-non-loopback`, sem `--allowed-hosts` | recusa em subir |
| E-08 | P1 | Limites: corpo e frame WS de 32 MiB mais 1 byte; slowloris; 10 mil conexões ociosas | rejeição limpa; memória estável |
| E-09 | P1 | CA do MITM: permissões `0600`, interceptação só dos domínios permitidos, CA entregue apenas ao processo filho | verificado com `stat`, captura de tráfego e inspeção de ambiente |
| E-10 | P1 | Modo transparente em VM real (nftables e iptables): `kill -9` no dono, `kill -9` no watchdog, queda de energia simulada | rede do usuário restaurada, ou `doctor` aponta o resíduo e `attach --restore` resolve |
| E-11 | P1 | Imagem Docker: `trivy` ou `grype`; execução com `--read-only`, `--cap-drop ALL`, usuário não root | sem CVE crítica ou alta; contêiner funcional |
| E-12 | P1 | Cadeia de suprimentos: SBOM, checksums e assinatura dos artefatos do GoReleaser | gerados e verificáveis |
| E-13 | P2 | Injeção de prompt: resultado de ferramenta instrui o modelo a chamar `compress` ou `decompress` de forma abusiva | sem perda de dados; I8 mantido |

### Frente F: desempenho e soak

| ID | Pri | Teste | Critério de aprovação |
|---|---|---|---|
| F-01 | P0 | Criar benchmarks Go para `kernel` (render, trigger), codecs (`Decode` e `Encode` com payload de 1 MiB e 16 MiB), `store` (turn, compress, search) | benchmarks no repositório, comparados por `benchstat` no CI |
| F-02 | P0 | Latência adicionada pelo proxy contra upstream falso de latência fixa, janela de 128K tokens | p50 ≤ 20 ms, p99 ≤ 100 ms (proposta) |
| F-03 | P0 | Streaming Anthropic: tempo até o primeiro byte com e sem proxy | diferença p95 ≤ 50 ms (proposta) |
| F-04 | P0 | Soak de 24 horas: 20 sessões simultâneas em laço, com compactações | sem crescimento contínuo de RSS, goroutines ou descritores; zero erros 5xx gerados pelo proxy |
| F-05 | P1 | Carga: 50, 100 e 200 sessões simultâneas (k6 ou `vegeta`) | sem `SQLITE_BUSY` exposto; degradação documentada |
| F-06 | P1 | `search_context` (FTS5) em banco de 1 GB e de 5 GB | p95 ≤ 200 ms (proposta) |
| F-07 | P1 | Vazamento de goroutines com `goleak` nos pacotes `proxy`, `session`, `plugin`, `mitm` | zero vazamentos |
| F-08 | P2 | `backup` e `restore` de banco de 2 GiB | tempo e pico de memória medidos e documentados |

### Frente G: resiliência e fail-open

| ID | Pri | Teste | Critério de aprovação |
|---|---|---|---|
| G-01 | P0 | Falha em cada etapa do caminho de compactação (decode, store, kernel, encode) por injeção de erro | requisição original encaminhada byte a byte; evento e métrica registrados |
| G-02 | P0 | Upstream com 429, 500, 529, timeout, conexão resetada e stream cortado no meio | erro repassado fielmente; ledger consistente; turno seguinte funciona |
| G-03 | P0 | Reinício do proxy no meio da sessão com cliente real (hoje só com upstream falso) | sessão continua; janela reconstruída a partir do banco |
| G-04 | P1 | `SIGTERM` com requisições e streams em andamento | encerramento gracioso; nenhuma transação pela metade |
| G-05 | P1 | Cliente cancela a requisição durante o laço de ferramentas internas | contexto cancelado; sem goroutine órfã; sem escrita parcial |
| G-06 | P1 | Recarga de configuração a cada 30 s com arquivo inválido | configuração anterior mantida; aviso no log |

### Frente H: operação

| ID | Pri | Teste | Critério de aprovação |
|---|---|---|---|
| H-01 | P0 | Ensaio de recuperação: `backup`, apagar tudo, `restore`, continuar a sessão | sessão retoma com `decompress` funcionando |
| H-02 | P1 | `clean` em todos os escopos, com `--dry-run`, `--backup` com falha forçada, stdin não terminal | nada apagado quando o backup falha; confirmação exigida |
| H-03 | P1 | `doctor --json` em instalação sã e em 5 instalações quebradas de propósito | cada defeito apontado |
| H-04 | P1 | Métricas e endpoints de observabilidade (pacote `obs`, cobertura de 34,3%) | cada métrica `acd_ctx_*` validada contra evento real; cardinalidade limitada |
| H-05 | P2 | Docker Compose conforme `docs/docker.md`, com backup via `--docker` | passo a passo da documentação funciona sem ajuste |

### Frente I: compatibilidade de plataforma

O CI só roda `go vet` cruzado para darwin e windows. Os testes nunca executaram nessas plataformas.

| ID | Pri | Teste | Critério de aprovação |
|---|---|---|---|
| I-01 | P0 | `go test -race ./...` em Linux amd64 e arm64, macOS arm64, Windows amd64 | verde em todas as plataformas declaradas como suportadas |
| I-02 | P1 | WSL2 com banco em `/mnt/c` (fallback de WAL) e em caminho nativo | aviso emitido; funciona nos dois |
| I-03 | P1 | Proxy de sistema em GNOME, KDE, macOS e Windows reais | configuração aplicada e restaurada |
| I-04 | P2 | IPv6: `[::1]` livre e ocupado | comportamento igual ao documentado |

### Frente J: CI e release

| ID | Pri | Teste | Critério de aprovação |
|---|---|---|---|
| J-01 | P0 | Primeira execução real de `ci.yml` em pull request | todos os jobs verdes, incluindo Docker |
| J-02 | P0 | `goreleaser check` e `goreleaser release --snapshot --clean` | sem erro; artefatos para todas as plataformas |
| J-03 | P0 | Tag de pré-release (por exemplo `v0.1.0-rc.1`) em fork ou após aprovação | `release.yml` conclui; `go install ...@v0.1.0-rc.1` funciona; binário reporta a versão |
| J-04 | P1 | Atualização entre versões: banco criado pelo rc.1 aberto pelo rc.2 | migração transparente |
| J-05 | P1 | Adicionar ao CI: `golangci-lint`, `govulncheck`, fuzz curto, cobertura com limite, matriz de SO | pipeline falha quando um gate é violado |

### Frente K: API pública e middlewares

| ID | Pri | Teste | Critério de aprovação |
|---|---|---|---|
| K-01 | P1 | `pkg/acdctx`: `Open`, `Turn`, `Compress`, `Decompress`, `Recall`, `Close`, `NewTransport`, `Handler` usados por um programa externo de exemplo | exemplos `Example*` compilam e passam; uso concorrente seguro com `-race` |
| K-02 | P1 | Vetores de conformidade em Go, Python 3.10 a 3.13 e Node 20 e 22 | todos verdes |
| K-03 | P2 | `apidiff` entre versões | mudanças incompatíveis listadas nas notas de release |

## 5. Ambiente de teste

| Item | Necessidade |
|---|---|
| Toolchain | Go 1.27.1 ou superior; `golangci-lint` e `govulncheck` compilados com a mesma versão |
| Máquinas | Linux amd64 e arm64, macOS arm64, Windows 11, WSL2; uma VM descartável para o modo transparente |
| Contas | chaves de API de Anthropic, OpenAI e Google com orçamento definido para as frentes C e D |
| Clientes | Claude Code, Codex CLI, Gemini CLI e OpenCode nas versões atuais, registradas no relatório |
| Ferramentas | k6 ou `vegeta`, `benchstat`, `goleak`, `trivy` ou `grype`, navegadores Chrome e Firefox |

## 6. Ordem de execução sugerida

1. **Etapa 1, sem custo externo:** frentes A, B, G-01, E-05, E-06, F-01, J-01 e J-02.
2. **Etapa 2, com provedores reais:** frentes C e D. É aqui que o maior risco se resolve.
3. **Etapa 3, robustez:** frentes E (restante), F (carga e soak), G (restante), H e I.
4. **Etapa 4, release candidate:** J-03 e J-04, K, e uma semana de uso diário real antes da tag final.

## 7. Critérios de entrada e de saída

**Entrada:** commit congelado; ambiente da seção 5 disponível; A-02 diagnosticado.

**Saída (go/no-go):**

- 100% dos casos P0 aprovados.
- Pelo menos 90% dos casos P1 aprovados; os demais com exceção escrita e prazo.
- Nenhum defeito aberto de perda de dados, corrupção, vazamento de credencial ou alteração indevida de requisição.
- Soak de 24 horas (F-04) e uma semana de uso real sem incidente.
- README atualizado: a seção Status deixa de dizer que nada foi validado, e lista as versões de clientes e provedores testadas.

## 8. Riscos

| Risco | Impacto | Mitigação |
|---|---|---|
| Provedor ou cliente muda o formato de wire | proxy quebra sessões | C-11 recorrente; fail-open (G-01); itens opacos |
| Resumo incorreto leva o agente a erro | trabalho errado sem aviso | frente D; `decompress` e `retrieve` disponíveis |
| Invalidação de cache anula a economia | custo maior que sem proxy | C-08 e D-04 com números reais |
| Modo transparente deixa a rede redirecionada | máquina sem acesso | E-10; manter o modo marcado como experimental |
| Listener sem autenticação | qualquer processo local usa o proxy como relay | documentar o limite; fora do escopo multiusuário |
| Estimativa de tokens diverge do provedor | estouro de janela (erro 400) | C-10; margem de segurança em `window.context_limit` |

## 9. Entregáveis

- Relatório de execução por frente, com evidência (comando, saída, commit).
- Lista de defeitos classificados por severidade.
- Benchmarks e testes novos incorporados ao repositório (F-01, E-06, J-05).
- Parecer final de go/no-go contra a seção 7.

## 10. Skills de QA encontradas com `/find-skills`

Busca feita com `npx skills find` em seis consultas. Nenhuma skill foi instalada. Li o `SKILL.md` das candidatas principais direto dos repositórios; a estrutura deste plano (resumo, escopo, estratégia, ambiente, critérios de entrada e saída, riscos, entregáveis) segue a de `qa-test-planner`.

O número de instalações vem do skills.sh. Não consegui consultar as estrelas dos repositórios nesta sessão, então essa parte da verificação de qualidade ficou pendente.

| Skill | Instalações | Uso neste plano | Instalação |
|---|---|---|---|
| `samber/cc-skills-golang@golang-testing` | 45,5 mil | frentes A, B, F: table tests, fuzz, `goleak`, `synctest`, cobertura | `npx skills add samber/cc-skills-golang@golang-testing` |
| `samber/cc-skills-golang@golang-security` | mesmo repositório | frente E: `gosec`, STRIDE, SSRF, path traversal | `npx skills add samber/cc-skills-golang@golang-security` |
| `samber/cc-skills-golang@golang-benchmark` | mesmo repositório | F-01 | `npx skills add samber/cc-skills-golang@golang-benchmark` |
| `anthropics/knowledge-work-plugins@testing-strategy` | 6,7 mil | estratégia geral; é curta (33 linhas) | `npx skills add anthropics/knowledge-work-plugins@testing-strategy` |
| `jeffallan/claude-skills@test-master` | 5,7 mil | estratégia geral | `npx skills add jeffallan/claude-skills@test-master` |
| `softaworks/agent-toolkit@qa-test-planner` | 4,4 mil | formato do plano, casos de teste e relatório de bug | `npx skills add softaworks/agent-toolkit@qa-test-planner` |
| `petrkindlmann/qa-skills@security-testing` | 1,4 mil | frente E: OWASP Top 10 e OWASP LLM Top 10 | `npx skills add petrkindlmann/qa-skills@security-testing` |
| `petrkindlmann/qa-skills@performance-testing` | 1,4 mil | frente F: perfis de carga com k6; a parte de Web Vitals não se aplica | `npx skills add petrkindlmann/qa-skills@performance-testing` |

Descartadas: as skills de `production-readiness` têm todas menos de 100 instalações, e `usestrix/strix` (16 mil) é voltada a pentest de aplicações web.