#!/usr/bin/env python3
"""Portable script control-flow tests, without binding a network port.

Uses disposable mock binaries, ps and curl. The actual Go HTTP handlers and
production builds are tested separately; this is not a network integration test.
"""
import os
import pathlib
import shutil
import subprocess
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]


class Scripts(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="codex session viewer scripts ")
        self.root = pathlib.Path(self.tmp.name)
        self.addCleanup(self.tmp.cleanup)
        for name in ("build.sh", "restart.sh", "stop.sh", "status.sh"):
            shutil.copy2(ROOT / name, self.root / name)
            subprocess.run(["/bin/sh", "-n", str(self.root / name)], check=True)
        for name in ("bin", "data", "log", "src", "mockbin"):
            (self.root / name).mkdir()
        self.env = dict(os.environ, SCRIPT_ROOT=str(self.root))
        self.env["PATH"] = str(self.root / "mockbin") + ":" + os.environ.get("PATH", "")

    def executable(self, path, text):
        path.write_text(text)
        path.chmod(0o755)

    def run_script(self, name, *args):
        return subprocess.run([str(self.root / name), *args], env=self.env,
                              capture_output=True, text=True, timeout=20)

    def test_lifecycle_and_pid_protection(self):
        self.executable(self.root / "bin/codex-session-viewer", '''#!/bin/sh
trap 'exit 0' TERM INT
printf '54321' > "$SCRIPT_ROOT/data/server.port"
while :; do sleep 0.1; done
''')
        self.executable(self.root / "mockbin/curl", '''#!/bin/sh
printf '{"ok":true,"pid":%s}' "$(cat "$SCRIPT_ROOT/data/server.pid")"
''')
        self.executable(self.root / "mockbin/ps", '''#!/bin/sh
case "$*" in *stat=*) printf 'R';; *) printf '%s --data-dir %s' "$SCRIPT_ROOT/bin/codex-session-viewer" "$SCRIPT_ROOT/data";; esac
''')
        try:
            started = self.run_script("restart.sh")
            self.assertEqual(started.returncode, 0, started.stderr + started.stdout)
            first = (self.root / "data/server.pid").read_text()
            self.assertEqual(self.run_script("status.sh").returncode, 0)
            restarted = self.run_script("restart.sh")
            self.assertEqual(restarted.returncode, 0, restarted.stderr + restarted.stdout)
            self.assertNotEqual(first, (self.root / "data/server.pid").read_text())
        finally:
            result = self.run_script("stop.sh")
            self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        self.assertNotEqual(self.run_script("status.sh").returncode, 0)
        (self.root / "data/server.pid").write_text("1")
        self.assertNotEqual(self.run_script("stop.sh").returncode, 0)
        (self.root / "data/server.pid").write_text("99999999")
        stale = self.run_script("status.sh")
        self.assertEqual(stale.returncode, 1)
        self.assertIn("过期 PID 99999999", stale.stdout)
        self.assertNotIn("unbound variable", stale.stderr)
        self.assertEqual(self.run_script("stop.sh").returncode, 0)
        self.assertFalse((self.root / "data/server.pid").exists())

    def test_build_matrix(self):
        self.executable(self.root / "mockbin/go", '''#!/bin/sh
if [ "$1" = env ]; then
 case "$2" in GOHOSTOS) printf 'darwin';; GOHOSTARCH) printf 'arm64';; esac
 exit 0
fi
printf '%s/%s\\n' "$GOOS" "$GOARCH" >> "$SCRIPT_ROOT/data/builds"
while [ "$#" -gt 0 ]; do
 if [ "$1" = -o ]; then shift; touch "$1"; break; fi
 shift
done
''')
        native = self.run_script("build.sh")
        self.assertEqual(native.returncode, 0, native.stderr)
        self.assertTrue((self.root / "bin/codex-session-viewer").exists())
        all_builds = self.run_script("build.sh", "all")
        self.assertEqual(all_builds.returncode, 0, all_builds.stderr)
        builds = (self.root / "data/builds").read_text().splitlines()
        self.assertEqual(builds[0], "darwin/arm64")
        self.assertEqual(set(builds[1:]), {f"{s}/{a}" for s in ("darwin", "linux", "windows") for a in ("amd64", "arm64")})
        self.assertEqual(len(builds[1:]), 6)
        self.assertNotEqual(self.run_script("build.sh", "unknown").returncode, 0)


if __name__ == "__main__":
    unittest.main(verbosity=2)
