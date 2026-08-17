# cf-dnspod

用于把 Cloudflare for SaaS 自定义主机名接入 DNSPod 子区，并分别管理访问 Cloudflare 的边缘入口和 Cloudflare 回源目标。

程序默认读取当前目录的 `.env`。它不会创建、修改、移动或删除该文件，进程环境变量优先于 `.env`。

## 配置

```dotenv
CF_API_TOKEN=
# 可选：Token 可访问多个 SaaS Zone 时指定默认 Zone 名称
CF_ZONE=platform.example.net

DNSPOD_SECRET_ID=
DNSPOD_SECRET_KEY=
# 可选，默认值为“默认”
DNSPOD_RECORD_LINE=默认
```

只需要一个 Cloudflare API Token，不需要配置 Zone ID 或 Fallback Host。CLI 会通过 API 自动发现：

- 托管目标域名的 Cloudflare 父 Zone；
- 已开启 Cloudflare for SaaS 且配置了有效 Fallback Origin 的 Zone；
- Fallback Origin、已有 Custom Hostname 和同名 Custom Origin DNS。

Cloudflare Token 至少需要可访问相关两个 Zone，并具备 Zone 读取、DNS 编辑和 Custom Hostnames/SSL for SaaS 编辑权限。可在 Cloudflare 控制台的 `My Profile > API Tokens` 创建。DNSPod 的 Secret ID/Key 在腾讯云控制台的 `访问管理 > API 密钥管理` 创建，账号需有对应 DNSPod 域名和记录的读写权限。

如果存在多个 SaaS Zone 候选，使用 `.env` 中的 `CF_ZONE`，或在单次命令中传 `--zone platform.example.net`；无法唯一确定时命令会停止，不会按 API 返回顺序随意选择。

## 使用

首次接入，使用 SaaS Zone 的原生 Fallback Origin 回源：

```bash
./cf-dnspod add test.example.com --wait
```

创建同名 Custom Origin：

```bash
# CNAME 到公开占位源站 example.com，便于后续用 set-backend 修改
./cf-dnspod add test.example.com --origin

# CNAME 到指定源站 Host
./cf-dnspod add test.example.com --origin=backend.example.org

# A 到指定公网 IPv4
./cf-dnspod add test.example.com --origin=1.1.1.1
```

例如父 Zone 是 `example.com`、SaaS Zone 是 `platform.example.net`，`test.example.com` 对应的 Custom Origin 名称是 `test.platform.example.net`。该记录在 Cloudflare 中启用代理，并写入 Custom Hostname 的 `custom_origin_server`。

显式源站值必须使用 `--origin=TARGET`。`--origin TARGET` 会被拒绝，避免位置参数产生歧义。

`add` 会先完成只读预检，再依次处理 DNSPod 所有权验证、子区创建、父区 NS 委派、Custom Hostname、证书 TXT 和初始 DNSPod CNAME。重复执行会复用匹配资源并恢复中断流程；如果 DNSPod 已有优选 A/AAAA/CNAME，不会把它重置为 Fallback Host。

### 更新边缘入口

`set-edge` 只修改 DNSPod 子区根记录，决定流量如何进入 Cloudflare：

```bash
./cf-dnspod set-edge test.example.com --host preferred.example.net
./cf-dnspod set-edge test.example.com --ip 1.1.1.1
```

相同目标返回 `unchanged`。它不会修改 Cloudflare Custom Origin、Fallback Origin 或 Custom Hostname。

### 更新实际源站

`set-backend` 只修改 Cloudflare 中的同名 Custom Origin DNS，并在需要时把 Custom Hostname 从 Fallback 模式切换为 Custom Origin 模式：

```bash
./cf-dnspod set-backend test.example.com backend.example.org
./cf-dnspod set-backend test.example.com 1.1.1.1
```

它不会修改 DNSPod 入口、NS、TXT 或 Zone 状态。

### 查看状态

```bash
./cf-dnspod status test.example.com
```

`status` 只读，并在 JSON 中分开报告：

- `edge`：DNSPod 当前 A/AAAA/CNAME 入口；
- `backend`：Cloudflare 当前使用原生 Fallback，还是同名 Custom Origin 及其 DNS 记录。

### 预览变更

```bash
./cf-dnspod add test.example.com --origin=backend.example.org --dry-run
./cf-dnspod set-edge test.example.com --host preferred.example.net --dry-run
./cf-dnspod set-backend test.example.com backend.example.org --dry-run
```

## 安全检查

所有写命令都先验证完整域名、Cloudflare Zone、Fallback Origin、DNSPod Zone、公共 NS 委派、Custom Hostname、目标解析和现有记录。发现以下情况时会在写入前停止：

- 无法唯一选择 SaaS Zone；
- 目标主机名已有冲突记录、应用托管记录或非 DNSPod NS；
- DNSPod 存在跨线路或多条入口记录；
- Custom Origin 记录不唯一或与请求模式冲突；
- Host/CNAME 形成回路，或 IP 不是全局可路由 IPv4。

`--replace-stale-ns` 仅允许替换目标完整域名上过期的 NS，不会授权删除其他冲突记录。

## 构建

需要 Go 1.25 或更高版本：

```bash
go test ./...
go test -race ./...
go vet ./...
make build
make smoke
```

发布产物：

```text
dist/cf-dnspod-linux-amd64
dist/cf-dnspod-linux-arm64
dist/cf-dnspod-darwin-arm64
```

三个目标均使用 `CGO_ENABLED=0`。GitHub Actions 在每次 push/PR 时执行测试、race test、vet 和构建；`main` 使用 Conventional Commits 与 `go-semantic-release` 发布版本和校验文件。

退出码：成功或 `unchanged` 为 `0`，远端状态/预检错误为 `1`，参数或配置错误为 `2`。Token、Secret 和验证 TXT 值不会写入正常 JSON 或错误输出。
