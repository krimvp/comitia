#!/usr/bin/env python3
"""Drive the gateway with a real MCP client library (not hand-rolled HTTP).

Uses the official `mcp` Python SDK's streamablehttp_client if installed.
"""
import json, pathlib, asyncio, sys

try:
    from mcp.client.streamable_http import (streamable_http_client as _shc,
                                            create_mcp_http_client as _mkclient)
    from mcp import ClientSession
except ImportError as e:
    print(f"SKIP: official mcp SDK not available ({e})")
    sys.exit(2)

TOKEN = json.loads(
    pathlib.Path("/root/.comitia/sessions/chatgpt.json").read_text())["credential"]["Token"]
CTX = [l.split("=", 1)[1].strip()
       for l in pathlib.Path("/root/.comitia/ids.env").read_text().splitlines()
       if l.startswith("COMITIA_CONTEXT_ID=")][0]

URL = "http://127.0.0.1:8391/mcp"

async def main():
    headers = {"Authorization": f"Bearer {TOKEN}"}
    client = _mkclient(headers=headers)
    async with _shc(URL, http_client=client) as streams:
        read, write = streams[0], streams[1]
        async with ClientSession(read, write) as session:
            init = await session.initialize()
            print("server:", init.server_info.name, init.server_info.version)
            print("protocol:", init.protocol_version)

            tools = await session.list_tools()
            print(f"tools ({len(tools.tools)}):", ", ".join(t.name for t in tools.tools))

            res = await session.call_tool("list_participants", {"context_id": CTX})
            sc = getattr(res, "structured_content", None) or {}
            peers = sc.get("result")
            print("peers:", [p.get("Name") for p in peers] if peers else res.content)

            res = await session.call_tool("send_message", {
                "context_id": CTX, "dedupe_key": "sdk-client-1",
                "body": "sent by the official MCP python SDK"})
            print("send_message isError:", res.is_error)

            res = await session.call_tool("replay_messages",
                                          {"context_id": CTX, "after": 0, "limit": 100})
            sc = getattr(res, "structured_content", None) or {}
            found = "sent by the official MCP python SDK" in json.dumps(sc)
            print("replayed our message:", found)
            return 0 if (found and len(tools.tools) == 11) else 1

rc = asyncio.run(main())
print("\nSDK client check:", "PASS" if rc == 0 else "FAIL")
sys.exit(rc)
