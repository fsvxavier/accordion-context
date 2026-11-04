"""Replays the shared vectors in conformance/vectors (see the Go runner for the format).

Without ACD_CTX_CONFORMANCE_URL and ACD_CTX_CONFORMANCE_TOKEN, every vector runs
against a fake server that checks each request against the vector and answers
with its expected response. With both variables set, the vectors run against
that live server instead; each vector gets its own conversation id.
"""

import os
import unittest
import uuid

from acd_ctx import PluginClient, PluginError
from support import ReplayServer, load_suites, match, substitute, TOKEN


class ConformanceTest(unittest.TestCase):
    def test_replay_all_vectors(self):
        live_url = os.environ.get("ACD_CTX_CONFORMANCE_URL", "")
        live_token = os.environ.get("ACD_CTX_CONFORMANCE_TOKEN", "")
        live = bool(live_url and live_token)
        suites = load_suites()
        self.assertTrue(suites, "no vector files found")
        vectors = 0
        for suite in suites:
            for vec in suite["vectors"]:
                label = "%s/%s" % (suite["suite"], vec["name"])
                with self.subTest(vector=label):
                    vectors += 1
                    if live:
                        self._run_live(live_url, live_token, vec, "conf-%s-%s" % (vec["name"], uuid.uuid4().hex[:8]))
                    else:
                        self._run_fake(vec, "conf-" + vec["name"])
        self.assertGreater(vectors, 0)

    def _run_steps(self, client, steps, conv):
        for i, step in enumerate(steps):
            req, exp = step["request"], step["expect"]
            status, body = client.request(
                req["method"],
                substitute(req["path"], conv),
                substitute(req.get("body"), conv) if "body" in req else None,
                auth=req.get("auth", True),
                raw=req.get("raw"),
            )
            self.assertEqual(status, exp["status"], "step %d: %s %s" % (i, req["method"], req["path"]))
            for key in exp.get("keys", []):
                self.assertIsInstance(body, dict, "step %d: keys need an object" % i)
                self.assertIn(key, body, "step %d: missing key %r" % (i, key))
            if "json" in exp:
                reason = match(exp["json"], body)
                self.assertIsNone(reason, "step %d: %s" % (i, reason))

    def _run_fake(self, vec, conv):
        server = ReplayServer(vec["steps"], conv)
        try:
            client = PluginClient(server.url, TOKEN, timeout=5)
            self._run_steps(client, vec["steps"], conv)
            self.assertEqual(server.mismatches, [])
            self.assertEqual(server.index, len(vec["steps"]))
        finally:
            server.close()

    def _run_live(self, url, token, vec, conv):
        client = PluginClient(url, token, timeout=10)
        try:
            self._run_steps(client, vec["steps"], conv)
        except PluginError as e:
            self.fail("live server unreachable: %s" % e)


if __name__ == "__main__":
    unittest.main()
