# Accordion Context (`acd-ctx`)

[English](README.md) | **Português (Brasil)** | [Español (España)](README.es.md)

Accordion Context é um proxy local que mantém sessões longas de agentes de código dentro de uma janela de contexto limitada. O modelo resume partes antigas da conversa, os resumos substituem as mensagens originais na janela, e os originais ficam em um banco SQLite para que possam ser recuperados.

> **Status: em desenvolvimento inicial (pré-1.0).** O proxy, os launchers de clientes, o caminho `attach` (incluindo os graus opcionais de proxy do sistema e transparente), backup, restauração, limpeza, os endpoints de observabilidade, o protocolo HTTP de plugin com seus clientes e a biblioteca `pkg/acdctx` estão implementados e passam em `go test ./...`. Nada foi validado contra as APIs reais dos provedores nem contra os binários reais dos clientes. Veja [Status](#status).

## O que é

Agentes de código como Claude Code, Codex, Gemini CLI e OpenCode podem rodar por dias, mas o modelo enxerga uma janela de 100K a 200K tokens. Accordion Context reimplementa em Go o modelo de compactação de contexto do [billion-context](https://github.com/ranxianglei/billion-context) (`acp-kernel`), com estas propriedades:

- **Incremental.** Só é dobrado conteúdo novo que já foi consumido. O histórico inteiro nunca é reescrito de uma vez.
- **Reversível.** Os originais nunca são apagados. Um intervalo dobrado sai da janela renderizada e pode ser recuperado.
- **Hierárquico.** Os resumos são organizados em níveis: T2 resume blocos T1, e T3 resume blocos T2.
- **O modelo escreve os resumos.** O sistema decide quando pedir um resumo, qual intervalo oferecer e como reconstruir a janela.
- **Proxy mais SQLite como fonte de verdade.** SQLite (modo WAL, FTS5, driver puro Go `modernc.org/sqlite`) guarda o log de eventos append-only, blocos, sessões e amostras de cache.

Diferenças em relação ao billion-context, conforme a especificação de design `compactacao-contexto-go-sqlite.md` (v0.1; §1 a §3):

- Go no lugar de TypeScript, com um pacote `internal/kernel` puro, sem I/O.
- O gatilho é uma única tabela de decisão, implementada como a função pura `kernel.DecideNudge`. Seus testes de tabela são a especificação executável.
- Uma compactação só conta quando existe uma linha em `compression_events` no banco. Texto com cara de marcador de compactação na saída do modelo não é confiável.
- Invariantes nomeadas (I1 a I9) são verificadas por checagens SQL, por testes de propriedade e, para o tamanho da janela (I8), por um truncamento de emergência.
- O custo de cache é modelado explicitamente, e um portão de cache pode adiar um nudge.

## Como funciona

1. A requisição do agente chega ao proxy, que registra cada mensagem na sessão com uma referência estável (por exemplo `m00451`).
2. O gatilho decide se a compactação é devida. O portão de cache pode adiar o nudge quando uma reescrita quebraria o cache de prompt do provedor a um custo alto.
3. Quando é devida, o proxy adiciona um nudge que lista os intervalos compactáveis. O modelo responde com uma chamada `compress`: intervalos mais um resumo.
4. O proxy valida a chamada e grava o bloco e os intervalos ocultos em uma única transação.
5. A janela seguinte mostra o resumo no lugar das mensagens dobradas. Os originais permanecem no SQLite.
6. Quando o modelo precisa de detalhes, chama `decompress`. As chamadas internas de `compress` e `decompress` rodam dentro do proxy (até 4 rodadas por requisição, por padrão) e não chegam ao cliente.
7. Qualquer falha no caminho de compactação é fail-open: a requisição original é encaminhada sem alteração.

## Protocolos suportados

O proxy reconhece quatro dialetos de API em `POST`:

| Dialeto | Endpoint reconhecido pelo proxy | Observações |
|---|---|---|
| Anthropic Messages | `/v1/messages` | O streaming é repassado conforme chega. |
| OpenAI Chat Completions | `/v1/chat/completions` | Cada rodada do upstream é lida inteira antes de ser enviada. |
| OpenAI Responses | `/v1/responses` | Em HTTP, as requisições são sem estado: as que usam `previous_response_id` ou `conversation` são encaminhadas sem alteração. Upgrades WebSocket são compactados por frame `response.create`. |
| Google Gemini | `.../models/<modelo>:generateContent`, e `:streamGenerateContent` com `alt=sse` | Os streams são armazenados em buffer por rodada. Sem `alt=sse`, a requisição é encaminhada sem alteração. |

Outros caminhos, e outros métodos, são repassados ao upstream sem compactação.

## Instalação

Requisitos: Go 1.27.1 ou superior (a diretiva `go` do `go.mod`). Não é preciso compilador C: o SQLite vem de `modernc.org/sqlite`, que é Go puro.

Build a partir do clone (verificado, inclusive com `CGO_ENABLED=0`):

```sh
git clone https://github.com/fsvxavier/accordion-context.git
cd accordion-context
go build -o acd-ctx ./cmd/acd-ctx
./acd-ctx help
```

Instale pelo caminho do módulo com `go install github.com/fsvxavier/accordion-context/cmd/acd-ctx@<commit>`. Ainda não existe tag de versão, então `@latest` não resolve. O valor de `version` é `dev`, a menos que o binário seja compilado com `-ldflags "-X main.version=<tag>"`.

## Docker

A imagem é um build estático, não-root (distroless, uid 65532), com os dados no volume `acd-ctx-data`. O compose publica a porta somente em `127.0.0.1`:

```sh
ACD_CTX_VERSION=$(acd-ctx version | awk '{print $2}') docker compose up -d --build
docker exec acd-ctx /acd-ctx healthcheck --port 8787   # código 0 quando saudável
```

Compile a imagem com a mesma versão do binário do host: o launcher do host reaproveita um proxy em execução somente quando a versão confere. Veja [docs/docker.md](docs/docker.md) para configuração, backup, atualização e notas de segurança.

## Início rápido

Suba o proxy em loopback, encaminhando para `api.anthropic.com`:

```sh
acd-ctx serve
curl -s http://127.0.0.1:8787/__acd-ctx/health
# {"ok":true}
```

O modo launcher inicia um proxy próprio em uma porta livre de loopback (ou reutiliza um proxy saudável da mesma versão), define a URL base do cliente e encerra o proxy quando o cliente termina:

```sh
acd-ctx claude        # define ANTHROPIC_BASE_URL só para este processo
```

O modo base-URL aponta manualmente um proxy já existente para um cliente. Há duas formas de caminho:

```sh
# Forma simples: upstream padrão; projeto pelo cabeçalho X-Acd-Ctx-Project-Dir ou "default"
ANTHROPIC_BASE_URL=http://127.0.0.1:8787 claude

# Forma explícita: ID do projeto em /p/<id>/, upstream em /ctx/<url>/ (precisa estar permitido)
ANTHROPIC_BASE_URL=http://127.0.0.1:8787/p/<project-id>/ctx/https://api.anthropic.com claude

# Outro provedor precisa da origem em --allow. Ainda não verificado com o cliente real.
acd-ctx serve --allow https://api.openai.com
OPENAI_BASE_URL=http://127.0.0.1:8787/ctx/https://api.openai.com/v1 codex
```

Flags do `serve` (todas opcionais):

| Flag | Padrão | Significado |
|---|---|---|
| `--listen` | `127.0.0.1:8787` | Precisa ser loopback, a menos que `--allow-non-loopback` e `--allowed-hosts` estejam ambos definidos (veja [Notas de segurança](#notas-de-segurança)). |
| `--upstream` | `https://api.anthropic.com` | Upstream usado em requisições sem prefixo `/ctx/<url>/`. |
| `--allow` | origem de `--upstream` | Origens separadas por vírgula acessíveis por `/ctx/<url>/`. Variável `ACD_CTX_ALLOW`. |
| `--allowed-hosts` | nomes de loopback | Valores do cabeçalho Host aceitos pela proteção de Host; substituem os padrões. Variável `ACD_CTX_ALLOWED_HOSTS`. |
| `--db` | `~/.local/share/acd-ctx/acd-ctx.db` | Caminho do banco SQLite. |
| `--config` | caminho de configuração do usuário | Arquivo de configuração global. |
| `--markers` | `go.mod,package.json,pyproject.toml,composer.json,Cargo.toml` | Marcadores de raiz de projeto. |
| `--plugin-token-file` | `~/.local/share/acd-ctx/plugin.token` | Recebe o token bearer do plugin, gerado a cada execução (modo `0600`). Uma porta não padrão usa `plugin-<porta>.token`. Se não puder ser gravado, o protocolo de plugin é desativado e o proxy continua funcionando. |

## Modos de integração

| Modo | Comando | Situação | Observações |
|---|---|---|---|
| **Base URL** | `acd-ctx serve` mais a variável de URL base do cliente | Implementado | Verificado com requisições HTTP e clientes substitutos. Clientes reais não foram executados. |
| **Launcher** | `acd-ctx claude`, `codex`, `gemini`, `opencode`, `run -- <cmd>` | Implementado para claude, codex e gemini | `run` aceita apenas comandos com perfil conhecido. `opencode` não tem variável de URL base e é recusado: use `attach`. |
| **Attach** | `acd-ctx attach [--profiles f] [--mitm-domain h] -- <cmd>` | Implementado | Testa cada grau antes de iniciar o cliente e usa o primeiro que funciona: env de URL base, flag de URL base, configuração temporária, console (qualquer comando) e MITM. |
| **MITM** | o grau `mitm` do `attach` | Implementado | Só os hosts da allowlist são terminados. Os demais são tunelados sem alteração. A CA é por instalação e não é adicionada ao repositório de certificados confiáveis do sistema. |
| **Proxy do sistema** | `attach --system-proxy --yes` | Implementado; não validado em desktops reais | Aponta o proxy HTTPS do desktop para o acd-ctx durante a execução. Backends: GNOME, KDE, macOS, Windows. Mudança em toda a máquina: exige `--yes`. |
| **Transparente** | `attach --transparent --yes --apply` | Implementado apenas para nftables no Linux | Redireciona o TCP 443 de um uid (por padrão, os domínios MITM) para o listener local. Exige root ou CAP_NET_ADMIN. Sem `--apply`, apenas as regras são impressas. |
| **Plugin** | `acd-ctx serve`, em `/__acd-ctx/plugin/` | Implementado | Token bearer por execução. Ferramentas: `compress`, `decompress`, `retrieve`, `search_context`, `ctx_status`. Veja [Protocolo de plugin e middleware](#protocolo-de-plugin-e-middleware). |
| **Middleware** | `middleware/python`, `middleware/node` | Implementado | Clientes sem dependências do protocolo de plugin, testados com os vetores de conformidade. |
| **Companheiro MCP** | `acd-ctx mcp` (stdio) | Implementado | Ferramentas: `decompress`, `retrieve`, `search_context`, `ctx_status`. |
| **Biblioteca Go** | `pkg/acdctx` | Implementado | `Open` e `Client` (Turn, Compress, Decompress, Recall, Close), `NewTransport` (um `http.RoundTripper`) e `Handler`. Pode mudar antes da 1.0. |
| **Doctor** | `acd-ctx doctor [--json] [cliente]` | Implementado | Verifica a instalação e a integração. Código de saída 0 para PASS, 3 para WARN, 1 para FAIL. |

`attach --profiles arquivo.yaml` adiciona perfis que são comparados antes dos embutidos. Um perfil define `match`, `upstream`, `base_url_env` ou `base_url_flag` (com `flag_position`), um `temp_config` (`file`, `format`, `template`, `merge_from`, `key`; `merge_from` aceita apenas JSON e YAML, não TOML), `env`, `detect` (`binaries`, `config_paths`, `version_regex`), `integration` (`prefer`, `base_url_env`, `https_proxy`, `ca_env`, `domains`), `model_info` e `quirks`.

## Configuração

Os valores são resolvidos por chave, da menor para a maior precedência:

1. Padrões embutidos.
2. Arquivo global: `$XDG_CONFIG_HOME/acd-ctx/config.yaml`, ou `~/.config/acd-ctx/config.yaml` no Linux.
3. Sobrescritas `projects` no arquivo global, para cada padrão que casa com a raiz do projeto.
4. Arquivo local do projeto: `<raiz do projeto>/.acd-ctx/config.yaml`.
5. Variáveis de ambiente `ACD_CTX_*`.
6. Flags explícitas.

Exemplo, com chaves reais (carrega sem avisos):

```yaml
# ~/.config/acd-ctx/config.yaml
trigger:
  u_high: 0.75          # começa a compactar em 75% da janela
  step_base: 50000      # tokens entre nudges, antes do ajuste adaptativo
storage: global         # ou "local": guarda o banco do projeto em .acd-ctx/
```

As chaves reais, com seus padrões (`config show --effective` imprime a mesma lista). A coluna `local` indica se o `.acd-ctx/config.yaml` de um projeto pode definir a chave:

| Chave | Local | Padrão |
|---|---|---|
| `trigger.u_floor`, `trigger.u_high`, `trigger.u_emerg` | sim | `0.3`, `0.75`, `0.95` |
| `trigger.step_base`, `trigger.step_min`, `trigger.step_max` | sim | `50000`, `30000`, `100000` |
| `trigger.step_read_gain`, `trigger.min_growth_floor`, `trigger.max_refusals`, `trigger.tiers_enabled` | sim | `1`, `20000`, `3`, `true` |
| `cache.ttl`, `cache.margin`, `cache.price_uncached`, `cache.price_cached` | não | `5m`, `1.5`, `0` (não definido), `0` (não definido) |
| `compress.preserve_recent_messages`, `compress.preserve_recent_tokens` | sim | `3`, `5000` |
| `compress.min_compress_range`, `compress.min_summary_chars`, `compress.max_summary_chars` | sim | `5000`, `50`, `20000` |
| `compress.protected_tools`, `compress.framing_check` | sim | lista vazia, `false` |
| `compress.user_turn_after_summaries`, `compress.intent_ledger` | sim | `true`, `true` |
| `ccr.threshold_tokens`, `ccr.crush` | sim | `4000`, `true` |
| `recall.assist`, `recall.min_accept_rate` | sim | `true`, `0.05` |
| `storage` | sim | `global` |
| `window.context_limit`, `window.models` | sim | `0` (derivado do modelo), mapa vazio |

As chaves com os prefixos `upstream.`, `credentials.`, `listen.` e `tools.external_summary.` são reservadas. Elas são ignoradas com um aviso, no arquivo global e no local.

Variáveis de ambiente usam a chave em maiúsculas, com pontos trocados por sublinhados:

```sh
ACD_CTX_TRIGGER_STEP_BASE=60000 acd-ctx claude
```

Mostre o valor efetivo e a origem de cada chave:

```sh
acd-ctx config show --effective
```

O proxy recarrega a configuração de cada raiz de projeto no máximo a cada 30 segundos, então a maioria das edições não exige reinício.

### Nota de segurança sobre a configuração local

`<projeto>/.acd-ctx/config.yaml` fica dentro do repositório em que você trabalha, então pode vir de um clone que você não escreveu. Ele pode alterar o comportamento de gatilho, compactação, CCR, recall, armazenamento e janela, mas não `upstream.*`, `credentials.*` nem `listen.*`. Revise como qualquer outro arquivo do projeto.

## Separação por projeto

- **Detecção.** Subindo a partir do diretório de trabalho, a raiz é o diretório mais próximo que contém `.acd-ctx/`, `.git` ou um marcador de raiz; dentro de um mesmo diretório a ordem é `.acd-ctx/`, depois `.git`, depois um marcador. Se nada for encontrado, o diretório de trabalho é a raiz.
- **ID do projeto.** Um projeto com `.acd-ctx/project-id` usa esse valor. Caso contrário, o ID são os primeiros 16 caracteres hexadecimais do SHA-256 do caminho da raiz.
- **Requisições.** O proxy obtém o projeto do cabeçalho `X-Acd-Ctx-Project-Dir` (caminho absoluto de um diretório existente) e, depois, do prefixo `/p/<id>/`. A conversa vem de `X-Acd-Ctx-Conversation`. Requisições sem sinal de projeto usam o projeto `default` e registram um aviso.
- **Armazenamento.** `global` guarda todos os projetos em um único banco. `local` guarda um projeto em `.acd-ctx/acd-ctx.db`.

Comandos de projeto:

```sh
acd-ctx project init [--local-storage]   # cria .acd-ctx/ na raiz do projeto
acd-ctx project show                     # projeto detectado, raiz, armazenamento e banco
acd-ctx project list                     # projetos no banco global
acd-ctx project move <antigo> <novo>     # reaponta a raiz de um projeto movido
acd-ctx project migrate --to local|global # copia as sessões do projeto entre bancos
acd-ctx project purge <id> [--yes]       # apaga todos os dados de um projeto
```

## Variáveis de ambiente

| Variável | Usada por | Significado |
|---|---|---|
| `ACD_CTX_<CHAVE>` | carregador de configuração | Sobrescreve uma chave, por exemplo `ACD_CTX_CCR_CRUSH=false`. |
| `ACD_CTX_DB` | comandos da CLI, `attach`, `doctor` | Caminho do banco global. O `serve` usa `--db`. |
| `ACD_CTX_ALLOW`, `ACD_CTX_ALLOWED_HOSTS`, `ACD_CTX_ALLOW_NON_LOOPBACK` | `serve` | Iguais a `--allow`, `--allowed-hosts` e `--allow-non-loopback` (`1` ou `true`). |
| `ACD_CTX_MAX_RESTORE_BYTES` | `restore` | Limite de tamanho de um backup, quando `--max-size` não é dado. Padrão 2GiB. |
| `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `HOME` | caminhos | Diretórios base. O `serve` ignora `XDG_DATA_HOME`. |
| `ANTHROPIC_BASE_URL`, `OPENAI_BASE_URL`, `GOOGLE_GEMINI_BASE_URL` | seu cliente | Apontam o cliente para o proxy no modo base-URL. Definidas pelos launchers. |
| `HTTPS_PROXY`, `SSL_CERT_FILE`, `NODE_EXTRA_CA_CERTS`, `REQUESTS_CA_BUNDLE`, `CURL_CA_BUNDLE` | `attach` (grau MITM) | Definidas só para o processo do cliente, para que ele confie na CA local. |
| `ACD_CTX_URL`, `ACD_CTX_TOKEN`, `ACD_CTX_TOKEN_FILE`, `ACD_CTX_CONVERSATION` | clientes de `middleware/` | Localizam e autenticam no protocolo de plugin. |

## Endpoints

Endpoints de controle (`/__acd-ctx/` recusa qualquer requisição com cabeçalho `Origin`; `metrics` e `sessions/...` aceitam apenas GET, e `health` e `status` não verificam o método):

| Caminho | Acesso | Resposta |
|---|---|---|
| `/__acd-ctx/health` | qualquer Host permitido | `{"ok":true}` |
| `/__acd-ctx/status` | qualquer Host permitido | JSON: `status`, `version`, `uptime_seconds`, `upstream_host`, `context_limit`, `max_loop`, `sessions_loaded`, `projects_seen`, `default_project_requests`, `protocols`. |
| `/__acd-ctx/metrics` | somente peers de loopback | Texto Prometheus. Famílias: `acd_ctx_requests_total`, `acd_ctx_request_duration_seconds`, `acd_ctx_upstream_duration_seconds`, `acd_ctx_context_tokens`, `acd_ctx_compress_total`, `acd_ctx_compress_ratio`, `acd_ctx_cache_read_ratio`, `acd_ctx_fold_cache_cost_tokens`, `acd_ctx_recall_hint_total`, `acd_ctx_token_calib_factor`. Os rótulos são um conjunto fechado: sem IDs de sessão, projeto ou mensagem. |
| `/__acd-ctx/sessions/{id}/status` | somente peers de loopback | Contagens, estado do gatilho, tamanho da janela, blocos, intenções abertas e decisões recentes (`?decisions=N`). Sem texto de mensagens ou blocos. |
| `/__acd-ctx/sessions/{id}/trace.jsonl` | somente peers de loopback | Trace da sessão em JSON lines. `?anonymize=1` troca os textos pelo tamanho e pelo SHA-256. Reproduza com `acd-ctx replay`. |

As requisições de API usam os caminhos de protocolo de [Protocolos suportados](#protocolos-suportados), opcionalmente atrás de `/p/<id>/` e `/ctx/<url>/`.

## Protocolo de plugin e middleware

O protocolo de plugin (spec §19.4) permite que um programa que monta os próprios prompts peça ao proxy a janela visível de uma conversa e execute as ferramentas de contexto. O `acd-ctx serve` o monta em `/__acd-ctx/plugin/`. Toda requisição precisa de `Authorization: Bearer <token>`. O `serve` cria um token aleatório novo a cada execução e o grava no arquivo indicado por `--plugin-token-file` (modo `0600`). Valem também as proteções de Host e Origin do proxy.

| Caminho (sob `/__acd-ctx/plugin/`) | Método | Finalidade |
|---|---|---|
| `manifest` | GET | Nome e versão do protocolo (`acd-ctx-plugin`, 1), ferramentas e doutrina. |
| `runtime-info` | POST | Declara a conversa, o modelo e a janela de contexto. |
| `turn` | POST | Envia mensagens e uma contagem de tokens; recebe a decisão, as dicas e a janela visível. |
| `tool` | POST | Executa `compress`, `decompress` (modos `context`, `peek`, `file`), `retrieve`, `search_context` ou `ctx_status`. |
| `status` | GET | Os mesmos dados de `ctx_status`, para `?conversationId=`. |
| `compact` | POST | Oferece os trechos compactáveis e arma um nudge para o próximo turno. Conversa desconhecida: 404. |

`search_context` é uma busca de texto completo (FTS5) sobre as mensagens e os resumos de bloco da sessão; todas as palavras precisam casar. O modo `file` do `decompress` grava o texto original do bloco no diretório de exportação do projeto e devolve o caminho.

Os clientes ficam em `middleware/python` (pacote `acd_ctx`, somente biblioteca padrão) e `middleware/node` (pacote `acd-ctx`, ESM, Node.js 18.13 ou superior, sem dependências). Ambos leem `ACD_CTX_URL` (padrão `http://127.0.0.1:8787`), `ACD_CTX_TOKEN` ou o arquivo em `ACD_CTX_TOKEN_FILE` (padrão `~/.local/share/acd-ctx/plugin.token`), e `ACD_CTX_CONVERSATION`. Em qualquer erro do cliente, a compactação é ignorada e a requisição segue.

`conformance/vectors/*.json` são vetores neutros em relação à linguagem, reproduzidos por `go test ./conformance/...` e pelas suítes Python e Node. `docs/compatibility.md` é gerado por `go run ./scripts/gen-compat-matrix`, e a CI falha quando ele está desatualizado.

## Backup e restauração

`acd-ctx backup` grava uma cópia verificada do banco global ou de um projeto. A cópia é feita com `VACUUM INTO` em um diretório temporário privado, verificada pela mesma inspeção que a `restore` aplica, e renomeada para o destino. Assim, o arquivo de saída é o antigo ou o novo completo. O nome padrão é `acd-ctx-backup-<hora UTC>.db` no diretório de trabalho; os arquivos são gravados com modo `0600`.

```sh
acd-ctx backup                               # banco global inteiro
acd-ctx backup --project <id|caminho|.> -o p.db  # um projeto; projeto local copia o próprio arquivo
```

`acd-ctx restore <arquivo.db>` (ou `--stdin`) importa um backup em uma única transação. Nunca copia um arquivo por cima do banco ativo.

- `--project <id>` restaura um projeto do backup. `--to local|global` escolhe o destino; `local` exige exatamente um projeto e grava em `.acd-ctx/acd-ctx.db` do projeto atual.
- `--mode merge` (padrão) recusa qualquer ID que já exista no destino, e não grava nada.
- `--mode replace` apaga antes o projeto de destino. Exige `--yes`.
- `--dry-run` planeja toda a importação em uma cópia de trabalho e a desfaz. Não grava nada e não cria arquivo de destino.
- `--max-size` (ou `ACD_CTX_MAX_RESTORE_BYTES`) limita o tamanho do backup: bytes, ou um número com `KiB`, `MiB` ou `GiB`. Padrão 2GiB.
- `--docker <contêiner>` (também em `backup` e `clean`) executa a operação dentro do contêiner; o arquivo é lido ou gravado no host.

Backups hostis ou danificados são rejeitados antes de qualquer gravação. Um arquivo é recusado quando não é SQLite, quando seu schema é mais novo que o do binário, quando tem qualquer tabela, view, trigger ou tabela virtual que o acd-ctx não cria (só o `search_idx` do FTS5 é aceito), quando `PRAGMA integrity_check` falha, quando algum conteúdo armazenado não gera o hash da própria chave, ou quando uma sessão quebra um invariante (I1 a I3, I6, I7). A cópia é aberta com `trusted_schema` desligado e `query_only` ligado.

## Limpeza

`acd-ctx clean` apaga dados de conversa. **A operação é irreversível.** Sem backup, as sessões, mensagens, blocos, eventos e conteúdos apagados se perdem. Execute sempre primeiro com `--dry-run` e use `--backup` para tudo o que você possa precisar depois.

```sh
acd-ctx clean --project . --dry-run                  # o que seria apagado, e nada mais
acd-ctx clean --project . --backup antes.db          # backup verificado primeiro, depois apaga
acd-ctx clean --older-than 30d --vacuum --exports
acd-ctx clean --all --docker acd-ctx --dry-run
```

- Escopo: exatamente um de `--project <id|caminho|.>`, `--session <id>`, `--older-than <dur>` (`30d`, `12h`, qualquer duração Go; `--project` pode restringi-lo) ou `--all`.
- `--backup <arquivo.db>` grava um backup verificado de cada banco antes de apagar. Se um backup falhar, nada é apagado.
- Confirmação: sem `--yes`, é preciso digitar a palavra exibida: o ID do projeto, o ID da sessão, ou `delete` para `--all` e `--older-than`. Uma entrada que não é terminal, sem `--yes`, é recusada.
- `--exports` remove também os arquivos de bloco exportados das sessões apagadas. `--vacuum` compacta os arquivos depois.
- `--include-local` limpa também o banco local do diretório atual, em `--all` ou `--older-than` sem `--project`.
- Configuração, a CA do MITM e o token de plugin nunca são apagados (`--keep-config` fica sempre ativo).
- Uma purga incrementa o epoch do store na mesma transação. Um `serve` ou plugin em execução descarta o estado em memória das sessões afetadas na próxima requisição. `restore --mode replace` faz o mesmo.

## Notas de segurança

- **Loopback por padrão.** O `serve` recusa um `--listen` que não seja loopback, a menos que `--allow-non-loopback` (ou `ACD_CTX_ALLOW_NON_LOOPBACK=1`) e um `--allowed-hosts` não vazio sejam dados. Em seguida, registra um aviso `SECURITY`. A imagem Docker usa esse caminho; veja [docs/docker.md](docs/docker.md).
- **Sem autenticação no listener de URL base.** Qualquer processo local pode enviar requisições a `/ctx/<host permitido>/...` na porta de loopback (só o `CONNECT` do MITM e o plugin exigem token). O proxy encaminha as credenciais do próprio chamador e nunca adiciona as suas, então funciona como um repasse para hosts permitidos. Use em uma máquina de um único usuário.
- **Proteções de Host e de navegador.** Só são atendidos nomes de Host permitidos (por padrão `localhost`, `127.0.0.1`, `::1` e o host de escuta); os demais recebem HTTP 403, o que visa impedir DNS rebinding (testado com cabeçalhos `Host` forjados, não com um navegador real). Preflights CORS recebem 403, e `/__acd-ctx/` recusa qualquer requisição com cabeçalho `Origin`. Nenhum dos dois é autenticação.
- **Allowlist de upstream.** Um alvo `/ctx/<url>/` só é usado se sua origem estiver em `--allow`. Redirecionamentos são devolvidos ao cliente, não seguidos.
- **CA do MITM.** Uma CA por instalação, com a chave em arquivo `0600`. Só os domínios permitidos são interceptados, e o `attach` entrega a CA apenas ao processo do cliente que ele inicia, por variáveis de ambiente.
- **Proxy do sistema e modo transparente.** Ambos alteram o caminho de rede da máquina e exigem consentimento explícito (`--yes`). `--system-proxy` muda as configurações de proxy do desktop; `--transparent` exige root ou CAP_NET_ADMIN, instala regras do nftables para um uid e se recusa a rodar com sudo quando o `HOME` não é o do usuário que chamou, a menos que o uid e o diretório de dados sejam explícitos. Ambos gravam arquivos de estado que são verificados antes de serem confiados, e `attach --restore` desfaz uma execução que travou.
- **Credenciais e limites.** Os cabeçalhos de credencial são encaminhados ao upstream e o proxy não os registra em log (os testes cobrem apenas que as mensagens de erro não vazam a query string). Um erro de compactação encaminha a requisição sem alteração. Corpos de requisição e frames WebSocket são limitados a 32 MiB.

## Desenvolvimento

```sh
go test ./...                                  # suíte completa
go test ./internal/kernel -run TestName        # um teste
go test -fuzz=FuzzParseToolArguments ./internal/wire/openai
go test ./conformance/...                      # vetores do protocolo de plugin
(cd middleware/python && python3 -m unittest discover -s tests)
(cd middleware/node && node --test)
go run ./scripts/gen-compat-matrix             # imprime docs/compatibility.md
```

`CLAUDE.md` está em inglês. A especificação de design `compactacao-contexto-go-sqlite.md` (em português) é a fonte de verdade; §N nos comentários do código se refere a ela.

A CI (`.github/workflows/ci.yml`) roda `gofmt`, `go vet`, `go test -race`, os vetores de conformidade, os testes dos clientes Python e Node, uma checagem de que `docs/compatibility.md` está atualizado, e um build da imagem Docker com teste de fumaça do health.

| Pacote | Papel |
|---|---|
| `internal/kernel` | Lógica pura: gatilho, níveis, renderização, recall, CCR e crush, truncamento, custo de cache. Sem I/O. |
| `internal/store` | Acesso ao SQLite (WAL, FTS5), schema, invariantes, purga, backup e restauração. |
| `internal/session` | Orquestração de turnos, ferramentas e a transação de compactação. O único escritor. |
| `internal/proxy` | Servidor HTTP, roteamento, adaptadores de protocolo, modo WebSocket, laço de ferramentas no servidor, endpoints de observabilidade. |
| `internal/wire/...` | Codecs de `anthropic`, `openai`, `responses` e `google`. |
| `internal/config` | Configuração em camadas e relatório da origem efetiva de cada chave. |
| `internal/project` | Detecção da raiz e do ID do projeto. |
| `internal/launcher` | Perfis de clientes, execução com variáveis de ambiente, configurações temporárias. |
| `internal/attach` | Escada de integração do `attach`: sondagens, graus de console, MITM e de sistema. |
| `internal/sysproxy` | Backends de proxy do sistema e o backend transparente com nftables, com arquivos de estado e restauração. |
| `internal/mitm` | CA local, certificados de folha e interceptação TLS restrita. |
| `internal/backup` | Backup e restauração verificados, e os executores de `--docker`. |
| `internal/obs` | Métricas Prometheus, relatórios de status e exportação de trace. |
| `internal/plugin` | Servidor MCP stdio, handler HTTP do plugin e a checagem do epoch do store. |
| `internal/cli`, `internal/doctor` | Implementação dos comandos de `cmd/acd-ctx`; verificações de instalação e integração. |
| `pkg/acdctx` | Biblioteca Go pública: API de cliente, transporte e handler HTTP. |
| `middleware/python`, `middleware/node` | Clientes do protocolo de plugin (sem dependências). |
| `conformance` | Vetores do plugin neutros em relação à linguagem e o executor em Go. |
| `scripts/gen-compat-matrix` | Gera `docs/compatibility.md`. |

## Status

Validado por testes automatizados, sem acesso à rede além do loopback. `go test ./...` passa com `CGO_ENABLED=0`:

- Testes unitários e de tabela do kernel, store, config, project, launcher, attach, sysproxy (com executores falsos), MITM, backup, restauração, limpeza, observabilidade e doctor.
- Testes de propriedade (`pgregory.net/rapid`) que verificam os invariantes após passos de compactação aleatórios.
- Alvos de fuzz para os parsers de argumentos de ferramenta de cada codec, o handler de `compress` e os frames JSON-RPC do plugin.
- Testes do proxy com upstreams falsos (`httptest` em loopback) para os quatro dialetos, inclusive streaming e o modo WebSocket do Responses. Launchers, `attach` e MITM rodam com binários de cliente substitutos.
- Testes ponta a ponta (`e2e/`) rodam o binário real contra um upstream falso para Anthropic Messages e OpenAI Chat Completions: 80 turnos além de duas vezes a janela de 128K, dobras e um ciclo de decompress, invariantes verificados ao final, e um reinício do proxy no meio da execução.
- Vetores de conformidade do plugin, reproduzidos por Go, Python e Node.
- A imagem Docker foi compilada e executada uma vez, localmente, em uma etapa anterior. Não foi verificada de novo nesta revisão.

Não validado:

- As APIs reais dos provedores (Anthropic, OpenAI, Google): detalhes de wire, casos extremos de streaming, valores de `usage`, comportamento de cache e preços.
- O endpoint WebSocket real da OpenAI Responses.
- Os clientes de agente reais (Claude Code, Codex, Gemini CLI, OpenCode) rodando pelo proxy, MITM, proxy do sistema ou modo transparente.
- `attach --system-proxy` e `--transparent`, e os backends de proxy do sistema de macOS e Windows, em sistemas reais.
- Uma execução do workflow de CI no GitHub Actions (incluindo o job do Docker), e a qualidade dos resumos em sessões reais longas.

Não implementado:

- O fallback com iptables para o modo transparente (somente nftables).
- Interceptação IPv6: o redirecionamento transparente casa apenas IPv4.
- Merge TOML em configurações temporárias (somente JSON e YAML).
- `search_context` e `retrieve` como ferramentas que o proxy injeta na requisição do modelo. Estão disponíveis pelo plugin e pelo companheiro MCP.
- Fail-open para um redirecionamento transparente que travou: a recuperação é `attach --restore`, executado manualmente.

## Limitações e riscos

- **Compactação com perdas.** Um resumo substitui, na janela, as mensagens que cobre. O modelo pode resumir de forma incorreta ou omitir detalhes. Os originais são recuperáveis com `decompress`, mas só se o modelo ou você pedir.
- **Custo de cache.** Reescrever uma parte anterior do prompt invalida o cache de prompt do provedor. O portão de cache reduz esse custo, mas não o elimina. Os preços de cache são específicos de cada provedor e precisam ser definidos por você.
- **As contagens de tokens são estimativas.** Elas são calibradas com os valores de `usage` do provedor, mas não são exatas.
- **Pré-1.0.** Não existe tag de versão ainda, e a especificação (v0.1) e a API da biblioteca podem mudar.

## Licença

Licença: a definir
