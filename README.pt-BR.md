# Accordion Context (`acd-ctx`)

[English](README.md) | **Português (Brasil)** | [Español (España)](README.es.md)

Accordion Context é um proxy local que mantém sessões longas de agentes de código dentro de uma janela de contexto limitada. O modelo resume partes antigas da conversa, os resumos substituem as mensagens originais na janela, e os originais ficam em um banco SQLite para que possam ser recuperados.

> **Status: em desenvolvimento inicial (pré-1.0).** O proxy, os launchers de clientes, os comandos `project`, `config`, `attach`, `doctor` e `mcp`, o protocolo HTTP de plugin com seus clientes Python e Node, e a biblioteca `pkg/acdctx` estão implementados e passam em `go test ./...`. Nada foi validado contra as APIs reais dos provedores nem contra os binários reais dos clientes. Veja [Status](#status).

## O que é

Agentes de código como Claude Code, Codex, Gemini CLI e OpenCode podem rodar por dias, mas o modelo enxerga uma janela de 100K a 200K tokens. Accordion Context reimplementa em Go o modelo de compactação de contexto do [billion-context](https://github.com/ranxianglei/billion-context) (`acp-kernel`), com estas propriedades:

- **Incremental.** Só é dobrado conteúdo novo que já foi consumido. O histórico inteiro nunca é reescrito de uma vez.
- **Reversível.** Os originais nunca são apagados. Um intervalo dobrado sai da janela renderizada e pode ser recuperado.
- **Hierárquico.** Os resumos são organizados em tiers: T2 resume blocos T1, e T3 resume blocos T2.
- **O modelo escreve os resumos.** O sistema decide quando pedir um resumo, qual intervalo oferecer e como reconstruir a janela.
- **Proxy mais SQLite como fonte de verdade.** SQLite (modo WAL, FTS5, driver puro Go `modernc.org/sqlite`) guarda o log de eventos append-only, blocos, sessões e amostras de cache.

Diferenças em relação ao billion-context, conforme a especificação de design `compactacao-contexto-go-sqlite.md` (v0.1; §1 a §3):

- Go no lugar de TypeScript, com um pacote `internal/kernel` puro, sem I/O.
- O gatilho é uma única tabela de decisão, implementada como a função pura `kernel.DecideNudge`. Seus testes de tabela são a especificação executável.
- Uma compactação só conta quando existe uma linha em `compression_events` no banco. Texto com cara de marcador de compactação na saída do modelo não é confiável.
- Invariantes nomeadas (I1 a I9) são verificadas por checagens SQL no store e por testes de propriedade.
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
| OpenAI Responses | `/v1/responses` | **Somente sem estado.** Requisições com `previous_response_id` ou `conversation` são encaminhadas sem alteração. O modo WebSocket não é tratado. |
| Google Gemini | `.../models/<modelo>:generateContent`, e `:streamGenerateContent` com `alt=sse` | Os streams são bufferizados por rodada. Sem `alt=sse`, a requisição é encaminhada sem alteração. |

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

Instalação pelo caminho do módulo:
Instalação pelo caminho do módulo, depois que uma versão for marcada com tag:
```sh
go install github.com/fsvxavier/accordion-context/cmd/acd-ctx@latest
```

Ainda não existe tag de versão, então `@latest` não resolve hoje. Até lá, instale um commit específico com `go install github.com/fsvxavier/accordion-context/cmd/acd-ctx@<commit>`, ou compile a partir do código-fonte como acima. O valor de `version` é `dev`, a menos que o binário seja compilado com `-ldflags "-X main.version=<tag>"`.

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

O modo base-URL aponta um proxy já existente para um cliente, manualmente. Há duas formas de caminho:

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
| `--listen` | `127.0.0.1:8787` | Precisa ser um endereço de loopback; caso contrário o comando se recusa a iniciar. |
| `--upstream` | `https://api.anthropic.com` | Upstream usado em requisições sem prefixo `/ctx/<url>/`. |
| `--allow` | origem de `--upstream` | Origens separadas por vírgula acessíveis por `/ctx/<url>/`. |
| `--db` | `~/.local/share/acd-ctx/acd-ctx.db` | Caminho do banco SQLite. |
| `--config` | caminho de configuração do usuário | Arquivo de configuração global. |
| `--markers` | `go.mod,package.json,pyproject.toml,composer.json,Cargo.toml` | Marcadores de raiz de projeto. |
| `--plugin-token-file` | `~/.local/share/acd-ctx/plugin.token` | Arquivo que recebe o token bearer do plugin, gerado a cada execução (modo `0600`). Se não puder ser gravado, o protocolo de plugin é desativado e o proxy continua funcionando. |

## Modos de integração

| Modo | Comando | Situação | Observações |
|---|---|---|---|
| **Base URL** | `acd-ctx serve` mais a variável de URL base do cliente | Implementado | Verificado com requisições HTTP. Clientes reais não foram executados. |
| **Launcher** | `acd-ctx claude`, `codex`, `gemini`, `opencode`, `run -- <cmd>` | Implementado para claude, codex e gemini | `run` aceita apenas um comando que corresponda a um perfil embutido. `opencode` não tem variável de URL base e é recusado; use `attach`. Testado com binários substitutos. |
| **Attach** | `acd-ctx attach [--mitm-domain <host>] -- <cmd> [args]` | Implementado (graus base-URL e MITM) | Testa cada grau antes de iniciar o cliente e usa o primeiro que funciona. Qualquer comando funciona no modo console. |
| **MITM** | o grau `mitm` do `attach` | Implementado | Só hosts da lista são terminados. Os demais são tunelados sem alteração. A CA é por instalação e não é adicionada ao repositório de certificados confiáveis do sistema. |
| **Proxy de sistema, transparente, configuração temporária** | `attach --system-proxy` / `--transparent`; arquivo temporário de URL base (grau 4 da spec) | Não implementado | As duas flags são recusadas com erro. O `attach` nunca tenta o grau 4. |
| **Plugin** (HTTP loopback, spec §19.4) | `acd-ctx serve`, em `/__acd-ctx/plugin/` | Implementado | Token bearer criado a cada execução. `compact` retorna 501 e `search_context` falha (o store ainda não tem API de busca). Veja [Protocolo de plugin e middleware](#protocolo-de-plugin-e-middleware). |
| **Middleware** | `middleware/python`, `middleware/node` | Implementado | Clientes do protocolo de plugin sem dependências, testados com os vetores de conformidade. |
| **Companheiro MCP** | `acd-ctx mcp` (stdio) | Implementado | Ferramentas: `decompress`, `retrieve`, `search_context`, `ctx_status`. `search_context` hoje retorna erro. |
| **Biblioteca Go** | `pkg/acdctx` | Implementado | `Open` e `Client` (Turn, Compress, Decompress, Recall), `NewTransport` (um `http.RoundTripper`) e `Handler`. A API pode mudar antes da 1.0. |
| **Doctor** | `acd-ctx doctor [--json] [cliente]` | Implementado | Verifica a instalação e a integração. Código de saída 0 para PASS, 3 para WARN, 1 para FAIL. |

## Configuração

Os valores são resolvidos por chave, da menor para a maior precedência:

1. Padrões embutidos.
2. Arquivo global: `$XDG_CONFIG_HOME/acd-ctx/config.yaml`, ou `~/.config/acd-ctx/config.yaml` no Linux.
3. Sobreposições `projects` no arquivo global, para cada padrão que casa com a raiz do projeto.
4. Arquivo local do projeto: `<raiz do projeto>/.acd-ctx/config.yaml`.
5. Variáveis de ambiente `ACD_CTX_*`.
6. Flags explícitas.

Exemplo, com chaves reais (carrega sem avisos):

```yaml
# ~/.config/acd-ctx/config.yaml
trigger:
  u_high: 0.75          # começa a compactar em 75% da janela
  step_base: 50000      # tokens entre nudges, antes do ajuste adaptativo
compress:
  preserve_recent_messages: 3
ccr:
  crush: true           # reduz saídas de ferramenta muito grandes de forma determinística
storage: global         # ou "local": guarda o banco do projeto em .acd-ctx/
```

As chaves reais, com seus padrões (`config show --effective` mostra a mesma lista). A coluna `local` indica se o `.acd-ctx/config.yaml` de um projeto pode definir a chave:

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

As chaves com os prefixos `upstream.`, `credentials.`, `listen.` e `tools.external_summary.` são reservadas. Elas são ignoradas com um aviso, tanto no arquivo global quanto no local.

Variáveis de ambiente usam a chave em maiúsculas, com pontos trocados por sublinhados:

```sh
ACD_CTX_TRIGGER_STEP_BASE=60000 acd-ctx claude
```

Mostra o valor efetivo e a origem de cada chave:

```sh
acd-ctx config show --effective
```

O proxy recarrega a configuração de cada raiz de projeto no máximo a cada 30 segundos, então a maioria das edições não exige reinício.

### Nota de segurança sobre a configuração local

`<projeto>/.acd-ctx/config.yaml` fica dentro do repositório em que você trabalha, então pode vir de um clone que você não escreveu. Ele pode alterar o comportamento de gatilho, compactação, CCR, recall e armazenamento, mas não `upstream.*`, `credentials.*` ou `listen.*`. Revise-o como qualquer outro arquivo do projeto.

## Separação por projeto

- **Detecção.** Subindo a partir do diretório de trabalho, vale a primeira correspondência: um diretório `.acd-ctx/`, depois `.git`, depois um marcador de raiz. Sem nenhum, o próprio diretório de trabalho é a raiz.
- **ID do projeto.** Um projeto com `.acd-ctx/project-id` usa esse valor. Caso contrário, o ID são os 16 primeiros caracteres hexadecimais do SHA-256 do caminho da raiz.
- **Requisições.** O proxy obtém o projeto do prefixo `/p/<id>/` e, depois, do cabeçalho `X-Acd-Ctx-Project-Dir`. A conversa vem de `X-Acd-Ctx-Conversation`. Requisições sem sinal de projeto usam o projeto `default` e registram um aviso.
- **Armazenamento.** `global` guarda todos os projetos em um único banco. `local` guarda um projeto em `.acd-ctx/acd-ctx.db`.

Comandos de projeto:

```sh
acd-ctx project init [--local-storage]   # cria .acd-ctx/ na raiz do projeto
acd-ctx project show                     # projeto detectado, raiz, armazenamento e banco
acd-ctx project list                     # projetos no banco global
acd-ctx project move <antigo> <novo>     # reaponta a raiz de um projeto movido
acd-ctx project migrate --to local|global
acd-ctx project purge <id> [--yes]       # apaga todos os dados de um projeto
```

## Variáveis de ambiente

| Variável | Usada por | Significado |
|---|---|---|
| `ACD_CTX_<CHAVE>` | carregador de configuração | Sobrescreve uma chave de configuração, por exemplo `ACD_CTX_CCR_CRUSH=false`. |
| `ACD_CTX_DB` | comandos de CLI, `attach`, `doctor` | Caminho do banco global. O `serve` usa `--db`. |
| `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `HOME` | caminhos | Diretórios base. O `serve` ignora `XDG_DATA_HOME`. |
| `ANTHROPIC_BASE_URL`, `OPENAI_BASE_URL`, `GOOGLE_GEMINI_BASE_URL` | seu cliente | Apontam o cliente para o proxy no modo base-URL. Definidas pelos launchers. |
| `HTTPS_PROXY`, `SSL_CERT_FILE`, `NODE_EXTRA_CA_CERTS`, `REQUESTS_CA_BUNDLE`, `CURL_CA_BUNDLE` | `attach` (grau MITM) | Definidas só para o processo do cliente, para que ele confie na CA local. |
| `ACD_CTX_URL`, `ACD_CTX_TOKEN`, `ACD_CTX_TOKEN_FILE`, `ACD_CTX_CONVERSATION` | clientes em `middleware/` | Localizam e autenticam no protocolo de plugin. Veja [Protocolo de plugin e middleware](#protocolo-de-plugin-e-middleware). |

## Endpoints

Endpoints de controle:

| Caminho | Método | Resposta |
|---|---|---|
| `/__acd-ctx/health` | GET | `{"ok":true}` |
| `/__acd-ctx/status` | GET | JSON com `status`, `version`, `uptime_seconds`, `upstream_host`, `context_limit`, `max_loop`, `sessions_loaded`, `projects_seen`, `default_project_requests` e `protocols`. |

As requisições de API usam os caminhos de protocolo de [Protocolos suportados](#protocolos-suportados), opcionalmente atrás de `/p/<id>/` e `/ctx/<url>/`. O companheiro MCP não tem endpoint HTTP; ele fala JSON-RPC sobre stdio.

## Protocolo de plugin e middleware

O protocolo de plugin (spec §19.4) permite que um programa que monta os próprios prompts peça ao proxy a janela visível de uma conversa e execute as ferramentas de contexto. O `acd-ctx serve` o monta em `/__acd-ctx/plugin/`. Toda requisição exige `Authorization: Bearer <token>`. O `serve` cria um token aleatório novo a cada execução e o grava no arquivo indicado por `--plugin-token-file` (modo `0600`). As guardas de Host e Origin do proxy se aplicam.

| Caminho (sob `/__acd-ctx/plugin/`) | Método | Finalidade |
|---|---|---|
| `manifest` | GET | Nome e versão do protocolo (`acd-ctx-plugin`, 1), ferramentas e doutrina. |
| `runtime-info` | POST | Declara a conversa, o modelo e a janela de contexto. |
| `turn` | POST | Envia mensagens e uma contagem de tokens; recebe a decisão, as dicas e a janela visível. |
| `tool` | POST | Executa `compress`, `decompress`, `retrieve`, `search_context` ou `ctx_status`. |
| `status` | GET | Mesmos dados de `ctx_status`, para `?conversationId=`. |
| `compact` | POST | Não implementado: sempre HTTP 501. |

Os clientes ficam em `middleware/python` (pacote `acd_ctx`, só biblioteca padrão) e `middleware/node` (pacote `acd-ctx`, ESM, Node.js 18.13 ou superior, sem dependências). Ambos leem `ACD_CTX_URL` (padrão `http://127.0.0.1:8787`), `ACD_CTX_TOKEN` ou o arquivo em `ACD_CTX_TOKEN_FILE` (padrão `~/.local/share/acd-ctx/plugin.token`) e `ACD_CTX_CONVERSATION`. Em qualquer erro do cliente, siga sem compactação.

`conformance/vectors/*.json` são vetores de requisição e expectativa neutros quanto à linguagem. `go test ./conformance/...` os reproduz contra o handler de plugin real. As suítes de Python e Node reproduzem os mesmos arquivos contra um servidor falso. Defina `ACD_CTX_CONFORMANCE_URL` e `ACD_CTX_CONFORMANCE_TOKEN` para reproduzir contra um `acd-ctx serve` em execução.

`docs/compatibility.md` é a matriz de compatibilidade gerada, com protocolos e perfis de cliente. O CI falha quando o arquivo versionado difere da saída de `go run ./scripts/gen-compat-matrix`.

## Notas de segurança

- **Somente loopback.** O `serve` recusa um `--listen` que não seja de loopback. O proxy carrega credenciais de API.
- **Sem autenticação no listener de base URL.** Qualquer processo local pode enviar requisições para `/ctx/<host da allowlist>/...` na porta de loopback (só o `CONNECT` do MITM e o plugin exigem token). O proxy repassa as credenciais do próprio chamador e nunca acrescenta as suas, então isso é um relay para hosts da allowlist, não um vazamento de chave. Use em uma máquina de um único usuário.
- **Guarda de Host.** As requisições só são aceitas quando o cabeçalho `Host` nomeia `localhost`, `127.0.0.1`, `::1` ou o host de escuta. Outros nomes recebem HTTP 403, o que bloqueia DNS rebinding.
- **Acesso pelo navegador.** Requisições de preflight CORS recebem HTTP 403. Os endpoints de controle em `/__acd-ctx/` recusam qualquer requisição com cabeçalho `Origin`.
- **Token do plugin.** Os endpoints de plugin exigem um token bearer, comparado em tempo constante. Ele é regenerado a cada execução do `serve` e guardado em um arquivo com modo `0600`.
- **Lista de upstreams.** Um alvo `/ctx/<url>/` só é usado se sua origem estiver em `--allow`. Redirecionamentos são devolvidos ao cliente, não seguidos.
- **Credenciais.** Cabeçalhos de credencial são encaminhados ao upstream. Os logs registram IDs de sessão, etapas e erros, não cabeçalhos de requisição.
- **Fail-open.** Um erro de compactação encaminha a requisição sem alteração.
- **Limites.** Corpos de requisição são limitados a 32 MiB. O diretório do banco é criado com modo `0700`, e a chave da CA do MITM com `0600`.

## Desenvolvimento

```sh
go test ./...                                  # suíte completa
go test ./internal/kernel -run TestName        # um teste
go test -fuzz=FuzzParseToolArguments ./internal/wire/openai
go test -fuzz=FuzzDecodeFrame ./internal/plugin
go test -race ./...                            # como no CI
go test ./conformance/...                      # vetores do protocolo de plugin
(cd middleware/python && python3 -m unittest discover -s tests)
(cd middleware/node && node --test)
go run ./scripts/gen-compat-matrix             # imprime docs/compatibility.md
```

O `CLAUDE.md` fica em inglês. A especificação de design `compactacao-contexto-go-sqlite.md` está em português e é a fonte de verdade. Os números de seção (§N) nos comentários do código referem-se a ela.

O CI (`.github/workflows/ci.yml`) executa `gofmt`, `go vet`, `go test -race`, os vetores de conformidade, os testes dos clientes Python e Node, e verifica se `docs/compatibility.md` está atualizado.

| Pacote | Papel |
|---|---|
| `internal/kernel` | Lógica pura: gatilho, tiers, renderização, recall, CCR e crush, custo de cache. Sem I/O. |
| `internal/store` | Acesso ao SQLite (WAL, FTS5), esquema e checagens de invariantes. |
| `internal/session` | Orquestração de turnos, ferramentas e transação de compactação. O único escritor. |
| `internal/proxy` | Servidor HTTP, roteamento, adaptadores de protocolo, laço de ferramentas no servidor, tratamento de forks. |
| `internal/wire/...` | Codecs para `anthropic`, `openai`, `responses` e `google`. |
| `internal/config` | Configuração em camadas e relato da origem efetiva. |
| `internal/project` | Detecção da raiz e do ID do projeto. |
| `internal/launcher` | Perfis de cliente e execução de processo com variáveis de ambiente. |
| `internal/attach` | Escada de integração do `attach`: testes, graus console e MITM. |
| `internal/mitm` | CA local, certificados de folha e interceptação TLS restrita. |
| `internal/plugin` | Servidor MCP em stdio e o handler HTTP de plugin montado pelo `serve`. |
| `internal/doctor` | Verificações de instalação e de integração. |
| `internal/cli` | Implementação dos comandos por trás de `cmd/acd-ctx`. |
| `pkg/acdctx` | Biblioteca Go pública: API de cliente, transporte HTTP e handler. |
| `middleware/python`, `middleware/node` | Clientes do protocolo de plugin (sem dependências). |
| `conformance` | Vetores de plugin neutros quanto à linguagem e o runner em Go. |
| `scripts/gen-compat-matrix` | Gera `docs/compatibility.md`. |

## Status

Validado por testes automatizados, sem acesso à rede além de loopback:

- Testes unitários e de tabela do kernel, store, config, project, launcher, attach, MITM e doctor.
- Testes de propriedade (`pgregory.net/rapid`) que verificam as invariantes após passos aleatórios de compactação.
- Alvos de fuzzing para os parsers de argumentos de ferramenta de cada codec, o handler de `compress` e os frames JSON-RPC do plugin.
- Testes do proxy contra upstreams falsos (`httptest` em loopback) para os quatro dialetos, incluindo streaming.
- Testes ponta a ponta (`e2e/`) executam o binário real contra um upstream falso para Anthropic Messages e OpenAI Chat Completions: 80 turnos além de duas vezes uma janela de 128K, dobras e um ciclo de descompressão, invariantes verificados após a execução, e um reinício do proxy no meio da execução.
- Vetores de conformidade do plugin, reproduzidos por Go, Python e Node.
- Launchers, `attach` e MITM com binários de cliente substitutos.

Não validado:

- As APIs reais dos provedores (Anthropic, OpenAI, Google): detalhes do protocolo, casos extremos de streaming, valores de `usage`, comportamento e preços de cache.
- Os clientes de agente reais (Claude Code, Codex, Gemini CLI, OpenCode) rodando pelo proxy ou pelo MITM.
- A qualidade dos resumos em sessões reais longas.

## Limitações e riscos

- **Compactação com perdas.** Um resumo substitui, na janela, as mensagens que cobre. O modelo pode resumir de forma incorreta ou omitir detalhes. Os originais são recuperáveis com `decompress`, mas só se o modelo ou você pedirem.
- **Custo de cache.** Reescrever uma parte anterior do prompt invalida o cache de prompt do provedor. O portão de cache reduz esse custo, mas não o elimina. Os preços de cache são específicos de cada provedor e precisam ser definidos por você.
- **Não validado com APIs reais nem com clientes reais.** Veja [Status](#status). Os testes usam upstreams falsos e binários de cliente substitutos.
- **WebSocket não tratado.** O tráfego WebSocket do Responses não é compactado.
- **Responses com estado não compactado.** Requisições encadeadas por `previous_response_id` ou `conversation` são encaminhadas sem alteração.
- **Não implementado.** `attach --system-proxy`, `--transparent` e o grau de configuração temporária; o endpoint `compact` do plugin; `search_context` no plugin e no companheiro MCP.
- **Contagens de tokens são estimativas.** Elas são calibradas com os valores `usage` do provedor, mas não são exatas.
- **Pré-1.0.** Ainda não existe tag de versão, e a especificação (v0.1) e a API da biblioteca podem mudar.


## Licença

Licença: a definir
