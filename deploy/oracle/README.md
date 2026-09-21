# Mac 开发、GHCR 发布与 O 机器部署

配置核对日期：2026-09-21。本目录是 O 机器（`oracle-a1`，Linux ARM64）的部署模板；
模板提交到 GitHub、工作流运行成功后才会发布镜像，文件本身不会触发服务器部署。

## 开发与发布

Mac 本地仓库使用 `origin` 管理自己的 fork，使用 `upstream` 跟踪原作者。
当前约定如下；新克隆尚未设置 upstream 时再执行添加命令：

```bash
git remote add upstream https://github.com/xiaozhou26/icloud-hme.git
git fetch --no-tags upstream
git remote -v
```

`fetch` 只更新远端跟踪分支；合并上游改动应在 Mac 的开发分支完成，先测试再推送到
`origin`。O 机器不负责合并源码。上游引用见
[xiaozhou26/icloud-hme](https://github.com/xiaozhou26/icloud-hme)。

| 触发方式 | 执行顺序与产物 |
| --- | --- |
| Pull request | `ci.yml`：前端 lint、测试、构建；Go race 测试、vet、构建；Compose 配置校验 |
| 推送 main / 手动运行 Docker Image | 调用同一提交的 `ci.yml`，全部成功后构建 amd64 + arm64 镜像，发布 `sha-<提交前12位>`；main 同时更新 `latest` |
| 推送语义版本标签，例如 v0.3.1 | 调用同一套 CI，成功后构建二进制和双架构镜像；发布 `0.3.1`、`0.3` 等标签并创建 Release |

CI 不另外监听 main 的 push，避免一次提交重复跑两套测试。
`latest` 由 main 的镜像工作流维护，版本发布不覆盖它；生产部署选择已验证的具体
版本或镜像 digest。标签本身可以被覆盖，digest 才能唯一锁定镜像内容。
推送版本标签时使用尚未存在的版本，并只推送这个标签，不批量推送所有本地标签。

首次使用 fork 时，在 GitHub 的 Actions 页面启用工作流；仓库策略需允许所用的
Actions。发布任务已经声明 `contents: read`、`packages: write`，使用 GitHub 自带的
`GITHUB_TOKEN`，不需要把个人 Token 或 O 机器 SSH 私钥写进 Actions Secrets。
GitHub Release 任务单独获得 `contents: write`。

本 fork 的预期镜像名称为 `ghcr.io/skykoo/icloud-hme`。首次成功发布后，在 GitHub
Packages 确认包与仓库关联、Actions 的写入权限以及镜像可见性：

- 公开镜像可匿名拉取；公开代码仓库不代表镜像自动公开。改为公开前检查镜像内容，
  GitHub 当前不支持把公开包改回私有。
- 私有镜像需要有包访问权限、含 `read:packages` 的 PAT classic。
  在 O 机器运行 `sudo docker login ghcr.io -u <GitHub用户名>` 并在交互提示中输入；
  不把 Token 放进命令参数、仓库或聊天。后续以同样的 sudo 身份拉取。

## O 机器首次部署

以下命令是待执行的部署步骤。执行前按 O 机器管理仓库的规则登记运维计划；
安装完成后记录镜像 digest、验证结果和回退位置。这里只使用已有 Docker/Compose。

在 Mac 的仓库根目录上传模板：

```bash
ssh oracle-a1 'install -d -m 700 ~/.local/state/icloud-hme-setup'
scp deploy/oracle/compose.yaml deploy/oracle/.env.example oracle-a1:~/.local/state/icloud-hme-setup/
ssh oracle-a1
```

在 O 机器准备目录和配置。容器使用 UID/GID `10001:10001`，不需要在宿主机创建
同名登录用户；仅数据目录交给该 UID，配置和备份仍由 root 管理：

```bash
sudo install -d -o root -g root -m 755 /opt/icloud-hme
sudo install -o root -g root -m 644 ~/.local/state/icloud-hme-setup/compose.yaml /opt/icloud-hme/compose.yaml
sudo test -e /opt/icloud-hme/.env || sudo install -o root -g root -m 600 ~/.local/state/icloud-hme-setup/.env.example /opt/icloud-hme/.env
sudo install -d -o 10001 -g 10001 -m 700 /opt/icloud-hme/data
sudo install -d -o root -g root -m 700 /opt/icloud-hme/backups
sudoedit /opt/icloud-hme/.env
```

必须填写：

- `ICLOUD_HME_IMAGE`：GHCR 中已存在的具体版本或 digest，例如
  `ghcr.io/skykoo/icloud-hme@sha256:<实际digest>`。模板不提供 latest 默认值。
- `ICLOUD_HME_ADMIN_PASSWORD`：独立随机密码，至少 8 字符。
  含 `$`、`#` 的值用单引号包裹，以免 .env 插值或注释处理改变密码。

可选参数见 [.env.example](.env.example)。默认端口为 8081；如与其他服务冲突，修改
`ICLOUD_HME_BIND_PORT`，SSH 隧道的远端端口也要相应修改。
HTTP 经 SSH 隧道访问时保持 `ICLOUD_HME_SECURE_COOKIE=false`；
将来通过 HTTPS 反向代理访问时改为 true，并单独核对 OCI 与主机防火墙规则。

启动及验收：

```bash
cd /opt/icloud-hme
sudo docker compose config --quiet
sudo docker compose pull
sudo docker compose up -d --wait --wait-timeout 90
sudo docker compose ps
curl -fsS -o /dev/null http://127.0.0.1:8081/
```

`config --quiet` 只验证，不打印包含管理员密码的完整配置。Compose 要求事先创建
data 目录；目录权限错误应按 UID/GID 修正，不使用 chmod 777。
容器根文件系统只读，只有 data 和临时目录可写；日志按 10 MiB × 3 轮转。
健康检查确认 HTTP 首页可用，首次部署还需手动验证管理员登录和实际账号功能。

从 Mac 建立隧道，在浏览器打开 http://127.0.0.1:18081：

```bash
ssh -N -o ExitOnForwardFailure=yes -L 127.0.0.1:18081:127.0.0.1:8081 oracle-a1
```

模板只将容器端口发布到 O 机器的 127.0.0.1。此方式不需要新增公网 8081 放行。
iCloud 账号、Cookie 和 App Password 通过管理界面配置，保存在受保护的 data 目录中，
不进入 Git、镜像、构建上下文或 Actions Secrets。

## 升级与回退

每次升级先记录运维计划。在 O 机器的 `/opt/icloud-hme` 目录按顺序执行：

1. 保存当前配置和旧镜像引用：

   ```bash
   backup_dir="/opt/icloud-hme/backups/$(date +%Y%m%d-%H%M%S)"
   sudo install -d -m 700 "$backup_dir"
   sudo cp -p compose.yaml .env "$backup_dir/"
   ```

2. 通过 `sudoedit .env` 把镜像改成待升级的版本或 digest，执行
   `sudo docker compose config --quiet` 和 `sudo docker compose pull`。
   校验或拉取失败就停止升级，此时旧容器仍在运行。

3. 用 `sudo docker compose stop icloud-hme` 短暂停止该服务，再创建一致的数据备份：

   ```bash
   sudo tar -C /opt/icloud-hme -czf "$backup_dir/data.tar.gz" data
   ```

   备份失败时先恢复原 .env 并启动原镜像，不继续升级。

4. 执行 `sudo docker compose up -d --wait --wait-timeout 90`，检查
   `sudo docker compose ps`、管理员登录和数据；重启后需要重新登录。
   此单容器方案存在短暂中断，不是滚动升级。

若新版本异常，保存失败状态，将镜像引用切回备份记录的旧 digest，重新启动并验证。
若涉及不兼容的数据格式变更，先停服务并将当前 data 目录改名保留，再恢复同版本的数据
备份，保持 UID/GID 为 10001:10001。镜像回退不等于数据回退。

不要在升级过程中执行 `docker compose down -v` 或清理旧镜像。
备份与服务器在同一台机器上，仅用于升级回退；机外备份需另行设置。

## 配置依据

- [GitHub：复用工作流](https://docs.github.com/en/actions/how-tos/reuse-automations/reuse-workflows)
- [GitHub：GHCR 认证与镜像拉取](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry)
- [GitHub：包可见性](https://docs.github.com/en/packages/learn-github-packages/configuring-a-packages-access-control-and-visibility)
- [Docker：多架构构建](https://docs.docker.com/build/building/multi-platform/)
