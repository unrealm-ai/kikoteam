import importlib.util
import os
from pathlib import Path
import tempfile
import unittest
from unittest import mock
import subprocess

spec = importlib.util.spec_from_file_location("local", Path(__file__).with_name("local.py"))
local = importlib.util.module_from_spec(spec)
spec.loader.exec_module(local)


class LocalStartupTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        template = Path(__file__).resolve().parent.parent / ".env.example"
        (self.root / ".env.example").write_text(template.read_text().replace("KIKO_STORAGE_LOCAL_ROOT=~/.kiko/", "KIKO_STORAGE_LOCAL_ROOT=" + str(self.root / "assets")))

    def test_compose_up_and_down_contract(self):
        calls = []
        def fake_run(args, **kwargs):
            calls.append((args, kwargs))
            return subprocess.CompletedProcess(args, 0, stdout="")
        with mock.patch.object(local, "ensure_docker", return_value="docker"), mock.patch.object(local, "ROOT", self.root), mock.patch.object(local, "run", fake_run), mock.patch.dict(os.environ, {}, clear=True):
            with mock.patch("sys.argv", ["local.py", "up"]):
                local.main()
            compose_commands = [args[args.index("-f") + 2:] for args, _ in calls[1:]]
            self.assertEqual(compose_commands, [
                ["build", "backend", "event-hub", "agent-engine"],
                ["stop", "--timeout", "90", "agent-engine"],
                ["stop", "--timeout", "25", "event-hub"],
                ["up", "-d", "--no-build", "--wait", "--wait-timeout", "180", "backend", "event-hub"],
                ["run", "--pull", "never", "--rm", "--no-deps", "template-catalog"],
                ["up", "-d", "--no-build", "--wait", "--wait-timeout", "180", "--no-deps", "agent-engine"],
            ])
            tokens = local.read_env(self.root / ".kiko-local" / "runtime.env")
            for args, kwargs in calls[1:]:
                self.assertIn("--env-file", args)
                self.assertEqual({key: kwargs["env"][key] for key in tokens}, tokens)
            with mock.patch("sys.argv", ["local.py", "down"]):
                local.main()
            self.assertEqual(calls[-1][0][-1], "down")
            self.assertNotIn("-v", calls[-1][0])

    def test_build_failure_leaves_services_running(self):
        def fail_build(args, **kwargs):
            if "build" in args:
                raise subprocess.CalledProcessError(1, args)
            return subprocess.CompletedProcess(args, 0, stdout="")

        with mock.patch.object(local, "ensure_docker", return_value="docker"), mock.patch.object(local, "ROOT", self.root), mock.patch.object(local, "run", side_effect=fail_build) as run, mock.patch("sys.argv", ["local.py", "up"]), self.assertRaises(SystemExit):
            local.main()
        self.assertEqual(run.call_count, 2)
        self.assertIn("build", run.call_args.args[0])

    def test_default_starts_checkout_without_pulling(self):
        (self.root / ".git").mkdir()
        with mock.patch.object(local, "ROOT", self.root), \
                mock.patch.object(local, "ensure_docker", return_value="docker"), \
                mock.patch.object(local, "run") as run, \
                mock.patch.object(local, "run_compose") as compose, \
                mock.patch.object(local.os, "execv") as launch, \
                mock.patch("sys.argv", ["local.py"]):
            local.main()
        run.assert_not_called()
        launch.assert_not_called()
        compose.assert_called_once_with("up", "docker")

    def test_update_pulls_before_loading_updated_launcher(self):
        (self.root / ".git").mkdir()
        for command in ("--update", "restart"):
            with self.subTest(command=command), \
                    mock.patch.object(local, "ROOT", self.root), \
                    mock.patch.object(local, "run") as run, \
                    mock.patch.object(local.os, "execv") as launch, \
                    mock.patch("sys.argv", ["local.py", command]):
                local.main()
                self.assertEqual(run.call_args.args[0], ["git", "pull", "--ff-only", "--no-rebase"])
                launch.assert_called_once_with(local.sys.executable, [local.sys.executable, str(self.root / "tools" / "local.py"), "up"])

    def test_update_keeps_services_when_pull_fails(self):
        (self.root / ".git").mkdir()
        failure = subprocess.CalledProcessError(1, "git pull")
        with mock.patch.object(local, "ROOT", self.root), \
                mock.patch.object(local, "run", side_effect=failure) as run, \
                mock.patch.object(local.os, "execv") as launch, \
                mock.patch("sys.argv", ["local.py", "--update"]), self.assertRaises(SystemExit):
            local.main()
        run.assert_called_once()
        launch.assert_not_called()

    def test_storage_path_is_explicit_persistent_and_owner_mapped(self):
        path = self.root / "assets with spaces"
        env = local.storage_environment({"KIKO_STORAGE_LOCAL_ROOT": str(path)}, create=True)
        self.assertEqual(env["KIKO_LOCAL_STORAGE_HOST_ROOT"], str(path))
        self.assertEqual(env["KIKO_LOCAL_UID"], str(os.getuid()))
        self.assertEqual(path.stat().st_mode & 0o777, 0o700)
        for invalid in ("relative/path", "../elsewhere"):
            with self.assertRaises(ValueError):
                local.storage_environment({"KIKO_STORAGE_LOCAL_ROOT": invalid})
        link = self.root / "link"
        link.symlink_to(path)
        with self.assertRaises(ValueError):
            local.storage_environment({"KIKO_STORAGE_LOCAL_ROOT": str(link)})

    def test_default_storage_root_is_current_user_home(self):
        with mock.patch.object(Path, "home", return_value=self.root):
            # expanduser is driven by the current process HOME.
            with mock.patch.dict(os.environ, {"HOME": str(self.root)}):
                result = local.storage_environment({})
                self.assertEqual(result["KIKO_LOCAL_STORAGE_HOST_ROOT"], str(self.root / ".kiko"))

    def test_first_start_and_stable_secrets(self):
        first = local.initialize(self.root, False, {})
        before = (self.root / ".env").read_bytes()
        second = local.initialize(self.root, True, {})
        self.assertEqual(first, second)
        self.assertEqual(before, (self.root / ".env").read_bytes())
        self.assertEqual((self.root / ".env").stat().st_mode & 0o777, 0o600)
        # Platform configuration and opt-in live-test credentials are not startup inputs.
        self.assertNotIn("KIKO_MAIL_ENABLED", first)
        self.assertNotIn("KIKO_REGISTRATION_VERIFICATION_REQUIRED", first)
        self.assertFalse(any(key.startswith(("SMTP_", "KIKO_GITHUB_OAUTH_", "DEEPSEEK_")) for key in first))

    def test_missing_env_with_data_refused(self):
        with self.assertRaises(ValueError):
            local.initialize(self.root, True, {})
        self.assertFalse((self.root / ".env").exists())
        local.initialize(self.root, False, {})
        (self.root / ".env").unlink()
        with self.assertRaises(ValueError):
            local.initialize(self.root, False, {})

    def test_runtime_upgrade_preserves_existing_configuration(self):
        local.initialize(self.root, False, {})
        before = (self.root / ".env").read_bytes()
        first = local.initialize_runtime(self.root)
        self.assertEqual(set(first), set(local.RUNTIME_TOKENS))
        self.assertTrue(all(len(token) >= 32 for token in first.values()))
        self.assertEqual(len(set(first.values())), 2)
        runtime = self.root / ".kiko-local" / "runtime.env"
        self.assertEqual(runtime.stat().st_mode & 0o777, 0o600)
        self.assertEqual(local.initialize_runtime(self.root), first)
        self.assertEqual((self.root / ".env").read_bytes(), before)

    def test_legacy_and_explicit_config_refused(self):
        with self.assertRaises(ValueError):
            local.initialize(self.root, False, {"DATABASE_URL": "postgres://existing"})
        (self.root / "backend").mkdir()
        (self.root / "backend" / "backend.local.json").write_text("{}")
        with self.assertRaises(ValueError):
            local.initialize(self.root, False, {})

    def test_secret_rotation_refused(self):
        local.initialize(self.root, False, {})
        path = self.root / ".env"
        path.write_text(path.read_text().replace("KIKO_AUTH_SECRET=", "KIKO_AUTH_SECRET=changed"))
        with self.assertRaises(ValueError):
            local.initialize(self.root, True, {})

    def test_invalid_existing_file_not_overwritten(self):
        path = self.root / ".env"
        path.write_text("invalid")
        path.chmod(0o600)
        with self.assertRaises(ValueError):
            local.initialize(self.root, False, {})
        self.assertEqual(path.read_text(), "invalid")

    def test_public_origin_refused(self):
        values = local.initialize(self.root, False, {})
        values["KIKO_PUBLIC_ORIGIN"] = "https://public.example.com"
        with self.assertRaises(ValueError):
            local.validate(values)

    def test_duplicate_env_and_public_permissions_refused(self):
        path = self.root / ".env"
        path.write_text("KEY=one\nKEY=two\n")
        path.chmod(0o600)
        with self.assertRaises(ValueError):
            local.read_env(path)
        path.write_text("KEY=one\n")
        path.chmod(0o644)
        with self.assertRaises(ValueError):
            local.read_env(path)

    def test_ambiguous_dotenv_values_refused(self):
        path = self.root / ".env"
        for value in ("'unclosed", "unquoted'", "a\\b", "a\tb", "'a'b'"):
            path.write_text("KEY=" + value + "\n")
            path.chmod(0o600)
            with self.subTest(value=value), self.assertRaises(ValueError):
                local.read_env(path)
        path.write_text("KEY='literal $ # value'\n")
        self.assertEqual(local.read_env(path)["KEY"], "literal $ # value")

    def test_symlink_refused(self):
        real = self.root / "real"
        real.write_text("KEY=one\n")
        real.chmod(0o600)
        (self.root / ".env").symlink_to(real)
        with self.assertRaises(ValueError):
            local.read_env(self.root / ".env")


if __name__ == "__main__":
    unittest.main()
