#!/usr/bin/env python3
"""Local-only Compose launcher. Never replaces configuration or removes data."""
import argparse
import fcntl
import hashlib
import os
from pathlib import Path
import re
import secrets
import subprocess
import sys
import tempfile
from docker_support import run, ensure_docker

ROOT = Path(__file__).resolve().parent.parent
VOLUME = "kikoteam-local-postgres-data"
RUNTIME_TOKENS = ("KIKO_HUB_TOKEN", "KIKO_ENGINE_TOKEN")


def read_env(path):
    if path.is_symlink() or not path.is_file() or path.stat().st_mode & 0o077:
        raise ValueError(".env must be a regular owner-only file (chmod 600 .env)")
    values = {}
    for line in path.read_text().splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        key, sep, value = line.partition("=")
        if not sep or not re.fullmatch(r"[A-Z][A-Z0-9_]*", key) or key in values:
            raise ValueError("invalid or duplicate .env key")
        if value.startswith("'") and value.endswith("'"):
            value = value[1:-1]
            if "'" in value or "\\" in value:
                raise ValueError("embedded quotes and escapes are unsupported in .env values")
        elif any(char in value for char in "\"'$#\\") or any(char.isspace() for char in value):
            raise ValueError("quote literal values with single quotes; interpolation is unsupported")
        values[key] = value
    return values


def validate(values):
    for key in ("POSTGRES_DB", "POSTGRES_USER"):
        if not re.fullmatch(r"[a-zA-Z_][a-zA-Z0-9_]*", values.get(key, "")):
            raise ValueError(key + " must be a simple SQL identifier")
    if not re.fullmatch(r"[A-Za-z0-9_-]{24,}", values.get("POSTGRES_PASSWORD", "")):
        raise ValueError("POSTGRES_PASSWORD must contain 24+ URL-safe letters/digits/_/-")
    if len(values.get("KIKO_AUTH_SECRET", "").encode()) < 32:
        raise ValueError("KIKO_AUTH_SECRET must be 32+ bytes")
    port = values.get("KIKO_LOCAL_PORT", "18080")
    if not port.isdigit() or not 1024 <= int(port) <= 65535:
        raise ValueError("KIKO_LOCAL_PORT must be 1024..65535")
    if values.get("KIKO_PUBLIC_ORIGIN") not in (f"http://localhost:{port}", f"http://127.0.0.1:{port}"):
        raise ValueError("local launcher only accepts localhost/127.0.0.1 origin matching its port")
    for key in ("KIKO_REGISTRATION_VERIFICATION_REQUIRED", "KIKO_MAIL_ENABLED", "SMTP_IMPLICIT_TLS"):
        if values.get(key, "false") not in ("true", "false"):
            raise ValueError(key + " must be true or false")
    if values.get("KIKO_REGISTRATION_VERIFICATION_REQUIRED") == "true" and values.get("KIKO_MAIL_ENABLED") != "true":
        raise ValueError("email verification requires mail enabled")


def initialize(root, volume_exists, inherited):
    path = root / ".env"
    marker = root / ".kiko-local" / "config.sha256"
    if not path.exists():
        legacy = (root / "backend" / ".env", root / "backend" / "backend.local.json", root / "backend.local.json")
        if any(p.exists() for p in legacy) or any(inherited.get(k) for k in ("DATABASE_URL", "EVENT_HUB_DATABASE_URL", "KIKO_CONFIG", "KIKO_AUTH_SECRET")):
            raise ValueError("existing deployment configuration detected; migrate explicitly, never generate replacement secrets")
        if marker.exists() or volume_exists:
            raise ValueError("existing local database/state detected without .env; restore .env from backup")
        text = (root / ".env.example").read_text()
        text = text.replace("POSTGRES_PASSWORD=\n", "POSTGRES_PASSWORD=" + secrets.token_hex(24) + "\n")
        text = text.replace("KIKO_AUTH_SECRET=\n", "KIKO_AUTH_SECRET=" + secrets.token_hex(32) + "\n")
        # O_EXCL prevents concurrent launchers overwriting each other's secrets.
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "w") as stream:
            stream.write(text)
    values = read_env(path)
    validate(values)
    # Guard accidental credential rotation against an existing persistent database.
    fingerprint = hashlib.sha256("\0".join(values[k] for k in ("POSTGRES_DB", "POSTGRES_USER", "POSTGRES_PASSWORD", "KIKO_AUTH_SECRET")).encode()).hexdigest()
    if marker.exists() and marker.read_text().strip() != fingerprint:
        raise ValueError("database identity/credentials changed; restore configuration or perform an explicit migration")
    if volume_exists and not marker.exists():
        raise ValueError("unrecognized existing Compose database; restore its .kiko-local state or migrate explicitly")
    marker.parent.mkdir(mode=0o700, exist_ok=True)
    if not marker.exists():
        fd = os.open(marker, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "w") as stream:
            stream.write(fingerprint + "\n")
    return values



def initialize_runtime(root):
    path = root / ".kiko-local" / "runtime.env"
    if not path.exists():
        # Publish the complete file at once and keep an existing copy intact.
        with tempfile.NamedTemporaryFile(mode="w", dir=path.parent) as pending:
            pending.writelines(f"{key}={secrets.token_hex(32)}\n" for key in RUNTIME_TOKENS)
            pending.flush()
            try:
                os.link(pending.name, path)
            except FileExistsError:
                pass
    values = read_env(path)
    tokens = {key: values.get(key, "") for key in RUNTIME_TOKENS}
    if any(len(value.encode()) < 32 for value in tokens.values()) or len(set(tokens.values())) != len(tokens):
        raise ValueError(".kiko-local/runtime.env must contain two distinct internal tokens of at least 32 bytes each")
    return tokens


def storage_environment(values, create=False):
    """Resolve the host asset directory; never rewrite a configured path or secrets."""
    raw = values.get("KIKO_STORAGE_LOCAL_ROOT", "").strip() or "~/.kiko"
    path = Path(raw).expanduser()
    if not path.is_absolute():
        raise ValueError("KIKO_STORAGE_LOCAL_ROOT must be absolute or start with ~/")
    if path.is_symlink():
        raise ValueError("KIKO_STORAGE_LOCAL_ROOT must not be a symlink")
    if create:
        path.mkdir(mode=0o700, parents=True, exist_ok=True)
        if not path.is_dir() or not os.access(path, os.W_OK | os.X_OK):
            raise ValueError("KIKO_STORAGE_LOCAL_ROOT must be a writable directory")
    return {
        "KIKO_LOCAL_STORAGE_HOST_ROOT": str(path),
        "KIKO_LOCAL_UID": str(os.getuid()),
        "KIKO_LOCAL_GID": str(os.getgid()),
    }


def run_compose(command, docker):
    tokens = {}
    if command in ("init", "up"):
        volumes = run([docker, "volume", "ls", "--format", "{{.Name}}"], label="检查已有数据卷", timeout=20, capture_output=True, text=True).stdout.splitlines()
        values = initialize(ROOT, VOLUME in volumes, os.environ)
        tokens = initialize_runtime(ROOT)
    else:
        values = read_env(ROOT / ".env")
        validate(values)
    if command == "init":
        print("本地配置已就绪。")
        return
    env = {key: value for key, value in os.environ.items() if key not in values and key not in RUNTIME_TOKENS}
    env.update(storage_environment(values, create=command == "up"))
    env.update(tokens)
    args = [docker, "compose", "--project-name", "kikoteam-local", "--project-directory", str(ROOT), "--env-file", str(ROOT / ".env"), "-f", str(ROOT / "compose.yaml")]
    opts = {"cwd": ROOT, "env": env}
    if command != "up":
        actions = {"down": ["--profile", "maintenance", "down"], "status": ["ps"], "logs": ["logs", "--tail", "100"]}
        run(args + actions[command], label="Compose " + command, timeout=180, **opts)
        return

    revision = "源码归档"
    if (ROOT / ".git").exists():
        revision = run(["git", "rev-parse", "--short", "HEAD"], label="确认构建版本", timeout=10, cwd=ROOT,
                       capture_output=True, text=True).stdout.strip()
        status = run(["git", "status", "--porcelain"], label="检查工作区版本", timeout=10, cwd=ROOT,
                     capture_output=True, text=True).stdout
        if status:
            revision += "（含本地改动）"
    run(args + ["build", "backend", "event-hub", "agent-engine"], label="构建 Web、Backend、Event Hub 和 Engine", timeout=1800, **opts)
    run(args + ["stop", "--timeout", "90", "agent-engine"], label="等待旧 Engine 停稳", timeout=110, **opts)
    run(args + ["stop", "--timeout", "25", "event-hub"], label="等待旧 Event Hub 退出", timeout=45, **opts)
    up = args + ["up", "-d", "--no-build", "--wait", "--wait-timeout", "180"]
    run(up + ["backend", "event-hub"], label="启动 Backend、Event Hub 并等待迁移", timeout=360, **opts)
    run(args + ["run", "--pull", "never", "--rm", "--no-deps", "template-catalog"], label="同步官方资源和员工模板", timeout=90, **opts)
    run(up + ["--no-deps", "agent-engine"], label="启动 Engine 并等待注册", timeout=210, **opts)
    print(f"已就绪 {values['KIKO_PUBLIC_ORIGIN']} · Git {revision}")


def main():
    parser = argparse.ArgumentParser(prog="./start.sh", description=__doc__, allow_abbrev=False)
    parser.add_argument(
        "command", nargs="?", default="up", choices=("init", "up", "restart", "down", "status", "logs"),
        help="默认执行 up，使用当前本地源码。restart 保留更新并重启的行为。",
    )
    parser.add_argument("--update", action="store_true", help="快进拉取当前分支的上游，再构建并启动")
    args = parser.parse_args()
    command = args.command
    if args.update and command not in ("up", "restart"):
        parser.error("--update 仅适用于 up 和 restart")
    try:
        if args.update or command == "restart":
            if (ROOT / ".git").exists():
                try:
                    run(["git", "pull", "--ff-only", "--no-rebase"], label="拉取当前分支", timeout=120, cwd=ROOT)
                except (OSError, subprocess.CalledProcessError, subprocess.TimeoutExpired) as error:
                    raise ValueError("拉取代码失败，当前服务未被停止。可运行 ./start.sh 构建当前工作区。") from error
            elif args.update:
                raise ValueError("--update 需要 Git 工作区。可运行 ./start.sh 构建当前源码。")
            # Load any launcher changes from the new checkout too.
            os.execv(sys.executable, [sys.executable, str(ROOT / "tools" / "local.py"), "up"])
            return
        docker = ensure_docker(command in ("init", "up"))
        if command in ("init", "up", "down"):
            state = ROOT / ".kiko-local"
            state.mkdir(mode=0o700, exist_ok=True)
            with (state / "start.lock").open("a") as lock:
                fcntl.flock(lock, fcntl.LOCK_EX)
                run_compose(command, docker)
        else:
            run_compose(command, docker)
    except KeyboardInterrupt:
        print("已中止等待；已完成的安装步骤或容器可能保留，请检查后重试。", file=sys.stderr)
        sys.exit(130)
    except (ValueError, OSError, subprocess.CalledProcessError, subprocess.TimeoutExpired) as error:
        if isinstance(error, ValueError):
            print(str(error), file=sys.stderr)
        elif isinstance(error, subprocess.TimeoutExpired):
            print("操作超时。已完成的安装步骤或容器可能保留，请检查状态后重试。", file=sys.stderr)
        else:
            print("Local startup failed. Check Docker Compose availability, ports and service logs; no data was deleted.", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
