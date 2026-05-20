# CatsCompany 腾讯云迁移清单

本文记录从旧服务器迁移到腾讯云广州 CVM + PostgreSQL 时需要保留的服务、目录和检查点。

## 目录约定

正式部署目录必须继续匹配现有 GitHub Actions：

- 测试环境：`/srv/catscompany-test`
- 生产环境：`/srv/catscompany-prod`

临时目录只能用于迁移预演，不要写入 CI/CD：

- 迁移预演：`/srv/catscompany-shadow`
- 备份文件：`/srv/cats-backups`

工具和密钥目录：

- 工具脚本：`/opt/catscompany/bin`
- 服务器本地密钥：`/opt/catscompany/secrets`

## CI/CD 约定

旧服务器和新服务器都应该保持同一套部署模型：

1. GitHub Actions 构建 `cats-company-server` 和 `cats-company-web` 镜像。
2. 镜像推送到 GHCR。
3. 服务器只执行 `docker compose pull server web` 和 `docker compose up -d`。

不要在生产服务器上本地构建正式镜像。服务器本地构建只允许用于 shadow
预演，因为它会依赖 Docker Hub 基础镜像和网络状态。

旧服务器当前线上镜像形态：

- `ghcr.io/buildsense-ai/cats-company-server:<commit-sha>`
- `ghcr.io/buildsense-ai/cats-company-web:<commit-sha>`

## 服务器间同步

大文件迁移不要经过本机。腾讯云服务器上保留了以下运维工具：

- `/opt/catscompany/bin/sync-uploads-from-old-prod`
- `/opt/catscompany/bin/pull-mysql-backup-from-old-prod`
- `/opt/catscompany/bin/refresh-shadow-from-backup`

这些脚本使用腾讯云服务器上的临时迁移 key：

- `/opt/catscompany/secrets/cats-prod-sync_ed25519`

旧服务器只允许腾讯云公网 IP 使用这把 key 登录。正式迁移完成后应删除旧服务器
`authorized_keys` 中包含 `cats-prod-sync-20260520` 的条目，并删除腾讯云上的
这把临时 key。

## Shadow 验证状态

腾讯云 shadow 当前用途：

- CatsCompany web/server：临时运行在 `/srv/catscompany-shadow`
- PostgreSQL shadow schema：`cats_shadow_20260520`
- advanced-reader：临时运行在 `/srv/advanced-reader-prod`

已验证：

- CatsCompany shadow web/API 健康检查正常。
- PostgreSQL shadow schema 已可承载历史用户和消息数据。
- `/uploads/...` 文件已从旧服同步，并可通过 shadow web 返回 `200 OK`。
- advanced-reader 绑定 `172.17.0.1:28110`，不对公网开放。
- CatsCompany 容器可通过 `http://host.docker.internal:28110` 访问 advanced-reader。

## 必须迁移

- CatsCompany web + server 容器
- PostgreSQL 数据库数据
- `/srv/catscompany-prod/data/uploads` 下的用户上传图片、文件和反馈截图
- Nginx 路由：`app.catsco.cc`、`api.catsco.cc`、`catsco.cc`、`www.catsco.cc`
- WebSocket 路由：`/v0/channels`
- 上传文件路由：`/uploads/`
- 静态/H5 路由：`/static`、`/h5/`
- HTTPS 证书和续期任务
- 腾讯云 SES 邮件配置
- 飞书反馈通知配置
- 企业微信相关环境变量（如果仍保留对应入口）
- 上传文件清理任务
- 数据库备份任务
- advanced-reader 内部服务，供图片/文件阅读代理使用

## 建议一起迁移

- cats-relay / Bifrost 中转相关服务
- Prometheus / Grafana / node-exporter / cadvisor / blackbox-exporter 监控栈
- 旧服务器上的 `/var/www/html/h5` 静态页面

## 迁移步骤

1. 在腾讯云 PostgreSQL 建库并确认 CVM 内网可访问。
2. 从旧 MySQL 导出数据备份。
3. 使用 `server/cmd/dbmigrate` 做 `dry-run-copy` 到临时 schema。
4. 迁移并校验上传目录。
5. 在腾讯云启动 shadow stack，只绑定本机端口。
6. 通过本地 SSH tunnel 或临时 Nginx 内网路由验收 shadow stack。
7. 合并 PostgreSQL 适配代码，等待 GitHub Actions 生成正式镜像。
8. 更新 GitHub Actions secrets 指向腾讯云 CVM。
9. 先部署 `/srv/catscompany-test`，再部署 `/srv/catscompany-prod`。
10. 切 DNS 到腾讯云公网 IP。
11. 观察登录、注册验证码、聊天、上传、反馈、CatsCo 桌面端连接和在线状态。
12. 确认稳定后冻结旧服务器写入，再做最终增量迁移或停旧服务。

## 切换前检查

- `go test ./server/...` 通过。
- `docker compose -f deploy/test/docker-compose.yml --env-file deploy/test/env.test.example config` 通过。
- `docker compose -f deploy/prod/docker-compose.yml --env-file deploy/prod/env.prod.example config` 通过。
- PostgreSQL 行数与旧 MySQL 对齐。
- 旧数据中的非法 JSON、NUL 字符、外键缺失已被迁移工具处理或报告。
- 新服务器只开放 SSH、HTTP、HTTPS。
- PostgreSQL 只允许 VPC 内网访问。
- Nginx `client_max_body_size` 和后端上传限制一致。
- 证书续期命令在新服务器上可执行。
