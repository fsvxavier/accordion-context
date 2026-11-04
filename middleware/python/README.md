# acd_ctx (Python)

Client for the Accordion Context plugin protocol (spec section 19.4). Standard
library only (`urllib`); nothing is imported from third-party packages.

```python
import acd_ctx

client = acd_ctx.PluginClient.from_env(conversation_id="my-conversation")

plan = acd_ctx.compact_messages(
    [acd_ctx.message("user", "Read the config."),
     acd_ctx.message("assistant", "It sets port 8787.")],
    token_count=12000,
    client=client,
)
window = plan["visible"]          # send this window to the model
if plan["decision"]["nudge"]:
    pass                          # ask the model for a compress call

result = acd_ctx.run_tool("ctx_status", client=client)
```

`PluginClient.from_env()` reads `ACD_CTX_URL` (default `http://127.0.0.1:8787`),
`ACD_CTX_TOKEN` or the token file named by `ACD_CTX_TOKEN_FILE` (default
`~/.local/share/acd-ctx/plugin.token`), and `ACD_CTX_CONVERSATION`.

Errors: an HTTP error status raises `PluginError` (with `.status` and `.message`);
an unreachable server raises it with status 0. Treat either as "continue without
compression". `PluginClient.request()` returns `(status, body)` without raising.

Tests use `unittest` only and need no installed packages:

```sh
cd middleware/python
python3 -m unittest discover -s tests -v
```

The conformance replay runs against a fake server by default. Set
`ACD_CTX_CONFORMANCE_URL` (origin of a running `acd-ctx serve`) and
`ACD_CTX_CONFORMANCE_TOKEN` (its token) to replay against that server instead.

Not included: a wrapper around `httpx` or `requests` transports. Add one only as
an optional module that imports them lazily.
