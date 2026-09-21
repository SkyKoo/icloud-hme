#!/usr/bin/env python3
"""管理 O 机器的部署状态、备份和失败回退；由 deploy.sh 按需以 root 调用。"""

import argparse
import json
import os
from pathlib import Path
import secrets
import shutil
import signal
import subprocess
import sys
import tempfile
from datetime import datetime, timezone


class Deployment:
    """源码与运行数据分离，部署过程只操作 icloud-hme Compose 项目。"""

    def __init__(self, root=Path('/opt/icloud-hme')):
        self.root = Path(root)
        self.config = self.root / 'compose.yaml'
        self.state_file = self.root / 'current.json'
        self.env_file = self.root / '.env'
        self.data = self.root / 'data'
        self.backups = self.root / 'backups'

    def docker(self, *args, capture=False, env=None):
        result = subprocess.run(['docker', *args], check=True, text=True,
                                stdout=subprocess.PIPE if capture else None, env=env)
        return result.stdout.strip() if capture else ''

    def compose(self, image, *args, template=None):
        # 环境变量优先级高于 .env，显式指定本次镜像并隔离外部 Compose 配置。
        env = {k: v for k, v in os.environ.items()
               if not k.startswith(('ICLOUD_HME_', 'COMPOSE_'))}
        env['ICLOUD_HME_IMAGE'] = image
        return self.docker('compose', '--project-name', 'icloud-hme',
                           '--project-directory', str(self.root),
                           '--env-file', str(self.env_file),
                           '-f', str(template or self.config), *args, env=env)

    def initialize(self):
        self.root.mkdir(mode=0o755, parents=True, exist_ok=True)
        self.backups.mkdir(mode=0o700, exist_ok=True)
        os.chmod(self.backups, 0o700)
        if not self.data.exists():
            self.data.mkdir(mode=0o700)
            os.chown(self.data, 10001, 10001)
        if not self.env_file.exists():
            # urlsafe 随机值不含 .env 插值字符；不在输出或命令参数中暴露密码。
            contents = ('ICLOUD_HME_ADMIN_PASSWORD=' + secrets.token_urlsafe(32) + '\n'
                        'ICLOUD_HME_BIND_PORT=8081\nICLOUD_HME_SESSION_TTL=12h\n'
                        'ICLOUD_HME_SECURE_COOKIE=false\n')
            with self.env_file.open('x') as stream:
                os.chmod(self.env_file, 0o600)
                stream.write(contents)
            print('已生成管理员密码，保存在 /opt/icloud-hme/.env（仅 root 可读）。')

    def state(self):
        return json.loads(self.state_file.read_text()) if self.state_file.exists() else None

    @staticmethod
    def copy_config(source, target, mode):
        # 临时文件从创建起即为 600，恢复 .env 时不出现凭据可读窗口。
        with tempfile.NamedTemporaryFile(dir=target.parent, prefix='.' + target.name,
                                         delete=False) as stream:
            temporary = Path(stream.name)
            try:
                with Path(source).open('rb') as original:
                    shutil.copyfileobj(original, stream)
                os.fchmod(stream.fileno(), mode)
            except BaseException:
                temporary.unlink(missing_ok=True)
                raise
        temporary.replace(target)

    def write_state(self, state):
        temporary = self.state_file.with_suffix('.tmp')
        temporary.write_text(json.dumps(state, ensure_ascii=False, indent=2) + '\n')
        os.chmod(temporary, 0o600)
        temporary.replace(self.state_file)

    def start(self, image):
        self.compose(image, 'up', '-d', '--no-build', '--pull', 'never',
                     '--wait', '--wait-timeout', '90', 'icloud-hme')

    def activate(self, image, commit, template):
        """新镜像已构建；备份后切换，失败时恢复旧配置和一致数据。"""
        self.initialize()
        old = self.state()
        platform = self.docker('image', 'inspect', '--format',
                               '{{.Os}}/{{.Architecture}}', image, capture=True)
        if platform != 'linux/arm64':
            raise RuntimeError('待部署镜像必须为 linux/arm64。')
        self.compose(image, 'config', '--quiet', template=template)
        if old:
            self.docker('image', 'inspect', '--format', '{{.Id}}', old['image'], capture=True)
            if not self.config.exists():
                raise RuntimeError('已有部署记录但 Compose 文件缺失，请先恢复配置。')
        elif self.docker('ps', '-aq', '--filter',
                         'label=com.docker.compose.project=icloud-hme', capture=True):
            raise RuntimeError('已有 icloud-hme 容器但无部署记录，请先核对，不自动覆盖。')

        name = datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%SZ-') + secrets.token_hex(3)
        backup = self.backups / name
        backup.mkdir(mode=0o700)
        if old:
            shutil.copy2(self.state_file, backup / 'current.json')
            shutil.copy2(self.config, backup / 'compose.yaml')
        shutil.copy2(self.env_file, backup / '.env')
        stopped = False
        snapshot_ready = False
        switched = False
        try:
            if old:
                # 保守地先标记：即使 stop 部分失败，也尝试恢复旧服务。
                stopped = True
                self.compose(old['image'], 'stop', 'icloud-hme')
            subprocess.run(['tar', '-C', str(self.root), '-czf',
                            str(backup / 'data.tar.gz'), 'data'], check=True)
            snapshot_ready = True
            self.copy_config(template, self.config, 0o644)
            switched = True
            self.start(image)
            self.write_state({'image': image, 'commit': commit,
                              'deployed_at': datetime.now(timezone.utc).isoformat(),
                              'backup': str(backup)})
        except (Exception, KeyboardInterrupt) as error:
            try:
                (backup / 'failure.txt').write_text(str(error) + '\n')
            except OSError:
                # 磁盘写满时仍须尝试恢复服务，不依赖诊断文件写入成功。
                print(f'无法写入诊断文件：{backup}', file=sys.stderr)
            try:
                if switched:
                    self.compose(image, 'stop', 'icloud-hme')
                if old and (stopped or switched):
                    if switched and snapshot_ready:
                        # 保留新版本接触过的数据，再恢复升级前的快照。
                        self.data.rename(backup / 'data.failed')
                        subprocess.run(['tar', '-C', str(self.root), '-xzf',
                                        str(backup / 'data.tar.gz')], check=True)
                    self.copy_config(backup / 'compose.yaml', self.config, 0o644)
                    self.copy_config(backup / '.env', self.env_file, 0o600)
                    self.copy_config(backup / 'current.json', self.state_file, 0o600)
                    self.start(old['image'])
                    print('部署失败，已恢复旧镜像、配置和升级前数据。', file=sys.stderr)
                elif switched:
                    # 首次失败没有旧版本可恢复；只移除本项目失败容器，保留数据。
                    self.compose(image, 'rm', '-f', 'icloud-hme')
                    print('首次部署失败，已停止失败容器，配置与数据已保留。', file=sys.stderr)
            except Exception as recovery_error:
                raise RuntimeError(f'自动恢复未完成，请检查 {backup}；{recovery_error}') from error
            raise RuntimeError(f'部署未成功；诊断及备份：{backup}') from error
        print(f'部署成功：{image}\n提交：{commit}\n备份：{backup}')
        self.compose(image, 'ps')

    def apply(self):
        """使用当前镜像重新应用运行配置，不拉代码或重新构建。"""
        current = self.state()
        if not current:
            raise RuntimeError('尚未部署，请先执行 deploy。')
        self.activate(current['image'], current['commit'], self.config)

    def rollback(self):
        """手动切回最近一次部署前的镜像与模板，保留目前业务数据。"""
        current = self.state()
        if not current:
            raise RuntimeError('没有当前部署记录，无法回退。')
        backup = Path(current['backup'])
        previous_file = backup / 'current.json'
        if not previous_file.exists():
            raise RuntimeError('当前为首次部署，没有上一版本。')
        previous = json.loads(previous_file.read_text())
        self.activate(previous['image'], previous['commit'], backup / 'compose.yaml')
        print('手动回退保留了当前业务数据；数据格式不兼容时需另行恢复数据备份。')

    def status(self):
        current = self.state()
        if not current:
            print('尚未部署。')
            return
        print(json.dumps(current, ensure_ascii=False, indent=2))
        self.compose(current['image'], 'ps')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['deploy', 'apply', 'rollback', 'status'])
    parser.add_argument('--image')
    parser.add_argument('--commit')
    parser.add_argument('--template', type=Path)
    args = parser.parse_args()
    if os.geteuid() != 0:
        parser.error('请通过 deploy.sh 调用；运行配置由 root 管理。')
    if args.action == 'deploy' and not all((args.image, args.commit, args.template)):
        parser.error('deploy 需要 image、commit 和 template。')

    def interrupted(signum, frame):
        raise RuntimeError(f'部署被信号 {signum} 中断。')

    signal.signal(signal.SIGTERM, interrupted)
    signal.signal(signal.SIGHUP, interrupted)
    try:
        deployment = Deployment()
        if args.action == 'deploy':
            deployment.activate(args.image, args.commit, args.template)
        elif args.action == 'apply':
            deployment.apply()
        elif args.action == 'rollback':
            deployment.rollback()
        else:
            deployment.status()
    except (Exception, KeyboardInterrupt) as error:
        print(f'错误：{error}', file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
