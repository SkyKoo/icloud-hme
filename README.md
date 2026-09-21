# iCloud Hide My Email 本地管理工具

[English](#english) | 中文

通过逆向 iCloud Web 接口和 IMAP 邮件协议，实现 Apple iCloud 隐藏邮箱别名的创建、列出和邮件收取功能。内置中文管理界面（React 单页应用，随二进制内嵌分发）。

## 功能特性

- ✅ **中文管理界面** — 浏览器访问 `http://localhost:8081` 即开即用
- ✅ **创建 HME 别名** — 自动生成 iCloud 隐藏邮箱地址
- ✅ **列出所有别名** — 查看账号下的所有 HME 别名
- ✅ **收取邮件** — 通过 IMAP 或 Web API 读取发到 HME 别名的邮件
- ✅ **双路径读信** — 邮件读取优先走 IMAP (App Password),无 App Password 时回退 Web API (Cookie)
- ✅ **多账号管理** — 支持多个 iCloud 账号并行管理
- ✅ **双认证模式** — Cookie (创建别名 + 读邮件回退) 和 App Password (IMAP 优先)
- ✅ **安全模型** — 单管理员会话、CSRF 校验、登录限流、响应脱敏

## 快速开始

### 1. 安装

#### 方式一：下载二进制发布版（推荐）

从 [GitHub Releases](https://github.com/SkyKoo/icloud-hme/releases) 下载对应平台的二进制文件：

| 平台 | 文件 |
|---|---|
| Linux x86_64 | `icloud-hme_linux_amd64` |
| Linux ARM64 | `icloud-hme_linux_arm64` |
| macOS Intel | `icloud-hme_darwin_amd64` |
| macOS Apple Silicon | `icloud-hme_darwin_arm64` |
| Windows x86_64 | `icloud-hme_windows_amd64.exe` |

```bash
# 示例：Linux 下直接运行（必须先设置管理员密码）
export ICLOUD_HME_ADMIN_PASSWORD='change-this-before-running-2026'
chmod +x icloud-hme_linux_amd64
./icloud-hme_linux_amd64
```

#### 方式二：Docker

可以使用仓库中的 Dockerfile 构建当前平台的镜像：

```bash
docker build -t icloud-hme:local .
export ICLOUD_HME_ADMIN_PASSWORD='replace-with-a-strong-password'
docker run -d --name icloud-hme \
  -p 127.0.0.1:8081:8081 \
  -e ICLOUD_HME_ADMIN_PASSWORD \
  -v icloud-hme-data:/app/data \
  icloud-hme:local -addr :8081 -data /app/data
```

浏览器访问 `http://localhost:8081`。账户数据保存在独立 Docker 卷中，管理员密码在运行时传入。
也可使用本 fork 按需发布的 `ghcr.io/skykoo/icloud-hme` 镜像，选择已发布的具体版本或 digest。

#### 方式三：源码编译（需要 Go 1.26+ 与 Node.js 22.12+ 双工具链）

```bash
# 前置要求: Go 1.26+、Node.js 22.12+
git clone https://github.com/SkyKoo/icloud-hme.git
cd icloud-hme

# 一键构建（安装前端依赖 → 前端测试 → 前端构建 → Go 测试 → 编译）
./build.sh

# 或者手动分步构建
npm --prefix web ci
npm --prefix web run build
go build -o icloud-hme .
```

### 2. 安全配置（必读）

管理界面与 API 均需要管理员登录，升级后所有 API 都必须先通过 `POST /api/auth/login` 获取会话：

| 环境变量 | 说明 | 默认 |
|---|---|---|
| `ICLOUD_HME_ADMIN_PASSWORD` | 管理员密码，**必填**，至少 8 字符 | 无（缺失时拒绝启动） |
| `ICLOUD_HME_SESSION_TTL` | 会话有效期 | `12h`（范围 `15m`–`168h`） |
| `ICLOUD_HME_SECURE_COOKIE` | 通过 TLS 反向代理部署时设为 `true` | `false` |
| `ICLOUD_HME_BASE_PATH` | 界面与 API 的部署路径,例如 `/hme` | `/` |

同一镜像或二进制可以在启动时通过 `ICLOUD_HME_BASE_PATH=/hme` 挂载到子路径,
无需重新构建。此时访问 `/hme/`,API 为 `/hme/api/...`,会话 Cookie 限于 `/hme/`。
反向代理必须保留 `/hme` 前缀传给应用,不要剥离路径。HTTPS 入口同时设置
`ICLOUD_HME_SECURE_COOKIE=true`。未设置子路径时,现有根路径访问方式不变。

> **Breaking Change（v0.3+）**：升级后未设置 `ICLOUD_HME_ADMIN_PASSWORD` 将拒绝启动；
> 原有匿名 API 调用将收到 `401 AUTH_REQUIRED`。管理员会话只存内存，进程重启即失效。

### 3. 配置账号

在程序 `data/` 目录下创建 `accounts.json`（参考仓库内 `accounts.json.template`）：

```json
{
  "accounts": {
    "acc_1": {
      "id": "acc_1",
      "name": "主号",
      "real_email": "owner@example.com",
      "icloud_email": "owner@icloud.com",
      "cookies": {
        "X-APPLE-WEBAUTH-TOKEN": "token_value",
        "X-APPLE-WEBAUTH-USER": "v=1:s=1:d=22789132008"
      },
      "host": "icloud.com",
      "proxy": "http://user:pass@host:port",
      "app_password": "xxxx-xxxx-xxxx-xxxx",
      "status": "active"
    }
  }
}
```

> **提示:** 也可以通过管理界面的「账号」页面动态添加账号，无需手动编辑 JSON 文件。`cookies`、`app_password`、`proxy` 都是可选的。

### 4. 启动服务

```bash
# 二进制方式（默认 data 目录）
export ICLOUD_HME_ADMIN_PASSWORD='your-strong-password'
./icloud-hme_linux_amd64

# 指定端口和数据目录
./icloud-hme_linux_amd64 -addr :9090 -data ./my_data

# 调试模式（启用请求日志）
./icloud-hme_linux_amd64 -debug

# 查看完整参数
./icloud-hme_linux_amd64 -h
```

服务默认监听 `:8081`。浏览器打开 `http://localhost:8081` 进入管理界面（账号 / 别名 / 收件箱）。完整 API 契约见 [API.md](API.md)。

## API 接口

> **认证**：除 `POST /api/auth/login` 与 `GET /api/auth/session` 外，所有 `/api` 接口都需要管理员会话 Cookie（`hme_session`）；非 GET/HEAD/OPTIONS 请求还需携带 `X-CSRF-Token` 请求头。完整契约与 curl 示例见 [API.md](API.md)。

### 核心接口

#### 创建 HME 别名

```bash
POST /api/create

# 请求体
{
  "account_id": "acc_1",      # 必填: 账号 ID
  "label": "注册某网站"        # 可选: 别名标签
}

# 响应
{
  "success": true,
  "data": {
    "email": "xyz123@icloud.com",
    "label": "注册某网站",
    "created_at": "2024-01-15T10:30:00Z",
    "account_id": "acc_1"
  }
}
```

#### 读取邮件

```bash
GET /api/inbox?account_id=acc_1&folder=all&alias=xyz123@icloud.com&limit=20&days=7

# 参数说明:
#   account_id - 必填: 账号 ID
#   alias      - 可选: 只读取发到该别名的邮件
#   folder     - 可选: all（默认，收件箱＋垃圾邮件）/ inbox / junk
#   limit      - 可选: 合并后返回邮件总数 (默认 20)
#   days       - 可选: 查找最近几天的邮件 (默认 7，两种读取方式均生效)

# 响应
{
  "success": true,
  "data": {
    "account_id": "acc_1",
    "alias": "xyz123@icloud.com",
    "count": 2,
    "method": "imap",
    "messages": [
      {
        "id": "1042",
        "from": "noreply@example.com",
        "to": "xyz123@icloud.com",
        "subject": "欢迎注册",
        "preview": "感谢您的注册...",
        "date": "2026-07-09T14:32:10+08:00"
      }
    ]
  }
}

# 读取方式 (自动选择):
#   method: "imap"    — 通过 App Password 认证 (优先)
#   method: "web_api" — 通过 Cookie 认证,无需 App Password (回退)
```

### 账号管理接口

#### 列出所有账号

```bash
GET /api/accounts

# 响应
{
  "success": true,
  "data": [
    {"id": "acc_1", "name": "主号"},
    {"id": "acc_2", "name": "副号"}
  ]
}
```

#### 添加账号

**简化版（cookies 可选）:**

```bash
POST /api/accounts

# 请求体
{
  "name": "新账号",
  "host": "icloud.com",           # 可选
  "proxy": "http://..."           # 可选
}

# 响应 - 状态为 pending,需登录
{
  "success": true,
  "data": {
    "id": "acc_xxx",
    "name": "新账号",
    "status": "pending"
  }
}
```

**完整版（带 Cookie）:**

```bash
POST /api/accounts

# 请求体
{
  "name": "新账号",
  "cookies": "{\"x-apple-session-token\":\"token_value\"}",  # JSON 或 Header 格式
  "host": "icloud.com",           # 可选
  "proxy": "http://..."           # 可选
}

# 响应
{
  "success": true,
  "data": {
    "id": "acc_3",
    "name": "新账号",
    "status": "active"
  }
}
```

#### 账号登录（获取 Cookie）

```bash
POST /api/accounts/:id/login

# 请求体
{
  "password": "用户的常规iCloud密码",  # 不是 App Password
  "otp_code": "123456"                  # 可选,2FA 验证码
}

# 响应
{
  "success": true,
  "data": {
    "id": "acc_1",
    "cookies": {
      "x-apple-session-token": "...",
      "X-APPLE-WEBAUTH-TOKEN": "..."
    }
  }
}
```

#### 删除账号

```bash
DELETE /api/accounts/:id

# 响应
{
  "success": true,
  "data": {"id": "acc_3"}
}
```

#### 设置 App Password

```bash
POST /api/accounts/:id/password

# 请求体
{
  "icloud_email": "your_email@icloud.com",
  "app_password": "xxxx-xxxx-xxxx-xxxx"
}

# 响应
{
  "success": true,
  "data": {
    "id": "acc_1",
    "icloud_email": "your_email@icloud.com"
  }
}
```

### 别名管理接口

#### 列出所有别名

```bash
GET /api/aliases?account_id=acc_1

# 响应
{
  "success": true,
  "data": {
    "account_id": "acc_1",
    "count": 15,
    "aliases": [
      {
        "email": "xyz123@icloud.com",
        "label": "注册某网站",
        "created_at": "2024-01-15T10:30:00Z"
      }
    ]
  }
}
```

#### 停用别名

```bash
POST /api/aliases/:id/deactivate

# 请求体
{
  "account_id": "acc_1"
}

# 响应
{
  "success": true,
  "data": {
    "anonymous_id": "abc123",
    "success": true
  }
}
```

#### 激活别名

```bash
POST /api/aliases/:id/reactivate

# 请求体
{
  "account_id": "acc_1"
}

# 响应
{
  "success": true,
  "data": {
    "anonymous_id": "abc123",
    "success": true
  }
}
```

#### 删除别名

```bash
DELETE /api/aliases/:id

# 请求体
{
  "account_id": "acc_1"
}

# 响应
{
  "success": true,
  "data": {
    "anonymous_id": "abc123"
  }
}
```

## 认证方式

### 方式一: Cookie 认证 (推荐,功能最完整)

Cookie 认证可实现所有功能:创建别名、读取邮件、管理别名。

**适用范围:**
- 创建/停用/激活/删除 HME 别名 ✅
- 读取邮件 (通过 iCloud Web API,无需 App Password) ✅

**获取 Cookie:**

1. 使用浏览器登录 [icloud.com](https://www.icloud.com) 或 [icloud.com.cn](https://www.icloud.com.cn) (国区)
2. 打开浏览器开发者工具 (F12)
3. 进入 Application → Cookies
4. 导出全部 Cookie 为 `{"key":"value"}` 格式的 JSON

**关键 Cookie (必需):**
- `X-APPLE-WEBAUTH-TOKEN` — 认证 token
- `X-APPLE-WEBAUTH-USER` — 含 dsid (`v=1:s=1:d=22789132008`)
- `X-APPLE-WEBAUTH-HSA-TRUST` — 设备信任 token
- `X-APPLE-DS-WEB-SESSION-TOKEN` — 会话 token

**注意:** 导出的 Cookie 值不要包含多余的引号或转义字符。

### 方式二: App Password 认证 (IMAP,优先读邮件)

App Password 用于 IMAP 读取邮件,是邮件读取的优先路径 (支持服务端按收件人搜索)。

**生成 App Password:**

1. 登录 [appleid.apple.com](https://appleid.apple.com)
2. 进入 "登录和安全" → "App 专用密码"
3. 生成新密码,用于此工具

### 邮件读取双路径

`GET /api/inbox` 自动选择读取方式:

1. **优先: IMAP (App Password)** — 设置了 App Password 时使用,支持服务端按收件人 (`TO`) 搜索
2. **回退: Web API (Cookie)** — 无 App Password 或 IMAP 失败时,通过 `mccgateway` 单封邮件接口读取,支持服务端按收件人过滤

响应中包含 `"method": "web_api"` 或 `"method": "imap"` 字段,标识实际使用的读取方式。

管理界面默认合并查询收件箱与垃圾邮件，可切换为单独查询；每封摘要的 `folder` 标识来源，
合并后按时间倒序排列并应用总条数限制。查询不移动邮件或修改其垃圾分类。
IMAP 自动识别垃圾邮件文件夹；Web API 使用 iCloud 的 `Junk` 文件夹。任一范围读取失败
会报错，可切换到单个文件夹排查。Web API 从邮件头读取发件人和收件人，批量获取摘要；
点击邮件主题可查看纯文本正文，保留已读状态，不下载附件或加载外部图片。
Web API 模式暂不支持删除；IMAP 模式保留原有详情和删除功能。


## 项目架构

```
icloud-hme/
├── main.go                 # 入口: 读取安全配置、加载账号、启动服务
├── web/                    # 前端工程 (React + TypeScript + Vite)
│   └── src/                #   管理界面源码
├── accounts.json           # 账号配置文件 (自动生成)
├── go.mod
└── internal/
    ├── account/
    │   ├── manager.go      # 多账号管理器 (持久化、客户端工厂)
    │   └── public.go       # 公开 DTO (Summary) 与输入校验
    ├── auth/
    │   ├── manager.go      # 管理员会话 + CSRF
    │   └── limiter.go      # 登录失败限流
    ├── hme/
    │   ├── client.go       # iCloud HME Web 客户端 (Cookie 认证)
    │   └── auth.go         # SRP 登录 (账号密码 + 2FA 获取 Cookie)
    ├── mail/
    │   ├── client.go       # IMAP 邮件客户端 (App Password 认证)
    │   └── web_client.go   # Web 邮件客户端 (Cookie 认证,无需 App Password)
    ├── server/
    │   ├── server.go       # 路由分组 (认证 + CSRF)
    │   ├── backend.go      # 业务接口与 Manager 适配器
    │   ├── auth.go         # 登录/会话/退出 handler 与中间件
    │   ├── account_handlers.go  # 账号管理 handler
    │   └── middleware.go   # 安全响应头、请求上限
    └── webui/
        └── embed.go        # 内嵌前端资源 + SPA fallback
```

### 核心模块

- **account.Manager**: 管理多个 iCloud 账号,负责配置持久化和客户端创建
- **hme.Client**: 封装 iCloud HME Web API,支持 Cookie 认证
- **hme.auth**: SRP 协议登录,支持账号密码 + 可选 2FA
- **mail.Client**: IMAP 邮件客户端 (App Password,优先读邮件)
- **mail.WebClient**: 通过 iCloud Web API (mccgateway) 读取邮件,无需 App Password
- **server.Server**: HTTP API 服务 + 管理界面静态资源

## 技术栈

- **Go 1.26+** / **Gin** — HTTP 框架
- **React 19 + TypeScript + Vite 8** — 管理界面
- **go-imap** — IMAP 协议实现
- **tls-client** — TLS 指纹模拟 (绕过 iCloud 反爬)

## 常见问题

### Q: 创建别名返回 401/403 错误?

**A:** Cookie 已过期，需要重新获取。iCloud Cookie 有效期通常为 24 小时。

### Q: 读取邮件返回超时?

**A:** 检查网络连接，确保可以访问 `imap.mail.me.com:993`。

### Q: 如何查看某个别名收到了哪些邮件?

**A:** 调用 `GET /api/inbox?account_id=acc_1&alias=your_alias@icloud.com`

### Q: 支持同时管理多个 iCloud 账号吗?

**A:** 支持，在 `accounts.json` 中配置多个账号即可，每个账号有独立的 `id`。

## 开发指南

### 本地开发

```bash
# 前端开发模式 (vite dev server, /api 代理到 :8081)
npm --prefix web ci
npm --prefix web run dev

# 后端开发模式
export ICLOUD_HME_ADMIN_PASSWORD='your-strong-password'
go run main.go -debug

# 前端检查 (lint + test + build)
npm --prefix web run check

# 完整构建 (含前端)
./build.sh

# 交叉编译
GOOS=linux GOARCH=amd64 go build -o icloud-hme .
GOOS=windows GOARCH=amd64 go build -o icloud-hme.exe .
```

### 发布

- 推送 `main` 或 Pull request：运行前端与 Go 检查，不自动发布镜像。
- 手动运行 `Docker Image`：先运行 CI，通过后发布 `linux/amd64` 和 `linux/arm64`
  镜像；标签为 `sha-<提交前12位>`，在 main 上手动运行还会更新 `latest`。
- 推送新的语义版本标签（例如 `v0.3.1`）：先运行 CI，通过后发布多平台二进制、
  版本镜像并创建 Release；不覆盖 `latest`。

版本标签选用尚未使用的版本，只执行 `git push origin <具体标签>`，不批量推送全部标签。

### 代码规范

- 代码注释使用中文
- 错误信息返回给用户时使用中文
- API 响应格式统一: `{success: bool, data: any, message: string}`

## 许可证

MIT License

---
## 社区

友情链接：[LINUX DO](https://linux.do)

## English

A local management tool for Apple iCloud Hide My Email (HME) aliases, supporting creation, listing, and email reading through reverse-engineered iCloud Web API and IMAP protocol. Ships with a built-in Chinese management UI (React SPA embedded in the single binary).

### Features

- Built-in management UI at `http://localhost:8081`
- Create HME aliases automatically
- List all aliases for an account
- Read emails sent to HME aliases via IMAP or Web API
- Manage multiple iCloud accounts
- Dual authentication: Cookie and App Password
- Security: single-admin session, CSRF checks, login rate limiting, redacted API responses

### Quick Start

#### Option 1: Binary (GitHub Releases)

Download the latest binary from [GitHub Releases](https://github.com/SkyKoo/icloud-hme/releases):

| Platform | File |
|---|---|
| Linux x86_64 | `icloud-hme_linux_amd64` |
| Linux ARM64 | `icloud-hme_linux_arm64` |
| macOS Intel | `icloud-hme_darwin_amd64` |
| macOS Apple Silicon | `icloud-hme_darwin_arm64` |
| Windows x86_64 | `icloud-hme_windows_amd64.exe` |

```bash
# Linux example (admin password is REQUIRED, min 8 chars)
export ICLOUD_HME_ADMIN_PASSWORD='change-this-before-running-2026'
chmod +x icloud-hme_linux_amd64
./icloud-hme_linux_amd64
```

#### Option 2: Docker

Build an image for the current platform using the repository's Dockerfile:

```bash
docker build -t icloud-hme:local .
export ICLOUD_HME_ADMIN_PASSWORD='replace-with-a-strong-password'
docker run -d --name icloud-hme \
  -p 127.0.0.1:8081:8081 \
  -e ICLOUD_HME_ADMIN_PASSWORD \
  -v icloud-hme-data:/app/data \
  icloud-hme:local -addr :8081 -data /app/data
```

Open `http://localhost:8081`. Account data lives in a separate Docker volume; credentials are supplied at runtime.
You can also use a published version or digest of `ghcr.io/skykoo/icloud-hme`.
Images are published on demand through manual workflow runs or version tags.

#### Option 3: Build from source (Go 1.26+ and Node.js 22.12+)

```bash
git clone https://github.com/SkyKoo/icloud-hme.git
cd icloud-hme

# One-shot build (frontend deps → frontend test → frontend build → Go test → binary)
./build.sh

# Or step by step
npm --prefix web ci
npm --prefix web run build
go build -o icloud-hme .
```

### Configuration

| Env var | Description | Default |
|---|---|---|
| `ICLOUD_HME_ADMIN_PASSWORD` | Admin password, **required**, min 8 chars | none (refuses to start) |
| `ICLOUD_HME_SESSION_TTL` | Session TTL | `12h` (range `15m`–`168h`) |
| `ICLOUD_HME_SECURE_COOKIE` | Set `true` when deployed behind TLS | `false` |

> **Breaking change (v0.3+)**: without `ICLOUD_HME_ADMIN_PASSWORD` the server refuses to start; all API endpoints now require login (`401 AUTH_REQUIRED`). Admin sessions are in-memory only and are lost on restart.

Create `data/accounts.json` (see `accounts.json.template`) and start the server (default port `:8081`). Open `http://localhost:8081` to use the management UI. Full API contract: [API.md](API.md).
