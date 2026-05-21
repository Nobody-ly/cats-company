# CatsCo 账号中心与鉴权接入方案

本文面向 CatsCompany、CatsCo 桌面端、relay 中转站以及后续内部业务应用。目标是让多个业务共用同一套 CatsCo 账号，同时保持各业务独立部署、独立权限和可撤销的访问凭证。

## 结论

第一阶段建议把账号中心放在 CatsCompany 内部实现，而不是立刻拆成独立 OAuth 服务。

原因：

- CatsCompany 已经有用户表、邮箱注册、密码重置、JWT、API Key、限流和 PostgreSQL 数据。
- 当前最急的是让其他业务能“验证这个用户是谁、有没有权限”，而不是完整第三方 OAuth 授权生态。
- 内置实现上线风险更低，能复用现有登录页和账号数据。
- 只要接口边界设计成账号中心风格，后续可以平滑迁到独立 `auth.catsco.cc` 服务。

推荐路线：

1. **V0：CatsCompany 内置账号中心**
   - 继续使用 `app.catsco.cc` 登录。
   - 提供标准化的用户 token 验证接口和服务间鉴权接口。
   - relay、后续业务通过接口校验 CatsCo 用户身份。

2. **V1：账号中心能力抽象**
   - 增加应用注册、service token、scope、审计日志、token 撤销。
   - 给每个业务配置独立权限。

3. **V2：独立 OAuth/OIDC 服务**
   - 当有多个公开第三方应用、需要授权页、回调地址、refresh token、用户同意页时，再拆出去。

## 现有能力

当前 CatsCompany 已有：

- 邮箱验证码注册：`POST /api/auth/send-code`、`POST /api/auth/register`
- 邮箱重置密码：`POST /api/auth/reset-password/send-code`、`POST /api/auth/reset-password`
- 登录：`POST /api/auth/login`
- 当前用户信息：`GET /api/me`
- 用户资料更新：`POST /api/me/update`
- JWT：`Authorization: Bearer <token>`
- Bot/API Key：`Authorization: ApiKey <key>`
- 账号类型：`human`、`bot`、`service`
- 登录、注册、重置密码、上传、reader、feedback 的限流

现有 JWT payload 主要字段：

```json
{
  "userId": 2,
  "username": "zhy8882",
  "email": "example@qq.com",
  "iss": "catscompany",
  "iat": 1770000000,
  "exp": 1770604800
}
```

## 核心概念

### User

真实用户账号。用于登录 CatsCompany、管理 CatsCo 桌面端、管理 relay key、访问其他业务。

### Service

内部业务系统，例如：

- CatsCompany
- CatsCo relay
- 未来的写作工具、监控平台、后台管理工具

Service 不直接读取用户密码，也不自己保存登录态。它只通过账号中心验证用户 token 或 service token。

### Access Token

用户登录后拿到的短期 JWT。用于浏览器、桌面端调用业务接口。

### Service Token

业务服务调用账号中心的机器凭证。用于调用 token introspection、用户查询、权限验证等内部接口。

Service Token 必须只保存在服务器环境变量或 secret 中，不给前端。

### API Key

给自动化程序、机器人、relay 客户端使用的长期凭证。API Key 应该：

- 只展示一次明文。
- 数据库只存 hash。
- 支持撤销、限流、scope。
- 能绑定用户或 service。

## 推荐接入链路

### 1. Web 用户登录

用户在 CatsCompany 登录：

```http
POST /api/auth/login
Content-Type: application/json

{
  "account": "3026804351@qq.com",
  "password": "******"
}
```

返回：

```json
{
  "token": "<jwt>",
  "uid": 2,
  "username": "zhy8882",
  "email": "3026804351@qq.com",
  "display_name": "布鲁斯",
  "avatar_url": "/uploads/avatar.png",
  "account_type": "human"
}
```

业务前端保存 token，后续请求带：

```http
Authorization: Bearer <jwt>
```

### 2. 业务服务验证用户 token

新增账号中心接口：

```http
POST /api/account/introspect
Authorization: Service <service_token>
Content-Type: application/json

{
  "token": "<user_jwt>"
}
```

返回：

```json
{
  "active": true,
  "user": {
    "uid": 2,
    "username": "zhy8882",
    "email": "3026804351@qq.com",
    "display_name": "布鲁斯",
    "avatar_url": "/uploads/avatar.png",
    "account_type": "human",
    "state": 0
  },
  "claims": {
    "issuer": "catscompany",
    "expires_at": "2026-05-28T12:00:00Z",
    "issued_at": "2026-05-21T12:00:00Z"
  }
}
```

token 无效：

```json
{
  "active": false,
  "error": "invalid_or_expired_token"
}
```

接入方行为：

- `active=true` 才允许访问。
- `active=false` 返回 401 并要求用户重新登录。
- 接入方本地可以短缓存 30-120 秒，降低账号中心压力。

### 3. 业务服务获取当前用户资料

新增接口：

```http
GET /api/account/users/{uid}
Authorization: Service <service_token>
```

返回：

```json
{
  "uid": 2,
  "username": "zhy8882",
  "email": "3026804351@qq.com",
  "display_name": "布鲁斯",
  "avatar_url": "/uploads/avatar.png",
  "account_type": "human",
  "state": 0,
  "created_at": "2026-05-01T08:00:00Z"
}
```

建议只给内部服务使用，普通前端仍使用 `GET /api/me`。

### 4. 服务自己的 API Key

例如 relay 需要给用户创建“中转 key”：

```http
POST /api/account/api-keys
Authorization: Bearer <user_jwt>
Content-Type: application/json

{
  "name": "relay default key",
  "service": "cats-relay",
  "scopes": ["relay.chat", "relay.models.read"],
  "expires_at": null
}
```

返回：

```json
{
  "id": "key_123",
  "key": "cat_sk_xxx",
  "name": "relay default key",
  "service": "cats-relay",
  "scopes": ["relay.chat", "relay.models.read"],
  "created_at": "2026-05-21T12:00:00Z"
}
```

注意：`key` 明文只返回这一次，后续只能重新生成或撤销。

调用时：

```http
Authorization: Bearer cat_sk_xxx
```

relay 收到后可以本地查自己的 key 表，也可以调用账号中心 introspect API Key：

```http
POST /api/account/api-keys/introspect
Authorization: Service <service_token>
Content-Type: application/json

{
  "key": "cat_sk_xxx",
  "required_scope": "relay.chat"
}
```

返回：

```json
{
  "active": true,
  "uid": 2,
  "service": "cats-relay",
  "scopes": ["relay.chat", "relay.models.read"],
  "rate_limit": {
    "rpm": 60,
    "daily": 1000
  }
}
```

## 推荐数据库表

第一阶段可以在现有 PostgreSQL 中新增这些表。

### auth_services

记录接入账号中心的业务系统。

字段建议：

- `id`
- `name`
- `slug`
- `service_token_hash`
- `allowed_origins`
- `allowed_redirect_uris`
- `scopes`
- `state`
- `created_at`
- `updated_at`

### auth_api_keys

记录用户或 service 创建的长期 API Key。

字段建议：

- `id`
- `owner_user_id`
- `service_id`
- `name`
- `key_prefix`
- `key_hash`
- `scopes`
- `state`
- `last_used_at`
- `expires_at`
- `created_at`
- `revoked_at`

### auth_sessions（可选）

如果要支持强制下线、设备管理、refresh token，需要 session 表。

字段建议：

- `id`
- `user_id`
- `refresh_token_hash`
- `device_name`
- `ip`
- `user_agent`
- `expires_at`
- `revoked_at`
- `created_at`

V0 可以先不做 refresh token，继续用 7 天 JWT。

### auth_audit_logs

记录关键认证行为。

字段建议：

- `id`
- `actor_user_id`
- `service_id`
- `action`
- `target_type`
- `target_id`
- `ip`
- `user_agent`
- `metadata`
- `created_at`

## 接入方怎么用

### 前端业务

适合 CatsCompany、未来有页面的工具。

流程：

1. 如果没有 token，跳到 CatsCompany 登录页。
2. 登录后保存 token。
3. 调业务接口时带 `Authorization: Bearer <token>`。
4. 业务后端调用 `/api/account/introspect` 验 token。

### 后端服务

适合 relay、内部工具、任务服务。

流程：

1. 后端环境变量配置 `CATS_ACCOUNT_CENTER_URL` 和 `CATS_SERVICE_TOKEN`。
2. 收到用户 token 或 API key。
3. 调账号中心验证。
4. 按返回的 `uid`、`scopes`、`state` 决定是否放行。

### CatsCo 桌面端

建议继续走 CatsCompany 登录或 API Key 绑定：

- 用户登录后 CatsCompany 下发桌面端需要的连接信息。
- CatsCo 用 bot/API key 接入实时消息。
- 后续可以增加“用 CatsCo 账号自动配置模型/relay”的接口，但不要把真实模型供应商 key 直接下发给客户端。

## 安全边界

必须做到：

- 生产环境必须配置固定 `OC_JWT_SECRET`。
- Service Token、API Key 只存 hash，不存明文。
- Service Token 不进入前端。
- Introspection 接口必须要求 `Authorization: Service <service_token>`。
- URL query 中的 `token`、`api_key` 后续应逐步废弃，优先 header。
- 所有认证相关接口必须有限流。
- 所有 key 创建、撤销、验证失败次数过多都进入审计日志。

暂时不建议：

- 现在就做完整 OAuth 授权码流程。
- 现在就让各业务直接连账号数据库。
- 现在就把账号中心拆到独立仓库和独立服务。

## OAuth 什么时候做

满足下面任一条件时，再做独立 OAuth/OIDC：

- 有外部第三方应用需要“用 CatsCo 登录”。
- 需要授权页面和用户 consent。
- 需要标准 `/.well-known/openid-configuration`。
- 需要 OAuth callback、PKCE、refresh token、client secret 管理。
- 接入应用超过 3-5 个，CatsCompany 内置 auth 开始影响业务迭代。

届时可以把 V0 的接口迁到：

- `https://auth.catsco.cc`
- 或 `https://account.catsco.cc`

并保留 CatsCompany 对旧接口的代理兼容。

## 第一阶段开发清单

建议按这个顺序做：

1. 新增 `POST /api/account/introspect`。
2. 新增 `GET /api/account/users/{uid}`。
3. 先用 `OC_ACCOUNT_SERVICE_TOKENS` 配置 service token，形成最小服务间鉴权闭环。
4. 新增 `auth_services` 表，把 service token 从环境变量迁到数据库。
5. 新增用户 API key 创建、列表、撤销接口。
6. 给 relay 写最小接入示例。
7. 给同事提供本文档和 curl 示例。

这样第一版上线后，其他业务就可以先通过“token introspection + service token”接入统一账号。

## V0 环境变量配置

第一版可以先在服务端环境变量里配置 service token：

```bash
OC_ACCOUNT_SERVICE_TOKENS="cats-relay=replace-with-secret;internal-tool=sha256:<hex-sha256>"
```

支持两种形式：

- `service=plain-token`：启动时在内存里计算 hash。
- `service=sha256:<hex>`：只把 token 的 SHA-256 hash 放进环境变量。

生产推荐使用 `sha256:<hex>`，避免环境变量里出现 service token 明文。

同时 V0 已支持数据库里的 `auth_services`。本地后台创建 service 后，会生成一次性明文 token；服务端只保存 token hash 和 prefix。创建后的 service token 也可以直接用于：

```http
Authorization: Service <service_token>
```

## 本地后台管理页

V0 提供一个只读的本地后台页面，用于通过 SSH 隧道查看账号状态。它不走公网入口，不需要在公网 nginx 上暴露后台路径。

访问方式：

```bash
ssh -L 26061:127.0.0.1:26061 <server-alias>
```

然后在本机浏览器打开：

```text
http://127.0.0.1:26061/local/account-admin
```

第一版能力：

- 按 UID 查询账号。
- 创建或轮换 service token。
- 撤销 service token。
- 查看用户名、邮箱、显示名、头像、账号类型、状态和创建时间。
- 查看 service token 是否已在服务端配置。

注意：

- 后台页面只接受本机/内网隧道来源请求。
- 文档和页面示例只写本地访问方式，不写服务器公网 IP。
- token 明文只在创建或轮换时显示一次，数据库只保存 hash。
- 目前后台不提供改密码、封禁、删除账号等高风险操作；这些等审计日志和权限模型补齐后再加。
