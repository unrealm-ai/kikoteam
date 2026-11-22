import contextlib
import io
from pathlib import Path
import subprocess
import unittest
from unittest import mock
import docker_support as support


class DockerSetupTests(unittest.TestCase):
    def test_noninteractive_never_installs(self):
        with mock.patch.object(support.sys.stdin, 'isatty', return_value=False), mock.patch.object(support, 'run') as run:
            with self.assertRaisesRegex(ValueError, '非交互'):
                support.confirm('Install?')
            run.assert_not_called()

    def test_old_macos_rejected_before_confirmation_or_download(self):
        with mock.patch.object(support.platform, 'mac_ver', return_value=('12.7.8', (), '')), mock.patch.object(support, 'confirm') as confirm, mock.patch.object(support, 'run') as run:
            with self.assertRaisesRegex(ValueError, '最低要求'):
                support.install_mac()
            confirm.assert_not_called()
            run.assert_not_called()

    def test_decline_never_downloads(self):
        with mock.patch.object(support.platform, 'mac_ver', return_value=('14.0', (), '')), mock.patch.object(support.platform, 'machine', return_value='arm64'), mock.patch.object(support, 'confirm', side_effect=ValueError('cancel')), mock.patch.object(support, 'run') as run:
            with self.assertRaises(ValueError):
                support.install_mac()
            run.assert_not_called()

    def test_mac_requires_manual_license_and_install(self):
        with mock.patch.object(support.platform, 'mac_ver', return_value=('14.0', (), '')), mock.patch.object(support.platform, 'machine', return_value='arm64'), mock.patch.object(support, 'confirm') as confirm, mock.patch.object(support.shutil, 'disk_usage', return_value=mock.Mock(free=10 * 1024 ** 3)), mock.patch.object(support.tempfile, 'mkdtemp', return_value='/tmp/test-only'), mock.patch.object(support, 'run') as run:
            with self.assertRaisesRegex(ValueError, '安装引导已打开'):
                support.install_mac()
            confirm.assert_called_once()
            self.assertIn('https://desktop.docker.com/mac/main/arm64/Docker.dmg', run.call_args_list[0].args[0])
            self.assertEqual(run.call_args_list[1].args[0], ['open', '/tmp/test-only/Docker.dmg'])
            self.assertNotIn('--accept-license', str(run.call_args_list))

    def test_linux_unsupported_distro(self):
        with mock.patch.object(Path, 'read_text', return_value='ID=unknown\nVERSION_CODENAME=unknown\n'):
            with self.assertRaisesRegex(ValueError, '发行版'):
                support.linux_distribution()

    def test_linux_existing_runtime_not_removed(self):
        with mock.patch.object(support, 'linux_distribution', return_value=('ubuntu', 'noble')), mock.patch.object(support.subprocess, 'run', return_value=subprocess.CompletedProcess([], 0, stdout='install ok installed')), mock.patch.object(support, 'confirm') as confirm:
            with self.assertRaisesRegex(ValueError, '不会自动卸载'):
                support.install_linux()
            confirm.assert_not_called()

    def test_macos13_selects_verified_history_with_warning(self):
        fake_digest = mock.Mock()
        fake_digest.hexdigest.return_value = 'fac73a1edc91e6bce5a449e83e3d0b537f19df74c5f51af4705e479cf0d32515'
        with mock.patch.object(support.platform, 'mac_ver', return_value=('13.7.8', (), '')), mock.patch.object(support.platform, 'machine', return_value='x86_64'), mock.patch.object(support, 'confirm') as confirm, mock.patch.object(support.shutil, 'disk_usage', return_value=mock.Mock(free=10 * 1024 ** 3)), mock.patch.object(support.tempfile, 'mkdtemp', return_value='/tmp/test-only'), mock.patch.object(support, 'run') as run, mock.patch.object(Path, 'open', mock.mock_open(read_data=b'')), mock.patch.object(support.hashlib, 'sha256', return_value=fake_digest):
            with self.assertRaisesRegex(ValueError, '安装引导已打开'):
                support.install_mac()
            self.assertIn('4.48.0', confirm.call_args.args[0])
            self.assertIn('支持已终止', confirm.call_args.args[0])
            self.assertIn('https://desktop.docker.com/mac/main/amd64/207573/Docker.dmg', run.call_args_list[0].args[0])

    def test_low_disk_does_not_download(self):
        with mock.patch.object(support.platform, 'mac_ver', return_value=('14.0', (), '')), mock.patch.object(support.platform, 'machine', return_value='arm64'), mock.patch.object(support, 'confirm'), mock.patch.object(support.shutil, 'disk_usage', return_value=mock.Mock(free=100)), mock.patch.object(support, 'run') as run:
            with self.assertRaisesRegex(ValueError, '磁盘'):
                support.install_mac()
            run.assert_not_called()

    def test_status_does_not_offer_install(self):
        with mock.patch.object(support, 'docker_path', return_value=None), mock.patch.object(support, 'install_mac') as install:
            with self.assertRaises(ValueError):
                support.ensure_docker(False)
            install.assert_not_called()

    def test_linux_daemon_failure_no_privilege_change(self):
        with mock.patch.object(support, 'docker_path', return_value='docker'), mock.patch.object(support.platform, 'system', return_value='Linux'), mock.patch.object(support, 'run', side_effect=[None, subprocess.CalledProcessError(1, ['docker', 'info'])]) as run:
            with self.assertRaisesRegex(ValueError, '不会自动 sudo'):
                support.ensure_docker(True)
            self.assertEqual(run.call_count, 2)

    def test_timed_runner_preserves_output_and_error(self):
        buffer = io.StringIO()
        with contextlib.redirect_stderr(buffer), mock.patch.object(support.subprocess, 'run', return_value=subprocess.CompletedProcess([], 0)) as run:
            support.run(['safe-test'], label='测试阶段', timeout=3)
            self.assertIn('[开始]', buffer.getvalue())
            self.assertIn('[完成]', buffer.getvalue())
            self.assertEqual(run.call_args.kwargs['timeout'], 3)
        with mock.patch.object(support.subprocess, 'run', side_effect=subprocess.TimeoutExpired(['safe-test'], 3)):
            with self.assertRaises(subprocess.TimeoutExpired):
                support.run(['safe-test'], timeout=3)
