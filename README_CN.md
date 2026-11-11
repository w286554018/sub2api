<div align="center">

<img src="assets/logo.svg" alt="Sub2API Logo" width="128" />

# Sub2API

[![Go](https://img.shields.io/badge/Go-1.27.3-00ADD8.svg)](https://golang.org/)
[![Vue](https://img.shields.io/badge/Vue-3.4+-4FC08D.svg)](https://vuejs.org/)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-15+-336791.svg)](https://www.postgresql.org/)
[![Redis](https://img.shields.io/badge/Redis-7+-DC382D.svg)](https://redis.io/)
[![Docker](https://img.shields.io/badge/Docker-Ready-2496ED.svg)](https://www.docker.com/)

<a href="https://trendshift.io/repositories/21823" target="_blank"><img src="https://trendshift.io/api/badge/repositories/21823" alt="Wei-Shaw%2Fsub2api | Trendshift" width="250" height="55"/></a>

**AI API 网关平台 - 订阅配额分发管理**

[English](README.md) | 中文 | [日本語](README_JA.md)

</div>


## ⚠️ 重要提醒

使用本项目前，请务必仔细阅读以下内容：

- **🚨 服务条款风险**：使用本项目可能违反 Anthropic 等上游服务商的服务条款。请在使用前仔细阅读相关服务商的用户协议，由此产生的一切风险由用户自行承担。
- **⚖️ 合规使用**：请在符合您所在国家或地区法律法规的前提下使用本项目，严禁将其用于任何违法违规用途。
- **📖 免责声明**：本项目仅供技术学习与研究使用，作者不对因使用本项目导致的账户封禁、服务中断、数据丢失或其他任何直接或间接损失承担责任。
- **🚫 无商业授权**：本项目从未授权任何个人或组织基于本项目开展任何形式的商业化运营。任何以本项目名义或基于本项目从事的商业行为均与本项目及其开发者无关，由此产生的一切纠纷、损失和法律责任由行为主体自行承担。

## 项目概述

Sub2API 是一个 AI API 网关平台，用于分发和管理 AI 产品订阅的 API 配额。用户通过平台生成的 API Key 调用上游 AI 服务，平台负责鉴权、计费、负载均衡和请求转发。

## 核心功能

- **多账号管理** - 支持多种上游账号类型（OAuth、API Key）
- **API Key 分发** - 为用户生成和管理 API Key
- **精确计费** - Token 级别的用量追踪和成本计算
- **智能调度** - 智能账号选择，支持粘性会话
- **账号性能监控** - 可选的指标阈值、独立暂停、人工恢复与区域传播控制（[说明](docs/account-performance-monitor.md)）
- **并发控制** - 用户级和账号级并发限制
- **速率限制** - 可配置的请求和 Token 速率限制
- **内置支付系统** - 支持 EasyPay 易支付、支付宝官方、微信官方、Stripe，用户自助充值，无需独立部署支付服务（[配置指南](docs/PAYMENT_CN.md)）
- **管理后台** - Web 界面进行监控和管理
- **外部系统集成** - 支持通过 iframe 嵌入外部系统（如工单等），扩展管理后台功能

---

## 生态项目

围绕 Sub2API 的社区扩展与集成项目：

| 项目 | 说明 | 功能 |
|------|------|------|
| ~~[Sub2ApiPay](https://github.com/touwaeriol/sub2apipay)~~ | ~~自助支付系统~~ | **已内置** — 支付功能已集成到 Sub2API 中，无需独立部署。详见 [支付配置指南](docs/PAYMENT_CN.md) |
| [sub2api-mobile](https://github.com/ckken/sub2api-mobile) | 移动端管理控制台 | 跨平台应用（iOS/Android/Web），支持用户管理、账号管理、监控看板、多后端切换；基于 Expo + React Native 构建 |

## 技术栈

| 组件 | 技术 |
|------|------|
| 后端 | Go 1.27.3, Gin, Ent |
| 前端 | Vue 3.4+, Vite 5+, TailwindCSS |
| 数据库 | PostgreSQL 15+ |
| 缓存/队列 | Redis 7+ |

---

## Nginx 反向代理注意事项

通过 Nginx 反向代理 Sub2API（或 CRS 服务）并搭配 Codex CLI 使用时，需要在 Nginx 配置的 `http` 块中添加：

```nginx
underscores_in_headers on;
```

Nginx 默认会丢弃名称中含下划线的请求头（如 `session_id`），这会导致多账号环境下的粘性会话功能失效。

## Codex Fast/Flex 策略说明

管理员后台的 `系统设置 -> 网关服务 -> OpenAI Fast/Flex 策略` 只负责处理请求体中的 `service_tier`，不会修改 Codex 客户端的模型目录，也不会让 Codex UI 自动出现 Speed 或 `/fast` 选项。

策略支持以下处理方式：

- `pass`：保留客户端传入的 `service_tier`；`fast` 会规范为上游使用的 `priority`。
- `filter`：移除 `service_tier`，按普通优先级请求。
- `block`：拒绝匹配的 Fast/Flex 请求。
- `force_priority`：将匹配请求强制设置为 `priority`，会按 Priority/Fast 价格计费。为避免升级后改变既有规则语义，`all` 只匹配显式存在的 tier；如需让省略 `service_tier` 的 OpenAI 请求也强制升级，必须新增 `service_tier=missing + force_priority` 规则。非 OpenAI 平台不会执行缺省 tier 注入。这种方式可以让请求实际使用 Fast，但 Codex UI 仍可能不显示 Fast 状态。

Codex 的 Fast 入口由客户端模型目录驱动。只有当前模型目录声明了 `additional_speed_tiers: ["fast"]` 和对应的 `service_tiers`，Codex 才会显示 `/fast`。通过 API Key 或自定义模型提供商连接 Sub2API 时，如果模型目录没有这些字段，即使后台配置了 `force_priority`，重启 Codex 后也不会出现 Speed 选项。

客户端可在 `~/.codex/config.toml` 中直接指定默认请求级别：

```toml
service_tier = "fast"

[features]
fast_mode = true
```

其中 `service_tier = "fast"` 会让请求携带 Fast 设置；`features.fast_mode` 只启用客户端 Fast 功能。模型目录没有声明 Fast 能力时，`/fast` 仍可能不显示。可通过 Sub2API 使用记录确认最终 `service_tier` 是否为 `priority`。

---

## 部署方式

### 方式一：脚本安装（推荐）

一键安装脚本，自动从 GitHub Releases 下载预编译的二进制文件。

#### 前置条件

- Linux 服务器（amd64 或 arm64）
- PostgreSQL 15+（已安装并运行）
- Redis 7+（已安装并运行）
- Root 权限

#### 安装步骤

```bash
curl -sSL https://raw.githubusercontent.com/Wei-Shaw/sub2api/main/deploy/install.sh | sudo bash
```

脚本会自动：
1. 检测系统架构
2. 下载最新版本
3. 安装二进制文件到 `/opt/sub2api`
4. 创建 systemd 服务
5. 配置系统用户和权限

#### 安装后配置

```bash
# 1. 启动服务
sudo systemctl start sub2api

# 2. 设置开机自启
sudo systemctl enable sub2api

# 3. 在浏览器中打开设置向导
# http://你的服务器IP:8080
```

设置向导将引导你完成：
- 数据库配置
- Redis 配置
- 管理员账号创建

#### 升级

可以直接在 **管理后台** 左上角点击 **检测更新** 按钮进行在线升级。

网页升级功能支持：
- 自动检测新版本
- 一键下载并应用更新
- 支持回滚

#### 常用命令

```bash
# 查看状态
sudo systemctl status sub2api

# 查看日志
sudo journalctl -u sub2api -f

# 重启服务
sudo systemctl restart sub2api

# 卸载
curl -sSL https://raw.githubusercontent.com/Wei-Shaw/sub2api/main/deploy/install.sh | sudo bash -s -- uninstall -y
```

---

### 方式二：Docker Compose（推荐）

使用 Docker Compose 部署，包含 PostgreSQL 和 Redis 容器。

#### 前置条件

- Docker 20.10+
- Docker Compose v2+

#### 快速开始（一键部署）

使用自动化部署脚本快速搭建：

```bash
# 创建部署目录
mkdir -p sub2api-deploy && cd sub2api-deploy

# 下载并运行部署准备脚本
curl -sSL https://raw.githubusercontent.com/Wei-Shaw/sub2api/main/deploy/docker-deploy.sh | bash

# 启动服务
docker compose up -d

# 查看日志
docker compose logs -f sub2api
```

**脚本功能：**
- 下载 `docker-compose.local.yml`（本地保存为 `docker-compose.yml`）和 `.env.example`
- 自动生成安全凭证（JWT_SECRET、TOTP_ENCRYPTION_KEY、POSTGRES_PASSWORD）
- 创建 `.env` 文件并填充自动生成的密钥
- 创建数据目录（使用本地目录，便于备份和迁移）
- 显示生成的凭证供你记录

#### 手动部署

如果你希望手动配置：

```bash
# 1. 克隆仓库
git clone https://github.com/Wei-Shaw/sub2api.git
cd sub2api/deploy

# 2. 复制环境配置文件
cp .env.example .env
chmod 600 .env

# 3. 编辑配置（生成安全密码）
nano .env
```

**`.env` 必须配置项：**

```bash
# PostgreSQL 密码（必需）
POSTGRES_PASSWORD=your_secure_password_here

# JWT 密钥（推荐 - 重启后保持用户登录状态）
JWT_SECRET=your_jwt_secret_here

# TOTP 加密密钥（推荐 - 重启后保留双因素认证）
TOTP_ENCRYPTION_KEY=your_totp_key_here

# 可选：管理员账号
# 留空则首次启动时自动生成随机邮箱（登录用户名）和密码，并输出到日志。
# 请勿使用 admin@example.com 这类可被猜中的值，它们是暴力破解的目标。
ADMIN_EMAIL=
ADMIN_PASSWORD=

# 可选：自定义端口
SERVER_PORT=8080
```

**生成安全密钥：**
```bash
# 生成 JWT_SECRET
openssl rand -hex 32

# 生成 TOTP_ENCRYPTION_KEY
openssl rand -hex 32

# 生成 POSTGRES_PASSWORD
openssl rand -hex 32
```

```bash
# 4. 创建数据目录（本地版）
mkdir -p data postgres_data redis_data

# 5. 启动所有服务
# 选项 A：本地目录版（推荐 - 易于迁移）
docker compose -f docker-compose.local.yml up -d

# 选项 B：命名卷版（简单设置）
docker compose up -d

# 6. 查看状态
docker compose -f docker-compose.local.yml ps

# 7. 查看日志
docker compose -f docker-compose.local.yml logs -f sub2api
```

#### 部署版本对比

| 版本 | 数据存储 | 迁移便利性 | 适用场景 |
|------|---------|-----------|---------|
| **docker-compose.local.yml** | 本地目录 | ✅ 简单（打包整个目录） | 生产环境、频繁备份 |
| **docker-compose.yml** | 命名卷 | ⚠️ 需要 docker 命令 | 简单设置 |

**推荐：** 使用 `docker-compose.local.yml`（脚本部署）以便更轻松地管理数据。

#### 启用“数据管理”功能（datamanagementd）

如需启用管理后台“数据管理”，需要额外部署宿主机数据管理进程 `datamanagementd`。

关键点：

- 主进程固定探测：`/tmp/sub2api-datamanagement.sock`
- 只有该 Socket 可连通时，数据管理功能才会开启
- Docker 场景需将宿主机 Socket 挂载到容器同路径

详细部署步骤见：`deploy/DATAMANAGEMENTD_CN.md`

#### 访问

在浏览器中打开 `http://你的服务器IP:8080`

如果管理员邮箱（登录用户名）或密码是自动生成的，在日志中查找：
```bash
docker compose -f docker-compose.local.yml logs sub2api | grep "Generated admin"
```

#### 升级

```bash
# 拉取最新镜像并重建容器
docker compose -f docker-compose.local.yml pull
docker compose -f docker-compose.local.yml up -d
```

#### 轻松迁移（本地目录版）

使用 `docker-compose.local.yml` 时，可以轻松迁移到新服务器：

```bash
# 源服务器
docker compose -f docker-compose.local.yml down
cd ..
tar czf sub2api-complete.tar.gz sub2api-deploy/

# 传输到新服务器
scp sub2api-complete.tar.gz user@new-server:/path/

# 新服务器
tar xzf sub2api-complete.tar.gz
cd sub2api-deploy/
docker compose -f docker-compose.local.yml up -d
```

#### 常用命令

```bash
# 停止所有服务
docker compose -f docker-compose.local.yml down

# 重启
docker compose -f docker-compose.local.yml restart

# 查看所有日志
docker compose -f docker-compose.local.yml logs -f

# 删除所有数据（谨慎！）
docker compose -f docker-compose.local.yml down
rm -rf data/ postgres_data/ redis_data/
```

---

### 方式三：Apple container（macOS）

Apple 芯片 Mac 在 macOS 26 上可使用 Apple `container` 1.1.0 或更高版本运行完整的 Sub2API、PostgreSQL 和 Redis：

```bash
git clone https://github.com/Wei-Shaw/sub2api.git
cd sub2api/deploy
./apple-container.sh init
./apple-container.sh up
./apple-container.sh status
```

该方式面向本地开发和人工运维，不提供持续重启监管；生产部署仍推荐 Docker Compose。生命周期命令、持久化、升级和运行时限制见 [deploy/APPLE_CONTAINER.md](deploy/APPLE_CONTAINER.md)。

---

### 方式四：源码编译

从源码编译安装，适合开发或定制需求。

#### 前置条件

- Go 1.21+
- Node.js 18+
- PostgreSQL 15+
- Redis 7+

#### 编译步骤

```bash
# 1. 克隆仓库
git clone https://github.com/Wei-Shaw/sub2api.git
cd sub2api

# 2. 安装 pnpm（如果还没有安装）
npm install -g pnpm

# 3. 编译前端
cd frontend
pnpm install
pnpm run build
# 构建产物输出到 ../backend/internal/web/dist/

# 4. 编译后端（嵌入前端）
cd ../backend
VERSION="$(./scripts/resolve-version.sh)"
go build -tags embed -ldflags="-X main.Version=${VERSION}" -o sub2api ./cmd/server

# 5. 创建配置文件
cp ../deploy/config.example.yaml ./config.yaml

# 6. 编辑配置
nano config.yaml
```

> **注意：** `-tags embed` 参数会将前端嵌入到二进制文件中。不使用此参数编译的程序将不包含前端界面。

**`config.yaml` 关键配置：**

```yaml
server:
  host: "0.0.0.0"
  port: 8080
  mode: "release"

database:
  host: "localhost"
  port: 5432
  user: "postgres"
  password: "your_password"
  dbname: "sub2api"

redis:
  host: "localhost"
  port: 6379
  password: ""

jwt:
  secret: "change-this-to-a-secure-random-string"
  expire_hour: 24

default:
  user_concurrency: 5
  user_balance: 0
  api_key_prefix: "sk-"
  rate_multiplier: 1.0
```

`config.yaml` 还支持以下安全相关配置：

- `cors.allowed_origins` 配置 CORS 白名单
- `security.url_allowlist` 配置上游/价格数据/CRS 主机白名单
- `security.url_allowlist.enabled` 可关闭 URL 校验（慎用）
- `security.url_allowlist.allow_insecure_http` 关闭校验时允许 HTTP URL
- `security.url_allowlist.allow_private_hosts` 允许私有/本地 IP 地址
- `security.response_headers.enabled` 可启用可配置响应头过滤（关闭时使用默认白名单）
- `security.csp` 配置 Content-Security-Policy
- `billing.circuit_breaker` 计费异常时 fail-closed
- `security.trust_forwarded_ip_for_api_key_acl` 控制旧版原始转发头接管（为升级兼容默认开启）；关闭后严格使用 `server.trusted_proxies`，其中只应填写直接连接 Sub2API 的精确代理 CIDR
- `security.forwarded_client_ip_headers` 最多配置 16 个第三方 CDN 客户端 IP 请求头；仅在旧版接管开启时按顺序优先于内置请求头解析
- `turnstile.required` 在 release 模式强制启用 Turnstile

自定义客户端 IP 请求头可通过 YAML 配置，也可使用逗号分隔的环境变量：

```bash
SECURITY_FORWARDED_CLIENT_IP_HEADERS=True-Client-IP,X-CDN-Client-IP
```

请求头名称会经过合法性校验、规范化和大小写无关去重。管理员可在安全设置中动态更新列表，无需重启；新安装会持久化 YAML/环境变量默认值，旧安装缺少数据库字段时会自动回填。关闭旧版接管后，自定义头和内置原始转发头均被忽略，只使用 `server.trusted_proxies`。开启接管时必须限制源站仅允许 CDN/代理访问，并确保边缘代理覆盖所有受信客户端 IP 请求头。完整迁移规则和信任边界见 [`deploy/EDGE_SECURITY.md`](deploy/EDGE_SECURITY.md)。

**网关防御纵深建议（重点）**

- `gateway.upstream_response_read_max_bytes`：限制非流式上游响应读取大小（默认 `8MB`），用于防止异常响应导致内存放大。
- `gateway.proxy_probe_response_read_max_bytes`：限制代理探测响应读取大小（默认 `1MB`）。
- `gateway.gemini_debug_response_headers`：默认 `false`，仅在排障时短时开启，避免高频请求日志开销。
- `/auth/register`、`/auth/login`、`/auth/login/2fa`、`/auth/send-verify-code` 已提供服务端兜底限流（Redis 故障时 fail-close）。
- 推荐将 WAF/CDN 作为第一层防护，服务端限流与响应读取上限作为第二层兜底；两层同时保留，避免旁路流量与误配置风险。

**⚠️ 安全警告：HTTP URL 配置**

当 `security.url_allowlist.enabled=false` 时，系统仅执行最小 URL 校验，且**默认允许 HTTP URL**（开发友好模式，Docker Compose 部署的默认值一致）。生产环境建议显式收紧为仅允许 HTTPS：

```yaml
security:
  url_allowlist:
    enabled: false                # 禁用白名单检查
    allow_insecure_http: false    # 仅允许 HTTPS（生产环境推荐）
```

**或通过环境变量：**

```bash
SECURITY_URL_ALLOWLIST_ENABLED=false
SECURITY_URL_ALLOWLIST_ALLOW_INSECURE_HTTP=false
```

**允许 HTTP 的风险：**
- API 密钥和数据以**明文传输**（可被截获）
- 易受**中间人攻击 (MITM)**
- **不适合生产环境**

**适用场景：**
- ✅ 开发/测试环境的本地服务器（http://localhost）
- ✅ 内网可信端点
- ✅ 获取 HTTPS 前测试账号连通性
- ❌ 生产环境（仅使用 HTTPS）

**设置 `allow_insecure_http: false` 后，HTTP URL 会返回如下错误：**
```
Invalid base URL: invalid url scheme: http
```

如关闭 URL 校验或响应头过滤，请加强网络层防护：
- 出站访问白名单限制上游域名/IP
- 阻断私网/回环/链路本地地址
- 强制仅允许 TLS 出站
- 在反向代理层移除敏感响应头

#### ⚠️ 重要：创建管理员账号

初始管理员账号**只能通过 setup 向导创建**（首次启动时访问 `http://<host>:8080`）。`config.yaml` 中的 `default.admin_email` / `default.admin_password` 字段**不会被用来创建管理员**——它们只是出于历史原因保留在模板里。

由于上面第 5 步预先创建了 `config.yaml`，**setup 向导在首次启动时会被跳过**：服务检测到 config 已存在，会直接进入正常模式，此时 `users` 表为空，首次登录会返回 `invalid email or password`。

**创建管理员的两种方式：**

1. **推荐——让向导自动生成 `config.yaml`：** 跳过上面的第 5 步（不要执行 `cp`）。直接运行 `./sub2api`，访问 `http://localhost:8080`，向导会引导你完成数据库、Redis 和管理员账号配置，并自动写出 `config.yaml`。

2. **如果你已经创建了 `config.yaml`：** 首次启动前先把它临时移走以触发向导，完成后再恢复：
   ```bash
   mv config.yaml config.yaml.bak
   ./sub2api        # 向导在 http://localhost:8080 启动，并生成新的 config.yaml
   # 向导完成后 Ctrl+C 停服，再恢复你的配置：
   mv config.yaml.bak config.yaml
   ./sub2api        # 重启进入正常模式，用刚创建的管理员登录
   ```

```bash
# 6. 运行应用
./sub2api
```

#### HTTP/2 (h2c) 与 HTTP/1.1 回退

后端明文端口默认支持 h2c，并保留 HTTP/1.1 回退用于 WebSocket 与旧客户端。浏览器通常不支持 h2c，性能收益主要在反向代理或内网链路。

**反向代理示例（Caddy）：**

```caddyfile
transport http {
	versions h2c h1
}
```

**验证：**

```bash
# h2c prior knowledge
curl --http2-prior-knowledge -I http://localhost:8080/health
# HTTP/1.1 回退
curl --http1.1 -I http://localhost:8080/health
# WebSocket 回退验证（需管理员 token）
websocat -H="Sec-WebSocket-Protocol: sub2api-admin, jwt.<ADMIN_TOKEN>" ws://localhost:8080/api/v1/admin/ops/ws/qps
```

#### 开发模式

```bash
# 后端（支持热重载）
cd backend
go run ./cmd/server

# 前端（支持热重载）
cd frontend
pnpm run dev
```

#### 代码生成

修改 `backend/ent/schema` 后，需要重新生成 Ent + Wire：

```bash
cd backend
go generate ./ent
go generate ./cmd/server
```

---

## OpenAI 图片模型

支持 `gpt-image-2.6-flare`、`gpt-image-2.6-sunburst`，以及 `gpt-image-2.5-flare`、`gpt-image-2.5-sunburst` 和 2.5 的 `2026-09-08` 日期快照，可通过 `/v1/images/generations`、`/v1/images/edits` 调用。保留显式 `complexity`（`low` / `medium` / `high`）；省略时不注入默认值。`quality`、自定义尺寸和图片 usage 明细保持透传。

OAuth / Setup Token 的 1.5、2、2.5 和 2.6 图片请求使用 Codex 原生 Images 端点。旧模型遇到端点 404 / 405 时仍可回退到 Responses；2.6 不回退，避免桥接层忽略图片模型或注入不支持的 `complexity`。其余模型继续通过 Responses 的 `image_generation` 工具调用，默认文本主控为 `gpt-5.6-luna`，可设置 `SUB2API_IMAGES_MAIN_MODEL` 切换。该配置仅影响 Responses 桥接。

原生端点保留 JSON / multipart 编辑的 `markup`（图片 URL 或上传文件）、GIF 帧参数及 SVG author 参数；`response_format=url` 使用上游实际输出格式的 MIME。GIF / SVG 可用性和参数生效情况由上游账号权限及端点决定，转发支持不代表上游一定输出该格式。若编辑端点对流式请求返回完整 JSON，网关会转换为完成事件并保留 usage。

升级后，无模型限制的账号自动支持新模型。已有显式账号映射或分组白名单需要主动加入两个 2.6 模型；升级不会扩大已保存的模型权限。2.6 内置官方文本输入、图片输入、缓存及图片输出 token 费率，远端价格表缺失或动态价格服务不可用时仍可兜底；实际按张或按 token 计费继续由既有分组/渠道配置决定。无需数据库迁移或新增环境变量。

## 简易模式

简易模式适合个人开发者或内部团队快速使用，不依赖完整 SaaS 功能。

- 启用方式：设置环境变量 `RUN_MODE=simple`
- 默认每次启动会补齐默认分组。设置 `SIMPLE_MODE_AUTO_CREATE_DEFAULT_GROUPS=false`（或 YAML `simple_mode.auto_create_default_groups: false`）可自行管理分组。默认值为 `true`；关闭后不删除已有分组，不改变运行时自动绑定或管理员并发设置。
- 功能差异：隐藏 SaaS 相关功能，跳过计费流程
- 可选密钥窗口：设置 `SIMPLE_MODE_KEY_RATE_LIMIT_ENABLED=true` 后，按每个 API Key 配置的 5 小时、1 天、7 天消费窗口进行限制，默认值为 `false`；启用后仍跳过余额和订阅扣费。
- 窗口限制以数据库为准，只记录 API Key 窗口用量。它在请求完成后记账，并发中的请求可能以各自最终费用超过窗口上限。启用前的历史用量不会自动补算。
- 安全注意事项：生产环境需同时设置 `SIMPLE_MODE_CONFIRM=true` 才允许启动

---

## TypeSafe / Jev 使用说明

Sub2API 支持使用 TypeSafe API Key 账户，通过 Jev 原生、非流式的 System One 协议调用模型。

- 平台：`typesafe`；账号类型：API Key
- 默认上游：`https://api.typesafe.ai`
- 对外端点：`POST /v1/systemone`
- 模型：`jev-latest`，TypeSafe 分组的 `/v1/models` 也会返回该模型
- 问题类型：`noul`、`choice`、`score`

请求和成功响应保持 System One 原生 JSON 结构。该端点不兼容 Chat Completions、Responses、Anthropic Messages 或流式客户端。

问题校验遵循 TypeSafe OpenAPI 的线上协议 schema（SDK v0.5.7 也使用该 schema）。所有问题的 `instructions` 都可以省略或为 `null`。Noul 的 `criteria` 可以省略或为 `null`，其中 `true`/`false` 的描述和 Choice 描述支持字符串、对象、数组或 `null`。Score 的 `criteria` 必须是至少包含一档描述的数组，每档支持字符串、对象或数组；单档也合法。SDK 的整数键 Score 映射会由 SDK 在发送前转换为数组。

```bash
curl https://your-sub2api.example.com/v1/systemone \
  -H 'Authorization: Bearer sk-your-sub2api-key' \
  -H 'Content-Type: application/json' \
  --data '{"model":"jev-latest","state":"待评估文本","questions":{"safety":{"type":"noul","instructions":"评估文本是否不安全"}}}'
```

`jev-latest` 内置价格为输入 `$0.042/百万 tokens`、输出 `$0`，渠道定价可以覆盖。凭据、欠费、权限、限流、过载、服务端和网络错误（`401`、`402`、`403`、`429`、`529`、`5xx`、传输错误）沿用现有账号错误策略（含自定义错误码与临时不可调度规则）并切换账号；请求错误（`400`、`413`、`422`）不会切换账号重试，也不会改变账号状态。TypeSafe 分组（以及路由到 TypeSafe 的 Composite 请求）调用 Messages、Chat Completions、Responses、count_tokens 时返回 `404`。

---

## Antigravity 使用说明

Sub2API 支持 [Antigravity](https://antigravity.so/) 账户，授权后可通过专用端点访问 Claude 和 Gemini 模型。

### 专用端点

| 端点 | 模型 |
|------|------|
| `/antigravity/v1/messages` | Claude 模型 |
| `/antigravity/v1beta/` | Gemini 模型 |

### Claude Code 配置示例

```bash
export ANTHROPIC_BASE_URL="http://localhost:8080/antigravity"
export ANTHROPIC_AUTH_TOKEN="sk-xxx"
```

### 混合调度模式

Antigravity 账户支持可选的**混合调度**功能。开启后，通用端点 `/v1/messages` 和 `/v1beta/` 也会调度该账户。

> **⚠️ 注意**：Anthropic Claude 和 Antigravity Claude **不能在同一上下文中混合使用**，请通过分组功能做好隔离。
## 项目结构

```
sub2api/
├── backend/                  # Go 后端服务
│   ├── cmd/server/           # 应用入口
│   ├── internal/             # 内部模块
│   │   ├── config/           # 配置管理
│   │   ├── model/            # 数据模型
│   │   ├── service/          # 业务逻辑
│   │   ├── handler/          # HTTP 处理器
│   │   └── gateway/          # API 网关核心
│   └── resources/            # 静态资源
│
├── frontend/                 # Vue 3 前端
│   └── src/
│       ├── api/              # API 调用
│       ├── stores/           # 状态管理
│       ├── views/            # 页面组件
│       └── components/       # 通用组件
│
└── deploy/                   # 部署文件
    ├── docker-compose.yml    # Docker Compose 配置
    ├── .env.example          # Docker Compose 环境变量
    ├── config.example.yaml   # 二进制部署完整配置文件
    └── install.sh            # 一键安装脚本
```

## Star History

<a href="https://star-history.dera.page/#Wei-Shaw/sub2api&Date">
 <picture>
   <source media="(prefers-color-scheme: dark)" srcset="https://star-history.dera.page/svg?repos=Wei-Shaw/sub2api&type=Date&theme=dark" />
   <source media="(prefers-color-scheme: light)" srcset="https://star-history.dera.page/svg?repos=Wei-Shaw/sub2api&type=Date" />
   <img alt="Star History Chart" src="https://star-history.dera.page/svg?repos=Wei-Shaw/sub2api&type=Date" />
 </picture>
</a>

---

## 许可证

本项目基于 [GNU 宽通用公共许可证 v3.0](LICENSE)（或更高版本）授权。

Copyright (c) 2026 Wesley Liddick

---

<div align="center">

**如果觉得有用，请给个 Star 支持一下！**

</div>


<!-- ranxi-source-sync-20261110 -->

账号检测、失败处置和自动恢复，请阅读[质量运维](docs/account-quality-operations.md)；BPS OAuth 登录、工具兼容和 403 恢复配置，请阅读[Excel / BPS 协议](docs/excel-bps.md)。

2.19.2 新增默认关闭的账号性能监控，支持指标阈值和独立暂停；启用、人工解除与区域传播边界见[账号性能监控](docs/account-performance-monitor.md)。

已经托管账号或完成部署，想在 Codex 中使用？请阅读[新人入门：使用 Sub2API 接入 Codex](docs/新人入门-使用Sub2API.md)，按步骤创建 API Key、配置 Cockpit Tools 并验证调用。


## 本仓库的维护方向

本仓库是 `ranxi2001/sub2api` 的独立生产 fork，基于 [Wei-Shaw/sub2api](https://github.com/Wei-Shaw/sub2api) 按需同步。应用功能、上游修复、Release 和生产验证都以 `production` 分支为准；不会用上游默认分支或 tag 直接覆盖本 fork 的生产历史。

- **DeepSeek 与 Codex 适配**：支持 Responses 到 Chat Completions 的转换、工具调用历史和上下文压缩兼容。配置模型映射后，可通过切换 API Key 分组使用 DeepSeek，沿用客户端配置。[操作教程](https://tosky.io/docs/?doc=deepseek-switch-group)
- **账号质量运维**：维护定时答题检测、判题记录、失败处置和自动恢复，支持按模型隔离及全部检测模型失败时移出指定分组。检测结果以实际回答和判题证据为准，不以票据形态判断质量。
- **Mihomo 出口管理**：维护订阅、动态代理、节点状态和账号业务出口；支持从订阅节点或动态代理单条、批量添加为静态代理，再给账号分别选择固定节点。[操作说明](docs/mihomo-static-node-proxies.md)
- **Excel / Basispoints**：维护模型级 BPS 路由、内嵌图片 HTTPS 中转、磁盘和并发保护、结构化输出校验，以及工具历史和 transport 恢复。路由遵循账号与模型配置；不支持的托管工具可按策略回退原生 Codex，或保持 BPS 并省略工具，具体边界见 [BPS 配置说明](docs/excel-bps.md)。
- **上游修复维护**：持续跟踪上游 Codex、Responses、工具调用、密文恢复和限流修复；先确认与本 fork 的行为差异，再按提交级别移植并补充回归测试。

已有代理任务报 `unsupported_encrypted_agent_message` 或 `encrypted_agent_recovery_*` 时，先保留任务及工具结果，按[加密代理任务续接与恢复边界](docs/openai-agent-continuation.md)核对实际通道和恢复条件。
- **独立发布与升级**：使用 `ranxi2001/sub2api` 的 Release、安装资源和容器镜像。版本变更见 [更新说明](https://github.com/ranxi2001/sub2api/releases)；Release 成功不代表生产服务已经部署，线上状态需要单独验证。

贡献代码时，请在独立分支中说明影响的请求路径、账号类型、配置默认值和兼容边界。涉及生产分支的修复应先进入原 PR head，通过 CI 后再合并；不要提交 Token、OAuth 导出、ticket、代理凭据或生产配置。

## 项目概述

Sub2API 是一个 AI API 网关平台，用于分发和管理 AI 产品订阅的 API 配额。用户通过平台生成的 API Key 调用上游 AI 服务，平台负责鉴权、计费、负载均衡和请求转发。

## 核心功能

- **多账号管理** - 支持多种上游账号类型（OAuth、API Key）
- **API Key 分发** - 为用户生成和管理 API Key
- **精确计费** - Token 级别的用量追踪和成本计算
- **智能调度** - 智能账号选择，支持粘性会话
- **并发控制** - 用户级、账号级和 API Key 级并发限制，支持可配置的 Key 等待队列
- **速率限制** - 可配置的请求和 Token 速率限制
- **内置支付系统** - 支持 EasyPay 易支付、支付宝官方、微信官方、Stripe，用户自助充值，无需独立部署支付服务（[配置指南](docs/PAYMENT_CN.md)）
- **USDT / USDC 加密货币支付** - 内置 BEpusdt 引擎，无需外部网关 API 或 Token；支持钱包管理、故障补扫、多网络充值与订阅购买，保留外接网关模式和幂等入账（[配置指南](docs/BEPUSDT_PAYMENT.md)）
- **管理后台** - Web 界面进行监控和管理
- **外部系统集成** - 支持通过 iframe 嵌入外部系统（如工单等），扩展管理后台功能

### OpenAI API Key 的 UltraFast 档位计费

账号新增/编辑页提供默认关闭的 **UltraFast 档位计费** 开关。开启后，按该账号所有有价 token 模型的上游响应 `service_tier` 结算；`ultrafast` 使用标准单价×6，包含缓存读写，再应用有效账号/分组倍率。响应未返回档位时按请求档位回退，该开关不强制上游加速。

客户端明确请求 `ultrafast` 且最终仍按该档位结算时，关闭此开关也按6倍收费。开关控制是否采用上游响应升档，不豁免客户端请求的 UltraFast 价格。配置字段、响应降档、长上下文和计费范围见 [UltraFast 账号计费说明](docs/openai-ultrafast-billing.md)。

### Anthropic API Key 客户端模拟与计费

创建或编辑 Anthropic API Key 账号时, 可开启 "模拟 Claude Code 客户端", 为要求 Claude Code 客户端的上游补齐缺失的请求头, system 身份句和会话信息. 原有 system 指令及缓存断点会保留, count_tokens 仅补请求头. 此选项与 "抹除 Claude Code 身份" 互斥, 同时存储为开启时以抹除身份为准. 身份句可能影响模型自述.

管理员可单独选择 "按模型估算扣除身份句 token". 默认关闭, 按上游用量正常计费. 开启后, 仅在网关实际补入身份句时估算扣除约 14-24 个输入或缓存 token, 不修改返回客户端的 usage. 型号别名, 上游分词器和混合缓存会影响估算准确度; 需要按上游原始用量结算时, 保持此开关关闭.

对应账号 extra 配置为 `anthropic_mimic_claude_code` 和 `anthropic_mimic_claude_code_deduct_tokens`, 均默认 `false`. 关闭模拟或开启抹除身份时, 页面同时清除估算扣减设置.

### 首次初始化管理员

全新自动安装时，`ADMIN_EMAIL` 和 `ADMIN_PASSWORD` 留空会生成随机登录邮箱和密码，可从首次启动日志中的 `Generated admin` 获取。显式指定时，邮箱必须符合登录格式，密码必须为 8–72 字节。已有管理员或已有用户的部署跳过创建与此项校验。详细配置见 [部署说明](deploy/README.md)。

## API Key 并发等待队列

当 API Key 设置了大于 `0` 的 `concurrency_limit` 时，达到上限后的新请求会在原连接上等待空闲槽位。默认值 `0` 不增加 Key 级并发限制。等待策略是全局配置，进程启动时读取：

```yaml
gateway:
  api_key_queue:
    max_waiting: 5
    timeout_seconds: 30
```

| 环境变量 | 默认值 | 说明 |
|----------|--------|------|
| `GATEWAY_API_KEY_QUEUE_MAX_WAITING` | `5` | 每个受限 Key 允许额外等待的请求数；`0` 关闭 Key 排队。 |
| `GATEWAY_API_KEY_QUEUE_TIMEOUT_SECONDS` | `30` | 单个请求最长等待秒数，必须为正整数。 |

- 等待名额按 Key 独立计算，不保证 FIFO；`concurrency_limit: 0` 的 Key 不进入队列。
- 队列用于 HTTP/SSE、OpenAI Responses WebSocket 每轮请求和 Live 创建的 Key 准入，与用户级、账号级等待限制独立。
- 等待期间复核 Key、用户及当前请求的模型/能力权限。Key 换组、平台或计费模式变化返回可重试的 `503` / `API_KEY_GROUP_CHANGED`。WebSocket 鉴权/权限失败以 `1008` 关闭，容量或临时服务错误以 `1013` 关闭。
- 关闭排队时达到上限返回 `429` / `gateway_concurrency_limit`；队列满返回 `429` / `api_key_queue_full`；等待超时返回 `429` / `api_key_queue_timeout`。
- 两个配置值必须为整数；负数、小数、非法字符串或超出范围会阻止启动。即使关闭排队，超时也必须为正数。
- 修改 Compose `.env` 后需要重建容器以更新环境变量。调大等待时间时，需要确认客户端及反向代理的首字节超时。
- 升级自动应用 `237_add_api_key_concurrency_limit.sql`，旧 Key 默认为 `0`。回退二进制不会撤销数据库新增字段。

### 中转 API Key 的上游 429

- OpenAI 平台普通 API Key 收到上游 429 时，使用管理端已有的“429 兜底冷却”设置（默认启用、5 秒），不再把透传的 ChatGPT/Codex 周窗口、`resets_at` 或使用率快照写成整个 Key 的配额状态。关闭该设置时不新增本地兜底冷却，上游错误仍按原有响应与故障转移逻辑处理。
- OAuth / Setup Token 保留真实配额窗口；Ollama Cloud、国产供应商和 OpenCode Go 的已识别专用配额逻辑，以及池模式、自定义错误策略和显式临时停调规则，仍按原有优先级处理。
- 管理端测试 API Key 时，不再用透传的 ChatGPT 429 重置时间更新 Key 的调度状态。升级不会自动清理此前已写入的长冷却；核实状态后可使用管理端恢复功能处理。

## 技术栈

| 组件 | 技术 |
|------|------|
| 后端 | Go 1.27.3, Gin, Ent |
| 前端 | Vue 3.4+, Vite 5+, TailwindCSS |
| 数据库 | PostgreSQL 15+ |
| 缓存/队列 | Redis 6.0+ |
| API 协议 | OpenAI Responses / Chat Completions、Anthropic Messages、Gemini、SSE、WebSocket |
| 调度与可靠性 | 粘性会话、并发控制、限流、故障转移、模型隔离与冷却 |
| 观测与运维 | 结构化日志、请求分段耗时、健康检查、Mihomo 出口与 systemd |
| 交付与质量 | Docker Compose、GitHub Actions、Go 单元测试、前端类型检查与构建 |

## 贡献与协作

欢迎围绕协议兼容、账号调度、质量运维、支付计费、管理后台和运维观测提交改进。高质量贡献应尽量保持边界清晰，并在 PR 中说明请求路径、状态变化、兼容性影响和验证证据。

开始前请阅读 [贡献指南](CONTRIBUTING.md)：包含最小复现、日志脱敏、开发环境、验证命令和 PR 流程。[提交 Issue](https://github.com/ranxi2001/sub2api/issues/new/choose) 时可选择 Bug、功能建议、文档或使用问题表单；提交 PR 时按模板填写行为变化和实际验证结果。

建议按以下方式提交：

- **协议与网关**：补充请求/响应样例，覆盖流式终止、工具调用、重试和上游错误映射。
- **调度与账号**：说明候选筛选、粘性状态、并发占用、冷却窗口和故障转移行为，避免改变幂等语义。
- **后台与配置**：同步前后端类型、默认值、权限边界和迁移兼容性。
- **运维与部署**：提供离线或 mock 验证，不在 PR 中写入 Token、ticket、代理凭据或生产配置。
- **验证与审查**：列出实际运行的测试、构建或脚本检查；未运行的检查不要标记为通过。

感谢已合并 PR 的贡献者（按本仓库已合并 PR 数量降序排列，数量相同时按 GitHub 用户名排序）：

<p>
  <a href="https://github.com/ranxi2001"><img src="https://avatars.githubusercontent.com/u/77790009?v=4" width="56" height="56" alt="Onefly" title="Onefly" /></a>
  <a href="https://github.com/akihitohyh"><img src="https://avatars.githubusercontent.com/u/79531840?v=4" width="56" height="56" alt="akihitohyh" title="akihitohyh" /></a>
  <a href="https://github.com/blackdm666"><img src="https://avatars.githubusercontent.com/u/67053678?v=4" width="56" height="56" alt="老黑" title="老黑" /></a>
  <a href="https://github.com/psyche314"><img src="https://avatars.githubusercontent.com/u/180074435?v=4" width="56" height="56" alt="psyche314" title="psyche314" /></a>
  <a href="https://github.com/akayedi"><img src="https://avatars.githubusercontent.com/u/90334896?v=4" width="56" height="56" alt="akayedi" title="akayedi" /></a>
  <a href="https://github.com/loserzero-7"><img src="https://avatars.githubusercontent.com/u/177290228?v=4" width="56" height="56" alt="loserzero-7" title="loserzero-7" /></a>
  <a href="https://github.com/289989142"><img src="https://avatars.githubusercontent.com/u/63082228?v=4" width="56" height="56" alt="289989142" title="289989142" /></a>
  <a href="https://github.com/AI8888-SHOP"><img src="https://avatars.githubusercontent.com/u/297756662?v=4" width="56" height="56" alt="AI8888-SHOP" title="AI8888-SHOP" /></a>
  <a href="https://github.com/damian2848"><img src="https://avatars.githubusercontent.com/u/55676325?v=4" width="56" height="56" alt="damian2848" title="damian2848" /></a>
  <a href="https://github.com/Cd1s"><img src="https://avatars.githubusercontent.com/u/135119404?v=4" width="56" height="56" alt="Cd1s" title="Cd1s" /></a>
  <a href="https://github.com/danvilig"><img src="https://avatars.githubusercontent.com/u/230506945?v=4" width="56" height="56" alt="danvilig" title="danvilig" /></a>
  <a href="https://github.com/EdmundMad0309"><img src="https://avatars.githubusercontent.com/u/122854730?v=4" width="56" height="56" alt="EdmundMad0309" title="EdmundMad0309" /></a>
  <a href="https://github.com/LeeSssong"><img src="https://avatars.githubusercontent.com/u/37948462?v=4" width="56" height="56" alt="LeeSssong" title="LeeSssong" /></a>
  <a href="https://github.com/QQQHHHDDD"><img src="https://avatars.githubusercontent.com/u/51820811?v=4" width="56" height="56" alt="QQQHHHDDD" title="QQQHHHDDD" /></a>
  <a href="https://github.com/spake404"><img src="https://avatars.githubusercontent.com/u/123435269?v=4" width="56" height="56" alt="spake404" title="spake404" /></a>
  <a href="https://github.com/stl25"><img src="https://avatars.githubusercontent.com/u/317681732?v=4" width="56" height="56" alt="stl25" title="stl25" /></a>
  <a href="https://github.com/wuwu131452011"><img src="https://avatars.githubusercontent.com/u/165636850?v=4" width="56" height="56" alt="wuwu131452011" title="wuwu131452011" /></a>
  <a href="https://github.com/abcgoodwei"><img src="https://avatars.githubusercontent.com/u/35887090?v=4" width="56" height="56" alt="abcgoodwei" title="abcgoodwei" /></a>
  <a href="https://github.com/alanbulan"><img src="https://avatars.githubusercontent.com/u/140389107?v=4" width="56" height="56" alt="alanbulan" title="alanbulan" /></a>
  <a href="https://github.com/ambi-vip"><img src="https://avatars.githubusercontent.com/u/46626203?v=4" width="56" height="56" alt="ambi-vip" title="ambi-vip" /></a>
  <a href="https://github.com/ape-ming"><img src="https://avatars.githubusercontent.com/u/30568607?v=4" width="56" height="56" alt="ape-ming" title="ape-ming" /></a>
  <a href="https://github.com/BeterXie"><img src="https://avatars.githubusercontent.com/u/30471270?v=4" width="56" height="56" alt="BeterXie" title="BeterXie" /></a>
  <a href="https://github.com/buluw"><img src="https://avatars.githubusercontent.com/u/45087912?v=4" width="56" height="56" alt="buluw" title="buluw" /></a>
  <a href="https://github.com/Coke165"><img src="https://avatars.githubusercontent.com/u/259744923?v=4" width="56" height="56" alt="Coke165" title="Coke165" /></a>
  <a href="https://github.com/Even1997"><img src="https://avatars.githubusercontent.com/u/95899304?v=4" width="56" height="56" alt="Even1997" title="Even1997" /></a>
  <a href="https://github.com/InCerryGit"><img src="https://avatars.githubusercontent.com/u/36690780?v=4" width="56" height="56" alt="InCerryGit" title="InCerryGit" /></a>
  <a href="https://github.com/Lanyang1176"><img src="https://avatars.githubusercontent.com/u/187152708?v=4" width="56" height="56" alt="Lanyang1176" title="Lanyang1176" /></a>
  <a href="https://github.com/Mickey0811"><img src="https://avatars.githubusercontent.com/u/49522921?v=4" width="56" height="56" alt="Mickey0811" title="Mickey0811" /></a>
  <a href="https://github.com/mracry"><img src="https://avatars.githubusercontent.com/u/112537993?v=4" width="56" height="56" alt="mracry" title="mracry" /></a>
  <a href="https://github.com/sylarchen1389"><img src="https://avatars.githubusercontent.com/u/43306063?v=4" width="56" height="56" alt="sylarchen1389" title="sylarchen1389" /></a>
  <a href="https://github.com/Terry1321"><img src="https://avatars.githubusercontent.com/u/41000037?v=4" width="56" height="56" alt="Terry1321" title="Terry1321" /></a>
  <a href="https://github.com/yuanyuan19"><img src="https://avatars.githubusercontent.com/u/120552623?v=4" width="56" height="56" alt="yuanyuan19" title="yuanyuan19" /></a>
  <a href="https://github.com/ZASENJC"><img src="https://avatars.githubusercontent.com/u/65362072?v=4" width="56" height="56" alt="ZASENJC" title="ZASENJC" /></a>
  <a href="https://github.com/zhoumooooo"><img src="https://avatars.githubusercontent.com/u/271002711?v=4" width="56" height="56" alt="zhoumooooo" title="zhoumooooo" /></a>
</p>
