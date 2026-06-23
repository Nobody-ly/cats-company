# CatsCompany WebSocket 协议

## 连接

**端点：** `ws://your-server:6061/v0/channels`

**认证方式：**
- JWT Token: `?token=<jwt_token>`
- API Key (Bot): Header `X-API-Key: <api_key>`

> 兼容说明：服务端仍兼容 `?api_key=<api_key>`，但不建议在新代码里使用。
> URL 可能进入浏览器历史、代理日志或监控日志，Bot SDK 默认使用 Header 传递。

## 消息格式

所有消息都是 JSON 格式。

### 客户端 → 服务器

#### 1. 握手 (hi)
```json
{
  "hi": {
    "id": "1",
    "ver": "0.1.0"
  }
}
```

#### 2. 发送消息 (pub)
```json
{
  "pub": {
    "id": "2",
    "topic": "p2p_3_5",
    "content": "Hello!",
    "reply_to": 123
  }
}
```

**content 支持：**
- 纯文本: `"Hello"`
- 富文本: `{"type": "image", "payload": {...}}`

#### 3. 订阅 (sub)
```json
{
  "sub": {
    "id": "3",
    "topic": "p2p_3_5"
  }
}
```

#### 4. 获取历史 (get)
```json
{
  "get": {
    "id": "4",
    "topic": "p2p_3_5",
    "what": "history",
    "seq": 100
  }
}
```

#### 5. 通知 (note)
```json
{
  "note": {
    "topic": "p2p_3_5",
    "what": "kp",
    "seq": 123
  }
}
```

**what 类型：**
- `kp`: 正在输入
- `read`: 已读回执

#### 6. 设备 RPC (device_rpc)

`device_rpc` 用于 bot 将被授权的工具请求路由到用户当前选定的本地设备。服务端只接受 bot 连接发起的 `request`，并要求请求绑定有效的 `grant_id`、会话、用户、设备和 operation。

当前 Device RPC operation：
- 普通文件任务：`read_file`、`resolve_common_directory`、`glob`、`grep`、`write_file`、`edit_file`
- 高风险命令任务：`execute_shell`

`execute_shell` 只有在目标设备声明了该 capability、服务端为当前会话下发的 grant 包含 `execute_shell`、并且请求通过 Device RPC grant 校验时才会被转发。服务端会记录设备审计事件，包括操作者、agent、目标设备、session、operation、tool、阶段、结果；`execute_shell` 还会记录本次 shell 命令文本。

### 服务器 → 客户端

#### 1. 控制消息 (ctrl)
```json
{
  "ctrl": {
    "id": "1",
    "code": 200,
    "text": "ok",
    "params": {
      "uid": "usr3",
      "name": "张三"
    }
  }
}
```

#### 2. 数据消息 (data)
```json
{
  "data": {
    "topic": "p2p_3_5",
    "from": "usr5",
    "seq": 456,
    "content": "Hi there!",
    "reply_to": 123
  }
}
```

#### 3. 在线状态 (pres)
```json
{
  "pres": {
    "topic": "me",
    "what": "on",
    "src": "usr5"
  }
}
```

#### 4. 信息通知 (info)
```json
{
  "info": {
    "topic": "p2p_3_5",
    "from": "usr5",
    "what": "kp"
  }
}
```

## Topic 格式

- **P2P:** `p2p_{smaller_uid}_{larger_uid}`
- **群组:** `grp_{group_id}`

## 错误码

- `200`: 成功
- `400`: 请求错误
- `401`: 未授权
- `403`: 禁止访问
- `429`: 频率限制
- `500`: 服务器错误
