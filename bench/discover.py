#!/usr/bin/env python3
"""
discover.py — probe any MCP server, rank tools by response size, and
optionally emit a scenario JSON file for bench/run.go.

Usage:
    python bench/discover.py -- npx -y @modelcontextprotocol/server-fetch
    python bench/discover.py --threshold 5000 --output bench/scenarios/discovered.json -- uvx mcp-server-git --repository /path/to/repo

Options:
    --threshold N    bytes to mark a response as "large"  (default: 10240)
    --output FILE    write a scenario JSON stub to FILE
    --timeout N      seconds to wait for each tool call    (default: 20)
"""

import argparse
import json
import os
import subprocess
import sys
import threading


def send(proc, msg):
    line = json.dumps(msg) + "\n"
    proc.stdin.write(line.encode())
    proc.stdin.flush()


def recv(proc, timeout=20):
    """Read the next non-notification JSON-RPC message, skipping notifications."""
    deadline = threading.Event()
    result = {"msg": None, "err": None}

    def _read():
        try:
            while True:
                raw = proc.stdout.readline()
                if not raw:
                    result["err"] = "connection closed"
                    return
                raw = raw.strip()
                if not raw:
                    continue
                try:
                    msg = json.loads(raw)
                except json.JSONDecodeError as e:
                    result["err"] = f"JSON parse error: {e}"
                    return
                # skip pure notifications (have "method" but no "id")
                if "method" in msg and "id" not in msg:
                    continue
                result["msg"] = msg
                return
        except Exception as e:
            result["err"] = str(e)

    t = threading.Thread(target=_read, daemon=True)
    t.start()
    t.join(timeout)
    if t.is_alive():
        raise TimeoutError(f"no response after {timeout}s")
    if result["err"]:
        raise RuntimeError(result["err"])
    return result["msg"]


def default_args_for(schema):
    """Best-effort: construct a minimal argument object from an inputSchema."""
    if not schema or schema.get("type") != "object":
        return {}
    props = schema.get("properties", {})
    required = schema.get("required", [])
    args = {}
    for key in required:
        prop = props.get(key, {})
        t = prop.get("type", "string")
        if t == "string":
            args[key] = prop.get("default", "")
        elif t == "integer":
            args[key] = prop.get("default", 0)
        elif t == "boolean":
            args[key] = prop.get("default", False)
        elif t == "array":
            args[key] = []
        elif t == "object":
            args[key] = {}
        else:
            args[key] = None
    return args


def main():
    parser = argparse.ArgumentParser(
        description="Probe an MCP server and find tools that produce large responses."
    )
    parser.add_argument("--threshold", type=int, default=10240,
                        help="bytes to consider 'large' (default: 10240)")
    parser.add_argument("--output", type=str, default=None,
                        help="write scenario JSON stubs to this file")
    parser.add_argument("--timeout", type=int, default=20,
                        help="seconds to wait per tool call (default: 20)")
    parser.add_argument("cmd", nargs=argparse.REMAINDER,
                        help="-- <mcp-server-cmd> [args...]")
    args = parser.parse_args()

    cmd = args.cmd
    if cmd and cmd[0] == "--":
        cmd = cmd[1:]
    if not cmd:
        parser.print_help()
        sys.exit(1)

    print(f"Starting MCP server: {' '.join(cmd)}", flush=True)
    proc = subprocess.Popen(
        cmd,
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )

    try:
        _run(proc, cmd, args)
    finally:
        try:
            proc.stdin.close()
        except Exception:
            pass
        proc.kill()
        proc.wait()


def _run(proc, cmd, args):
    # initialize handshake
    send(proc, {
        "jsonrpc": "2.0", "id": 1, "method": "initialize",
        "params": {
            "protocolVersion": "2024-11-05",
            "capabilities": {},
            "clientInfo": {"name": "discover", "version": "1"},
        },
    })
    resp = recv(proc, timeout=15)
    server_info = resp.get("result", {}).get("serverInfo", {})
    print(f"Connected to: {server_info.get('name', '?')} v{server_info.get('version', '?')}\n")

    # list tools
    send(proc, {"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
    resp = recv(proc, timeout=15)
    tools = resp.get("result", {}).get("tools", [])
    print(f"Found {len(tools)} tool(s):\n")

    records = []
    for i, tool in enumerate(tools):
        name = tool["name"]
        schema = tool.get("inputSchema", {})
        required = schema.get("required", [])
        call_args = default_args_for(schema)
        has_unfilled = any(
            call_args.get(k) in (None, "", 0, [], {})
            for k in required
        )

        prefix = f"  [{i+1:>2}/{len(tools)}] {name}"
        if has_unfilled:
            print(f"{prefix}")
            print(f"           SKIP — requires args that can't be auto-filled: "
                  f"{[k for k in required if call_args.get(k) in (None, '', 0, [], {})]}")
            records.append({
                "tool": name, "status": "skipped",
                "required": required, "size": None,
            })
            continue

        print(f"{prefix}  (calling with {json.dumps(call_args)}) ...", end="", flush=True)
        try:
            send(proc, {
                "jsonrpc": "2.0", "id": 100 + i, "method": "tools/call",
                "params": {"name": name, "arguments": call_args},
            })
            resp = recv(proc, timeout=args.timeout)
            result = resp.get("result", {})
            is_error = result.get("isError", False)
            content_raw = json.dumps(result.get("content", []))
            size = len(content_raw.encode("utf-8"))
            tag = " *** LARGE" if size >= args.threshold else ""
            err_tag = " [tool error]" if is_error else ""
            print(f"  {size:>10,} bytes{tag}{err_tag}")
            records.append({
                "tool": name, "status": "ok" if not is_error else "tool_error",
                "size": size, "args_used": call_args,
            })
        except TimeoutError:
            print(f"  TIMEOUT after {args.timeout}s")
            records.append({"tool": name, "status": "timeout", "size": None})
        except Exception as e:
            print(f"  ERROR: {e}")
            records.append({"tool": name, "status": "error", "error": str(e), "size": None})

    # summary
    large = [r for r in records if r.get("size") and r["size"] >= args.threshold]
    ok = [r for r in records if r.get("status") == "ok"]
    print(f"\n{'─'*60}")
    print(f"Results: {len(ok)} callable, {len(large)} above {args.threshold:,}B threshold\n")

    if large:
        print("Large tools (sorted by response size):")
        for r in sorted(large, key=lambda x: x["size"], reverse=True):
            tokens = r["size"] // 4
            print(f"  {r['tool']:<35} {r['size']:>10,} bytes  (~{tokens:,} tokens)")
    else:
        print("No tools produced large responses with the auto-generated arguments.")
        print("Try calling specific tools manually or adjust --threshold.")

    if args.output:
        if not large:
            print(f"\nNothing to write (no large tools found).")
            return
        scenario = {
            "name": "discovered",
            "mcp": " ".join(cmd),
            "cmd": cmd,
            "notes": f"Auto-generated by discover.py. Threshold: {args.threshold} bytes.",
            "cases": [
                {
                    "name": r["tool"],
                    "tool": r["tool"],
                    "arguments": r.get("args_used", {}),
                    "filter": ".[0].text[:2000]",
                }
                for r in sorted(large, key=lambda x: x["size"], reverse=True)
            ],
        }
        with open(args.output, "w", encoding="utf-8") as f:
            json.dump(scenario, f, indent=2)
        print(f"\nWrote {len(scenario['cases'])} scenario case(s) to {args.output}")
        print("Edit the 'filter' fields before running bench/run.go.")


if __name__ == "__main__":
    main()
