"""HTTP client for the Accordion Context plugin protocol (spec section 19.4).

Every call goes to the proxy's /__acd-ctx/plugin/ endpoints with the instance
bearer token. Calls that get an HTTP error status raise PluginError; callers
should treat any failure as "continue without compression" (fail-open, §19.7).
"""

from __future__ import annotations

import json
import os
import urllib.error
import urllib.parse
import urllib.request
from typing import Any, Dict, Iterable, Optional, Tuple

PLUGIN_PREFIX = "/__acd-ctx/plugin"
DEFAULT_URL = "http://127.0.0.1:8787"
DEFAULT_TIMEOUT = 10.0


class PluginError(Exception):
    """A failed plugin call. status is 0 when the server is unreachable."""

    def __init__(self, status: int, message: str, body: Any = None):
        super().__init__(f"acd-ctx plugin: HTTP {status}: {message}")
        self.status = status
        self.message = message
        self.body = body


def default_token_path() -> str:
    """Where serve writes the token when --plugin-token-file is not given."""
    return os.path.join(os.path.expanduser("~"), ".local", "share", "acd-ctx", "plugin.token")


def message(role: str, text: str, kind: str = "text", **extra: Any) -> Dict[str, Any]:
    """Build one wire message. Extra keys: toolName, toolCallId, tokens."""
    m: Dict[str, Any] = {"role": role, "kind": kind, "text": text}
    m.update(extra)
    return m


class PluginClient:
    """Speaks the plugin protocol to one Accordion Context instance.

    conversation_id is the agent's conversation; pass conversation_id to a
    method to override it for one call.
    """

    def __init__(
        self,
        base_url: str = DEFAULT_URL,
        token: str = "",
        conversation_id: Optional[str] = None,
        timeout: float = DEFAULT_TIMEOUT,
    ):
        if not token:
            raise ValueError("token is required")
        self.base_url = base_url.rstrip("/")
        self.token = token
        self.conversation_id = conversation_id
        self.timeout = timeout
        # An empty ProxyHandler keeps HTTP(S)_PROXY from the environment out of
        # loopback traffic.
        self._opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    @classmethod
    def from_env(cls, conversation_id: Optional[str] = None) -> "PluginClient":
        """Build a client from ACD_CTX_URL, ACD_CTX_TOKEN or ACD_CTX_TOKEN_FILE,
        and ACD_CTX_CONVERSATION. The token file defaults to ~/.local/share/acd-ctx/plugin.token."""
        url = os.environ.get("ACD_CTX_URL", DEFAULT_URL)
        token = os.environ.get("ACD_CTX_TOKEN", "").strip()
        if not token:
            path = os.environ.get("ACD_CTX_TOKEN_FILE") or default_token_path()
            with open(path, encoding="utf-8") as f:
                token = f.read().strip()
        conv = conversation_id or os.environ.get("ACD_CTX_CONVERSATION") or None
        return cls(url, token, conv)

    def request(
        self,
        method: str,
        path: str,
        body: Any = None,
        *,
        auth: Any = True,
        raw: Optional[str] = None,
    ) -> Tuple[int, Any]:
        """Send one request and return (status, parsed JSON or None).

        path is relative to /__acd-ctx/plugin and may carry a query string.
        auth is True (instance token), False (no header) or "wrong" (a token
        that is not the instance token). raw, when set, is sent as the body
        instead of body. It never raises for an HTTP status; it raises
        PluginError only when the server cannot be reached.
        """
        data: Optional[bytes] = None
        if raw is not None:
            data = raw.encode("utf-8")
        elif body is not None:
            data = json.dumps(body).encode("utf-8")
        elif method == "POST":
            data = b""
        headers = {"Accept": "application/json"}
        if data is not None:
            headers["Content-Type"] = "application/json"
        if auth is True:
            headers["Authorization"] = "Bearer " + self.token
        elif auth == "wrong":
            headers["Authorization"] = "Bearer " + "x" * 40
        req = urllib.request.Request(
            self.base_url + PLUGIN_PREFIX + "/" + path.lstrip("/"),
            data=data, method=method, headers=headers
        )
        try:
            with self._opener.open(req, timeout=self.timeout) as resp:
                status, payload = resp.status, resp.read()
        except urllib.error.HTTPError as e:
            status, payload = e.code, e.read()
        except urllib.error.URLError as e:
            raise PluginError(0, f"unreachable: {e.reason}") from e
        parsed: Any = None
        if payload:
            try:
                parsed = json.loads(payload.decode("utf-8"))
            except ValueError:
                parsed = None
        return status, parsed

    def _call(self, method: str, path: str, body: Any = None) -> Any:
        status, parsed = self.request(method, path, body)
        if status >= 400:
            msg = parsed.get("error") if isinstance(parsed, dict) else None
            raise PluginError(status, msg or "request failed", parsed)
        return parsed

    def _cid(self, conversation_id: Optional[str]) -> str:
        cid = conversation_id or self.conversation_id
        if not cid:
            raise ValueError("conversation_id is required")
        return cid

    def manifest(self) -> Dict[str, Any]:
        """GET manifest: protocol version, tool specs and doctrine."""
        return self._call("GET", "/manifest")

    def runtime_info(
        self,
        *,
        model: str = "",
        context_window: int = 0,
        max_output_tokens: int = 0,
        cwd: str = "",
        conversation_id: Optional[str] = None,
    ) -> Dict[str, Any]:
        """POST runtime-info. The window and model apply when the session is created."""
        body = {
            "conversationId": self._cid(conversation_id),
            "model": model,
            "contextWindow": context_window,
            "maxOutputTokens": max_output_tokens,
            "cwd": cwd,
        }
        return self._call("POST", "/runtime-info", body)

    def turn(
        self,
        messages: Iterable[Dict[str, Any]],
        token_count: int = 0,
        *,
        conversation_id: Optional[str] = None,
    ) -> Dict[str, Any]:
        """POST turn: ingest the transcript and return the plan.

        The response has ingested, lowWater, decision, hints and visible (the
        window the model should see, as wire messages).
        """
        body = {
            "conversationId": self._cid(conversation_id),
            "tokenCount": token_count,
            "messages": list(messages),
        }
        return self._call("POST", "/turn", body)

    def run_tool(
        self,
        name: str,
        arguments: Optional[Dict[str, Any]] = None,
        *,
        conversation_id: Optional[str] = None,
    ) -> Any:
        """POST tool and return its result. name is one of the manifest tools."""
        body = {
            "conversationId": self._cid(conversation_id),
            "name": name,
            "arguments": arguments or {},
        }
        return self._call("POST", "/tool", body)["result"]

    def status(self, conversation_id: Optional[str] = None) -> Dict[str, Any]:
        """GET status for the conversation."""
        query = urllib.parse.urlencode({"conversationId": self._cid(conversation_id)})
        return self._call("GET", "/status?" + query)

    def compact(self, conversation_id: Optional[str] = None) -> Dict[str, Any]:
        """POST compact. The server answers 501 until forced compaction exists."""
        return self._call("POST", "/compact", {"conversationId": self._cid(conversation_id)})


def compact_messages(
    messages: Iterable[Dict[str, Any]],
    token_count: int = 0,
    *,
    client: Optional[PluginClient] = None,
    conversation_id: Optional[str] = None,
) -> Dict[str, Any]:
    """Ingest the transcript and return the turn plan.

    Use the "visible" list of the result as the window to send to the model.
    If decision.nudge is true, the agent should ask for a compress call.
    Without a client, PluginClient.from_env() is used.
    """
    if client is None:
        client = PluginClient.from_env(conversation_id)
    return client.turn(messages, token_count, conversation_id=conversation_id)


def run_tool(
    name: str,
    args: Optional[Dict[str, Any]] = None,
    *,
    client: Optional[PluginClient] = None,
    conversation_id: Optional[str] = None,
) -> Any:
    """Run one tool (compress, decompress, retrieve, ctx_status) and return its result.

    Without a client, PluginClient.from_env() is used.
    """
    if client is None:
        client = PluginClient.from_env(conversation_id)
    return client.run_tool(name, args, conversation_id=conversation_id)
