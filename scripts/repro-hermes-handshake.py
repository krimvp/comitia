#!/usr/bin/env python3
"""Reproduce Hermes' MCP client handshake against the Comitia gateway.

Uses the same mcp SDK version Hermes uses, so any method-name or framing
mismatch shows up here with a real traceback instead of a generic error.
"""
import asyncio, json, pathlib, sys, traceback

from mcp import ClientSession
from mcp.client.streamable_http import (streamable_http_client,
                                        create_mcp_http_client)

TOKEN = json.loads(
    pathlib.Path("/root/.comitia/sessions/hermes.json").read_text())["credential"]["Token"]
URL = "http://127.0.0.1:8391/mcp"


async def main():
    client = create_mcp_http_client(headers={"Authorization": f"Bearer {TOKEN}"})
    async with streamable_http_client(URL, http_client=client) as streams:
        read, write = streams[0], streams[1]
        async with ClientSession(read, write) as s:
            init = await s.initialize()
            print("OK initialize ->", init.server_info.name, init.protocol_version)
            tools = await s.list_tools()
            print("OK list_tools ->", len(tools.tools), "tools")
            return 0


try:
    rc = asyncio.run(main())
    print("RESULT: handshake OK")
    sys.exit(rc)
except Exception as exc:
    print("RESULT: FAILED")
    traceback.print_exc()
    # Surface any structured error detail.
    for c in getattr(exc, "exceptions", []) or []:
        print("  sub:", type(c).__name__, c)
    sys.exit(1)
