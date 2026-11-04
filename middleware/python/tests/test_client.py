"""Unit tests for acd_ctx against a fake plugin server (stdlib unittest only)."""

import json
import os
import tempfile
import unittest
from unittest import mock

import acd_ctx
from acd_ctx import PluginClient, PluginError, compact_messages, message, run_tool
from support import PLUGIN_PREFIX, TOKEN, FakeServer


def make_handler(routes):
    """routes maps "METHOD /path" (path without the prefix, query stripped) to a
    function(headers, body_dict_or_None) -> (status, value). The token is checked first."""

    def handler(method, path, headers, body):
        if headers.get("Authorization") != "Bearer " + TOKEN:
            return 401, {"error": "missing or invalid token"}
        bare = path.split("?", 1)[0]
        key = "%s %s" % (method, bare[len(PLUGIN_PREFIX):])
        if key not in routes:
            return 404, {"error": "no route " + key}
        parsed = json.loads(body) if body else None
        return routes[key](headers, parsed)

    return handler


class ClientTest(unittest.TestCase):
    def setUp(self):
        self.server = None

    def tearDown(self):
        if self.server:
            self.server.close()

    def serve(self, routes):
        self.server = FakeServer(make_handler(routes))
        return PluginClient(self.server.url, TOKEN, conversation_id="conv-1")

    def test_manifest(self):
        client = self.serve({"GET /manifest": lambda h, b: (200, {"protocol": "acd-ctx-plugin",
                                                                  "protocolVersion": 1, "tools": []})})
        m = client.manifest()
        self.assertEqual(m["protocol"], "acd-ctx-plugin")
        self.assertEqual(m["protocolVersion"], 1)

    def test_missing_token_raises_401(self):
        client = self.serve({"GET /manifest": lambda h, b: (200, {})})
        client.token = "wrong"
        with self.assertRaises(PluginError) as ctx:
            client.manifest()
        self.assertEqual(ctx.exception.status, 401)

    def test_request_reports_status_without_raising(self):
        client = self.serve({"GET /manifest": lambda h, b: (200, {"ok": True})})
        status, body = client.request("GET", "/manifest", auth="wrong")
        self.assertEqual(status, 401)
        self.assertIn("error", body)
        status, body = client.request("GET", "/manifest", auth=False)
        self.assertEqual(status, 401)

    def test_runtime_info_body(self):
        seen = {}

        def rt(headers, body):
            seen["body"] = body
            return 200, {"sessionId": "plg-1", "created": True}

        client = self.serve({"POST /runtime-info": rt})
        out = client.runtime_info(model="m1", context_window=1000, max_output_tokens=50, cwd="/w")
        self.assertEqual(out["sessionId"], "plg-1")
        self.assertEqual(seen["body"], {"conversationId": "conv-1", "model": "m1",
                                        "contextWindow": 1000, "maxOutputTokens": 50, "cwd": "/w"})

    def test_turn_returns_plan_and_sends_messages(self):
        seen = {}

        def turn(headers, body):
            seen["body"] = body
            return 200, {"ingested": len(body["messages"]), "lowWater": 0,
                         "decision": {"nudge": True, "tier": 1, "voice": "soft", "row": "r", "reason": ""},
                         "hints": [], "visible": body["messages"]}

        client = self.serve({"POST /turn": turn})
        msgs = [message("user", "hello"), message("assistant", "hi", tokens=3)]
        plan = client.turn(msgs, 500)
        self.assertEqual(seen["body"]["tokenCount"], 500)
        self.assertEqual(seen["body"]["messages"][1]["tokens"], 3)
        self.assertTrue(plan["decision"]["nudge"])
        self.assertEqual(plan["ingested"], 2)

    def test_run_tool_returns_result(self):
        def tool(headers, body):
            self.assertEqual(body["name"], "retrieve")
            self.assertEqual(body["arguments"], {"ref": "m00001"})
            return 200, {"result": {"ref": "m00001", "text": "alpha"}}

        client = self.serve({"POST /tool": tool})
        self.assertEqual(client.run_tool("retrieve", {"ref": "m00001"}),
                         {"ref": "m00001", "text": "alpha"})

    def test_tool_error_raises_with_message(self):
        client = self.serve({"POST /tool": lambda h, b: (422, {"error": "range 0: covers 3 tokens"})})
        with self.assertRaises(PluginError) as ctx:
            client.run_tool("compress", {"ranges": []})
        self.assertEqual(ctx.exception.status, 422)
        self.assertEqual(ctx.exception.message, "range 0: covers 3 tokens")

    def test_status_sends_query(self):
        client = self.serve({"GET /status": lambda h, b: (200, {"messages": 2})})
        self.assertEqual(client.status(), {"messages": 2})

    def test_compact_returns_spans_and_nudge_flag(self):
        seen = {}

        def handler(h, b):
            seen["body"] = b
            return 200, {"sessionId": "s", "nudgePending": True, "spans": [{"start": "m00002", "end": "m00004", "tokens": 6000}]}

        client = self.serve({"POST /compact": handler})
        out = client.compact()
        self.assertEqual(seen["body"], {"conversationId": "conv-1"})
        self.assertTrue(out["nudgePending"])
        self.assertEqual(out["spans"][0]["start"], "m00002")

    def test_compact_unknown_conversation_is_404(self):
        client = self.serve({"POST /compact": lambda h, b: (404, {"error": "unknown conversation"})})
        with self.assertRaises(PluginError) as ctx:
            client.compact()
        self.assertEqual(ctx.exception.status, 404)

    def test_conversation_id_is_required(self):
        client = PluginClient("http://127.0.0.1:1", TOKEN)
        with self.assertRaises(ValueError):
            client.turn([])

    def test_unreachable_server_raises_status_zero(self):
        client = PluginClient("http://127.0.0.1:1", TOKEN, conversation_id="c", timeout=1)
        with self.assertRaises(PluginError) as ctx:
            client.manifest()
        self.assertEqual(ctx.exception.status, 0)

    def test_base_url_trailing_slash_is_trimmed(self):
        client = PluginClient("http://127.0.0.1:9/", TOKEN)
        self.assertEqual(client.base_url, "http://127.0.0.1:9")


class HelperTest(unittest.TestCase):
    def setUp(self):
        self.server = None

    def tearDown(self):
        if self.server:
            self.server.close()

    def test_compact_messages_and_run_tool_use_given_client(self):
        def turn(headers, body):
            return 200, {"ingested": 1, "visible": body["messages"], "decision": {"nudge": False},
                         "hints": [], "lowWater": 0}

        def tool(headers, body):
            return 200, {"result": {"activeBlocks": [], "messages": 1}}

        self.server = FakeServer(make_handler({"POST /turn": turn, "POST /tool": tool}))
        client = PluginClient(self.server.url, TOKEN)
        plan = compact_messages([message("user", "x")], 10, client=client, conversation_id="c9")
        self.assertEqual(plan["visible"][0]["text"], "x")
        self.assertEqual(run_tool("ctx_status", client=client, conversation_id="c9")["messages"], 1)

    def test_from_env_reads_token_file(self):
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, "plugin.token")
            with open(path, "w", encoding="utf-8") as f:
                f.write(TOKEN + "\n")
            env = {"ACD_CTX_URL": "http://127.0.0.1:9999", "ACD_CTX_TOKEN_FILE": path,
                   "ACD_CTX_CONVERSATION": "from-env"}
            with mock.patch.dict(os.environ, env, clear=False):
                os.environ.pop("ACD_CTX_TOKEN", None)
                client = PluginClient.from_env()
            self.assertEqual(client.token, TOKEN)
            self.assertEqual(client.base_url, "http://127.0.0.1:9999")
            self.assertEqual(client.conversation_id, "from-env")

    def test_from_env_prefers_token_variable(self):
        with mock.patch.dict(os.environ, {"ACD_CTX_TOKEN": "  " + TOKEN + " "}, clear=False):
            client = PluginClient.from_env("c")
        self.assertEqual(client.token, TOKEN)

    def test_version_and_exports(self):
        self.assertEqual(acd_ctx.PLUGIN_PREFIX, "/__acd-ctx/plugin")
        self.assertTrue(acd_ctx.__version__)


if __name__ == "__main__":
    unittest.main()
