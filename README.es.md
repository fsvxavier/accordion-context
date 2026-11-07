# Accordion Context (`acd-ctx`)

[English](README.md) | [Português (Brasil)](README.pt-BR.md) | **Español (España)**

Accordion Context es un proxy local que mantiene las sesiones largas de agentes de programación dentro de una ventana de contexto limitada. El modelo resume las partes antiguas de la conversación, los resúmenes sustituyen a los mensajes originales en la ventana, y los originales se conservan en una base de datos SQLite para poder recuperarlos.

> **Estado: 1.0.0.** El proxy (con sus cuatro herramientas internas), los lanzadores de clientes, el camino `attach` (incluidos los grados opcionales de proxy del sistema y transparente), la copia de seguridad, la restauración, la limpieza, los endpoints de observabilidad, el protocolo HTTP de plugins con sus clientes y la biblioteca `pkg/acdctx` están implementados y superan `go test ./...`. Consulte [Estado](#estado).

## Qué es

Agentes de programación como Claude Code, Codex, Gemini CLI y OpenCode pueden ejecutarse durante días, pero el modelo solo ve una ventana de 100K a 200K tokens. Accordion Context reimplementa en Go el modelo de compactación de contexto de [billion-context](https://github.com/ranxianglei/billion-context) (`acp-kernel`), con estas propiedades:

- **Incremental.** Solo se pliega contenido nuevo que ya se ha consumido. El historial completo nunca se reescribe de una vez.
- **Reversible.** Los originales nunca se borran. Un intervalo plegado deja de mostrarse en la ventana y puede recuperarse.
- **Jerárquico.** Los resúmenes se organizan en niveles: T2 resume bloques T1, y T3 resume bloques T2.
- **El modelo escribe los resúmenes.** El sistema decide cuándo pedir uno, qué intervalo ofrecer y cómo reconstruir la ventana.
- **Proxy más SQLite como fuente de verdad.** SQLite (modo WAL, FTS5, driver puro Go `modernc.org/sqlite`) guarda el registro de eventos de solo adición, los bloques, las sesiones y las muestras de caché.

Diferencias con billion-context, según la especificación de diseño `compactacao-contexto-go-sqlite.md` (en portugués, v0.1; §1 a §3):

- Go en lugar de TypeScript, con un paquete `internal/kernel` puro, sin E/S.
- El disparador es una única tabla de decisión, implementada como la función pura `kernel.DecideNudge`. Sus pruebas de tabla son la especificación ejecutable.
- Una compactación solo cuenta cuando existe una fila en `compression_events` en la base de datos. El texto con aspecto de marcador de compactación en la salida del modelo no se da por bueno.
- Los invariantes con nombre (I1 a I9) se verifican con comprobaciones SQL, con pruebas de propiedades y, para el tamaño de la ventana (I8), con un truncamiento de emergencia.
- El coste de la caché se modela de forma explícita, y una compuerta de caché puede aplazar un nudge.

## Cómo funciona

1. La petición del agente llega al proxy, que registra cada mensaje en la sesión con una referencia estable (por ejemplo `m00451`).
2. El disparador decide si la compactación toca. La compuerta de caché puede aplazar el nudge cuando una reescritura rompería la caché de prompt del proveedor a un coste alto.
3. Cuando toca, el proxy añade un nudge que enumera los intervalos compactables. El modelo responde con una llamada `compress`: intervalos más un resumen.
4. El proxy valida la llamada y escribe el bloque y los intervalos ocultos en una sola transacción.
5. La ventana siguiente muestra el resumen en lugar de los mensajes plegados. Los originales siguen en SQLite.
6. El proxy añade cuatro herramientas internas a cada petición, en este orden: `compress`, `decompress`, `search_context` y `retrieve`. Cuando el modelo llama a `decompress`, `search_context` o `retrieve`, el proxy responde desde la base de datos. Las llamadas internas se ejecutan dentro del proxy (hasta 4 rondas por petición, por defecto) y no llegan al cliente.
7. Cualquier fallo en el camino de compactación es fail-open: la petición original se reenvía sin cambios.

## Protocolos soportados

El proxy reconoce cuatro dialectos de API en `POST`:

| Dialecto | Endpoint que reconoce el proxy | Observaciones |
|---|---|---|
| Anthropic Messages | `/v1/messages` | El streaming se reenvía según llega. |
| OpenAI Chat Completions | `/v1/chat/completions` | Cada ronda del upstream se lee completa antes de enviarse. |
| OpenAI Responses | `/v1/responses` | En HTTP, las peticiones son sin estado: las que usan `previous_response_id` o `conversation` se reenvían sin cambios. Los upgrades WebSocket se compactan por cada frame `response.create`. Los ítems de entrada de otros tipos, como `compaction` o `local_shell_call`, se mantienen como ítems opacos y se devuelven sin cambios, en lugar de hacer fallar la petición. |
| Google Gemini | `.../models/<modelo>:generateContent`, y `:streamGenerateContent` con `alt=sse` | Los streams se almacenan en búfer por ronda. Sin `alt=sse`, la petición se reenvía sin cambios. |

Las demás rutas, y los demás métodos, se reenvían al upstream sin compactar.

## Comandos de compactación del cliente

Los clientes de programación también compactan su propio historial: `/compact` en Claude Code, Codex, OpenCode, Kilo Code y Cline, `/compress` en Gemini CLI, y la compactación automática cerca del límite. Cada uno envía una petición que pide al modelo resumir la transcripción. acd-ctx reconoce esa petición y la reenvía sin cambios: sin ingesta, sin herramientas inyectadas y sin nudge, de modo que el cliente resume su transcripción real.

La petición se cuenta como resultado `client_compaction` y en la métrica `acd_ctx_client_compactions_total{protocol,signal}`, donde `signal` nombra la regla que coincidió. Se registra en INFO, no como aviso de fail-open. Si la sesión ya existe, se graba en ella un evento `client_compaction`. Hasta una hora después de la compactación, la siguiente sesión nueva del mismo proyecto, o la siguiente petición que lleve `x-claude-code-context-compacted`, recibe un evento `client_compaction_parent` que la vincula con la sesión compactada. Ningún mensaje se copia a la sesión nueva; los mensajes antiguos siguen en SQLite y se pueden encontrar con `search_context` en el ámbito del proyecto.

| Cliente | Señal | Por defecto | Confianza |
|---|---|---|---|
| Claude Code | Cabecera `x-claude-code-compaction` (`auto`, `manual` o `reactive`), o `x-claude-code-request-class: compaction` | Activa. El lanzador `claude` define `CLAUDE_CODE_GATEWAY_HINT_HEADERS=1`; otras configuraciones deben definirla | Alta. La cabecera está documentada. No hay regla de texto, porque el texto del prompt no se publica |
| Codex, proveedores OpenAI y Azure, HTTP y WebSocket | El último elemento de entrada es `compaction_trigger` | Activa | Alta |
| Codex, versiones antiguas | `POST /v1/responses/compact` | Activa | Alta para esas versiones |
| Codex, otros proveedores (compactación local) | El último mensaje del usuario empieza con "You are performing a CONTEXT CHECKPOINT COMPACTION." | `strong` | Media a alta. Un `compact_prompt` personalizado no se reconoce |
| Gemini CLI | `generateContent` sin streaming, con instrucción de sistema, sin `tools` y sin `generationConfig` (la forma). El texto de sistema "distilling chat history into a structured XML <state_snapshot>" también se reconoce, junto con la forma | Activa | Alta para el texto. La forma sola es estructural y puede coincidir con una llamada SDK normal con instrucción de sistema, sin tools y sin `generationConfig` |
| OpenCode v1 y Kilo Code (prompt `compaction.txt`) | Texto de sistema "You are a context summarization agent. You are given a conversation between a user and an agent.", y un último mensaje del usuario que contiene "Create a new anchored summary from the conversation history in the <conversation> tags above" | `strong` | Alta |
| Kilo Code, proveedor Kilo gateway | Cabecera `x-kilocode-mode: compaction` | Activa | Alta. También aparece en el resumen de traspaso de plan de Kilo, que es inofensivo |
| OpenCode v2 | El último mensaje del usuario empieza con "You MUST summarize the conversation above into a structured summary", o con "Update the existing checkpoint in the conversation above" | solo `all` | Media a alta. Un usuario que cite el texto produce un falso positivo |
| Cline SDK | Texto de sistema "Summarize the provided coding session into a concise continuation note with detailed next steps.", y un último mensaje del usuario que empieza con "Summarize this session for continuation. Be concise and factual." | `strong` | Alta |
| Cline clásico | El último mensaje del usuario contiene `<explicit_instructions type="summarize_task">` o `type="condense"` | `strong` | Alta. En el cliente clásico esto es un turno normal, así que el turno entero se reenvía sin cambios |
| Copilot CLI, Cursor, Windsurf | Ninguna | n/a | No detectado |

`compaction.text_rules` (solo en el fichero global) elige las reglas de texto: `strong` (por defecto) activa las reglas marcadas como `strong` arriba, `all` activa además las reglas de OpenCode v2, y `off` desactiva todas las reglas de texto. Las señales de cabecera, de elemento, de ruta, la cabecera de Kilo y la forma de Gemini no dependen de ella.

Cada texto de regla se leyó del código fuente de los clientes, en los commits que enumera [docs/compaction-commands.md](docs/compaction-commands.md). OpenCode v2 y Cline se detectan solo por texto.

Límites conocidos:
- Codex `custom_tool_call`, `local_shell_call` y cualquier otro tipo de elemento de entrada que el codec no modela son barreras opacas: se mantienen en su sitio, se devuelven sin cambios, nunca se compactan, y ningún intervalo de compactación puede cubrir uno de ellos.
- La regla de forma de Gemini puede coincidir con una llamada SDK normal que tenga instrucción de sistema, sin `tools` y sin `generationConfig`.
- El vínculo padre apunta a cualquier sesión nueva del proyecto dentro de una hora, y solo se guarda en memoria: un reinicio lo descarta.
- Si la sesión todavía no existe, la petición se cuenta y se registra, pero no se graba ningún evento.

## Instalación

Requisitos: Go 1.27.1 o posterior (la directiva `go` de `go.mod`). No hace falta compilador de C: SQLite viene de `modernc.org/sqlite`, que es Go puro.

Compilar desde el clon (verificado, también con `CGO_ENABLED=0`):

```sh
git clone https://github.com/fsvxavier/accordion-context.git
cd accordion-context
go build -o acd-ctx ./cmd/acd-ctx
./acd-ctx help
```

Instale una versión publicada en `$(go env GOPATH)/bin` con `go install`. El toolchain debe ser al menos la versión `go` de `go.mod`:

```sh
go install github.com/fsvxavier/accordion-context/cmd/acd-ctx@v1.0.0
acd-ctx help
```

`@latest` se resuelve a la etiqueta `v*` más reciente. `@main` instala la punta de `main` en lugar de una versión publicada, y `@<commit>` fija un commit. Un binario compilado con `go install` informa `dev` como `version`.

Cada etiqueta `v*` también recibe archivos precompilados de GoReleaser en la [publicación de GitHub](https://github.com/fsvxavier/accordion-context/releases): `tar.gz` para Linux y macOS, `zip` para Windows, cada uno para `amd64` y `arm64`, con un `checksums.txt`. Esos binarios informan la etiqueta como `version`. Véase [docs/release.md](docs/release.md).

## Docker

La imagen es un build estático y sin privilegios de root (distroless, uid 65532), con los datos en el volumen `acd-ctx-data`. Compose publica el puerto solo en `127.0.0.1`:

```sh
ACD_CTX_VERSION=$(acd-ctx version | awk '{print $2}') docker compose up -d --build
docker exec acd-ctx /acd-ctx healthcheck --port 8787   # código 0 cuando está sano
```

Compile la imagen con la misma versión que el binario del host: el lanzador del host reutiliza un proxy en marcha solo cuando la versión coincide. Consulte [docs/docker.md](docs/docker.md) para la configuración, la copia de seguridad, la actualización y las notas de seguridad.

## Inicio rápido

Arranque el proxy en loopback, reenviando a `api.anthropic.com`:

```sh
acd-ctx serve
curl -s http://127.0.0.1:8787/__acd-ctx/health
# {"ok":true}
```

El modo lanzador inicia un proxy propio en un puerto libre de loopback (o reutiliza un proxy sano de la misma versión), define la URL base del cliente y detiene el proxy cuando el cliente termina:

```sh
acd-ctx claude        # define ANTHROPIC_BASE_URL solo para este proceso
```

El modo URL base apunta a mano un proxy ya en marcha hacia un cliente. Hay dos formas de ruta:

```sh
# Forma simple: upstream por defecto; proyecto por la cabecera X-Acd-Ctx-Project-Dir o "default"
ANTHROPIC_BASE_URL=http://127.0.0.1:8787 claude

# Forma explícita: ID del proyecto en /p/<id>/, upstream en /ctx/<url>/ (debe estar permitido)
ANTHROPIC_BASE_URL=http://127.0.0.1:8787/p/<project-id>/ctx/https://api.anthropic.com claude

# Otro proveedor necesita su origen en --allow.
acd-ctx serve --allow https://api.openai.com
OPENAI_BASE_URL=http://127.0.0.1:8787/ctx/https://api.openai.com/v1 codex
```

Opciones de `serve` (todas opcionales):

| Opción | Valor por defecto | Significado |
|---|---|---|
| `--listen` | `127.0.0.1:8787` | Debe ser loopback, salvo que `--allow-non-loopback` y `--allowed-hosts` estén ambos definidos (consulte [Notas de seguridad](#notas-de-seguridad)). |
| `--upstream` | `https://api.anthropic.com` | Upstream usado en peticiones sin prefijo `/ctx/<url>/`. |
| `--allow` | origen de `--upstream` | Orígenes separados por comas accesibles mediante `/ctx/<url>/`. Variable `ACD_CTX_ALLOW`. |
| `--allowed-hosts` | nombres de loopback | Valores de la cabecera Host que acepta la protección de Host; sustituyen a los valores por defecto. Variable `ACD_CTX_ALLOWED_HOSTS`. |
| `--db` | `~/.local/share/acd-ctx/acd-ctx.db` | Ruta de la base de datos SQLite. |
| `--config` | ruta de configuración del usuario | Fichero de configuración global. |
| `--markers` | `go.mod,package.json,pyproject.toml,composer.json,Cargo.toml` | Marcadores de raíz de proyecto. |
| `--plugin-token-file` | `~/.local/share/acd-ctx/plugin.token` | Fichero que recibe el token bearer del plugin, generado en cada ejecución (modo `0600`). Un puerto distinto del predeterminado usa `plugin-<puerto>.token`. Si no se puede escribir, el protocolo de plugins se desactiva y el proxy sigue funcionando. |

## Modos de integración

| Modo | Comando | Estado | Observaciones |
|---|---|---|---|
| **URL base** | `acd-ctx serve` más la variable de URL base del cliente | Implementado | Verificado con peticiones HTTP y clientes sustitutos. |
| **Lanzador** | `acd-ctx claude`, `codex`, `gemini`, `opencode`, `run -- <cmd>` | Implementado para claude, codex, gemini y opencode | `run` solo acepta comandos con un perfil conocido. `opencode` se lanza con una configuración temporal (`OPENCODE_CONFIG`). |
| **Attach** | `acd-ctx attach [--profiles f] [--mitm-domain h] -- <cmd>` | Implementado | Comprueba cada grado antes de arrancar el cliente y usa el primero que funciona: variable de URL base, opción de URL base, configuración temporal, consola (cualquier comando) y MITM. |
| **MITM** | el grado `mitm` de `attach` | Implementado | Solo se terminan los hosts de la lista de permitidos. Los demás se tunelan sin tocarlos. La CA es por instalación y no se añade al almacén de confianza del sistema. |
| **Proxy del sistema** | `attach --system-proxy --yes` | Implementado | Apunta el proxy HTTPS del escritorio a acd-ctx durante la ejecución. Backends: GNOME, KDE, macOS, Windows. Cambio en toda la máquina: exige `--yes`. |
| **Transparente** | `attach --transparent --yes --apply` | Implementado para Linux (nftables, o iptables cuando nft no está instalado) | Redirige el TCP 443 de un uid (por defecto, los dominios MITM) al listener local, en IPv4 y, cuando `[::1]` está libre en el puerto de redirección, en IPv6 (con nft, o con iptables cuando `ip6tables` está presente). Exige root o CAP_NET_ADMIN. Sin `--apply`, solo se imprimen las reglas. Un proceso auxiliar restaura la redirección si acd-ctx se cierra. |
| **Plugin** | `acd-ctx serve`, bajo `/__acd-ctx/plugin/` | Implementado | Token bearer por ejecución. Herramientas: `compress`, `decompress`, `retrieve`, `search_context`, `ctx_status`. Consulte [Protocolo de plugins y middleware](#protocolo-de-plugins-y-middleware). |
| **Middleware** | `middleware/python`, `middleware/node` | Implementado | Clientes sin dependencias del protocolo de plugins, probados con los vectores de conformidad. |
| **Compañero MCP** | `acd-ctx mcp` (stdio) | Implementado | Herramientas: `decompress`, `retrieve`, `search_context`, `ctx_status`. |
| **Biblioteca Go** | `pkg/acdctx` | Implementado | `Open` y `Client` (Turn, Compress, Decompress, Recall, Close), `NewTransport` (un `http.RoundTripper`) y `Handler`. Puede cambiar antes de la 1.0. |
| **Doctor** | `acd-ctx doctor [--json] [cliente]` | Implementado | Comprueba la instalación y la integración. Código de salida 0 para PASS, 3 para WARN, 1 para FAIL. |

`attach --profiles fichero.yaml` añade perfiles que se comparan antes que los integrados. Un perfil define `match`, `upstream`, `base_url_env` o `base_url_flag` (con `flag_position`), un `temp_config` (`file`, `format`, `template`, `merge_from`, `key`; `merge_from` lee un fichero existente en formato `json`, `yaml`, `toml`, `env` o `ini` y fija una `key` con puntos; solo se escribe la copia temporal, nunca el origen, y json, yaml y toml se vuelven a serializar, así que los comentarios de la copia combinada se pierden), `env`, `detect` (`binaries`, `config_paths`, `version_regex`), `integration` (`prefer`, `base_url_env`, `https_proxy`, `ca_env`, `domains`), `model_info` y `quirks`.

## Integraciones con otros clientes y gateways

El estado de los clientes procede de [docs/integrations.md](docs/integrations.md), que enumera las fuentes.

| Cliente | Cómo integrar | Estado |
|---|---|---|
| Claude Code CLI | Lanzador integrado `acd-ctx claude`, que define `ANTHROPIC_BASE_URL` y `CLAUDE_CODE_GATEWAY_HINT_HEADERS=1`. | Implementado. |
| Chat de Claude Code en el IDE (VS Code) | Manual: defina `ANTHROPIC_BASE_URL` en `claudeCode.environmentVariables` o en el bloque `env` de `~/.claude/settings.json`. | Documentado. |
| OpenCode | Lanzador `acd-ctx opencode`, que escribe una configuración temporal y apunta `OPENCODE_CONFIG` a ella. `opencode-openai` se alcanza mediante un enlace o wrapper con ese nombre. | Implementado. |
| Kilo Code (extensión de VS Code) | Manual: Settings, Providers, Custom provider. Pegue `http://127.0.0.1:8787/p/<id>/ctx/<upstream>` como Base URL. | Documentado. La CLI de Kilo no tiene perfil. |
| GitHub Copilot Chat (VS Code) | Endpoint personalizado BYOK manual en `chatLanguageModels.json`. Solo chat; el modelo debe soportar tool calling. Una política de la organización puede desactivar BYOK. | Documentado. |
| GitHub Copilot CLI | Sin perfil. | No soportado. |
| Cline (extensión de VS Code) | Manual: proveedor OpenAI Compatible o Anthropic, con el campo de URL base personalizada. | Documentado. |
| Cursor, Windsurf | No soportado: no hay un mecanismo confirmado para una URL base local. | No soportado. |

### Gateways

freellmapi, Bifrost y OmniRoute pueden funcionar detrás de acd-ctx. Use un perfil de cliente con el gateway como upstream mediante `/ctx/` y añada el origen del gateway a `ACD_CTX_ALLOW`, por ejemplo `ACD_CTX_ALLOW=http://127.0.0.1:20128`. Puertos por defecto: freellmapi 3001, Bifrost 8080, OmniRoute 20128.

- **Bifrost** descarta `anthropic-beta` salvo que la cabecera esté en la lista de permitidas. Sin ella, las funciones beta se descartan o se rechazan, y Claude Code puede fallar. Añádala en la configuración de cliente de Bifrost.
- **OmniRoute**: la caché semántica y la compresión reescriben las peticiones. Eso entra en conflicto con el prefijo de prompt estable byte a byte de acd-ctx, así que desactívelas para el tráfico que pasa por acd-ctx.
- **freellmapi**: el failover cambia el modelo entre turnos. Eso entra en conflicto con el dimensionamiento de la ventana (`window.context_limit`) y con la caché de prompt del proveedor. Use un modelo fijo, no `auto` ni `fusion`.

## Configuración

Los valores se resuelven por clave, de menor a mayor precedencia:

1. Valores por defecto integrados.
2. Fichero global: `$XDG_CONFIG_HOME/acd-ctx/config.yaml`, o `~/.config/acd-ctx/config.yaml` en Linux.
3. Sustituciones `projects` en el fichero global, para cada patrón que coincida con la raíz del proyecto.
4. Fichero local del proyecto: `<raíz del proyecto>/.acd-ctx/config.yaml`.
5. Variables de entorno `ACD_CTX_*`.
6. Opciones explícitas.

Ejemplo, con claves reales (se carga sin avisos):

```yaml
# ~/.config/acd-ctx/config.yaml
trigger:
  u_high: 0.75          # empieza a compactar al 75% de la ventana
  step_base: 50000      # tokens entre nudges, antes del ajuste adaptativo
storage: global         # o "local": guarda la base de datos del proyecto en .acd-ctx/
```

Las claves reales, con sus valores por defecto (`config show --effective` imprime la misma lista). La columna `local` indica si el `.acd-ctx/config.yaml` de un proyecto puede definir la clave:

| Clave | Local | Por defecto |
|---|---|---|
| `trigger.u_floor`, `trigger.u_high`, `trigger.u_emerg` | sí | `0.3`, `0.75`, `0.95` |
| `trigger.step_base`, `trigger.step_min`, `trigger.step_max` | sí | `50000`, `30000`, `100000` |
| `trigger.step_read_gain`, `trigger.min_growth_floor`, `trigger.max_refusals`, `trigger.tiers_enabled` | sí | `1`, `20000`, `3`, `true` |
| `cache.ttl`, `cache.margin`, `cache.price_uncached`, `cache.price_cached` | no | `5m`, `1.5`, `0` (sin definir), `0` (sin definir) |
| `compress.preserve_recent_messages`, `compress.preserve_recent_tokens` | sí | `3`, `5000` |
| `compress.min_compress_range`, `compress.min_summary_chars`, `compress.max_summary_chars` | sí | `5000`, `50`, `20000` |
| `compress.protected_tools`, `compress.framing_check` | sí | lista vacía, `false` |
| `compress.user_turn_after_summaries`, `compress.intent_ledger` | sí | `true`, `true` |
| `ccr.threshold_tokens`, `ccr.crush` | sí | `4000`, `true` |
| `recall.assist`, `recall.min_accept_rate` | sí | `true`, `0.05` |
| `storage` | sí | `global` |
| `window.context_limit`, `window.models` | sí | `0` (derivado del modelo), mapa vacío |
| `compaction.text_rules` | no | `strong` (también `all`, `off`) |

Las claves con los prefijos `upstream.`, `credentials.`, `listen.` y `tools.external_summary.` están reservadas. Se ignoran con un aviso, tanto en el fichero global como en el local.

Las variables de entorno usan la clave en mayúsculas, con los puntos sustituidos por guiones bajos:

```sh
ACD_CTX_TRIGGER_STEP_BASE=60000 acd-ctx claude
```

Muestre el valor efectivo y el origen de cada clave:

```sh
acd-ctx config show --effective
```

El proxy vuelve a leer la configuración de cada raíz de proyecto como máximo cada 30 segundos, así que la mayoría de los cambios no requieren reiniciar.

### Nota de seguridad sobre la configuración local

`<proyecto>/.acd-ctx/config.yaml` está dentro del repositorio en el que trabaja, así que puede venir de un clon que usted no escribió. Puede cambiar el comportamiento del disparador, la compactación, CCR, recall, el almacenamiento y la ventana, pero no `upstream.*`, `credentials.*` ni `listen.*`. Revíselo como cualquier otro fichero del proyecto.

## Separación por proyecto

- **Detección.** Subiendo desde el directorio de trabajo, la raíz es el directorio más cercano que contiene `.acd-ctx/`, `.git` o un marcador de raíz; dentro de un mismo directorio el orden es `.acd-ctx/`, después `.git` y después un marcador. Si no se encuentra ninguno, el directorio de trabajo es la raíz.
- **ID del proyecto.** Un proyecto con `.acd-ctx/project-id` usa ese valor. Si no, el ID son los primeros 16 caracteres hexadecimales del SHA-256 de la ruta de la raíz.
- **Peticiones.** El proxy toma el proyecto de la cabecera `X-Acd-Ctx-Project-Dir` (ruta absoluta de un directorio existente) y, después, del prefijo `/p/<id>/`. La conversación viene de `X-Acd-Ctx-Conversation`. Las peticiones sin señal de proyecto usan el proyecto `default` y registran un aviso.
- **Almacenamiento.** `global` guarda todos los proyectos en una base de datos. `local` guarda un proyecto en `.acd-ctx/acd-ctx.db`.

Comandos de proyecto:

```sh
acd-ctx project init [--local-storage]   # crea .acd-ctx/ en la raíz del proyecto
acd-ctx project show                     # proyecto detectado, raíz, almacenamiento y base de datos
acd-ctx project list                     # proyectos de la base de datos global
acd-ctx project move <antigua> <nueva>   # reapunta la raíz de un proyecto movido
acd-ctx project migrate --to local|global # copia las sesiones del proyecto entre bases de datos
acd-ctx project purge <id> [--yes]       # borra todos los datos de un proyecto
```

## Variables de entorno

| Variable | La usa | Significado |
|---|---|---|
| `ACD_CTX_<CLAVE>` | cargador de configuración | Sustituye una clave, por ejemplo `ACD_CTX_CCR_CRUSH=false`. |
| `ACD_CTX_DB` | comandos de la CLI, `attach`, `doctor` | Ruta de la base de datos global. `serve` usa `--db`. |
| `ACD_CTX_ALLOW`, `ACD_CTX_ALLOWED_HOSTS`, `ACD_CTX_ALLOW_NON_LOOPBACK` | `serve` | Iguales a `--allow`, `--allowed-hosts` y `--allow-non-loopback` (`1` o `true`). |
| `ACD_CTX_MAX_RESTORE_BYTES` | `restore` | Límite de tamaño de una copia de seguridad, cuando no se da `--max-size`. Por defecto 2GiB. |
| `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `HOME` | rutas | Directorios base. `serve` ignora `XDG_DATA_HOME`. |
| `ANTHROPIC_BASE_URL`, `OPENAI_BASE_URL`, `GOOGLE_GEMINI_BASE_URL` | su cliente | Apuntan el cliente al proxy en modo URL base. Los lanzadores las definen. |
| `CLAUDE_CODE_GATEWAY_HINT_HEADERS` | `acd-ctx claude` | Con valor `1`, para que Claude Code envíe las cabeceras de pista `x-claude-code-*` que marcan sus llamadas de compactación. |
| `HTTPS_PROXY`, `SSL_CERT_FILE`, `NODE_EXTRA_CA_CERTS`, `REQUESTS_CA_BUNDLE`, `CURL_CA_BUNDLE` | `attach` (grado MITM) | Se definen solo para el proceso del cliente, para que confíe en la CA local. |
| `ACD_CTX_URL`, `ACD_CTX_TOKEN`, `ACD_CTX_TOKEN_FILE`, `ACD_CTX_CONVERSATION` | clientes de `middleware/` | Localizan y autentican en el protocolo de plugins. |

## Endpoints

Endpoints de control (`/__acd-ctx/` rechaza cualquier petición con cabecera `Origin`; `metrics` y `sessions/...` solo aceptan GET, y `health` y `status` no comprueban el método):

| Ruta | Acceso | Respuesta |
|---|---|---|
| `/__acd-ctx/health` | cualquier Host permitido | `{"ok":true}` |
| `/__acd-ctx/status` | cualquier Host permitido | JSON: `status`, `version`, `uptime_seconds`, `upstream_host`, `context_limit`, `max_loop`, `sessions_loaded`, `projects_seen`, `default_project_requests`, `protocols`. |
| `/__acd-ctx/metrics` | solo pares de loopback | Texto Prometheus. Familias: `acd_ctx_requests_total`, `acd_ctx_request_duration_seconds`, `acd_ctx_upstream_duration_seconds`, `acd_ctx_context_tokens`, `acd_ctx_compress_total`, `acd_ctx_compress_ratio`, `acd_ctx_cache_read_ratio`, `acd_ctx_fold_cache_cost_tokens`, `acd_ctx_recall_hint_total`, `acd_ctx_token_calib_factor`, `acd_ctx_tool_calls_total{tool,outcome}` (`tool` es uno de los cuatro nombres de herramienta interna o `unknown`; `outcome` es `accepted` o `rejected`), `acd_ctx_client_compactions_total{protocol,signal}` (compactaciones del cliente reenviadas sin cambios). Las etiquetas son un conjunto cerrado: sin IDs de sesión, proyecto ni mensaje. |
| `/__acd-ctx/sessions/{id}/status` | solo pares de loopback | Recuentos, estado del disparador, tamaño de la ventana, bloques, intenciones abiertas y decisiones recientes (`?decisions=N`). Sin texto de mensajes ni de bloques. |
| `/__acd-ctx/sessions/{id}/trace.jsonl` | solo pares de loopback | Traza de la sesión en JSON lines. `?anonymize=1` sustituye los textos por su longitud y su SHA-256. Reprodúzcala con `acd-ctx replay`. |

Las peticiones de API usan las rutas de protocolo de [Protocolos soportados](#protocolos-soportados), opcionalmente tras `/p/<id>/` y `/ctx/<url>/`.

## Protocolo de plugins y middleware

El protocolo de plugins (spec §19.4) permite que un programa que construye sus propios prompts pida al proxy la ventana visible de una conversación y ejecute las herramientas de contexto. `acd-ctx serve` lo monta bajo `/__acd-ctx/plugin/`. Cada petición necesita `Authorization: Bearer <token>`. `serve` crea un token aleatorio nuevo en cada ejecución y lo escribe en el fichero indicado por `--plugin-token-file` (modo `0600`). Se aplican también las protecciones de Host y Origin del proxy.

| Ruta (bajo `/__acd-ctx/plugin/`) | Método | Finalidad |
|---|---|---|
| `manifest` | GET | Nombre y versión del protocolo (`acd-ctx-plugin`, 1), herramientas y doctrina. |
| `runtime-info` | POST | Declara la conversación, el modelo y la ventana de contexto. |
| `turn` | POST | Envía mensajes y un recuento de tokens; recibe la decisión, las pistas y la ventana visible. |
| `tool` | POST | Ejecuta `compress`, `decompress` (modos `context`, `peek`, `file`), `retrieve`, `search_context` o `ctx_status`. |
| `status` | GET | Los mismos datos que `ctx_status`, para `?conversationId=`. |
| `compact` | POST | Ofrece los tramos compactables y arma un nudge para el siguiente turno. Conversación desconocida: 404. |

`search_context` es una búsqueda de texto completo (FTS5) sobre los mensajes y los resúmenes de bloque de la sesión; todas las palabras deben coincidir. El modo `file` de `decompress` escribe el texto original del bloque en el directorio de exportación del proyecto y devuelve la ruta.

El proxy inyecta también estas cuatro herramientas en la petición del modelo, en todos los dialectos, tanto por las rutas HTTP como en el modo WebSocket de Responses: `compress`, `decompress`, `search_context` y `retrieve`. El proxy las ejecuta todas y nunca envía sus llamadas al cliente. Los resultados llegan en un envoltorio `<history>` que indica al modelo que son datos pasados, no instrucciones. `search_context` devuelve como máximo 20 coincidencias (10 por defecto), con fragmentos de como máximo 300 runas. Su ámbito `project` busca también en las otras conversaciones del mismo proyecto, pero solo como fragmentos; `retrieve` lee únicamente la conversación actual. El resultado de `retrieve` está limitado a unos 32K tokens.

Los clientes están en `middleware/python` (paquete `acd_ctx`, solo biblioteca estándar) y `middleware/node` (paquete `acd-ctx`, ESM, Node.js 18.13 o posterior, sin dependencias). Ambos leen `ACD_CTX_URL` (por defecto `http://127.0.0.1:8787`), `ACD_CTX_TOKEN` o el fichero de `ACD_CTX_TOKEN_FILE` (por defecto `~/.local/share/acd-ctx/plugin.token`), y `ACD_CTX_CONVERSATION`. Ante cualquier error del cliente, se continúa sin compactar.

`conformance/vectors/*.json` son vectores neutros respecto al lenguaje, que reproducen `go test ./conformance/...` y las suites de Python y Node. `docs/compatibility.md` lo genera `go run ./scripts/gen-compat-matrix`, y la CI falla cuando está desactualizado.

## Almacenamiento

La base de datos está en el esquema versión 4, controlado por `PRAGMA user_version`. La versión 4 añade el tipo de mensaje `opaque`, que guarda un elemento de entrada de Responses que el codec no modela (véase [Protocolos soportados](#protocolos-soportados)). La migración se ejecuta al abrir, dentro de una transacción, y una actualización fallida deja la versión anterior en su sitio. Las versiones antiguas rechazan una base de datos en versión 4 e informan de que es más nueva de lo que soportan.

SQLite usa normalmente el modo WAL. Cuando el sistema de archivos no puede abrir los archivos del WAL, el store registra un aviso y pasa a usar el diario de rollback (`journal_mode = DELETE`). Esto ocurre en unidades de WSL bajo `/mnt/<unidad>` (DrvFs) y en algunos montajes de red. Un proceso obsoleto que siga manteniendo la base de datos en modo WAL sigue bloqueando la apertura, así que deténgalo primero. Es preferible una ruta Linux nativa para la base de datos, como una bajo `~/.local/share`.

## Copia de seguridad y restauración

`acd-ctx backup` escribe una copia verificada de la base de datos global o de un proyecto. La copia se hace con `VACUUM INTO` en un directorio temporal privado, la verifica la misma inspección que aplica `restore`, y se renombra al destino. Así, el fichero de salida es el antiguo o la copia completa nueva. El nombre por defecto es `acd-ctx-backup-<hora UTC>.db` en el directorio de trabajo; los ficheros se escriben con modo `0600`.

```sh
acd-ctx backup                               # la base de datos global completa
acd-ctx backup --project <id|ruta|.> -o p.db # un proyecto; un proyecto local copia su propio fichero
```

`acd-ctx restore <fichero.db>` (o `--stdin`) importa una copia en una sola transacción. Nunca copia un fichero encima de la base de datos activa.

- `--project <id>` restaura un proyecto de la copia. `--to local|global` elige el destino; `local` exige exactamente un proyecto y escribe en el `.acd-ctx/acd-ctx.db` del proyecto actual.
- `--mode merge` (por defecto) rechaza cualquier ID que ya exista en el destino, y no escribe nada.
- `--mode replace` borra antes el proyecto de destino. Exige `--yes`.
- `--dry-run` planifica toda la importación sobre una copia de trabajo y la deshace. No escribe nada y no crea el fichero de destino.
- `--max-size` (o `ACD_CTX_MAX_RESTORE_BYTES`) limita el tamaño de la copia: bytes, o un número con `KiB`, `MiB` o `GiB`. Por defecto 2GiB.
- `--docker <contenedor>` (también en `backup` y `clean`) ejecuta la operación dentro del contenedor; el fichero se lee o se escribe en el host.

Las copias hostiles o dañadas se rechazan antes de escribir nada. Se rechaza un fichero que no es SQLite, cuyo esquema es más nuevo que el del binario, que contiene cualquier tabla, vista, disparador o tabla virtual que acd-ctx no crea (solo se admite el `search_idx` de FTS5), cuando falla `PRAGMA integrity_check`, cuando algún contenido almacenado no produce el hash de su clave, o cuando una sesión rompe un invariante (I1 a I3, I6, I7). La copia se abre con `trusted_schema` desactivado y `query_only` activado.

## Limpieza

`acd-ctx clean` borra datos de conversación. **La operación es irreversible.** Sin copia de seguridad, las sesiones, mensajes, bloques, eventos y contenidos borrados se pierden. Ejecútela siempre primero con `--dry-run`, y use `--backup` para cualquier cosa que pueda necesitar después.

```sh
acd-ctx clean --project . --dry-run                  # lo que se borraría, y nada más
acd-ctx clean --project . --backup antes.db          # copia verificada primero, luego borra
acd-ctx clean --older-than 30d --vacuum --exports
acd-ctx clean --all --docker acd-ctx --dry-run
```

- Alcance: exactamente uno de `--project <id|ruta|.>`, `--session <id>`, `--older-than <dur>` (`30d`, `12h`, cualquier duración de Go; `--project` puede acotarlo) o `--all`.
- `--backup <fichero.db>` escribe una copia verificada de cada base de datos antes de borrar. Si una copia falla, no se borra nada.
- Confirmación: sin `--yes` hay que escribir la palabra que se muestra: el ID del proyecto, el ID de la sesión, o `delete` para `--all` y `--older-than`. Una entrada que no es un terminal, sin `--yes`, se rechaza.
- `--exports` borra también los ficheros de bloque exportados de las sesiones eliminadas. `--vacuum` compacta los ficheros después.
- `--include-local` limpia también la base de datos local del directorio actual, en `--all`, `--older-than` o `--session` sin `--project`.
- La configuración, la CA del MITM y el token de plugin nunca se borran (`--keep-config` siempre está activo).
- Una purga incrementa el epoch del store en la misma transacción. Un `serve` o plugin en marcha descarta el estado en memoria de las sesiones afectadas en su siguiente petición. `restore --mode replace` hace lo mismo.

## Notas de seguridad

- **Loopback por defecto.** `serve` rechaza un `--listen` que no sea loopback, salvo que se den `--allow-non-loopback` (o `ACD_CTX_ALLOW_NON_LOOPBACK=1`) y un `--allowed-hosts` no vacío. Después registra un aviso `SECURITY`. La imagen Docker usa esa vía; consulte [docs/docker.md](docs/docker.md).
- **Sin autenticación en el listener de URL base.** Cualquier proceso local puede enviar peticiones a `/ctx/<host permitido>/...` en el puerto de loopback (solo el `CONNECT` del MITM y el plugin exigen token). El proxy reenvía las credenciales del propio llamador y nunca añade las suyas, así que funciona como un relevo hacia hosts permitidos. Úselo en una máquina de un solo usuario.
- **Protecciones de Host y de navegador.** Solo se atienden los nombres de Host permitidos (por defecto `localhost`, `127.0.0.1`, `::1` y el host de escucha); los demás reciben HTTP 403, lo que pretende impedir el DNS rebinding (probado con cabeceras `Host` falsificadas, no con un navegador real). Los preflight de CORS reciben 403, y `/__acd-ctx/` rechaza cualquier petición con cabecera `Origin`. Ninguno de los dos es autenticación.
- **Lista de permitidos del upstream.** Un destino `/ctx/<url>/` solo se usa si su origen está en `--allow`. Las redirecciones se devuelven al cliente, no se siguen.
- **CA del MITM.** Una CA por instalación, con la clave en un fichero `0600`. Solo se interceptan los dominios permitidos, y `attach` entrega la CA solo al proceso del cliente que arranca, mediante variables de entorno.
- **Proxy del sistema y modo transparente.** Ambos cambian la ruta de red de la máquina y exigen consentimiento explícito (`--yes`). `--system-proxy` cambia la configuración de proxy del escritorio; `--transparent` exige root o CAP_NET_ADMIN, instala reglas de nftables (o de iptables, cuando nft no está instalado) para un uid y se niega a ejecutarse con sudo cuando el `HOME` no es el del usuario que lo invoca, salvo que el uid y el directorio de datos sean explícitos. La tabla de nftables se crea desde cero: acd-ctx se niega a adoptar una tabla existente. Ambos escriben ficheros de estado que se comprueban antes de confiar en ellos. Antes del primer cambio se arranca un proceso auxiliar de vigilancia (watchdog), que restaura la redirección cuando acd-ctx muere. Si el propio auxiliar recibe SIGKILL, la redirección permanece hasta que se ejecute `attach --restore`, y `doctor` lo informa. `attach --restore` sin privilegios conserva el fichero de estado del modo transparente, porque no se puede leer el cortafuegos para restaurarlo.
- **Credenciales y límites.** Las cabeceras de credenciales se reenvían al upstream y el proxy no las registra en el log (las pruebas solo cubren que los mensajes de error no filtren la query string). Un error de compactación reenvía la petición sin cambios. Los cuerpos de petición y los frames WebSocket se limitan a 32 MiB.

## Desarrollo

```sh
go test -race -count=1 ./...                 # obligatorio antes de cada commit
go test ./...                                  # todas las pruebas
go test ./internal/kernel -run TestName        # una prueba
go test -fuzz=FuzzParseToolArguments ./internal/wire/openai
go test ./conformance/...                      # vectores del protocolo de plugins
(cd middleware/python && python3 -m unittest discover -s tests)
(cd middleware/node && node --test)
go run ./scripts/gen-compat-matrix             # imprime docs/compatibility.md
```

Ejecute `go test -race -count=1 ./...` sobre todo el módulo antes de cada commit. Es obligatorio: la CI lo ejecuta y detecta condiciones de carrera que un `go test` simple no detecta.

`CLAUDE.md` está en inglés. La especificación de diseño `compactacao-contexto-go-sqlite.md` (en portugués) es la fuente de verdad; los §N de los comentarios del código se refieren a ella.

La CI (`.github/workflows/ci.yml`) ejecuta `gofmt`, `go vet`, `go test -race`, un vet cruzado para darwin y windows con `CGO_ENABLED=0`, los vectores de conformidad, las pruebas de los clientes Python y Node, una comprobación de que `docs/compatibility.md` está al día, y una compilación de la imagen Docker con prueba de humo del health. El workflow de publicación (`.github/workflows/release.yml`) se ejecuta solo al subir una etiqueta `v*` o a mano; véase [docs/release.md](docs/release.md).

| Paquete | Función |
|---|---|
| `internal/kernel` | Lógica pura: disparador, niveles, renderizado, recall, CCR y crush, truncamiento, coste de caché. Sin E/S. |
| `internal/store` | Acceso a SQLite (WAL, FTS5), esquema, invariantes, purga, copia de seguridad y restauración. |
| `internal/session` | Orquestación de turnos, herramientas y la transacción de compactación. El único escritor. |
| `internal/proxy` | Servidor HTTP, enrutamiento, adaptadores de protocolo, modo WebSocket, bucle de herramientas en el servidor, endpoints de observabilidad. |
| `internal/wire/...` | Codecs de `anthropic`, `openai`, `responses` y `google`. |
| `internal/config` | Configuración por capas e informe del origen efectivo de cada clave. |
| `internal/project` | Detección de la raíz y del ID del proyecto. |
| `internal/launcher` | Perfiles de cliente, ejecución con variables de entorno, configuraciones temporales. |
| `internal/attach` | Escalera de integración de `attach`: sondeos, grados de consola, MITM y de sistema. |
| `internal/sysproxy` | Backends de proxy del sistema y los backends transparentes de nftables e iptables, con ficheros de estado, el proceso auxiliar de vigilancia y restauración. |
| `internal/mitm` | CA local, certificados hoja e interceptación TLS acotada. |
| `internal/backup` | Copia de seguridad y restauración verificadas, y los ejecutores de `--docker`. |
| `internal/obs` | Métricas Prometheus, informes de estado y exportación de trazas. |
| `internal/plugin` | Servidor MCP por stdio, handler HTTP del plugin y la comprobación del epoch del store. |
| `internal/cli`, `internal/doctor` | Implementación de los comandos de `cmd/acd-ctx`; comprobaciones de instalación e integración. |
| `pkg/acdctx` | Biblioteca pública de Go: API de cliente, transporte y handler HTTP. |
| `middleware/python`, `middleware/node` | Clientes del protocolo de plugins (sin dependencias). |
| `conformance` | Vectores del plugin neutros respecto al lenguaje y el ejecutor en Go. |
| `scripts/gen-compat-matrix` | Genera `docs/compatibility.md`. |
| `.goreleaser.yaml`, `.github/workflows/release.yml` | Compilaciones y archivos de publicación, solo para una etiqueta `v*`. |

## Estado

Validado con pruebas automatizadas, sin acceso a la red más allá del loopback. `go test ./...` supera las pruebas con `CGO_ENABLED=0`:

- Pruebas unitarias y de tabla del kernel, store, config, project, launcher, attach, sysproxy (con ejecutores falsos), MITM, copia de seguridad, restauración, limpieza, observabilidad y doctor.
- Pruebas de propiedades (`pgregory.net/rapid`) que comprueban los invariantes tras pasos de compactación aleatorios.
- Objetivos de fuzz para los analizadores de argumentos de herramientas de cada codec, el handler de `compress` y los frames JSON-RPC del plugin.
- Pruebas del proxy con upstreams falsos (`httptest` en loopback) para los cuatro dialectos, incluidos el streaming y el modo WebSocket de Responses. Los lanzadores, `attach` y el MITM se ejecutan con binarios de cliente sustitutos.
- Pruebas de extremo a extremo (`e2e/`) ejecutan el binario real contra un upstream falso para Anthropic Messages y OpenAI Chat Completions: 80 turnos más allá del doble de una ventana de 128K, pliegues y un ciclo de decompress, invariantes comprobados al final, y un reinicio del proxy a mitad de ejecución.
- Vectores de conformidad del plugin, reproducidos por Go, Python y Node.
- Detección de compactación de clientes, con fixtures sintéticos construidos a partir del texto del código fuente de los clientes (`compaction_rules_test.go`, `client_compaction_test.go`, `gemini_shape_test.go`).
- Migración del esquema v4 (`migrate_v4_test.go`), el respaldo de WAL (`wal_fallback_test.go`) y los ítems opacos de Responses (`opaque_test.go`).
- Las herramientas internas `search_context` y `retrieve`, en las rutas HTTP y WebSocket, con los límites de resultados, fragmentos y tokens.
- Dependencias actualizadas con `go get -u`; la suite supera las pruebas con ellas.

No implementado:

- Un watchdog al que SIGKILL ha matado: la redirección permanece hasta que se ejecute `attach --restore` a mano. `doctor` lo informa.
- Redirección IPv6 cuando `[::1]` no está libre en el puerto de redirección: el IPv6 entonces evita acd-ctx, y la salida de attach lo indica.
- Detección de compactación de clientes para Copilot CLI, Cursor y Windsurf: sin señal documentada, así que no existe regla.

## Limitaciones y riesgos

- **Compactación con pérdidas.** Un resumen sustituye, en la ventana, a los mensajes que cubre. El modelo puede resumir de forma incorrecta u omitir detalles. Los originales se pueden recuperar con `decompress`, pero solo si lo pide el modelo o usted.
- **Coste de caché.** Reescribir una parte anterior del prompt invalida la caché de prompt del proveedor. La compuerta de caché reduce ese coste, pero no lo elimina. Los precios de caché son específicos de cada proveedor y debe definirlos usted.
- **Los recuentos de tokens son estimaciones.** Se calibran con los valores de `usage` del proveedor, pero no son exactos.
- **Especificación v0.1.** La especificación de diseño sigue en la v0.1 y puede cambiar en versiones posteriores.

## Licencia

Licenciado bajo la [Apache License, Version 2.0](LICENSE). Consulte el archivo `LICENSE` para el texto completo.

```text
Copyright 2026 Fabricio Valadares

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
```
