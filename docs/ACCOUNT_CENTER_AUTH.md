# CatsCo 账号中心接入文档

本文面向 CatsCompany、CatsCo relay/Bifrost、内部工具以及后续业务服务。当前版本的目标很简单：所有业务共用 CatsCo 账号登录，但各业务仍然独立部署、独立授权、独立管理自己的业务凭证。

当前账号中心内置在 CatsCompany 中。后续如果业务数量变多，再拆成独立 `auth.catsco.cc` 或 OAuth/OIDC 服务。

## 当前版本能做什么

已支持：

- 用户登录、注册、重置密码仍走 CatsCompany 现有接口。
- 业务服务可以用 `Service Token` 调账号中心验证用户 JWT。
- 业务服务可以按 UID 查询用户基础资料。
- 本地后台可以通过 SSH 隧道创建、撤销 `Service Token`。
- 数据库只保存 service token hash，不保存明文。

当前版本不做：

- 不做完整 OAuth 授权码流程。
- 不做 refresh token / 设备管理。
- 不在 CatsCompany 里管理 relay/Bifrost 的 Virtual Key。
- 不在 CatsCompany 里保存模型供应商 key、模型路由、预算和限流策略。
- 不让其他业务直接连 CatsCompany 数据库。

## 核心边界

### CatsCompany 账号中心

负责回答：

- 这个用户 JWT 是否有效？
- 这个用户是谁？
- 这个用户账号状态是否正常？
- 这个内部服务是否有资格查询账号中心？

### 业务服务

例如 relay/Bifrost、写作工具、监控后台、内部管理工具。

负责自己业务内的：

- 权限模型。
- 业务角色。
- 业务 API Key。
- 业务限流和配额。
- 业务数据。

### Relay / Bifrost

Bifrost 自己有 Virtual Keys / governance 机制，适合管理：

- 用户或应用调用中转的 key。
- provider key。
- OpenAI / Anthropic 兼容层。
- 模型路由。
- 预算、限流、team/customer 映射。

CatsCompany 账号中心只提供 CatsCo 用户身份。relay/Bifrost 用返回的 `uid` 去映射自己的 customer、team 或 Virtual Key。

## 接入总流程

```text
用户登录 CatsCompany
  -> 拿到用户 JWT
  -> 业务前端或客户端请求自己的业务后端
  -> 业务后端带 Service Token 调账号中心 introspect
  -> 账号中心返回 active + user
  -> 业务后端按 uid/account_type/state 放行或拒绝
```

不要让前端直接拿 `Service Token`。`Service Token` 只能放在业务服务端环境变量或 secret 里。

## 第一步：给业务服务创建 Service Token

当前版本推荐通过本地后台创建 service token。后台不走公网入口，只通过 SSH 隧道访问。

```bash
ssh -L 26061:127.0.0.1:26061 <server-alias>
```

然后在本机浏览器打开：

```text
http://127.0.0.1:26061/local/account-admin
```

在后台里创建服务，例如：

- `cats-relay`
- `writing-app`
- `ops-dashboard`

创建后会显示一次性明文 token。立刻保存到对应服务的 secret 或环境变量里。CatsCompany 数据库只保存 hash 和 prefix，后续无法找回明文；如果丢失或怀疑泄露，就在本地后台重新生成并替换到对应服务。

这里即使后台只通过 SSH 隧道访问，也不建议支持重复查看明文 token。原因不是 SSH 不安全，而是账号中心不保存明文本身，后续即使数据库或后台页面被误读，也不会直接泄露可用的 service token。

也可以用环境变量配置固定 service token：

```bash
OC_ACCOUNT_SERVICE_TOKENS="cats-relay=replace-with-secret;internal-tool=sha256:<hex-sha256>"
```

支持两种形式：

- `service=plain-token`：启动时在内存里计算 hash。
- `service=sha256:<hex>`：只把 token 的 SHA-256 hash 放进环境变量。

生产更推荐用本地后台生成并落库管理，方便撤销。

## 第二步：用户登录并拿到 JWT

用户仍然使用 CatsCompany 登录。

```http
POST /api/auth/login
Content-Type: application/json

{
  "account": "user@example.com",
  "password": "******"
}
```

成功返回：

```json
{
  "token": "<user_jwt>",
  "uid": 2,
  "username": "zhy8882",
  "email": "user@example.com",
  "display_name": "布鲁斯",
  "avatar_url": "/uploads/avatar.png",
  "account_type": "human"
}
```

业务前端或客户端保存用户 JWT，请求自己的业务后端时带上：

```http
Authorization: Bearer <user_jwt>
```

## 第三步：业务后端验证用户 JWT

业务后端收到用户 JWT 后，调用账号中心：

```http
POST /api/account/introspect
Authorization: Service <service_token>
Content-Type: application/json

{
  "token": "<user_jwt>"
}
```

成功返回：

```json
{
  "active": true,
  "user": {
    "uid": 2,
    "username": "zhy8882",
    "email": "user@example.com",
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

无效或过期 token 返回：

```json
{
  "active": false,
  "error": "invalid_or_expired_token"
}
```

业务服务建议：

- `active=true` 且 `user.state=0` 才放行。
- `active=false` 返回 401，让用户重新登录。
- 本地短缓存验证结果 30-120 秒，减少账号中心压力。
- 不要把用户密码、CatsCompany 数据库连接、service token 下发到浏览器。

## 第四步：按 UID 查询用户资料

业务服务需要补全用户资料时，可以调用：

```http
GET /api/account/users/{uid}
Authorization: Service <service_token>
```

返回：

```json
{
  "uid": 2,
  "username": "zhy8882",
  "email": "user@example.com",
  "display_name": "布鲁斯",
  "avatar_url": "/uploads/avatar.png",
  "account_type": "human",
  "state": 0,
  "created_at": "2026-05-01T08:00:00Z"
}
```

普通前端不要直接调用这个接口。普通前端查询自己资料仍然用：

```http
GET /api/me
Authorization: Bearer <user_jwt>
```

## Relay / Bifrost 推荐接法

relay/Bifrost 不需要 CatsCompany 帮它生成用户中转 key。

推荐流程：

1. 用户登录 CatsCompany，拿到 CatsCo 用户 JWT。
2. relay/Bifrost 后端用自己的 `Service Token` 调 `/api/account/introspect`。
3. 账号中心返回 `uid`。
4. relay/Bifrost 用 `uid` 查或创建自己的 customer/team/Virtual Key。
5. relay/Bifrost 自己处理 provider key、模型权限、预算、限流、OpenAI/Anthropic 兼容。

这样以后即使 relay 从 Bifrost 换成别的网关，账号中心接口也不用大改。

## 接入方伪代码

### Node / TypeScript

```ts
async function verifyCatsUser(userToken: string) {
  const resp = await fetch(`${process.env.CATS_ACCOUNT_CENTER_URL}/api/account/introspect`, {
    method: 'POST',
    headers: {
      'Authorization': `Service ${process.env.CATS_SERVICE_TOKEN}`,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ token: userToken }),
  });

  if (!resp.ok) {
    throw new Error(`account center failed: ${resp.status}`);
  }

  const result = await resp.json();
  if (!result.active || result.user?.state !== 0) {
    return null;
  }

  return result.user;
}
```

### Go

```go
func verifyCatsUser(ctx context.Context, accountURL, serviceToken, userToken string) (*CatsUser, error) {
	body := strings.NewReader(fmt.Sprintf(`{"token":%q}`, userToken))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, accountURL+"/api/account/introspect", body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Service "+serviceToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("account center status %d", resp.StatusCode)
	}

	var out struct {
		Active bool      `json:"active"`
		User   *CatsUser `json:"user"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if !out.Active || out.User == nil || out.User.State != 0 {
		return nil, nil
	}
	return out.User, nil
}
```

## 错误处理

### Service Token 未配置

```http
HTTP/1.1 503 Service Unavailable
```

```json
{
  "error": "account center service tokens are not configured"
}
```

说明 CatsCompany 服务端还没有配置 `OC_ACCOUNT_SERVICE_TOKENS`，也没有可用的数据库 service token。

### Service Token 无效

```http
HTTP/1.1 401 Unauthorized
```

```json
{
  "error": "invalid service token"
}
```

说明业务服务端传错了 `Authorization: Service <service_token>`。

### 用户 JWT 无效

```http
HTTP/1.1 200 OK
```

```json
{
  "active": false,
  "error": "invalid_or_expired_token"
}
```

说明用户需要重新登录。

## 安全要求

- `Service Token` 只能放在服务端，不进前端，不进客户端，不进 Git。
- `Service Token` 明文只在创建时显示一次。
- 业务服务不要直连 CatsCompany 数据库。
- 业务服务不要保存用户密码。
- 业务服务验证用户身份时必须走 `/api/account/introspect`。
- relay/Bifrost 的 Virtual Key、provider key、预算和限流留在 relay/Bifrost。
- 生产环境必须配置固定 `OC_JWT_SECRET`。
- URL query 中的 `token`、`api_key` 后续应逐步废弃，优先 header。

## 同事接入清单

接入一个新服务时，只需要完成这些事：

1. 确定服务标识，例如 `writing-app`。
2. 通过本地后台创建 service token。
3. 把 service token 放入该服务的 secret 或环境变量。
4. 前端让用户使用 CatsCompany 登录，拿到用户 JWT。
5. 后端收到用户 JWT 后调用 `/api/account/introspect`。
6. 后端按返回的 `uid`、`account_type`、`state` 做本业务权限判断。

后续如果要拆成独立账号中心，优先保持这两个接口兼容：

- `POST /api/account/introspect`
- `GET /api/account/users/{uid}`
