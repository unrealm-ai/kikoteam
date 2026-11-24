"""Exercise deployment ordering and the write fence against the real schema."""
from contextlib import ExitStack
import json
import os
from pathlib import Path
import subprocess
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

import admission
import deploy


class DeploymentTest(unittest.TestCase):
    def setUp(self):
        stack = ExitStack()
        self.addCleanup(stack.close)
        self.root = Path(stack.enter_context(tempfile.TemporaryDirectory()))
        for name in ("CURRENT", "PENDING", "MAINTENANCE", "BACKUPS", "STAGED"):
            stack.enter_context(patch.object(deploy, name, self.root / name))
        self.old = {"run": 51, "sha": "1" * 40}
        self.new = {"run": 53, "sha": "2" * 40}
        deploy.atomic(deploy.CURRENT, self.old)
        self.mock = {}
        for name in ("release", "configuration", "space_check", "pull", "healthy", "idle",
                     "hold", "sql", "stop", "backup", "switch_tag", "start", "run", "output",
                     "http_status"):
            self.mock[name] = stack.enter_context(patch.object(deploy, name))
        stack.enter_context(patch.object(deploy.shutil, "disk_usage", return_value=SimpleNamespace(free=8 * 1024**3)))
        self.mock["release"].return_value = self.new
        self.mock["configuration"].return_value = self.root, "test"
        self.mock["sql"].return_value = "f"

    def test_older_and_failed_pull_leave_service_online(self):
        self.mock["release"].return_value = self.old
        deploy.tick()
        self.mock["pull"].assert_not_called()
        self.mock["release"].return_value = self.new
        self.mock["pull"].side_effect = RuntimeError("registry unavailable")
        with self.assertRaises(RuntimeError):
            deploy.tick()
        self.mock["hold"].assert_not_called()
        self.mock["stop"].assert_not_called()
        self.assertFalse(deploy.PENDING.exists())

    def test_staging_requires_success_and_three_promoted_indexes(self):
        metadata = {"name": "Publish images", "event": "push", "head_branch": "main",
                    "status": "completed", "conclusion": "failure", "path": ".github/workflows/images.yml",
                    "head_repository": {"full_name": "unrealm-ai/kikoteam"}, "head_sha": self.new["sha"],
                    "run_number": self.new["run"]}
        self.mock["release"].return_value = dict(self.old, version="v1.9.9")
        with self.assertRaises(ValueError):
            deploy.stage_success(metadata)
        metadata["conclusion"] = "success"
        with patch.object(subprocess, "check_output", side_effect=[b"a", b"a", b"b", b"b", b"c", b"d"]):
            with self.assertRaises(ValueError):
                deploy.stage_success(metadata)
        self.assertFalse(deploy.STAGED.exists())
        with patch.object(subprocess, "check_output", side_effect=[b"a", b"a", b"b", b"b", b"c", b"c"]):
            deploy.stage_success(metadata)
        self.assertEqual(deploy.read(deploy.STAGED)["run"], self.new["run"])
        self.assertEqual(set(deploy.read(deploy.STAGED)["digests"]), set(deploy.APPS))

    def test_tagged_stage_requires_three_matching_aliases_and_newer_version(self):
        metadata = {"name": "Publish images", "event": "push", "head_branch": "v1.10.0",
                    "status": "completed", "conclusion": "success", "path": ".github/workflows/images.yml",
                    "head_repository": {"full_name": "unrealm-ai/kikoteam"}, "head_sha": self.new["sha"],
                    "run_number": self.new["run"]}
        self.mock["release"].return_value = dict(self.old, run=56, version="v1.9.9")
        index = json.dumps({"annotations": {
            "org.opencontainers.image.revision": self.new["sha"],
            "org.opencontainers.image.version": "v1.10.0",
        }}).encode()
        with patch.object(subprocess, "check_output", side_effect=[index, b"bad"]):
            with self.assertRaisesRegex(ValueError, "indexes differ"):
                deploy.stage_success(metadata)
        self.assertFalse(deploy.STAGED.exists())
        wrong_revision = index.replace(self.new["sha"].encode(), self.old["sha"].encode())
        with patch.object(subprocess, "check_output", side_effect=[wrong_revision] * 2):
            with self.assertRaisesRegex(ValueError, "does not match"):
                deploy.stage_success(metadata)
        self.assertFalse(deploy.STAGED.exists())
        with patch.object(subprocess, "check_output", side_effect=[index] * 6) as inspect:
            deploy.stage_success(metadata)
        self.assertEqual(deploy.read(deploy.STAGED)["version"], "v1.10.0")
        self.assertEqual(deploy.read(deploy.STAGED)["run"], 57)
        for i in range(0, 6, 2):
            refs = [call.args[0][-1].rsplit(":", 1)[1] for call in inspect.call_args_list[i:i+2]]
            self.assertEqual(refs, ["v1.10.0", "latest"])
        with patch.object(subprocess, "check_output") as inspect:
            for version in ("v1.8.0", "v01.10.0", "v2.0.0-rc.1"):
                with self.subTest(version=version), self.assertRaises(ValueError):
                    deploy.stage_success(dict(metadata, head_branch=version))
            inspect.assert_not_called()

    def test_releasing_same_commit_keeps_old_image_tag_for_rollback(self):
        current = dict(self.old, digests={app: "old" for app in deploy.APPS})
        target = dict(current, run=53, version="v1.2.3",
                      digests={app: "new" for app in deploy.APPS})
        deploy.atomic(deploy.CURRENT, current)
        self.mock["release"].return_value = target
        selected = dict(target, local_tag="v1.2.3")
        self.mock["idle"].return_value = True
        self.mock["output"].return_value = '{"phase":"running"}'
        self.mock["http_status"].return_value = 200
        deploy.tick()
        self.mock["pull"].assert_called_once_with(selected)
        self.assertEqual([call.args[0] for call in self.mock["healthy"].call_args_list],
                         [current, selected])
        self.mock["backup"].assert_called_once()
        self.assertEqual(self.mock["backup"].call_args.args[2], current)
        self.mock["switch_tag"].assert_called_once_with(current, selected)
        self.assertEqual(deploy.read(deploy.CURRENT), selected)
        self.assertTrue(deploy.image_ref("backend", current).endswith(":1111111"))
        self.assertTrue(deploy.image_ref("backend", selected).endswith(":v1.2.3"))

    def test_busy_at_final_check_reopens(self):
        def busy(host):
            deploy.MAINTENANCE.touch()
            return False
        self.mock["hold"].side_effect = busy
        deploy.tick()
        self.mock["stop"].assert_not_called()
        self.assertFalse(deploy.MAINTENANCE.exists())
        self.assertFalse(deploy.PENDING.exists())
        self.assertEqual(deploy.read(deploy.CURRENT), self.old)

    def test_failed_migration_pauses_future_attempts(self):
        self.mock["hold"].side_effect = lambda host: deploy.MAINTENANCE.touch() or True
        self.mock["output"].return_value = '{"phase":"failed"}'
        with self.assertRaises(RuntimeError):
            deploy.tick()
        self.assertEqual([c.args[0] for c in self.mock["stop"].call_args_list],
                         ["agent-engine", "event-hub", "backend"])
        self.mock["backup"].assert_called_once()
        self.assertTrue(deploy.MAINTENANCE.exists())
        self.assertEqual(deploy.read(deploy.PENDING)["phase"], "cutover")
        self.assertEqual(deploy.read(deploy.CURRENT), self.old)
        calls = self.mock["release"].call_count
        deploy.tick()
        self.assertEqual(self.mock["release"].call_count, calls)

    def test_failed_ingress_does_not_record_success(self):
        deploy.MAINTENANCE.touch()
        deploy.PENDING.touch()
        self.mock["http_status"].return_value = 502
        with self.assertRaises(RuntimeError):
            deploy.publish_healthy(self.new, "test")
        self.assertTrue(deploy.PENDING.exists())
        self.assertTrue(deploy.MAINTENANCE.exists())
        self.assertEqual(deploy.read(deploy.CURRENT), self.old)


@unittest.skipUnless(os.getenv("KIKO_TEST_POSTGRES_BIN"), "set KIKO_TEST_POSTGRES_BIN to run isolated PostgreSQL")
class AdmissionTest(unittest.TestCase):
    def test_concurrent_writer_and_busy_and_idle(self):
        pg = Path(os.environ["KIKO_TEST_POSTGRES_BIN"])
        schema = Path(__file__).resolve().parents[2] / "event-hub/database/postgres/schema.sql"
        # macOS has a short maximum Unix socket pathname.
        with tempfile.TemporaryDirectory(dir="/tmp") as folder:
            cluster = Path(folder) / "pg"
            subprocess.run([pg / "initdb", "-D", cluster, "-A", "trust", "--no-locale"],
                           check=True, stdout=subprocess.DEVNULL)
            subprocess.run([pg / "pg_ctl", "-D", cluster, "-l", cluster / "server.log",
                            "-o", f"-k {folder} -c listen_addresses=''", "-w", "start"], check=True)
            try:
                psql = [pg / "psql", "-h", folder, "-d", "postgres", "-XqAt", "-v", "ON_ERROR_STOP=1"]
                def sql(query):
                    return subprocess.run(psql, input=query, capture_output=True, text=True)
                subprocess.run([*psql, "-f", schema], check=True, stdout=subprocess.DEVNULL)
                with subprocess.Popen(psql, stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True) as writer:
                    writer.stdin.write("BEGIN; LOCK public.manager_tasks IN ROW EXCLUSIVE MODE;\n\\echo READY\n")
                    writer.stdin.flush()
                    self.assertEqual(writer.stdout.readline().strip(), "READY")
                    result = sql(admission.acquire_sql())
                    writer.communicate("ROLLBACK;\n")
                    self.assertNotEqual(result.returncode, 0)
                    self.assertIn("lock timeout", result.stderr)
                insert = """INSERT INTO platform_model_probe_leases VALUES
                    (gen_random_uuid(),gen_random_uuid(),gen_random_uuid(),now()+interval '1 minute');"""
                result = sql("SET session_replication_role=replica; " + insert)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(sql(admission.acquire_sql()).stdout.strip(), "BUSY")
                self.assertEqual(sql("DELETE FROM platform_model_probe_leases").returncode, 0)
                result = sql(admission.acquire_sql())
                self.assertEqual(result.stdout.strip(), "FENCED", result.stderr)
                for table in admission.TABLES:
                    result = sql(f"INSERT INTO public.{table} DEFAULT VALUES")
                    self.assertIn("deployment in progress", result.stderr, table)
                result = sql(admission.release_sql())
                self.assertEqual(result.returncode, 0, result.stderr)
                result = sql("SET session_replication_role=replica; " + insert)
                self.assertEqual(result.returncode, 0, result.stderr)
            finally:
                subprocess.run([pg / "pg_ctl", "-D", cluster, "-m", "fast", "-w", "stop"], check=True)


if __name__ == "__main__":
    unittest.main()
