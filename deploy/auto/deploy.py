"""Pull and deploy verified images for KIKO 企业级AI员工云平台. Invoke through the locking deploy.sh."""
import argparse
import hashlib
import http.client
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import time
from urllib.parse import urlsplit

from admission import SCHEMA, acquire_sql, busy_query, release_sql

ROOT = Path("/opt/kikoteam")
STATE = ROOT / ".deploy"
CURRENT = STATE / "current.json"
PENDING = STATE / "pending.json"
MAINTENANCE = Path("/var/lib/kikoteam-ingress/maintenance")
BACKUPS = STATE / "backups"
STAGED = STATE / "accepted-run.json"
REGISTRY = "ghcr.io/unrealm-ai/kikoteam"
APPS = ("backend", "event-hub", "agent-engine")
COMPOSE = ["docker", "compose", "-f", "compose.yaml", "--env-file", ".env"]
PSQL = COMPOSE + ["exec", "-T", "postgres", "sh", "-ec",
                  'exec psql -XqAt -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"']


def output(*cmd, timeout=120, stdin=None):
    proc = subprocess.run(cmd, input=stdin, capture_output=True, text=True, timeout=timeout)
    if proc.returncode:
        raise RuntimeError(f"{cmd[0]}: {proc.stderr.strip()[:800] or 'command failed'}")
    return proc.stdout.strip()


def run(*cmd, timeout=600, **kwargs):
    subprocess.run(cmd, check=True, timeout=timeout, **kwargs)


def atomic(path, data):
    if not isinstance(data, bytes):
        data = (json.dumps(data, sort_keys=True) + "\n").encode()
    with tempfile.NamedTemporaryFile(dir=path.parent, delete=False) as f:
        try:
            f.write(data)
            f.flush()
            os.fsync(f.fileno())
            os.replace(f.name, path)
        finally:
            Path(f.name).unlink(missing_ok=True)
    fd = os.open(path.parent, os.O_DIRECTORY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def read(path):
    return json.loads(path.read_bytes())


def sql(query):
    return output(*PSQL, stdin=query, timeout=40)


def registry_release():
    raw = output("docker", "buildx", "imagetools", "inspect", "--raw",
                 REGISTRY + "/backend:verified-deploy", timeout=50)
    a = json.loads(raw)["annotations"]
    number, sha = a["io.kikoteam.run"], a["org.opencontainers.image.revision"]
    digests = {app: a["io.kikoteam." + app] for app in APPS}
    if not (re.fullmatch(r"[1-9][0-9]*", number) and re.fullmatch(r"[0-9a-f]{40}", sha)
            and all(re.fullmatch(r"sha256:[0-9a-f]{64}", d) for d in digests.values())):
        raise ValueError("Invalid registry release pointer")
    return {"run": int(number), "sha": sha, "digests": digests}


def release():
    latest = registry_release()
    if STAGED.exists():
        staged = read(STAGED)
        if staged["run"] > latest["run"]:
            return staged
    return latest


def stage_success(metadata):
    # The operator supplies GitHub's successful Actions run through SSH. This
    # permits recovery of a missing marker without giving the host write access.
    required = {"name": "Publish images", "event": "push", "head_branch": "main",
                "status": "completed", "conclusion": "success", "path": ".github/workflows/images.yml"}
    if (any(metadata.get(k) != v for k, v in required.items())
            or metadata["head_repository"]["full_name"] != "unrealm-ai/kikoteam"):
        raise ValueError("Expected a successful main image workflow from the deployment repository")
    sha, number = metadata["head_sha"], metadata["run_number"]
    if type(number) is not int or number < 1 or not re.fullmatch("[0-9a-f]{40}", sha):
        raise ValueError("Invalid GitHub run metadata")
    if number <= max(read(CURRENT)["run"], release()["run"]):
        print("Accepted release already known")
        return
    digests = {}
    for app in APPS:
        image = f"{REGISTRY}/{app}"
        def index(tag):
            return subprocess.check_output(["docker", "buildx", "imagetools", "inspect", "--raw",
                                            image + ":" + tag], timeout=90)
        raw = index(sha[:7])
        if raw != index("latest"):
            raise ValueError(f"{app}: latest and successful commit indexes differ")
        digests[app] = "sha256:" + hashlib.sha256(raw).hexdigest()
    atomic(STAGED, {"run": number, "sha": sha, "digests": digests})
    print("Staged verified run for normal timer processing", number, sha[:7])


def image_ref(app, revision):
    return f"{REGISTRY}/{app}:{revision['sha'][:7]}"


def verified_image(app, target):
    tag = image_ref(app, target)
    fmt = '{{json .RepoDigests}} {{index .Config.Labels "org.opencontainers.image.revision"}}'
    proc = subprocess.run(["docker", "image", "inspect", "--format", fmt, tag],
                          capture_output=True, text=True, timeout=60)
    if proc.returncode:
        return False
    digests, sha = proc.stdout.strip().rsplit(" ", 1)
    immutable = f"{REGISTRY}/{app}@{target['digests'][app]}"
    return sha == target["sha"] and immutable in json.loads(digests)


def pull(target):
    for app in APPS:
        if verified_image(app, target):
            continue
        tag = image_ref(app, target)
        immutable = f"{REGISTRY}/{app}@{target['digests'][app]}"
        # A short tag can collide or be rebuilt. Bind the local tag to exactly
        # the index published by the completion workflow.
        run("docker", "pull", "--quiet", immutable, timeout=2400)
        run("docker", "tag", immutable, tag)
        if not verified_image(app, target):
            raise RuntimeError(f"{app}: image verification failed")


def container(app):
    cid = output(*COMPOSE, "ps", "-aq", app)
    if len(cid.splitlines()) != 1 or not cid:
        raise RuntimeError(f"Expected one {app} container")
    return cid


def healthy(revision):
    for app in (*APPS, "postgres"):
        cid = container(app)
        health = output("docker", "inspect", "-f", "{{.State.Health.Status}}", cid)
        if health != "healthy":
            raise RuntimeError(f"{app}: {health}")
        if app in APPS:
            fmt = '{{.Config.Image}} {{.Image}} {{index .Config.Labels "org.opencontainers.image.revision"}}'
            tag, actual, sha = output("docker", "inspect", "-f", fmt, cid).split()
            expected = image_ref(app, revision)
            if (tag != expected or sha != revision["sha"]
                    or actual != output("docker", "image", "inspect", "-f", "{{.Id}}", expected)):
                raise RuntimeError(f"{app}: running image differs from deployment")
            if "digests" in revision and not verified_image(app, revision):
                raise RuntimeError(f"{app}: installed digest differs from verified release")
    status = output(*COMPOSE, "exec", "-T", "event-hub", "wget", "-qO-", "http://127.0.0.1:8081/status")
    if json.loads(status)["phase"] != "running":
        raise RuntimeError("Target Event Hub is not leading")
    # A ready Engine has received a successful heartbeat response from Backend.
    run(*COMPOSE, "exec", "-T", "agent-engine", "node", "-e",
        "fetch('http://127.0.0.1:8083/readyz').then(r=>process.exit(r.status===204?0:1))")


def configuration(current):
    cfg = json.loads(output(*COMPOSE, "config", "--format", "json"))["services"]
    data = {}
    for app in APPS:
        if cfg[app]["image"] != image_ref(app, current):
            raise RuntimeError(f"{app}: configured tag and installed release differ")
    for app in ("backend", "agent-engine"):
        mount, = [m for m in cfg[app]["volumes"] if m["target"] == "/var/lib/kiko"]
        if mount["type"] != "bind":
            raise ValueError("Expected host bind mount for shared storage")
        data[app] = Path(mount["source"]).resolve(strict=True)
    if data["backend"] != data["agent-engine"] or data["backend"] in (ROOT, Path("/")):
        raise ValueError("Unexpected shared storage layout")
    env = cfg["backend"]["environment"]
    if any(value for key, value in env.items() if key.startswith("KIKO_STORAGE_S3_")):
        raise ValueError("Configure an external object-store backup before enabling this deployment")
    port, = cfg["backend"]["ports"]
    if port["host_ip"] != "127.0.0.1" or int(port["published"]) != 18081:
        raise ValueError("Unexpected backend port binding")
    return data["backend"], urlsplit(env["KIKO_PUBLIC_ORIGIN"]).netloc


def http_status(host, path):
    c = http.client.HTTPConnection("127.0.0.1", 80, timeout=5)
    try:
        c.request("GET", path, headers={"Host": host})
        return c.getresponse().status
    finally:
        c.close()


def engine_idle():
    listing = output("docker", "top", container("agent-engine"), "-eo", "pid,ppid,args")
    processes = {}
    for line in listing.splitlines()[1:]:
        pid, parent, args = line.split(None, 2)
        processes[pid] = (parent, args)
    engines = [pid for pid, (_, a) in processes.items()
               if a.split() == ["/opt/kikoteam/agent-engine/bin/agent-engine"]]
    if len(engines) != 1:
        return False
    engine = engines[0]
    init = processes[engine][0]
    runners = {pid for pid, (parent, a) in processes.items() if parent == engine and
               re.fullmatch(r"/opt/kikoteam/sandbox-runner/bin/sandbox-runner --config /tmp/ke-[^ /]+/runner.json", a)}
    for pid, (parent, args) in processes.items():
        if pid in {engine, init} or pid in runners:
            continue
        if parent in runners and re.fullmatch(
            r"node --disable-warning=ExperimentalWarning /opt/kikoteam/sandbox-runner/builtin-mcp/dist/http-main\.js --config \S+", args
        ):
            continue
        if args.startswith("node -e fetch('http://127.0.0.1:8083/readyz')"):
            continue
        return False
    return True


def idle():
    busy = sql("SET statement_timeout='8s'; " + busy_query() + ";")
    if busy:
        print("Deferred; active work:", ", ".join(busy.splitlines()))
        return False
    if not engine_idle():
        print("Deferred; unaccounted Engine processes")
        return False
    return True


def hold(host):
    atomic(MAINTENANCE, b"")
    if http_status(host, "/api/v1/auth/options") != 503:
        raise RuntimeError("Nginx did not close public admission")
    # Nginx opens a loopback upstream connection for each proxied request.
    # An upload, callback or stream admitted before the marker must finish.
    for _ in range(5):
        if not output("ss", "-Htn", "state", "established", "dport", "=", ":18081"):
            break
        time.sleep(1)
    else:
        print("Deferred; open upstream requests")
        return False
    try:
        fenced = sql(acquire_sql()) == "FENCED"
    except RuntimeError as exc:
        if "lock timeout" in str(exc) or "deadlock detected" in str(exc):
            print("Deferred; a concurrent database transaction held the admission locks")
            return False
        raise
    return fenced and engine_idle()


def reopen():
    sql(release_sql())
    MAINTENANCE.unlink(missing_ok=True)
    PENDING.unlink(missing_ok=True)


def space_check(data):
    n = int(sql("SELECT pg_database_size(current_database());"))
    n += int(output("du", "-sb", str(data), timeout=300).split()[0])
    if shutil.disk_usage(STATE).free < n + 256 * 1024**2:
        raise RuntimeError("Insufficient space for a verified backup")


def publish_healthy(target, host):
    MAINTENANCE.unlink(missing_ok=True)
    try:
        if http_status(host, "/api/v1/auth/options") != 200:
            raise RuntimeError("Public ingress failed verification")
        atomic(CURRENT, target)
        PENDING.unlink()
    except BaseException:
        atomic(MAINTENANCE, b"")
        raise


def backup(folder, data, current):
    folder.mkdir(mode=0o700, parents=True, exist_ok=False)
    for name in (".env", "compose.yaml", "nginx.conf"):
        shutil.copyfile(ROOT / name, folder / name)
    shutil.copyfile("/etc/nginx/snippets/kikoteam-maintenance.conf", folder / "maintenance.nginx.conf")
    with (folder / "database.dump").open("xb") as f:
        run(*COMPOSE, "exec", "-T", "postgres", "sh", "-ec",
            'exec pg_dump -Fc -U "$POSTGRES_USER" -d "$POSTGRES_DB"', stdout=f, timeout=1800)
    with (folder / "database.dump").open("rb") as f:
        run(*COMPOSE, "exec", "-T", "postgres", "pg_restore", "-f", "/dev/null", stdin=f, timeout=1800)
    archive = folder / "data.tar.gz"
    run("tar", "-C", str(data), "-czf", str(archive), ".", timeout=1800)
    run("tar", "-C", str(data), "-dzf", str(archive), timeout=1800)
    checksums = {}
    for p in sorted(folder.iterdir()):
        with p.open("rb") as f:
            checksums[p.name] = hashlib.file_digest(f, "sha256").hexdigest()
    atomic(folder / "manifest.json", {"release": current, "sha256": checksums})


def switch_tag(old, new):
    p = ROOT / ".env"
    s = p.read_bytes()
    pattern = rb"(?m)^KIKO_IMAGE_TAG=" + old["sha"][:7].encode() + rb"$"
    s, count = re.subn(pattern, b"KIKO_IMAGE_TAG=" + new["sha"][:7].encode(), s)
    if count != 1:
        raise ValueError("Expected exactly one matching KIKO_IMAGE_TAG in .env")
    atomic(p, s)


def stop(app):
    cid = container(app)
    run(*COMPOSE, "stop", app, timeout=150)
    state = output("docker", "inspect", "-f", "{{.State.Status}} {{.State.ExitCode}}", cid)
    if state not in ("exited 0", "exited 143"):
        raise RuntimeError(f"{app}: unexpected shutdown: {state}")


def start(*apps):
    run(*COMPOSE, "up", "-d", "--no-deps", "--no-build", "--pull", "never",
        "--wait", "--wait-timeout", "640", *apps, timeout=680)


def tick():
    if PENDING.exists():
        if read(PENDING)["phase"] == "admission":
            reopen()  # Previous process died before it could stop any service.
            print("Recovered pre-cutover admission")
        else:
            if not MAINTENANCE.exists():
                atomic(MAINTENANCE, b"")
            print("PAUSED: operator recovery required; see", PENDING)
        return
    current = read(CURRENT)
    target = release()
    if target["run"] <= current["run"]:
        return
    if target["sha"] == current["sha"]:
        healthy(target)
        atomic(CURRENT, target)
        return
    if target["sha"][:7] == current["sha"][:7]:
        raise ValueError("Target short SHA collides with the installed version")
    print(f"Preparing run {target['run']} {target['sha'][:7]}")
    data, host = configuration(current)
    space_check(data)
    if shutil.disk_usage("/var/lib/docker").free < 2 * 1024**3:
        raise RuntimeError("Less than 2 GiB available for pulling images")
    pull(target)
    healthy(current)
    if not idle():
        return
    space_check(data)
    if release() != target:
        print("Deployment pointer changed during preparation; retrying newest release next tick")
        return
    if MAINTENANCE.exists() or sql(f"SELECT to_regnamespace('{SCHEMA}') IS NOT NULL;") != "f":
        raise RuntimeError("Unexpected existing maintenance gate")
    record = {"from": current, "to": target, "phase": "admission"}
    atomic(PENDING, record)
    try:
        fenced = hold(host)
    except Exception:
        reopen()
        raise
    if not fenced:
        reopen()
        print("Deferred; final admission check did not confirm idle")
        return
    folder = BACKUPS / f"run-{current['run']}-before-{target['run']}-{time.time_ns()}"
    record.update(phase="cutover", backup=str(folder))
    atomic(PENDING, record)
    for app in ("agent-engine", "event-hub", "backend"):
        stop(app)
    sql(release_sql())  # All writers stopped; keep the temporary fence out of backups.
    backup(folder, data, current)
    switch_tag(current, target)
    start("backend")
    start("event-hub")
    phase = output(*COMPOSE, "exec", "-T", "event-hub", "wget", "-qO-", "http://127.0.0.1:8081/status")
    if json.loads(phase)["phase"] != "running":
        raise RuntimeError("Hub has not completed migration and recovery")
    run(*COMPOSE, "exec", "-T", "backend", "/app/backend", "sync-catalog", timeout=120)
    start("agent-engine")
    healthy(target)
    publish_healthy(target, host)
    print(f"Deployed run {target['run']} {target['sha'][:7]}; backup {folder}")


def main():
    parser = argparse.ArgumentParser()
    actions = parser.add_mutually_exclusive_group()
    actions.add_argument("--initialize", nargs=2, metavar=("RUN", "FULL_SHA"))
    actions.add_argument("--check", action="store_true")
    actions.add_argument("--stage-success", action="store_true",
                         help="read successful GitHub Actions run JSON from stdin")
    actions.add_argument("--recover", choices=("from", "to"),
                         help="adopt the operator-restored, healthy old or target stack")
    args = parser.parse_args()
    if args.initialize:
        run_number, sha = args.initialize
        if CURRENT.exists() or PENDING.exists() or not re.fullmatch(r"[0-9a-f]{40}", sha) or int(run_number) < 1:
            raise ValueError("Invalid baseline or existing deployment state")
        current = {"run": int(run_number), "sha": sha}
        configuration(current)
        healthy(current)
        atomic(CURRENT, current)
        print("Initialized verified running baseline", run_number, sha[:7])
    elif args.stage_success:
        stage_success(json.load(sys.stdin))
    elif args.recover:
        pending = read(PENDING)
        if pending["phase"] != "cutover" or not MAINTENANCE.exists():
            raise ValueError("Expected a paused cutover with ingress closed")
        revision = pending[args.recover]
        _, host = configuration(revision)
        healthy(revision)
        if sql(f"SELECT to_regnamespace('{SCHEMA}') IS NOT NULL;") != "f":
            raise ValueError("Restore the database or release the old task fence before recovery")
        publish_healthy(revision, host)
        print("Resumed verified deployment", revision["run"], revision["sha"][:7])
    elif args.check:
        current = read(CURRENT)
        target = release()
        configuration(current)
        healthy(current)
        print("Installed:", current["run"], current["sha"][:7], "available:", target["run"], target["sha"][:7],
              "idle:", idle(), "paused:", PENDING.exists())
    else:
        tick()


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, KeyError, RuntimeError, subprocess.SubprocessError) as exc:
        print("Deployment check failed:", exc, file=sys.stderr)
        if PENDING.exists():
            record = read(PENDING)
            if record["phase"] == "cutover":
                log = STATE / (Path(record["backup"]).name + ".log")
                if not log.exists():
                    with log.open("x") as f:
                        print(exc, file=f, flush=True)
                        subprocess.run([*COMPOSE, "logs", "--no-color", "--tail", "250",
                                        *APPS, "postgres"], stdout=f, stderr=f, timeout=120)
        sys.exit(1)
