# cf-dnspod Go CLI

这是 Cloudflare for SaaS + DNSPod 子区接入 CLI。命令分为首次接入、入口更新和只读查询，避免重复设置优选 Host/IP 时重跑完整接入流程。

## 配置

程序默认只读当前工作目录的 `.env`，不会创建、改写、移动或删除该文件。进程环境变量优先于 `.env` 中的同名值。

```dotenv
CF_API_TOKEN=
CF_PARENT_ZONE_ID=
CF_PARENT_ZONE_NAME=example.com
CF_SAAS_ZONE_ID=
CF_FALLBACK_HOST=
DNSPOD_SECRET_ID=
DNSPOD_SECRET_KEY=
DNSPOD_RECORD_LINE=默认
```

一个 Cloudflare Token 同时访问两个 Zone ID：

- `CF_PARENT_ZONE_ID`：`example.com` 所在的权威父区，用于 DNSPod 子区的 NS 委派及 DNSPod 归属验证 TXT。
- `CF_SAAS_ZONE_ID`：启用了 Cloudflare for SaaS 的 Zone，用于 Fallback Origin 和 Custom Hostname。
- `CF_FALLBACK_HOST`：SaaS Zone 中当前唯一且状态为 `active` 的 Fallback Origin 主机名。

`add` 需要全部变量。`set-edge` 和 `status` 不要求 `CF_FALLBACK_HOST`，但仍会验证 Parent Zone、SaaS Zone、DNSPod Zone 和 Custom Hostname 的身份/状态。

## 命令

首次添加或恢复中断的接入流程：

```bash
./cf-dnspod add --subdomain custom --wait
```

`add` 不接受优选 Host/IP。完整只读预检通过后，它才会按顺序处理 DNSPod 归属验证、子区、父区 NS 委派、子区启用、Custom Hostname、证书 TXT 和初始 Fallback CNAME。

已经存在的 DNSPod 根 A/AAAA/CNAME 会被保留，`add` 不会把已设置的优选入口改回 Fallback Origin。

Custom Hostname 与 SSL 激活后，仅更新 DNSPod 入口：

```bash
./cf-dnspod set-edge --subdomain custom --host preferred.example.com
./cf-dnspod set-edge --subdomain custom --ip 1.1.1.1
```

`set-edge` 先检查 DNSPod Zone、公共 NS 委派、Custom Hostname/SSL、目标解析和现有入口记录。通过后最多执行一次 DNSPod A/CNAME 创建或修改；相同目标返回 `unchanged`，不会写入。它不会修改 Cloudflare、NS、TXT、Fallback Origin 或 Custom Hostname。

查看状态：

```bash
./cf-dnspod status --subdomain custom
```

预览写操作：

```bash
./cf-dnspod add --subdomain custom --dry-run
./cf-dnspod set-edge --subdomain custom --host preferred.example.com --dry-run
```

## 预检阻断

所有命令先验证配置和相对子域名。`add` 在完整主机名上发现 A、AAAA、CNAME、Workers/应用托管记录、其他不兼容记录或非 DNSPod 的 NS 委派时停止且不写入。Zone ID/name/status、唯一 Fallback Origin 或供应商响应不一致时也会停止。

`--replace-stale-ns` 只授权替换该完整主机名上与当前 DNSPod Zone 不匹配的 NS，不授权删除 A、AAAA、CNAME、Workers/应用托管记录或其他类型记录。

`set-edge` 要求 DNSPod Zone 已启用、至少两个分配 NS 已在公网委派、Custom Hostname 和 SSL 均为 `active`。Host 必须可解析且 CNAME 链不能循环或指回被管理主机；IP 必须是全局可路由 IPv4。跨线路或多条入口记录会被视为歧义并停止。

## 构建与验证

```bash
go test ./...
go test -race ./...
go vet ./...
make build
make smoke
```

产物：

```text
dist/cf-dnspod-linux-amd64
dist/cf-dnspod-linux-arm64
dist/cf-dnspod-darwin-arm64
```

三个目标均使用 `CGO_ENABLED=0`、`-trimpath` 和剥离符号的发布参数。Linux 两个版本是静态 ELF；macOS arm64 是无需额外 Go 运行时或第三方动态库的单文件 Mach-O。

成功和 `unchanged` 返回退出码 `0`，预检/供应商/超时错误返回 `1`，参数或配置错误返回 `2`。正常 JSON 和错误信息都不会输出 Cloudflare Token、DNSPod Secret 或 TXT 验证值。

## CI 和 Release

GitHub Actions 会在每次 push 和 pull request 时执行测试、race test、vet 及三平台构建。`main` 分支使用 Conventional Commits 和 `go-semantic-release` 自动发布语义版本。

每个 GitHub Release 包含 Linux amd64、Linux arm64、macOS arm64 三个二进制文件以及 `SHA256SUMS`。本地 `.env` 被 Git 忽略，不会进入仓库或构建产物。
