#!/usr/bin/env python3
"""Smoke test the MCP server over stdio.

Speaks newline-delimited JSON-RPC: initialize → initialized → tools/list → tools/call.
"""
import json
import select
import subprocess
import sys
import time


def send(proc, msg):
    proc.stdin.write((json.dumps(msg) + "\n").encode())
    proc.stdin.flush()


def recv(proc, timeout=120):
    """Read one JSON line from stdout, with a soft timeout."""
    end = time.time() + timeout
    buf = b""
    while time.time() < end:
        rlist, _, _ = select.select([proc.stdout], [], [], 1.0)
        if not rlist:
            if proc.poll() is not None:
                raise RuntimeError(f"server exited (code={proc.returncode})")
            continue
        chunk = proc.stdout.readline()
        if not chunk:
            if proc.poll() is not None:
                raise RuntimeError(f"server exited (code={proc.returncode})")
            continue
        buf = chunk
        break
    if not buf:
        raise RuntimeError("recv timeout")
    return json.loads(buf)


def main():
    proc = subprocess.Popen(
        ["./bin/beans-preserver", "--config", "configs/default.yaml"],
        stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=sys.stderr,
    )
    try:
        # 1. initialize
        send(proc, {
            "jsonrpc": "2.0", "id": 1, "method": "initialize",
            "params": {
                "protocolVersion": "2025-06-18",
                "capabilities": {},
                "clientInfo": {"name": "smoke", "version": "0.1"},
            },
        })
        init = recv(proc, timeout=10)
        print("[init]", json.dumps(init)[:300])

        # 2. initialized notification
        send(proc, {"jsonrpc": "2.0", "method": "notifications/initialized"})

        # 3. tools/list
        send(proc, {"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
        listed = recv(proc, timeout=10)
        tools = [t["name"] for t in listed.get("result", {}).get("tools", [])]
        print("[tools]", tools)
        assert "local_filter" in tools, "local_filter not registered"

        # 4. tools/call — small input first (should be fast)
        sample = "\n".join([
            "2026-05-06T10:00:00Z INFO  api ok",
            "2026-05-06T10:00:01Z DEBUG cache miss key=foo",
            "2026-05-06T10:00:02Z ERROR db: connection refused",
            "2026-05-06T10:00:03Z INFO  api ok",
            "2026-05-06T10:00:04Z WARN  auth: token expiring",
            "2026-05-06T10:00:05Z DEBUG nothing happened",
            "2026-05-06T10:00:06Z ERROR db: query timeout",
        ])
        t0 = time.time()
        send(proc, {
            "jsonrpc": "2.0", "id": 3, "method": "tools/call",
            "params": {
                "name": "local_filter",
                "arguments": {
                    "criterion": "lines containing ERROR or WARN",
                    "content": sample,
                },
            },
        })
        called = recv(proc, timeout=180)
        elapsed = time.time() - t0
        print(f"[call] elapsed={elapsed:.1f}s")
        struct = called.get("result", {}).get("structuredContent", {})
        print("[result]", json.dumps(struct, indent=2)[:1200])

        # 5. cache hit — second identical call should be near-instant
        t0 = time.time()
        send(proc, {
            "jsonrpc": "2.0", "id": 4, "method": "tools/call",
            "params": {
                "name": "local_filter",
                "arguments": {
                    "criterion": "lines containing ERROR or WARN",
                    "content": sample,
                },
            },
        })
        cached = recv(proc, timeout=10)
        elapsed2 = time.time() - t0
        print(f"[cache call] elapsed={elapsed2:.2f}s")
        struct2 = cached.get("result", {}).get("structuredContent", {})
        cache_hit = struct2.get("cache_hit", False)
        print(f"[cache hit] {cache_hit}  elapsed_speedup={elapsed/max(elapsed2,0.001):.0f}x")

        ok = cache_hit and elapsed2 < 0.5
        print("\n=== SMOKE", "OK" if ok else "FAIL", "===")
        return 0 if ok else 1
    finally:
        proc.terminate()
        try:
            proc.wait(timeout=2)
        except subprocess.TimeoutExpired:
            proc.kill()


if __name__ == "__main__":
    sys.exit(main())
