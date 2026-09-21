"""用真实临时目录和备份归档验证升级失败后的恢复边界，不启动 Docker。"""
import contextlib
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from deploy import Deployment


class FakeDockerDeployment(Deployment):
    """仅替换 Docker 进程；文件、状态和 tar 备份仍使用真实实现。"""

    def __init__(self, root):
        super().__init__(root)
        self.events = []
        self.fail_image = None
        self.invalid_config = False
        self.missing_image = None
        self.unmanaged_container = False

    def docker(self, *args, capture=False, env=None):
        image = (env or {}).get('ICLOUD_HME_IMAGE')
        self.events.append((args, image))
        if args[:2] == ('image', 'inspect'):
            if args[-1] == self.missing_image:
                raise RuntimeError('旧镜像不存在')
            return 'linux/arm64' if 'Architecture' in args[3] else 'sha256:existing'
        if args[0] == 'ps':
            return 'existing-container' if self.unmanaged_container else ''
        if 'config' in args and self.invalid_config:
            raise RuntimeError('配置错误')
        if 'up' in args and image == self.fail_image:
            (self.data / 'accounts.json').write_text('new-version-wrote-this')
            raise RuntimeError('健康检查失败')
        return ''


class DeploymentTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        base = Path(self.temp.name)
        self.template = base / 'template.yaml'
        self.template.write_text('services: {}\n')
        self.app = FakeDockerDeployment(base / 'runtime')
        self.chown = patch('deploy.os.chown').start()
        self.addCleanup(patch.stopall)
        self.output = contextlib.redirect_stdout(io.StringIO())
        self.output.__enter__()
        self.addCleanup(self.output.__exit__, None, None, None)
        self.errors = contextlib.redirect_stderr(io.StringIO())
        self.errors.__enter__()
        self.addCleanup(self.errors.__exit__, None, None, None)

    def initial(self):
        self.app.activate('local:v1', 'commit-1', self.template)
        (self.app.data / 'accounts.json').write_text('original-data')
        self.app.events.clear()

    def test_initial_deploy_creates_protected_credentials_and_state(self):
        self.initial()
        self.assertEqual(self.app.state()['image'], 'local:v1')
        self.assertEqual(self.app.env_file.stat().st_mode & 0o777, 0o600)
        self.assertEqual(self.app.backups.stat().st_mode & 0o777, 0o700)
        self.chown.assert_called_once_with(self.app.data, 10001, 10001)
        self.assertTrue((Path(self.app.state()['backup']) / 'data.tar.gz').exists())

    def test_upgrade_backs_up_previous_version_without_changing_credentials(self):
        self.initial()
        env = self.app.env_file.read_bytes()
        self.template.write_text('services: {updated: {}}\n')
        self.app.activate('local:v2', 'commit-2', self.template)
        backup = Path(self.app.state()['backup'])
        self.assertEqual(json.loads((backup / 'current.json').read_text())['image'], 'local:v1')
        self.assertEqual((backup / 'compose.yaml').read_text(), 'services: {}\n')
        self.assertEqual(self.app.env_file.read_bytes(), env)
        events = self.app.events
        stop = next(i for i, (a, _) in enumerate(events) if 'stop' in a)
        start = next(i for i, (a, _) in enumerate(events) if 'up' in a)
        self.assertLess(stop, start)

    def test_failed_healthcheck_restores_old_data_config_and_image(self):
        self.initial()
        old_state = self.app.state()
        self.template.write_text('services: {updated: {}}\n')
        self.app.fail_image = 'local:v2'
        with self.assertRaisesRegex(RuntimeError, '部署未成功'):
            self.app.activate('local:v2', 'commit-2', self.template)
        self.assertEqual(self.app.state(), old_state)
        self.assertEqual((self.app.data / 'accounts.json').read_text(), 'original-data')
        self.assertEqual(self.app.config.read_text(), 'services: {}\n')
        failed_data = list(self.app.backups.glob('*/data.failed/accounts.json'))
        self.assertEqual(len(failed_data), 1)
        self.assertEqual(failed_data[0].read_text(), 'new-version-wrote-this')
        self.assertEqual([image for args, image in self.app.events if 'up' in args],
                         ['local:v2', 'local:v1'])

    def test_invalid_config_leaves_old_service_running(self):
        self.initial()
        self.app.invalid_config = True
        with self.assertRaisesRegex(RuntimeError, '配置错误'):
            self.app.activate('local:v2', 'commit-2', self.template)
        self.assertFalse(any('stop' in args for args, _ in self.app.events))
        self.assertEqual(self.app.state()['image'], 'local:v1')

    def test_missing_previous_image_blocks_switch(self):
        self.initial()
        self.app.missing_image = 'local:v1'
        with self.assertRaisesRegex(RuntimeError, '旧镜像不存在'):
            self.app.activate('local:v2', 'commit-2', self.template)
        self.assertFalse(any('stop' in args for args, _ in self.app.events))

    def test_failed_backup_restarts_old_service_without_touching_data(self):
        self.initial()
        with patch('deploy.subprocess.run', side_effect=OSError('磁盘已满')):
            with self.assertRaisesRegex(RuntimeError, '部署未成功'):
                self.app.activate('local:v2', 'commit-2', self.template)
        self.assertEqual((self.app.data / 'accounts.json').read_text(), 'original-data')
        self.assertEqual([image for args, image in self.app.events if 'up' in args], ['local:v1'])

    def test_failed_diagnostic_write_does_not_prevent_recovery(self):
        self.initial()
        with patch('deploy.subprocess.run', side_effect=OSError('磁盘已满')):
            with patch('deploy.Path.write_text', side_effect=OSError('磁盘已满')):
                with self.assertRaisesRegex(RuntimeError, '部署未成功'):
                    self.app.activate('local:v2', 'commit-2', self.template)
        self.assertEqual([image for args, image in self.app.events if 'up' in args], ['local:v1'])

    def test_apply_uses_current_image_and_preserves_updated_env(self):
        self.initial()
        self.app.env_file.write_text('ICLOUD_HME_ADMIN_PASSWORD=new-password\n')
        self.app.apply()
        self.assertEqual(self.app.state()['image'], 'local:v1')
        self.assertIn('new-password', self.app.env_file.read_text())

    def test_manual_rollback_keeps_current_business_data(self):
        self.initial()
        self.app.activate('local:v2', 'commit-2', self.template)
        (self.app.data / 'accounts.json').write_text('new-user-data')
        self.app.rollback()
        self.assertEqual(self.app.state()['image'], 'local:v1')
        self.assertEqual((self.app.data / 'accounts.json').read_text(), 'new-user-data')

    def test_first_failure_removes_only_failed_container_and_keeps_data(self):
        self.app.fail_image = 'local:v1'
        with self.assertRaisesRegex(RuntimeError, '部署未成功'):
            self.app.activate('local:v1', 'commit-1', self.template)
        self.assertIsNone(self.app.state())
        self.assertTrue(self.app.data.exists())
        self.assertTrue(any('rm' in args and args[-1] == 'icloud-hme' for args, _ in self.app.events))

    def test_existing_unmanaged_container_is_not_overwritten(self):
        self.app.unmanaged_container = True
        with self.assertRaisesRegex(RuntimeError, '不自动覆盖'):
            self.app.activate('local:v1', 'commit-1', self.template)
        self.assertFalse(any('stop' in args or 'up' in args for args, _ in self.app.events))

    def test_first_deployment_has_no_previous_version(self):
        self.initial()
        with self.assertRaisesRegex(RuntimeError, '没有上一版本'):
            self.app.rollback()


if __name__ == '__main__':
    unittest.main()
