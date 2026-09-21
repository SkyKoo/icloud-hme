#!/usr/bin/env bash
# 以普通用户拉取代码，使用 sudo 构建镜像并切换应用容器。
set -euo pipefail

usage() {
  cat <<'HELP'
用法：./deploy/oracle/deploy.sh [deploy|apply|rollback|status|--help]
  deploy    快进拉取 origin/main，从该提交构建 ARM64 镜像，再部署（默认）
  apply     应用修改后的运行配置，使用当前镜像，不拉代码或构建
  rollback  切回最近一次部署前的镜像和 Compose 配置，保留当前业务数据
  status    查看当前部署提交、镜像和容器状态

在 O 机器上以 ubuntu 执行，不要 sudo 运行整个脚本。
首次部署自动创建 /opt/icloud-hme/.env 和随机密码；密码不会打印。
部署失败自动恢复旧版本及升级前数据；手动 rollback 不恢复业务数据。
每次远端变更前后，按服务器管理仓库要求登记 OPERATIONS_LOG.md。
HELP
}

action="${1:-deploy}"
case "$action" in
  -h|--help) usage; exit 0 ;;
  deploy|apply|rollback|status) ;;
  *) usage >&2; exit 2 ;;
esac
[[ $# -le 1 ]] || { usage >&2; exit 2; }
[[ $(uname -s) == Linux ]] || { echo '请在 O 机器（Linux）上运行。' >&2; exit 1; }
[[ $EUID -ne 0 ]] || { echo '请以普通用户运行；不要使用 sudo git。' >&2; exit 1; }
repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$repo"

# 持有仓库级锁，避免两个发布同时拉取、构建或切换容器。
exec 9>"$(git rev-parse --git-path icloud-hme-deploy.lock)"
flock -n 9 || { echo '已有部署正在执行。' >&2; exit 1; }
sudo -n true

if [[ "$action" == deploy ]]; then
  [[ $(uname -m) == aarch64 ]] || { echo '当前模板仅用于 ARM64 O 机器。' >&2; exit 1; }
  [[ $(git branch --show-current) == main ]] || { echo '请先切回 main 分支。' >&2; exit 1; }
  [[ -z $(git status --porcelain) ]] || { echo '源码有本地修改，请先处理；部署不会覆盖它们。' >&2; exit 1; }
  GIT_TERMINAL_PROMPT=0 git pull --ff-only origin main
  commit="$(git rev-parse HEAD)"
  [[ "$commit" == "$(git rev-parse origin/main)" ]] || { echo '本地 main 含未推送提交，请先处理。' >&2; exit 1; }
  image="icloud-hme:sha-${commit:0:12}-$(date -u +%Y%m%d%H%M%S)"
  echo "构建提交 $commit → $image（旧容器继续运行）"
  # 只打包已提交文件；忽略本机未跟踪文件，确保镜像与提交号对应。
  git archive --format=tar "$commit" | sudo -n docker build \
    --platform linux/arm64 --label "org.opencontainers.image.revision=$commit" \
    -t "$image" -
  sudo -n python3 "$repo/deploy/oracle/deploy.py" deploy \
    --image "$image" --commit "$commit" --template "$repo/deploy/oracle/compose.yaml"
else
  sudo -n python3 "$repo/deploy/oracle/deploy.py" "$action"
fi
