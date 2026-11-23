"""Black-box launcher tests. Python is a test dependency, absent from launcher PATH."""
import hashlib
import os
import signal
import time
from pathlib import Path
import shutil
import subprocess
import tempfile
import sys
import unittest

ROOT = Path(__file__).resolve().parents[1]
# Older macOS versions do not reliably provide C.UTF-8.
UTF8_LOCALE = "en_US.UTF-8" if sys.platform == "darwin" else "C.UTF-8"
TOOLS = """sh awk basename cat chmod chown cmp cut date dd dirname env expr grep
head id ln ls mkdir mkfifo mktemp mv od openssl ps pwd readlink realpath rm rmdir sed
sha256sum shasum sleep sort stat tail tee timeout touch tr uname wc xargs""".split()


class Launcher(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name) / "checkout with spaces"
        self.root.mkdir()
        (self.root / "tools").mkdir()
        (self.root / "home").mkdir()
        self.bin = self.root / "bin"
        self.bin.mkdir()
        self.trace = self.root / "trace"
        for name in ("start.sh", ".env.example", "compose.yaml",
                     "tools/local-launcher.sh", "tools/docker-support.sh"):
            shutil.copy2(ROOT / name, self.root / name)
        # Keep real Docker Desktop out of the mocked CLI test even when the
        # local Mac has installed the app at the production fallback location.
        helper = self.root / "tools/docker-support.sh"
        setup = helper.read_text()
        fallback = "docker_mac_app_path() { printf '%s\\n' /Applications/Docker.app; }"
        self.assertIn(fallback, setup)
        helper.write_text(setup.replace(
            fallback, """docker_mac_app_path() { printf '%s\\n' "$HOME/fixture/Docker.app"; }""",
            1,
        ))
        for tool in TOOLS:
            source = shutil.which(tool)
            if source:
                if tool == "shasum":
                    self.stub(tool, f'exec "{source}" "$@"')
                else:
                    (self.bin / tool).symlink_to(source)
        self.stub("docker", r"""
printf '%s\n' "$*" >> "$TRACE"
case "$*" in
  'volume ls'*) [ "${OLD_VOLUME:-}" != yes ] || printf '%s\n' kikoteam-local-postgres-data; exit 0 ;;
  'volume inspect '*) [ "${OLD_VOLUME:-}" = yes ]; exit $? ;;
  *' build backend event-hub agent-engine'*) [ "${FAIL_BUILD:-}" != yes ]; exit $? ;;
esac
exit 0
""")
        self.stub("git", r"""
printf 'git %s\n' "$*" >> "$TRACE"
case "$*" in
  'pull --ff-only --no-rebase') [ "${FAIL_PULL:-}" != yes ]; exit $? ;;
  *rev-parse*) printf '%s\n' abcdef0 ;;
  'status --porcelain') [ "${DIRTY_GIT:-}" != yes ] || printf '%s\n' " M fixture" ;;
esac
exit 0
""")
        self.cfg = self.root / ".env"
        self.runtime = self.root / ".kiko-local/runtime.env"

    def stub(self, name, script):
        path = self.bin / name
        if path.is_symlink():
            path.unlink()
        path.write_text("#!/bin/sh\n" + script)
        path.chmod(0o755)

    def launch(self, *args, success=True, **env):
        result = subprocess.run(
            ["./start.sh", *args], cwd=self.root, input="", capture_output=True,
            encoding="utf-8", errors="strict", timeout=35, env={
                "PATH": str(self.bin), "HOME": str(self.root / "home"),
                "TRACE": str(self.trace), "LC_ALL": "C", **env,
            },
        )
        self.assertEqual(result.returncode == 0, success,
                         f"exit={result.returncode}\n{result.stdout}\n{result.stderr}")
        return result

    def commands(self):
        return self.trace.read_text().splitlines() if self.trace.exists() else []

    def test_no_python_default_up_and_compose_order(self):
        (self.root / ".git").mkdir()
        self.assertIsNone(shutil.which("python3", path=str(self.bin)))
        self.launch()
        commands = self.commands()
        self.assertFalse(any(line.startswith("git pull") for line in commands))
        actions = (
            "build backend event-hub agent-engine", "stop --timeout 90 agent-engine",
            "stop --timeout 25 event-hub",
            "up -d --no-build --wait --wait-timeout 180 backend event-hub",
            "run --pull never --rm --no-deps template-catalog",
            "up -d --no-build --wait --wait-timeout 180 --no-deps agent-engine",
        )
        found = [action for line in commands for action in actions if line.endswith(action)]
        self.assertEqual(found, list(actions))
        for line in commands:
            if any(line.endswith(action) for action in actions):
                self.assertIn("--env-file", line)
        self.launch("down")
        self.assertTrue(self.commands()[-1].endswith(" down"))
        self.assertNotIn("-v", self.commands()[-1].split())
        self.assertNotIn("--volumes", self.commands()[-1].split())
        self.launch("status")
        self.assertIn(" ps", self.commands()[-1])
        self.launch("logs")
        self.assertIn(" logs", self.commands()[-1])

    def test_dirty_checkout_keeps_revision_diagnostic_in_utf8_locale(self):
        (self.root / ".git").mkdir()
        result = self.launch("up", LC_ALL=UTF8_LOCALE, DIRTY_GIT="yes")
        self.assertIn("Git abcdef0（含本地改动）", result.stdout)

    def test_repeated_secrets_are_identical_private_and_distinct(self):
        self.launch("up")
        before = self.cfg.read_bytes(), self.runtime.read_bytes()
        for path in (self.cfg, self.runtime):
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
        values = dict(line.split("=", 1) for line in self.runtime.read_text().splitlines()
                      if line and not line.startswith("#"))
        self.assertEqual(set(values), {"KIKO_HUB_TOKEN", "KIKO_ENGINE_TOKEN"})
        self.assertEqual(len(set(values.values())), 2)
        self.assertTrue(all(len(value) >= 32 for value in values.values()))
        self.assertTrue((self.root / "home/.kiko").is_dir())
        self.launch("down")
        self.launch("up")
        self.assertEqual((self.cfg.read_bytes(), self.runtime.read_bytes()), before)

    def test_existing_lock_never_gets_taken_over_automatically(self):
        self.launch("init")
        lock = self.runtime.parent / "start.lock.d"
        lock.mkdir(mode=0o700)
        owner = lock / "pid"
        owner.write_text("99999999\n")
        owner.chmod(0o600)
        before = self.cfg.read_bytes()
        self.launch("up", success=False)
        self.assertEqual(owner.read_text(), "99999999\n")
        self.assertEqual(self.cfg.read_bytes(), before)
        self.assertFalse(any(" build " in line for line in self.commands()))

    def test_cancelling_build_releases_lock_and_never_stops_services(self):
        self.stub("docker", r"""
printf '%s\n' "$*" >> "$TRACE"
case "$*" in
  *' build backend event-hub agent-engine'*)
    printf '%s\n' "$$" > "$BLOCKED"
    exec sleep 60 ;;
esac
exit 0
""")
        blocked = self.root / "blocked"
        proc = subprocess.Popen(["./start.sh", "up"], cwd=self.root, stdin=subprocess.DEVNULL,
                                text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                env={"PATH": str(self.bin), "HOME": str(self.root / "home"),
                                     "TRACE": str(self.trace), "BLOCKED": str(blocked), "LC_ALL": "C"})
        child = None
        try:
            deadline = time.monotonic() + 25
            while (not blocked.exists() or not blocked.stat().st_size) and proc.poll() is None and time.monotonic() < deadline:
                time.sleep(0.03)
            self.assertTrue(blocked.exists(), "launcher did not enter mocked build")
            child = int(blocked.read_text())
            proc.send_signal(signal.SIGTERM)
            proc.communicate(timeout=8)
            self.assertEqual(proc.returncode, 143)
            self.assertFalse((self.runtime.parent / "start.lock.d").exists())
            self.assertFalse(any(" stop " in line for line in self.commands()))
            with self.assertRaises(ProcessLookupError):
                os.kill(child, 0)
            child = None
        finally:
            if child is not None:
                try:
                    os.kill(child, signal.SIGKILL)
                except ProcessLookupError:
                    pass
            if proc.poll() is None:
                proc.kill()
            proc.communicate()

    def test_unclean_exit_keeps_staged_files_in_ignored_private_state(self):
        self.stub("ln", r"""
case "$2" in
  */.env) printf '%s\n' "$1" > "$BLOCKED"; exec sleep 60 ;;
esac
exec /bin/ln "$@"
""")
        blocked = self.root / "blocked"
        proc = subprocess.Popen(["./start.sh", "init"], cwd=self.root, stdin=subprocess.DEVNULL,
                                text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                start_new_session=True,
                                env={"PATH": str(self.bin), "HOME": str(self.root / "home"),
                                     "TRACE": str(self.trace), "BLOCKED": str(blocked), "LC_ALL": "C"})
        try:
            deadline = time.monotonic() + 25
            while (not blocked.exists() or not blocked.stat().st_size) and proc.poll() is None and time.monotonic() < deadline:
                time.sleep(0.03)
            self.assertTrue(blocked.exists(), "launcher did not stage .env")
            staged = Path(blocked.read_text().strip())
            self.assertEqual(staged.parent.resolve(), (self.root / ".kiko-local").resolve())
            self.assertEqual(staged.stat().st_mode & 0o777, 0o600)
            self.assertIn(".kiko-local/", (ROOT / ".gitignore").read_text().splitlines())
            self.assertIn(".kiko-local", (ROOT / ".dockerignore").read_text().splitlines())
        finally:
            try:
                os.killpg(proc.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            proc.communicate(timeout=8)
        self.assertTrue(staged.exists())
        self.assertFalse(self.cfg.exists())
        self.launch("init", success=False)
        self.assertFalse(self.cfg.exists())

    def test_build_failure_never_stops_old_stack(self):
        self.launch("up", success=False, FAIL_BUILD="yes")
        self.assertTrue(any(" build " in line for line in self.commands()))
        self.assertFalse(any(" stop " in line for line in self.commands()))

    def test_legacy_python_restart_execs_bridge_after_git_pull(self):
        # A previous checkout's updater execs tools/local.py up after git pull.
        # Model that crossing with an actual process replacement and an isolated git stub.
        bridge = ROOT / "tools/local.py"
        shutil.copy2(bridge, self.root / "tools/local.next")
        old_updater = self.root / "tools/updater.py"
        old_updater.write_text(
            "import os, subprocess, sys\n"
            "subprocess.run(['git', 'pull', '--ff-only', '--no-rebase'], check=True)\n"
            "os.execv(sys.executable, [sys.executable, 'tools/local.py', 'up'])\n"
        )
        self.stub("git", """
if [ "$1" = pull ]; then
    printf 'legacy update\\n' >> "$TRACE"
    mv tools/local.next tools/local.py
fi
exit 0
""")
        result = subprocess.run([sys.executable, str(old_updater)], cwd=self.root,
                                input="", text=True, capture_output=True, timeout=35,
                                env={"PATH": str(self.bin), "HOME": str(self.root / "home"),
                                     "TRACE": str(self.trace), "LC_ALL": "C"})
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.commands()[0], "legacy update")
        self.assertTrue(any(" build backend " in line for line in self.commands()))

    def test_update_failure_and_success(self):
        (self.root / ".git").mkdir()
        self.launch("--update", success=False, FAIL_PULL="yes")
        self.assertEqual(self.commands(), ["git pull --ff-only --no-rebase"])
        self.trace.unlink()
        self.launch("restart")
        self.assertEqual(self.commands()[0], "git pull --ff-only --no-rebase")

    def test_state_without_env_and_credential_rotation_are_rejected(self):
        self.launch("up")
        original = self.cfg.read_bytes()
        self.cfg.unlink()
        self.launch("up", success=False)
        self.assertFalse(self.cfg.exists())
        self.cfg.write_bytes(original.replace(b"KIKO_AUTH_SECRET=", b"KIKO_AUTH_SECRET=changed"))
        self.cfg.chmod(0o600)
        self.trace.unlink()
        self.launch("up", success=False)
        self.assertFalse(any(" stop " in line or " build " in line for line in self.commands()))

    def test_upgrade_old_fingerprint_and_runtime_are_preserved(self):
        self.launch("init")
        old_env = self.cfg.read_text()
        values = dict(line.split("=", 1) for line in old_env.splitlines()
                      if "=" in line and not line.startswith("#"))
        marker = self.runtime.parent / "config.sha256"
        legacy_digest = hashlib.sha256("\0".join(values[k] for k in (
            "POSTGRES_DB", "POSTGRES_USER", "POSTGRES_PASSWORD", "KIKO_AUTH_SECRET"
        )).encode()).hexdigest()
        self.assertEqual(marker.read_text().strip(), legacy_digest)
        legacy_runtime = "KIKO_HUB_TOKEN=" + "a" * 64 + "\nKIKO_ENGINE_TOKEN=" + "b" * 64 + "\n"
        self.runtime.write_text(legacy_runtime)
        self.runtime.chmod(0o600)
        self.launch("up", OLD_VOLUME="yes")
        self.assertEqual(self.cfg.read_text(), old_env)
        self.assertEqual(self.runtime.read_text(), legacy_runtime)
        self.assertEqual(marker.read_text().strip(), legacy_digest)

    def test_missing_env_with_existing_volume_refuses_to_generate_secrets(self):
        self.launch("init", success=False, OLD_VOLUME="yes")
        self.assertFalse(self.cfg.exists())
        self.assertFalse(self.runtime.exists())

    def test_missing_env_with_only_runtime_state_refuses_replacement(self):
        self.runtime.parent.mkdir(mode=0o700)
        self.runtime.write_text("existing-runtime-state")
        self.runtime.chmod(0o600)
        result = self.launch("init", success=False)
        self.assertIn("restore .env from backup", result.stderr)
        self.assertFalse(self.cfg.exists())
        self.assertEqual(self.runtime.read_text(), "existing-runtime-state")

    def test_storage_path_with_spaces_and_rejected_relative_or_symlink(self):
        storage = self.root / "custom storage"
        template = self.root / ".env.example"
        template.write_text(template.read_text().replace(
            "KIKO_STORAGE_LOCAL_ROOT=~/.kiko/", f"KIKO_STORAGE_LOCAL_ROOT='{storage}'"
        ))
        self.launch("up")
        self.assertEqual(storage.stat().st_mode & 0o777, 0o700)
        text = self.cfg.read_text()
        link = self.root / "storage link"
        link.symlink_to(storage, target_is_directory=True)
        for path in ("relative/path", "../elsewhere", str(link)):
            with self.subTest(path=path):
                self.cfg.write_text(text.replace(f"'{storage}'", f"'{path}'"))
                self.launch("up", success=False)

    def test_valid_single_quotes_treat_shell_syntax_as_literal(self):
        self.launch("init")
        with self.cfg.open("a") as stream:
            stream.write("KEY='literal $ # $(touch should-not-exist)'\n")
        self.launch("init")
        self.assertFalse((self.root / "should-not-exist").exists())

    def test_flags_and_docker_not_ready_are_safe(self):
        for invalid in (("down", "--update"), ("--unknown",), ("--update",)):
            self.launch(*invalid, success=False)
        self.assertEqual(self.commands(), [])
        self.stub("docker", 'exit 1')
        self.stub("sudo", 'printf "sudo invocation\\n" >> "$TRACE"; exit 1')
        self.launch("status", success=False)
        self.launch("up", success=False)
        self.assertEqual(self.commands(), [])

    def test_environment_rejections_are_non_destructive(self):
        self.launch("init")
        text = self.cfg.read_text()
        for content, mode in (
            (text + "KEY=one\nKEY=two\n", 0o600),
            (text + "KEY='unclosed\n", 0o600),
            (text + "KEY=a\\b\n", 0o600),
            (text + "KEY=a\tb\n", 0o600),
            (text + "KEY='a'b'\n", 0o600),
            (text + "invalid\n", 0o600),
            (text.replace("http://localhost:18080", "https://public.example.com"), 0o600),
            (text, 0o644),
        ):
            with self.subTest(content=content[-30:], mode=mode):
                self.cfg.write_text(content)
                self.cfg.chmod(mode)
                self.launch("init", success=False)
                self.assertEqual(self.cfg.read_text(), content)
        self.cfg.unlink()
        target = self.root / "target"
        target.write_text(text)
        target.chmod(0o600)
        self.cfg.symlink_to(target)
        self.launch("init", success=False)
        self.assertEqual(target.read_text(), text)

    def test_legacy_and_inherited_config_rejected(self):
        self.launch("init", success=False, DATABASE_URL="postgres://existing")
        (self.root / "backend").mkdir()
        (self.root / "backend/backend.local.json").write_text("{}")
        self.launch("init", success=False)
        self.assertFalse(self.cfg.exists())

    def test_missing_docker_never_installs_without_a_terminal(self):
        (self.bin / "docker").unlink()
        self.stub("curl", 'printf "curl invocation\\n" >> "$TRACE"; exit 1')
        self.stub("uname", 'case "$1" in -s) echo Darwin;; -m) echo arm64;; esac')
        self.stub("sw_vers", 'echo 14.0')
        for command in ("init", "up"):
            result = self.launch(command, success=False)
            self.assertIn("非交互环境", result.stderr)
        result = self.launch("status", success=False)
        self.assertIn("未找到 Docker", result.stderr)
        self.assertNotIn("非交互环境", result.stderr)
        self.assertFalse(any("curl invocation" in line for line in self.commands()))
        self.assertFalse(self.cfg.exists())


    def installer(self, body, *, status=0):
        """Test public shell operations with read/install/network effects replaced."""
        proc = subprocess.run(
            ["/bin/sh", "-c", 'set -eu; . ./tools/docker-support.sh;\n' + body],
            cwd=self.root, input="", encoding="utf-8", errors="strict", capture_output=True, timeout=12,
            env={"PATH": str(self.bin), "LC_ALL": UTF8_LOCALE, "TRACE": str(self.trace),
                 "HOME": str(self.root / "home"), "TMPDIR": str(self.root)},
        )
        self.assertEqual(proc.returncode, status, proc.stdout + proc.stderr)
        return proc

    def test_linux_installer_distro_conflict_and_existing_sources_are_safe(self):
        unsupported = self.installer(
            "docker_linux_release(){ printf 'ID=unknown\\nVERSION_CODENAME=no\\n'; }; docker_linux_distribution",
            status=1,
        )
        self.assertIn("发行版", unsupported.stderr)
        distro = "docker_linux_release(){ printf 'ID=ubuntu\\nVERSION_CODENAME=noble\\n'; }; "
        conflict = self.installer(distro + """run_step(){ printf 'install ok installed'; };
          docker_confirm(){ exit 77; }; docker_install_linux""", status=1)
        self.assertIn("发现已有 docker.io，不会自动卸载。", conflict.stderr)
        sources = self.installer(distro + """run_step(){ return 1; };
          docker_apt_paths_clear(){ return 1; };
          docker_confirm(){ exit 77; }; docker_install_linux""", status=1)
        self.assertIn("不自动覆盖", sources.stderr)

    def test_linux_uses_official_signed_source_and_never_changes_group(self):
        self.stub("id", 'printf "%s\\n" 1000')
        self.stub("sudo", 'exit 70')
        self.installer(r"""
docker_linux_release(){ printf 'ID=debian\nVERSION_CODENAME=bookworm\n'; }
docker_apt_paths_clear(){ return 0; }
docker_confirm(){ printf 'confirm\n' >> "$TRACE"; }
run_step(){
    shift 2
    printf '%s\n' "$*" >> "$TRACE"
    case "$*" in
        dpkg-query*) return 1 ;;
        'dpkg --print-architecture') printf '%s\n' amd64 ;;
        *'/etc/apt/sources.list.d/kiko-docker.sources')
            cat "$5" >> "$TRACE" ;;
    esac
}
docker_install_linux
""")
        trace = self.trace.read_text()
        self.assertIn("confirm\nsudo -v\n", trace)
        self.assertIn("https://download.docker.com/linux/debian/gpg", trace)
        self.assertIn("Suites: bookworm", trace)
        self.assertIn("Architectures: amd64", trace)
        self.assertIn("Signed-By: /etc/apt/keyrings/kiko-docker.asc", trace)
        self.assertIn("docker-buildx-plugin docker-compose-plugin", trace)
        for forbidden in ("apt-get remove", "usermod", "chmod 666"):
            self.assertNotIn(forbidden, trace)

    def test_mac_installer_guards_and_only_opens_manual_installer(self):
        self.stub("uname", 'printf "%s\\n" arm64')
        common = """
docker_confirm(){ printf 'confirm %s\\n' "$1" >> "$TRACE"; }
docker_check_macos_version(){ KIKO_MAC_VERSION="$MOCK_MAC"; }
docker_disk_has_space(){ [ "$MOCK_DISK" = ample ]; }
run_step(){ shift 2; printf '%s\\n' "$*" >> "$TRACE";
  if [ "$1" = docker_file_digest ]; then printf '%s\\n' "$MOCK_HASH"; fi; }
"""
        low = self.installer('MOCK_MAC=14.0; MOCK_DISK=low; ' + common + 'docker_install_mac', status=1)
        self.assertIn("磁盘可用空间不足", low.stderr)
        self.assertNotIn("curl --", self.trace.read_text())
        self.trace.unlink()
        bad = self.installer('MOCK_MAC=13.7; MOCK_DISK=ample; MOCK_HASH=bad; ' + common + 'docker_install_mac', status=1)
        self.assertIn("SHA256 不匹配", bad.stderr)
        trace = self.trace.read_text()
        self.assertIn("/arm64/207573/Docker.dmg", trace)
        self.assertIn("4.48.0", trace)
        self.assertFalse(any(line.startswith("open ") for line in trace.splitlines()))
        self.trace.unlink()
        manual = self.installer('MOCK_MAC=14.0; MOCK_DISK=ample; ' + common + 'docker_install_mac', status=1)
        self.assertIn("安装引导已打开", manual.stderr)
        self.assertIn("许可和权限由你确认", manual.stderr)
        trace = self.trace.read_text()
        self.assertIn("/arm64/Docker.dmg", trace)
        self.assertTrue(any(line.startswith("open ") for line in trace.splitlines()))
        self.assertNotIn("--accept-license", trace)

    def test_unsupported_macos_and_decline_never_download(self):
        self.stub("sw_vers", 'printf "%s\\n" 12.7.8')
        old = self.installer(
            "docker_confirm(){ exit 77; }; run_step(){ exit 77; }; docker_install_mac", status=1)
        self.assertIn("最低要求", old.stderr)
        self.stub("uname", 'printf "%s\\n" arm64')
        self.installer("""docker_check_macos_version(){ KIKO_MAC_VERSION=14.0; };
          docker_confirm(){ exit 42; }; run_step(){ exit 77; }; docker_install_mac""", status=42)

    def test_actual_interactive_decline_does_not_continue(self):
        master, tty = os.openpty()
        try:
            proc = subprocess.Popen(
                ["/bin/sh", "-c", '. ./tools/docker-support.sh; docker_confirm "Install?"; printf "unsafe"'],
                cwd=self.root, stdin=tty, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                text=True,
                env={"PATH": str(self.bin), "HOME": str(self.root / "home"), "LC_ALL": "C"},
            )
            os.write(master, b"no\n")
            try:
                out, err = proc.communicate(timeout=8)
            finally:
                if proc.poll() is None:
                    proc.kill()
                    proc.communicate()
            self.assertEqual(proc.returncode, 1, err)
            self.assertIn("已取消", err)
            self.assertEqual(out, "")
        finally:
            os.close(master)
            os.close(tty)

    def test_supervisor_preserves_output_and_times_out(self):
        result = self.installer("""run_step 'mock-command' 4 /bin/sh -c 'printf payload'""")
        self.assertEqual(result.stdout, "payload")
        self.assertIn("[开始]", result.stderr)
        self.assertIn("[完成]", result.stderr)
        result = self.installer("""run_step 'timeout-mock' 1 sleep 30""", status=124)
        self.assertIn("超时", result.stderr)


if __name__ == "__main__":
    unittest.main()
