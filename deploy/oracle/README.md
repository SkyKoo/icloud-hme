# Mac 开发、O 机器构建与部署

配置核对日期：2026-09-21。默认流程为 Mac 修改并测试、推送自己的 fork，再在
O 机器（`oracle-a1`，Linux ARM64）拉取源码、构建镜像、更新容器。
不需要等待 GitHub 镜像发布，也不需要在服务器配置 GHCR 登录。

## 日常使用

在 Mac 完成修改和测试后，提交并 `git push origin main`。然后在 Mac 执行一条命令：

```bash
ssh oracle-a1 'cd ~/projects/icloud-hme && ./deploy/oracle/deploy.sh'
```

也可以登录 O 机器，在仓库根目录运行 `./deploy/oracle/deploy.sh`。脚本会：

1. 检查普通用户、ARM64、main 分支、干净工作树，并取得部署锁。
2. 用普通用户执行 `git pull --ff-only origin main`；拒绝覆盖本地修改或部署未推送提交。
3. 用 `git archive` 导出已提交源码，sudo Docker 构建 ARM64 镜像；本机未跟踪文件不会进入构建。
4. 将镜像命名为 `icloud-hme:sha-<提交前12位>-<构建时间>`，此时旧容器继续运行。
5. 检查新配置和旧镜像，短暂停止应用并备份一致数据，然后用新镜像重建容器并等待健康检查。
6. 成功后保存部署记录；若切换失败，恢复旧镜像、配置及升级前数据，脚本以非零状态退出。

每次服务器变更前后，仍须按服务器管理仓库要求登记 `OPERATIONS_LOG.md`。
CI 在 GitHub 后台独立运行，脚本不会等待或绕过其状态；部署前应在 Mac 验证改动，
例如运行 `./build.sh`，不要把“构建成功”当成所有业务测试通过。

构建会占用 O 机器的资源，首次需要下载基础镜像和 npm/Go 依赖；后续可复用 Docker
层缓存。只构建 ARM64，不上传 GHCR。单容器更新存在短暂中断，不保证零停机或固定耗时。

## 首次准备

O 机器需要 Git、Bash、Python 3、flock、tar，以及支持 `up --wait` 的 Docker Compose。
Node.js、Go 工具链都在 Docker 构建阶段使用，无需在宿主机安装。
`ubuntu` 负责源码操作，Docker 和运行目录管理按需使用已有 sudo 权限。

已有仓库时，先引入本次新增脚本，然后执行：

```bash
ssh oracle-a1
cd ~/projects/icloud-hme
git pull --ff-only origin main
./deploy/oracle/deploy.sh
```

新服务器才需要先克隆：

```bash
umask 027
mkdir -p ~/projects
git clone https://github.com/SkyKoo/icloud-hme.git ~/projects/icloud-hme
```

公开 HTTPS 仓库无需 GitHub 密钥；不要使用 sudo git。
首次运行自动初始化以下目录，已有 `.env` 不会被覆盖：

| 路径 | 归属与用途 |
| --- | --- |
| `~/projects/icloud-hme` | ubuntu 管理的源码，仅在 Mac 修改代码，在 O 机器拉取 |
| `/opt/icloud-hme/compose.yaml` | root 管理的当前部署模板，每次发布从源码复制 |
| `/opt/icloud-hme/.env` | root-only、600，首次生成独立随机管理员密码 |
| `/opt/icloud-hme/current.json` | root-only，当前提交、镜像与升级前备份位置 |
| `/opt/icloud-hme/data` | UID/GID 10001:10001、700，业务持久化数据 |
| `/opt/icloud-hme/backups` | root-only、700，每次切换前的配置和数据备份 |

镜像引用由部署脚本选择，无需填写 `ICLOUD_HME_IMAGE`。密码只保存在服务器，
不在脚本输出中展示，也不进入 Git、镜像或 GitHub Secrets。在自己的 SSH 终端查看：

```bash
sudo cat /opt/icloud-hme/.env
```

需要修改密码、端口等参数时使用 `sudoedit /opt/icloud-hme/.env`。参数说明见
[.env.example](.env.example)。含 `$` 或 `#` 的值用单引号包裹；不要把真实凭据提交到仓库。
修改运行配置后，使用当前镜像重新应用（不拉取或构建镜像）：

```bash
cd ~/projects/icloud-hme
./deploy/oracle/deploy.sh apply
```

## 访问和检查

在 Mac 建立 SSH 隧道，浏览器打开 `http://127.0.0.1:18081`，使用上述管理员密码登录：

```bash
ssh -N -o ExitOnForwardFailure=yes -L 127.0.0.1:18081:127.0.0.1:8081 oracle-a1
```

默认只发布 O 机器回环端口 8081，不新增公网入口，不改 OCI、UFW、iptables 或其他服务。
端口改动后同步调整隧道。SSH 隧道的 HTTP 访问保持 `ICLOUD_HME_SECURE_COOKIE=false`；
将来配置 HTTPS 反向代理时再改为 true，并核对 OCI 和主机防火墙。

```bash
./deploy/oracle/deploy.sh status
```

健康检查确认首页可访问；首次部署还应检查管理员登录和实际账号功能。
iCloud Cookie、App Password 等由用户通过管理界面添加，不进入发布流程。
容器使用非 root 身份、只读根文件系统和独立 data 挂载，日志按 10 MiB × 3 轮转。

## 回退与备份

```bash
./deploy/oracle/deploy.sh rollback
```

手动回退不拉代码、不构建，使用当前记录对应的上一版镜像和 Compose 模板，
保留当前密码及业务数据；回退前也会创建备份。首次部署没有上一版本。

自动失败恢复会停掉失败版本，将其数据移至该次备份中的 `data.failed`，再恢复
升级前 `data.tar.gz` 和配置，启动旧版本；首次部署失败只停止并移除失败容器，保留数据。
每次失败都保留诊断和备份。若 Docker 本身不可用等原因导致自动恢复失败，脚本会报错，
需要根据输出的备份路径手动处理。

手动镜像回退不等于数据回退；有不兼容的数据格式变化时，先停止服务、保留当前数据，
再恢复同版本的数据备份，并保留 UID/GID 10001:10001。业务恢复确认后再接受新写入。
不要执行 `docker compose down -v`，不要在更新流程里删除旧镜像或全局清理 Docker。
这些备份与服务位于同一台机器，仅用于升级恢复；机外备份另行配置。

## GitHub workflows 的用途

| 触发 | 行为 |
| --- | --- |
| 推送 main / Pull request | `ci.yml` 运行前端、Go、部署脚本与 Compose 检查；不发布镜像、不部署 O 机器 |
| 手动运行 Docker Image | CI 成功后发布 GHCR 双架构镜像；main 更新 latest，并发布 sha 标签 |
| 推送新的 v* 版本标签 | Release 先运行 CI，再发布多平台二进制和版本镜像；不覆盖 latest |

不必禁用整个 Actions。日常部署不依赖 GHCR；已有包和历史构建保留，正式版本发布时再用。
如手动运行普通 `docker compose` 使用预构建镜像，需要设置 `ICLOUD_HME_IMAGE` 为
实际存在的版本或 digest；脚本管理的服务器以 `current.json` 中的镜像为准。

Mac 使用 `origin` 指向自己的 fork，`upstream` 指向 `xiaozhou26/icloud-hme`；
从 upstream 获取、合并及测试改动都在 Mac 完成。O 机器只快进拉取 origin/main。

## 配置依据

- [Docker：构建镜像](https://docs.docker.com/reference/cli/docker/buildx/build/)
- [Docker：Compose 更新与健康检查](https://docs.docker.com/reference/cli/docker/compose/up/)
- [GitHub：工作流触发方式](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows)
