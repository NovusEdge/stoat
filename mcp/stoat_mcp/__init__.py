"""stoat-mcp: an MCP server over the stoat CLI's JSON contract.

It runs the `stoat` binary with `--json` and decodes JSON Lines. It never
links Go and never reads ~/.stoat directly, so the CLI's contract
(docs/reference/json.md) is the only coupling.

The layering rule, from docs/design/mcp-server.md: this is a thin mapping. If
a tool needs logic `core` does not have, the layering is wrong and the fix
belongs in `core`, not here.
"""

from .client import EXPECTED_CONTRACT, Client
from .errors import ContractMismatch, GuardRejection, StoatCrashed, StoatError

__all__ = [
    "Client",
    "ContractMismatch",
    "EXPECTED_CONTRACT",
    "GuardRejection",
    "StoatCrashed",
    "StoatError",
]
