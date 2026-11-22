#!/usr/bin/env python3
"""Run the minimal backend/Engine/bare Runner stack as native Mac/Linux processes."""
import argparse
import os
from pathlib import Path
import shutil
import signal
import subprocess
import sys
import time
from local import read_env

ROOT = Path(__file__).resolve().parent.parent
COMPONENTS = ("event-hub", "backend", "sandbox-runner", "agent-engine")


def prepare():
    for name in ("go", "node", "npm", "bash", "rg", "fd"):
        if not shutil.which(name):
            raise ValueError("Missing local dependency: " + name)
    for component in ("agent-engine", "sandbox-runner/builtin-mcp"):
        subprocess.run(["npm", "ci", "--no-audit", "--no-fund"], cwd=ROOT / component, check=True)
    subprocess.run(["npm", "rebuild", "node-pty"], cwd=ROOT / "sandbox-runner/builtin-mcp", check=True)
    subprocess.run(["npm", "run", "build"], cwd=ROOT / "sandbox-runner/builtin-mcp", check=True)
    for component in COMPONENTS:
        subprocess.run(["make", "build"], cwd=ROOT / component, check=True)


def run():
    env = dict(os.environ)
    for name in ("DATABASE_URL", "KIKO_AUTH_SECRET", "KIKO_ENGINE_TOKEN"):
        value = env.get(name, "")
        if not value or name != "DATABASE_URL" and len(value.encode()) < 32:
            raise ValueError(name + " is required (secrets need 32+ bytes); existing secrets are never replaced")
    for component in COMPONENTS:
        if not (ROOT / component / "bin" / component).is_file():
            raise ValueError("Run python3 tools/runtime.py prepare first")
    env.setdefault("KIKO_PUBLIC_ORIGIN", "http://localhost:8080")
    env.setdefault("KIKO_HTTP_ADDRESS", "127.0.0.1:8080")
    env.setdefault("KIKO_BACKEND_URL", "http://127.0.0.1:8080")
    env.setdefault("EVENT_HUB_DATABASE_URL", env["DATABASE_URL"])
    env["KIKO_REPO_ROOT"] = str(ROOT)
    children = []
    stopping = False

    def stop(_signum, _frame):
        nonlocal stopping
        stopping = True

    signal.signal(signal.SIGINT, stop)
    signal.signal(signal.SIGTERM, stop)
    try:
        # Backend and Engine start independently; Event Hub owns migration and
        # retries. Runner is started lazily by Engine, never as a global daemon.
        for component in ("event-hub", "backend", "agent-engine"):
            child_env = env.copy()
            if component != "backend":
                child_env.pop("KIKO_GITHUB_OAUTH_CLIENT_ID", None)
                child_env.pop("KIKO_GITHUB_OAUTH_CLIENT_SECRET", None)
            process = subprocess.Popen([str(ROOT / component / "bin" / component)], cwd=ROOT, env=child_env, start_new_session=True)
            children.append((component, process))
        print("Native processes started: " + env["KIKO_PUBLIC_ORIGIN"] + " (API only; Ctrl-C stops processes and retains data)", flush=True)
        while not stopping:
            if any(process.poll() is not None for _, process in children):
                raise ValueError("A service exited; inspect service logs above. Existing state is retained.")
            time.sleep(0.2)
    finally:
        # Keep Backend available for Engine stop/release/reclaim confirmation.
        for component, process in reversed(children):
            if process.poll() is not None:
                continue
            process.terminate()
            try:
                process.wait(timeout=70 if component == "agent-engine" else 20)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()
                print(component + " forced exit; retained execution state requires inspection", file=sys.stderr)


def verify_model():
    """Explicit live acceptance; credentials stay in the ignored local .env."""
    env = dict(os.environ)
    path = ROOT / ".env"
    values = read_env(path) if path.exists() else {}
    for name in ("DEEPSEEK_API_KEY", "DEEPSEEK_BASE_URL", "DEEPSEEK_MODEL"):
        if not env.get(name):
            env[name] = values.get(name, "")
        if not env[name]:
            raise ValueError(name + " is required in local .env or environment")
    if not env.get("BACKEND_TEST_DATABASE_URL"):
        raise ValueError("BACKEND_TEST_DATABASE_URL is required for isolated live acceptance")
    for name in ("agent-engine/node_modules/.bin/pi", "sandbox-runner/bin/sandbox-runner", "sandbox-runner/builtin-mcp/dist/http-main.js"):
        if not (ROOT / name).is_file():
            raise ValueError("Run python3 tools/runtime.py prepare first")
    env["KIKO_LIVE_MODEL_E2E"] = "1"
    subprocess.run(["go", "test", "-race", "-count=1", "-timeout", "300s", "-run", "^TestManagerRuntimeLiveDeepSeek$", "-v", "./internal/platform/repository"], cwd=ROOT / "backend", env=env, check=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("prepare", "run", "verify-model"))
    args = parser.parse_args()
    try:
        {"prepare": prepare, "run": run, "verify-model": verify_model}[args.command]()
    except (ValueError, OSError, subprocess.CalledProcessError) as error:
        print(str(error) if isinstance(error, ValueError) else "Native stack operation failed; inspect build/service logs", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
