# Accordion Context (`acd-ctx`)

[English](README.md) | [Português (Brasil)](README.pt-BR.md) | **Español (España)**

Accordion Context es un proxy local que mantiene las sesiones largas de agentes de programación dentro de una ventana de contexto limitada. El modelo resume las partes antiguas de la conversación, los resúmenes sustituyen a los mensajes originales en la ventana, y los originales se conservan en una base de datos SQLite para poder recuperarlos.

> **Estado: en desarrollo inicial (anterior a la 1.0).** El proxy, los lanzadores de clientes, los comandos `project`, `config`, `attach`, `doctor` y `mcp`, el protocolo HTTP de plugins con sus clientes Python y Node, y la biblioteca `pkg/acdctx` están implementados y superan `go test ./...`. Nada se ha validado contra las API reales de los proveedores ni contra los binarios reales de los clientes. Consulte [Estado](#estado).

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
- Los invariantes con nombre (I1 a I9) se verifican con comprobaciones SQL en el store y con pruebas de propiedades.
- El coste de la caché se modela de forma explícita, y una compuerta de caché puede aplazar un aviso (nudge).

## Cómo funciona

1. La petición del agente llega al proxy, que registra cada mensaje en la sesión con una referencia estable (por ejemplo `m00451`).
2. El disparador decide si la compactación toca. La compuerta de caché puede aplazar el aviso cuando una reescritura rompería la caché de prompt del proveedor a un coste alto.
3. Cuando toca, el proxy añade un aviso que enumera los intervalos compactables. El modelo responde con una llamada `compress`: intervalos más un resumen.
4. El proxy valida la llamada y escribe el bloque y los intervalos ocultos en una sola transacción.
5. La ventana siguiente muestra el resumen en lugar de los mensajes plegados. Los originales siguen en SQLite.
6. Cuando el modelo necesita detalles, llama a `decompress`. Las llamadas internas de `compress` y `decompress` se ejecutan dentro del proxy (hasta 4 rondas por petición, por defecto) y no llegan al cliente.
7. Cualquier fallo en el camino de compactación es fail-open: la petición original se reenvía sin cambios.

## Protocolos soportados

El proxy reconoce cuatro dialectos de API en `POST`:

| Dialecto | Endpoint que reconoce el proxy | Observaciones |
|---|---|---|
| Anthropic Messages | `/v1/messages` | El streaming se reenvía según llega. |
| OpenAI Chat Completions | `/v1/chat/completions` | Cada ronda del upstream se lee completa antes de enviarse. |
| OpenAI Responses | `/v1/responses` | **Solo sin estado.** Las peticiones con `previous_response_id` o `conversation` se reenvían sin cambios. El modo WebSocket no se gestiona. |
| Google Gemini | `.../models/<modelo>:generateContent`, y `:streamGenerateContent` con `alt=sse` | Los streams se almacenan en búfer por ronda. Sin `alt=sse`, la petición se reenvía sin cambios. |

Las demás rutas, y los demás métodos, se reenvían al upstream sin compactar.

## Instalación

Requisitos: Go 1.27.1 o posterior (la directiva `go` de `go.mod`). No hace falta compilador de C: SQLite viene de `modernc.org/sqlite`, que es Go puro.

Compilar desde el clon (verificado, también con `CGO_ENABLED=0`):

```sh
git clone https://github.com/fsvxavier/accordion-context.git
cd accordion-context
go build -o acd-ctx ./cmd/acd-ctx
./acd-ctx help
```

Instalar desde la ruta del módulo:
Instalación desde la ruta del módulo, cuando se publique una versión etiquetada:
```sh
go install github.com/fsvxavier/accordion-context/cmd/acd-ctx@latest
```

Todavía no existe ninguna etiqueta de versión, así que `@latest` no se resuelve hoy. Mientras tanto, instale un commit concreto con `go install github.com/fsvxavier/accordion-context/cmd/acd-ctx@<commit>`, o compile desde el código fuente como arriba. El valor de `version` es `dev`, salvo que el binario se compile con `-ldflags "-X main.version=<etiqueta>"`.

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

# Otro proveedor necesita su origen en --allow. Aún no verificado con el cliente real.
acd-ctx serve --allow https://api.openai.com
OPENAI_BASE_URL=http://127.0.0.1:8787/ctx/https://api.openai.com/v1 codex
```

Opciones de `serve` (todas opcionales):

| Opción | Valor por defecto | Significado |
|---|---|---|
| `--listen` | `127.0.0.1:8787` | Debe ser una dirección de loopback; si no, el comando se niega a arrancar. |
| `--upstream` | `https://api.anthropic.com` | Upstream usado en peticiones sin prefijo `/ctx/<url>/`. |
| `--allow` | origen de `--upstream` | Orígenes separados por comas accesibles mediante `/ctx/<url>/`. |
| `--db` | `~/.local/share/acd-ctx/acd-ctx.db` | Ruta de la base de datos SQLite. |
| `--config` | ruta de configuración del usuario | Archivo de configuración global. |
| `--markers` | `go.mod,package.json,pyproject.toml,composer.json,Cargo.toml` | Marcadores de raíz de proyecto. |
| `--plugin-token-file` | `~/.local/share/acd-ctx/plugin.token` | Fichero que recibe el token bearer del plugin, generado en cada ejecución (modo `0600`). Si no se puede escribir, el protocolo de plugins se desactiva y el proxy sigue funcionando. |

## Modos de integración

| Modo | Comando | Estado | Observaciones |
|---|---|---|---|
| **URL base** | `acd-ctx serve` más la variable de URL base del cliente | Implementado | Verificado con peticiones HTTP. No se han ejecutado clientes reales. |
| **Lanzador** | `acd-ctx claude`, `codex`, `gemini`, `opencode`, `run -- <cmd>` | Implementado para claude, codex y gemini | `run` solo acepta un comando que coincida con un perfil integrado. `opencode` no tiene variable de URL base y se rechaza; use `attach`. Probado con binarios sustitutos. |
| **Attach** | `acd-ctx attach [--mitm-domain <host>] -- <cmd> [args]` | Implementado (grados URL base y MITM) | Comprueba cada grado antes de arrancar el cliente y usa el primero que funciona. Cualquier comando funciona en modo consola. |
| **MITM** | el grado `mitm` de `attach` | Implementado | Solo se terminan los hosts de la lista. Los demás se tunelan sin tocarlos. La CA es por instalación y no se añade al almacén de confianza del sistema. |
| **Proxy de sistema, transparente, configuración temporal** | `attach --system-proxy` / `--transparent`; fichero temporal de URL base (grado 4 de la spec) | No implementado | Las dos opciones se rechazan con un error. `attach` nunca prueba el grado 4. |
| **Plugin** (HTTP loopback, spec §19.4) | `acd-ctx serve`, en `/__acd-ctx/plugin/` | Implementado | Token bearer creado en cada ejecución. `compact` devuelve 501 y `search_context` falla (el store aún no tiene API de búsqueda). Consulte [Protocolo de plugins y middleware](#protocolo-de-plugins-y-middleware). |
| **Middleware** | `middleware/python`, `middleware/node` | Implementado | Clientes del protocolo de plugins sin dependencias, probados con los vectores de conformidad. |
| **Compañero MCP** | `acd-ctx mcp` (stdio) | Implementado | Herramientas: `decompress`, `retrieve`, `search_context`, `ctx_status`. `search_context` devuelve hoy un error. |
| **Biblioteca Go** | `pkg/acdctx` | Implementado | `Open` y `Client` (Turn, Compress, Decompress, Recall), `NewTransport` (un `http.RoundTripper`) y `Handler`. La API puede cambiar antes de la 1.0. |
| **Doctor** | `acd-ctx doctor [--json] [cliente]` | Implementado | Comprueba la instalación y la integración. Código de salida 0 para PASS, 3 para WARN, 1 para FAIL. |

## Configuración

Los valores se resuelven por clave, de menor a mayor precedencia:

1. Valores por defecto integrados.
2. Archivo global: `$XDG_CONFIG_HOME/acd-ctx/config.yaml`, o `~/.config/acd-ctx/config.yaml` en Linux.
3. Sobrescrituras `projects` en el archivo global, para cada patrón que coincida con la raíz del proyecto.
4. Archivo local del proyecto: `<raíz del proyecto>/.acd-ctx/config.yaml`.
5. Variables de entorno `ACD_CTX_*`.
6. Opciones explícitas.

Ejemplo, con claves reales (carga sin avisos):

```yaml
# ~/.config/acd-ctx/config.yaml
trigger:
  u_high: 0.75          # empieza a compactar al 75% de la ventana
  step_base: 50000      # tokens entre avisos, antes del ajuste adaptativo
compress:
  preserve_recent_messages: 3
ccr:
  crush: true           # reduce de forma determinista las salidas de herramienta muy grandes
storage: global         # o "local": guarda la base de datos del proyecto en .acd-ctx/
```

Las claves reales, con sus valores por defecto (`config show --effective` muestra la misma lista). La columna `local` indica si el `.acd-ctx/config.yaml` de un proyecto puede definir la clave:

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

Las claves con los prefijos `upstream.`, `credentials.`, `listen.` y `tools.external_summary.` están reservadas. Se ignoran con un aviso, tanto en el archivo global como en el local.

Las variables de entorno usan la clave en mayúsculas, con los puntos sustituidos por guiones bajos:

```sh
ACD_CTX_TRIGGER_STEP_BASE=60000 acd-ctx claude
```

Muestra el valor efectivo y el origen de cada clave:

```sh
acd-ctx config show --effective
```

El proxy vuelve a cargar la configuración de cada raíz de proyecto como máximo cada 30 segundos, así que la mayoría de los cambios no requieren reiniciar.

### Nota de seguridad sobre la configuración local

`<proyecto>/.acd-ctx/config.yaml` está dentro del repositorio en el que trabaja, así que puede venir de un clon que usted no escribió. Puede cambiar el comportamiento del disparador, la compactación, CCR, el recall y el almacenamiento, pero no `upstream.*`, `credentials.*` ni `listen.*`. Revíselo como cualquier otro archivo del proyecto.

## Separación por proyecto

- **Detección.** Subiendo desde el directorio de trabajo, gana la primera coincidencia: un directorio `.acd-ctx/`, después `.git` y después un marcador de raíz. Si no hay ninguno, el propio directorio de trabajo es la raíz.
- **ID del proyecto.** Un proyecto con `.acd-ctx/project-id` usa ese valor. Si no, el ID son los 16 primeros caracteres hexadecimales del SHA-256 de la ruta de la raíz.
- **Peticiones.** El proxy toma el proyecto del prefijo `/p/<id>/` y, después, de la cabecera `X-Acd-Ctx-Project-Dir`. La conversación viene de `X-Acd-Ctx-Conversation`. Las peticiones sin señal de proyecto usan el proyecto `default` y registran un aviso.
- **Almacenamiento.** `global` guarda todos los proyectos en una sola base de datos. `local` guarda un proyecto en `.acd-ctx/acd-ctx.db`.

Comandos de proyecto:

```sh
acd-ctx project init [--local-storage]   # crea .acd-ctx/ en la raíz del proyecto
acd-ctx project show                     # proyecto detectado, raíz, almacenamiento y base de datos
acd-ctx project list                     # proyectos de la base de datos global
acd-ctx project move <antigua> <nueva>   # reapunta la raíz de un proyecto movido
acd-ctx project migrate --to local|global
acd-ctx project purge <id> [--yes]       # borra todos los datos de un proyecto
```

## Variables de entorno

| Variable | La usa | Significado |
|---|---|---|
| `ACD_CTX_<CLAVE>` | cargador de configuración | Sustituye una clave de configuración, por ejemplo `ACD_CTX_CCR_CRUSH=false`. |
| `ACD_CTX_DB` | comandos de CLI, `attach`, `doctor` | Ruta de la base de datos global. `serve` usa `--db`. |
| `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `HOME` | rutas | Directorios base. `serve` ignora `XDG_DATA_HOME`. |
| `ANTHROPIC_BASE_URL`, `OPENAI_BASE_URL`, `GOOGLE_GEMINI_BASE_URL` | su cliente | Apuntan el cliente al proxy en modo URL base. Los lanzadores las definen. |
| `HTTPS_PROXY`, `SSL_CERT_FILE`, `NODE_EXTRA_CA_CERTS`, `REQUESTS_CA_BUNDLE`, `CURL_CA_BUNDLE` | `attach` (grado MITM) | Se definen solo para el proceso del cliente, para que confíe en la CA local. |
| `ACD_CTX_URL`, `ACD_CTX_TOKEN`, `ACD_CTX_TOKEN_FILE`, `ACD_CTX_CONVERSATION` | clientes de `middleware/` | Localizan y autentican en el protocolo de plugins. Consulte [Protocolo de plugins y middleware](#protocolo-de-plugins-y-middleware). |

## Endpoints

Endpoints de control:

| Ruta | Método | Respuesta |
|---|---|---|
| `/__acd-ctx/health` | GET | `{"ok":true}` |
| `/__acd-ctx/status` | GET | JSON con `status`, `version`, `uptime_seconds`, `upstream_host`, `context_limit`, `max_loop`, `sessions_loaded`, `projects_seen`, `default_project_requests` y `protocols`. |

Las peticiones de API usan las rutas de protocolo de [Protocolos soportados](#protocolos-soportados), opcionalmente detrás de `/p/<id>/` y `/ctx/<url>/`. El compañero MCP no tiene endpoint HTTP; habla JSON-RPC sobre stdio.

## Protocolo de plugins y middleware

El protocolo de plugins (spec §19.4) permite que un programa que construye sus propios prompts pida al proxy la ventana visible de una conversación y ejecute las herramientas de contexto. `acd-ctx serve` lo monta en `/__acd-ctx/plugin/`. Toda petición exige `Authorization: Bearer <token>`. `serve` crea un token aleatorio nuevo en cada ejecución y lo escribe en el fichero indicado por `--plugin-token-file` (modo `0600`). Se aplican las guardias de Host y Origin del proxy.

| Ruta (bajo `/__acd-ctx/plugin/`) | Método | Finalidad |
|---|---|---|
| `manifest` | GET | Nombre y versión del protocolo (`acd-ctx-plugin`, 1), herramientas y doctrina. |
| `runtime-info` | POST | Declara la conversación, el modelo y la ventana de contexto. |
| `turn` | POST | Envía mensajes y un recuento de tokens; devuelve la decisión, las pistas y la ventana visible. |
| `tool` | POST | Ejecuta `compress`, `decompress`, `retrieve`, `search_context` o `ctx_status`. |
| `status` | GET | Los mismos datos que `ctx_status`, para `?conversationId=`. |
| `compact` | POST | No implementado: siempre HTTP 501. |

Los clientes están en `middleware/python` (paquete `acd_ctx`, solo biblioteca estándar) y `middleware/node` (paquete `acd-ctx`, ESM, Node.js 18.13 o superior, sin dependencias). Ambos leen `ACD_CTX_URL` (por defecto `http://127.0.0.1:8787`), `ACD_CTX_TOKEN` o el fichero de `ACD_CTX_TOKEN_FILE` (por defecto `~/.local/share/acd-ctx/plugin.token`) y `ACD_CTX_CONVERSATION`. Ante cualquier error del cliente, continúe sin compactación.

`conformance/vectors/*.json` son vectores de petición y expectativa independientes del lenguaje. `go test ./conformance/...` los reproduce contra el manejador de plugins real. Las suites de Python y Node reproducen los mismos ficheros contra un servidor simulado. Defina `ACD_CTX_CONFORMANCE_URL` y `ACD_CTX_CONFORMANCE_TOKEN` para reproducirlos contra un `acd-ctx serve` en ejecución.

`docs/compatibility.md` es la matriz de compatibilidad generada, con protocolos y perfiles de cliente. La CI falla cuando el fichero versionado difiere de la salida de `go run ./scripts/gen-compat-matrix`.

## Notas de seguridad

- **Solo loopback.** `serve` rechaza un `--listen` que no sea de loopback. El proxy transporta credenciales de API.
- **Sin autenticación en el listener de base URL.** Cualquier proceso local puede enviar peticiones a `/ctx/<host de la lista permitida>/...` en el puerto de loopback (solo el `CONNECT` del MITM y el plugin exigen token). El proxy reenvía las credenciales del propio llamante y nunca añade las suyas, así que es un relay hacia hosts de la lista permitida, no una fuga de claves. Úsalo en un ordenador de un solo usuario.
- **Guardia de Host.** Las peticiones solo se aceptan cuando la cabecera `Host` nombra `localhost`, `127.0.0.1`, `::1` o el host de escucha. Otros nombres reciben HTTP 403, lo que bloquea el DNS rebinding.
- **Acceso desde el navegador.** Las peticiones de preflight CORS reciben HTTP 403. Los endpoints de control bajo `/__acd-ctx/` rechazan cualquier petición que lleve cabecera `Origin`.
- **Token del plugin.** Los endpoints de plugins exigen un token bearer, comparado en tiempo constante. Se regenera en cada ejecución de `serve` y se guarda en un fichero con modo `0600`.
- **Lista blanca de upstreams.** Un destino `/ctx/<url>/` solo se usa si su origen está en `--allow`. Las redirecciones se devuelven al cliente, no se siguen.
- **Credenciales.** Las cabeceras de credenciales se reenvían al upstream. Los registros guardan IDs de sesión, etapas y errores, no cabeceras de las peticiones.
- **Fail-open.** Un error de compactación reenvía la petición sin cambios.
- **Límites.** Los cuerpos de petición se limitan a 32 MiB. El directorio de la base de datos se crea con modo `0700`, y la clave de la CA del MITM con `0600`.

## Desarrollo

```sh
go test ./...                                  # suite completa
go test ./internal/kernel -run TestName        # una prueba
go test -fuzz=FuzzParseToolArguments ./internal/wire/openai
go test -fuzz=FuzzDecodeFrame ./internal/plugin
go test -race ./...                            # como en la CI
go test ./conformance/...                      # vectores del protocolo de plugins
(cd middleware/python && python3 -m unittest discover -s tests)
(cd middleware/node && node --test)
go run ./scripts/gen-compat-matrix             # imprime docs/compatibility.md
```

El `CLAUDE.md` está en inglés. La especificación de diseño `compactacao-contexto-go-sqlite.md` está en portugués y es la fuente de verdad. Los números de sección (§N) en los comentarios del código se refieren a ella.

La CI (`.github/workflows/ci.yml`) ejecuta `gofmt`, `go vet`, `go test -race`, los vectores de conformidad, las pruebas de los clientes Python y Node, y comprueba que `docs/compatibility.md` esté al día.

| Paquete | Función |
|---|---|
| `internal/kernel` | Lógica pura: disparador, niveles, renderizado, recall, CCR y crush, coste de caché. Sin E/S. |
| `internal/store` | Acceso a SQLite (WAL, FTS5), esquema y comprobaciones de invariantes. |
| `internal/session` | Orquestación de turnos, herramientas y transacción de compactación. El único escritor. |
| `internal/proxy` | Servidor HTTP, enrutado, adaptadores de protocolo, bucle de herramientas en el servidor, gestión de forks. |
| `internal/wire/...` | Códecs para `anthropic`, `openai`, `responses` y `google`. |
| `internal/config` | Configuración por capas e informe del origen efectivo. |
| `internal/project` | Detección de la raíz y del ID del proyecto. |
| `internal/launcher` | Perfiles de cliente y lanzamiento de procesos con variables de entorno. |
| `internal/attach` | Escalera de integración de `attach`: comprobaciones, grados consola y MITM. |
| `internal/mitm` | CA local, certificados hoja e interceptación TLS acotada. |
| `internal/plugin` | Servidor MCP por stdio y el manejador HTTP de plugins que monta `serve`. |
| `internal/doctor` | Comprobaciones de instalación y de integración. |
| `internal/cli` | Implementación de los comandos detrás de `cmd/acd-ctx`. |
| `pkg/acdctx` | Biblioteca Go pública: API de cliente, transporte HTTP y manejador. |
| `middleware/python`, `middleware/node` | Clientes del protocolo de plugins (sin dependencias). |
| `conformance` | Vectores de plugins independientes del lenguaje y el ejecutor en Go. |
| `scripts/gen-compat-matrix` | Genera `docs/compatibility.md`. |

## Estado

Validado con pruebas automáticas, sin acceso a la red más allá de loopback:

- Pruebas unitarias y de tabla del kernel, store, config, project, launcher, attach, MITM y doctor.
- Pruebas de propiedades (`pgregory.net/rapid`) que comprueban las invariantes tras pasos aleatorios de compactación.
- Objetivos de fuzzing para los analizadores de argumentos de herramientas de cada códec, el manejador de `compress` y las tramas JSON-RPC del plugin.
- Pruebas del proxy contra upstreams simulados (`httptest` en loopback) para los cuatro dialectos, incluido el streaming.
- Pruebas de extremo a extremo (`e2e/`) ejecutan el binario real contra un upstream simulado para Anthropic Messages y OpenAI Chat Completions: 80 turnos más allá del doble de una ventana de 128K, pliegues y un ciclo de descompresión, invariantes verificados tras la ejecución, y un reinicio del proxy a mitad de la ejecución.
- Vectores de conformidad del plugin, reproducidos por Go, Python y Node.
- Lanzadores, `attach` y MITM con binarios de cliente sustitutos.

No validado:

- Las API reales de los proveedores (Anthropic, OpenAI, Google): detalles del protocolo, casos límite de streaming, valores de `usage`, comportamiento y precios de la caché.
- Los clientes de agente reales (Claude Code, Codex, Gemini CLI, OpenCode) funcionando a través del proxy o del MITM.
- La calidad de los resúmenes en sesiones reales largas.

## Limitaciones y riesgos

- **Compactación con pérdida.** Un resumen sustituye en la ventana a los mensajes que cubre. El modelo puede resumir mal u omitir detalles. Los originales se recuperan con `decompress`, pero solo si el modelo o usted lo piden.
- **Coste de caché.** Reescribir una parte anterior del prompt invalida la caché de prompt del proveedor. La compuerta de caché reduce ese coste, pero no lo elimina. Los precios de caché son específicos de cada proveedor y debe definirlos usted.
- **No validado con API reales ni con clientes reales.** Consulte [Estado](#estado). Las pruebas usan upstreams simulados y binarios de cliente sustitutos.
- **WebSocket no gestionado.** El tráfico WebSocket de Responses no se compacta.
- **Responses con estado sin compactar.** Las peticiones encadenadas mediante `previous_response_id` o `conversation` se reenvían sin cambios.
- **No implementado.** `attach --system-proxy`, `--transparent` y el grado de configuración temporal; el endpoint `compact` del plugin; `search_context` en el plugin y en el compañero MCP.
- **Los recuentos de tokens son estimaciones.** Se calibran con los valores `usage` del proveedor, pero no son exactos.
- **Anterior a la 1.0.** Todavía no existe ninguna etiqueta de versión, y la especificación (v0.1) y la API de la biblioteca pueden cambiar.


## Licencia

Licencia: pendiente de definir
