#!/usr/bin/env python3
"""Generate a physical >5GB file and measure the Go test process's peak RSS."""
import os
import pathlib
import shutil
import subprocess
import sys
import tempfile

root = pathlib.Path(__file__).resolve().parents[2]
go = shutil.which("go")
if not go:
    raise SystemExit("Go is required for tests")
(root / "data").mkdir(exist_ok=True)
with tempfile.TemporaryDirectory(prefix="perf-runner-", dir=root / "data") as tmp:
    runner = str(pathlib.Path(tmp) / "session-tests")
    subprocess.run([go, "-C", str(root / "src"), "test", "-c", "-o", runner,
                    "./internal/session"], check=True)
    env = dict(os.environ)
    env.setdefault("CODEX_SESSION_VIEWER_PERF_GB", "6")
    proc = subprocess.Popen([runner, "-test.run=^TestSixGBPerformance$", "-test.v"],
                            env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                            text=True)
    for line in proc.stdout:
        print(line, end="", flush=True)
    _, status, usage = os.wait4(proc.pid, 0)
    proc.returncode = os.waitstatus_to_exitcode(status)
    rss = usage.ru_maxrss * (1 if sys.platform == "darwin" else 1024)
    print(f"Peak RSS of test process: {rss} bytes ({rss / 1048576:.1f} MiB)")
    if rss > 2 * 1024 ** 3:
        raise SystemExit("Memory acceptance failed: peak RSS > 2 GiB")
    raise SystemExit(proc.returncode)
