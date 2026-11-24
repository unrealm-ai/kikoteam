"""Exercise release selection, deployment ordering and recovery."""
from contextlib import ExitStack
import json
from pathlib import Path
import subprocess
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

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
        for name in ("release", "configuration", "space_check", "pull", "healthy",
                     "sql", "stop", "backup", "switch_tag", "start", "run", "output",
                     "http_status"):
            self.mock[name] = stack.enter_context(patch.object(deploy, name))
        stack.enter_context(patch.object(deploy.shutil, "disk_usage", return_value=SimpleNamespace(free=8 * 1024**3)))
        self.mock["release"].return_value = self.new
        self.mock["configuration"].return_value = self.root, "test"
        self.mock["sql"].side_effect = AssertionError("unexpected database query outside backup sizing")
        self.mock["http_status"].side_effect = [503, 200]
        self.mock["output"].return_value = '{"phase":"running"}'

    def test_older_and_failed_pull_leave_service_online(self):
        for number in (50, 51):
            self.mock["release"].return_value = dict(self.new, run=number)
            deploy.tick()
            self.mock["pull"].assert_not_called()
        self.mock["release"].return_value = self.new
        self.mock["pull"].side_effect = RuntimeError("registry unavailable")
        with self.assertRaises(RuntimeError):
            deploy.tick()
        self.mock["http_status"].assert_not_called()
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
        deploy.tick()
        self.mock["pull"].assert_called_once_with(selected)
        self.mock["healthy"].assert_called_once_with(selected)
        self.mock["backup"].assert_called_once()
        self.assertEqual(self.mock["backup"].call_args.args[2], current)
        self.mock["switch_tag"].assert_called_once_with(current, selected)
        self.assertEqual(deploy.read(deploy.CURRENT), selected)
        self.assertTrue(deploy.image_ref("backend", current).endswith(":1111111"))
        self.assertTrue(deploy.image_ref("backend", selected).endswith(":v1.2.3"))

    def test_higher_run_restarts_even_with_identical_images(self):
        self.new = dict(self.old, run=52)
        self.mock["release"].return_value = self.new
        deploy.tick()
        self.assertEqual([c.args[0] for c in self.mock["stop"].call_args_list],
                         ["agent-engine", "event-hub", "backend"])
        self.mock["backup"].assert_called_once()
        self.mock["healthy"].assert_called_once_with(self.new)
        self.assertEqual(deploy.read(deploy.CURRENT), self.new)
        self.assertFalse(deploy.PENDING.exists())

    def test_main_release_with_colliding_short_sha_keeps_rollback_tag(self):
        target = dict(self.new, sha=self.old["sha"][:7] + "a" * 33)
        self.mock["release"].return_value = target
        deploy.tick()
        deployed = deploy.read(deploy.CURRENT)
        self.assertTrue(deploy.image_ref("backend", deployed).endswith(":run-53"))
        self.mock["switch_tag"].assert_called_once_with(self.old, deployed)

    def test_locally_imported_rollback_image_uses_recorded_digest(self):
        baseline = dict(self.old, source="mac", digests={"backend": "sha256:" + "a" * 64})
        tag = deploy.image_ref("backend", baseline)
        self.assertTrue(tag.endswith(":mac-1111111"))
        image = {"Os": "linux", "Architecture": "amd64", "Id": baseline["digests"]["backend"],
                 "Config": {"Labels": {"org.opencontainers.image.revision": baseline["sha"]}}}
        self.mock["output"].return_value = json.dumps([image])
        self.assertTrue(deploy.verified_image("backend", baseline))
        self.mock["output"].assert_called_once_with("docker", "image", "inspect", tag)
        self.mock["output"].return_value = json.dumps([dict(image, Id="other")])
        self.assertFalse(deploy.verified_image("backend", baseline))

    def test_maintenance_failure_before_stop_reopens(self):
        self.mock["http_status"].side_effect = [200]
        with self.assertRaisesRegex(RuntimeError, "public ingress"):
            deploy.tick()
        self.mock["stop"].assert_not_called()
        self.assertFalse(deploy.MAINTENANCE.exists())
        self.assertFalse(deploy.PENDING.exists())
        self.assertEqual(deploy.read(deploy.CURRENT), self.old)

    def test_failed_migration_pauses_future_attempts(self):
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
        self.mock["http_status"].side_effect = [502]
        with self.assertRaises(RuntimeError):
            deploy.publish_healthy(self.new, "test")
        self.assertTrue(deploy.PENDING.exists())
        self.assertTrue(deploy.MAINTENANCE.exists())
        self.assertEqual(deploy.read(deploy.CURRENT), self.old)


if __name__ == "__main__":
    unittest.main()
