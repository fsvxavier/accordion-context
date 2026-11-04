"""Accordion Context client for the plugin protocol (spec section 19.4).

Standard library only. Import this package freely: nothing here imports a
third-party HTTP library.
"""

from .client import (
    DEFAULT_URL,
    PLUGIN_PREFIX,
    PluginClient,
    PluginError,
    compact_messages,
    message,
    run_tool,
)

__all__ = [
    "DEFAULT_URL",
    "PLUGIN_PREFIX",
    "PluginClient",
    "PluginError",
    "compact_messages",
    "message",
    "run_tool",
]

__version__ = "0.1.0"
