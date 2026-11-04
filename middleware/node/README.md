# acd-ctx (Node.js)

Client for the Accordion Context plugin protocol (spec section 19.4). ESM, no
dependencies: it uses the built-in `fetch`, `AbortSignal.timeout` and `node:fs`.
Requires Node.js 18.13 or later.

```js
import { PluginClient, compactMessages, message, runTool } from "acd-ctx";

const client = PluginClient.fromEnv({ conversationId: "my-conversation" });

const plan = await compactMessages(
  [message("user", "Read the config."), message("assistant", "It sets port 8787.")],
  12000,
  { client },
);
const window = plan.visible;      // send this window to the model
if (plan.decision.nudge) {
  // ask the model for a compress call
}

const status = await runTool("ctx_status", {}, { client });
```

`PluginClient.fromEnv()` reads `ACD_CTX_URL` (default `http://127.0.0.1:8787`),
`ACD_CTX_TOKEN` or the token file named by `ACD_CTX_TOKEN_FILE` (default
`~/.local/share/acd-ctx/plugin.token`), and `ACD_CTX_CONVERSATION`.

Errors: an HTTP error status throws `PluginError` (with `.status`); an
unreachable server throws it with status 0. Treat either as "continue without
compression". `client.request()` resolves to `{ status, body }` without throwing.

Tests use `node:test`:

```sh
cd middleware/node
node --test
```

The conformance replay runs against a fake server by default. Set
`ACD_CTX_CONFORMANCE_URL` (origin of a running `acd-ctx serve`) and
`ACD_CTX_CONFORMANCE_TOKEN` (its token) to replay against that server instead.
