# CoralBay Rules

自托管的订阅转换、分流配置与规则镜像平台。提供原始订阅合并、客户端配置生成、固定订阅链接、PPanel 模板，以及相互独立的 **666OS 规则镜像**和 **MetaCubeX 自定义分流**。

**当前版本：4.13.0** · Linux amd64 / arm64 · Docker Compose 部署

[搭建与升级指南](docs/deployment.md) · [GitHub Releases](https://github.com/sexyfeifan/Coralbay-Rules/releases) · [Docker Hub](https://hub.docker.com/r/sexyfeifan/coralbay-rules) · [4.13.0 更新说明](RELEASE-4.13.0.md)

项目使用你已有的代理订阅，不提供代理节点。部署和使用不依赖 PPanel 或 Nextin；PPanel 用户可另外使用模板功能。

## 功能选择

| 你想完成的事 | 使用入口 | 交付内容 |
| --- | --- | --- |
| 合并、筛选、重命名节点，转换为不同客户端格式 | 订阅服务 → 普通订阅转换 | 可更新的签名 `/sub` 链接 |
| 让 AI、视频、社交等服务使用不同节点或策略 | 订阅服务 → 自定义分流订阅 | Mihomo / OpenClash / Stash 的完整分流配置链接 |
| 查看已有链接、停用或恢复、查看拉取记录 | 订阅服务 → 订阅管理 | 普通转换与自定义分流分别管理 |
| 给 PPanel 配置客户端订阅模板 | 客户端配置 → PPanel 模板 | 供 PPanel 渲染的 `.gotmpl` 模板 |
| 为 OpenClash 使用 MihomoPro 覆写 | 客户端配置 → MihomoPro 覆写 | 覆写文件与使用说明 |
| 镜像 666OS 的规则文件、查看和同步可读源 | 规则与配置源 → 666OS 规则资源 | Mihomo、sing-box、Surge 原生产物和转换产物 |
| 查看新分流规则的内容、版本与上游来源 | 规则与配置源 → MetaCubeX 分流源 | 78 项独立规则目录与同步状态 |

桌面使用分组侧栏，手机使用抽屉。默认登录入口为 `/`；新分流页面为 `/routing`，规则源页面为 `/routing#sources`。原有 hash 页面入口继续有效，`/admin/` 跳转至 `/`。

## 快速搭建

准备一台 Linux x86_64 / ARM64 服务器、Root 或 sudo 权限、正在运行的 Docker Engine 和 Docker Compose 插件，以及 Bash、curl、coreutils、awk。域名需要解析到你的服务器，并由已有 Nginx / OpenResty 或反代面板提供 HTTPS。详细前提及 Docker 官方安装链接见[搭建指南](docs/deployment.md)。

下文 `rules.example.com` 均为示例，请替换成你自己的域名。

在服务器的交互式 SSH 终端执行：

```bash
(
  set -eu
  coralbay_installer="$(mktemp)"
  trap 'rm -f "$coralbay_installer"' EXIT
  curl -fsSL --retry 3 --connect-timeout 15 --max-time 120 \
    "https://raw.githubusercontent.com/sexyfeifan/Coralbay-Rules/main/install.sh?t=$(date +%s)" \
    -o "$coralbay_installer"
  bash -n "$coralbay_installer"
  sudo bash "$coralbay_installer" install
)
```

此命令直接进入安装问答，默认安装公开的 `sexyfeifan/coralbay-rules:latest` 镜像。安装者无需 Fork 仓库、GitHub Secrets 或 Docker Hub 登录。

按提示填写：

| 配置 | 说明 |
| --- | --- |
| 安装目录 | 默认 `/opt/coralbay-rules`；使用独立子目录 |
| 规则域名 | 必填，例如 `rules.example.com`，不带 `https://` 或路径 |
| 本地端口 | 默认 `3999`；容器仅绑定宿主机 `127.0.0.1`，遇到占用可更换 |
| 666OS 同步间隔 | 默认 6 小时；与新 MetaCubeX 规则同步独立 |
| 管理员密码 | 首次安装至少 12 位，隐藏输入并二次确认；允许字母、数字及 `._@+-` |

随后在已有 HTTPS 站点中，将域名反向代理至 `http://127.0.0.1:3999`（如果更换端口，相应调整）。安装器不接管 80/443，也不申请或续期证书。反代配置、容器化反代注意事项及超时设置见[完整搭建指南](docs/deployment.md)。

访问 `https://你的域名/`，使用安装时设置的密码登录。首次 666OS 同步完成后可使用镜像和模板；MetaCubeX 首次启动自动补齐 78 项本地原始文件，之后独立每日同步，也可在源页手动同步。

## 已安装用户升级

升级前按[完整备份步骤](docs/deployment.md#备份与回退)保留 `.env`、`compose.yaml` 和整个 `data/`。安装脚本自带的备份仅覆盖配置文件，不包含订阅数据库、密钥和规则数据。

升级到 `latest`：

```bash
sudo env CORALBAY_IMAGE=sexyfeifan/coralbay-rules:latest rules update
```

固定到本次发布的 4.13.0：

```bash
sudo env CORALBAY_IMAGE=sexyfeifan/coralbay-rules:4.13.0 rules update
```

`rules update` 会下载管理脚本并更新 Compose 服务，保留已有密码、密钥和数据。它默认沿用 `.env` 中的镜像设置：如果原来固定了旧版本，单独执行 `rules update` 不会自动切换到新标签，需要像上面一样显式指定 `CORALBAY_IMAGE`。自定义目录、旧快捷命令不可用时的升级方式见[搭建指南](docs/deployment.md)。

启动检查失败时会保留或恢复原配置并报错，已拉取的镜像不会自动回退。已有安装日常升级使用 `update`；`install` 用于首次安装或重新配置，重新选择的同步间隔会覆盖网页保存的设置。

## 日常管理

安装后执行 `sudo rules` 或 `sudo 666` 打开中文管理菜单，也可直接运行：

| 命令 | 作用 |
| --- | --- |
| `sudo rules status` | 查看容器和 666OS 规则状态 |
| `sudo rules logs` | 查看服务日志 |
| `sudo rules sync` | 立即同步旧 666OS 规则；MetaCubeX 在独立规则源页面同步 |
| `sudo rules template` | 获取 PPanel 模板地址 |
| `sudo rules password` | 修改管理员密码 |
| `sudo rules certificate` | 检测公网 HTTPS，证书由现有反代管理 |
| `sudo rules update` | 按当前镜像设置升级管理脚本和容器 |

安装目录会记在 `/etc/coralbay-rules/install-dir`。命令中的目录提示默认使用该目录，也可用 `CORALBAY_INSTALL_DIR` 明确指定。卸载、故障排查和恢复说明见[搭建指南](docs/deployment.md)。

## 自定义分流订阅

独立分流页面把原始订阅中的节点与自选策略重新组合，生成可持续更新的完整配置链接。4.13.0 增加规则本机镜像、上游来源选择和资源详情；范围与验证边界见 [4.13.0 发布说明](RELEASE-4.13.0.md)。

1. 打开 `/routing`，输入方案名称和 1–8 个原始 HTTP(S) 订阅链接。
2. 选择 Mihomo、OpenClash 或 Stash，选择规则下载来源（默认 CoralBay 本机），勾选 AI、流媒体、社交、开发等规则。
3. 设置全局地区、包含/排除正则；每个业务规则可继承全局、指定自己的节点筛选与策略，或选择直连/拦截。代理组支持手选、自动测速与故障转移。
4. 预览匹配节点、策略组、规则顺序和完整 YAML。空组、未知字段或无法保持语义的协议参数会阻止发布。
5. 保存后获得 `/routing/sub/{token}/{client}` 固定链接，可复制、下载或生成二维码。编辑方案后，用户更新同一地址取得新配置；停用和重置令牌作用于新模块自己的链接。

当前输入与输出范围：

| 项目 | 支持范围 |
| --- | --- |
| 完整配置输出 | Mihomo / OpenClash、Stash 的 YAML，包含节点、策略组、DNS 骨架和规则集合；旧方案保留原生内嵌规则 |
| 原生 YAML 输入 | 直接包含 `proxies` 的 SS、VMess、VLESS、Trojan、Hysteria2、TUIC、HTTP、SOCKS5 节点；字段按协议与目标客户端校验 |
| URI / Base64 输入 | SS、VMess、VLESS、Trojan、Hysteria2 / `hy2` 的受支持参数；尚不接受的扩展会明确报错，可改用兼容的原生 YAML |
| 输入边界 | 不展开远程 `proxy-providers`；不继承上游配置里的规则、DNS、脚本与策略组。单个订阅最多 8 MiB，合并最多 5,000 个节点 |
| 客户端边界 | 不输出 Surge、Loon、sing-box 等其他软件的完整分流配置；Stash 未验证的特定字段会拒绝输出，尚未做 Stash 真机验收 |

新规则目录直接同步 [MetaCubeX/meta-rules-dat](https://github.com/MetaCubeX/meta-rules-dat) 的 78 项来源；域名采用 `geo/geosite/classical/`，IP 采用 `geo/geoip/`，保留精确域名、后缀、关键词、正则与 IPv4/IPv6 语义。新方案通过固定版本规则集合交付，可选择 CoralBay 本机或同提交的上游文件；旧内嵌方案保持兼容，不依赖 Nextin 或旧 666OS 规则服务。BT Tracker 是 BT 跟踪服务器集合，默认代理，未纳入推荐广告拦截。来源映射、许可与实际文件验证见 [来源说明](docs/reference/metacubex-routing-source-notice.md)。

新方案和交付统计保存到 `$DATA_DIR/routing/routing.sqlite`，新规则快照保存到 `$DATA_DIR/routing/rules`。旧 `/sub`、旧订阅历史、`$DATA_DIR/current` 和 666OS 同步/回滚目录沿用原行为。导航中的订阅管理分别展示普通转换和自定义分流记录；整理导航不会迁移旧数据。

客户端拉取时，新配置构建缓存为 5 分钟，同一方案的并发刷新合并执行；节点刷新失败返回错误并保留已存输出，不把旧节点配置静默当成刷新成功。失败后有 30 秒重试冷却。MetaCubeX 独立每日同步，也可手动同步；严格本机方案构建与本地详情只读已发布副本，不因缓存过期临时联网。旧内嵌方案保留原有按需读取策略；每个候选快照固定到同一提交并通过 SHA-256 校验后原子发布。规则更新失败时继续使用本模块上一份有效快照并显示警告，不回退到旧 666OS 来源。客户端建议更新间隔与服务器的 5 分钟构建缓存分别设置。

地区识别依据节点名称，不是实际出口测量或服务解锁检测。配置结构校验不能替代各客户端中的连接、路由命中和订阅更新验收。

## 规则本机副本与上游来源（4.13.0）

- **资源页管理内容**：666OS 与 MetaCubeX 分别显示本地版本、上游检查、文件大小与摘要；详情支持本地／上游切换、搜索和分页。查看上游不会修改活动本地版本，同步失败保留已有资源。
- **订阅页选择使用方式**：新分流方案默认从 CoralBay 下载固定版本 YAML 集合，也可选择同提交的上游地址。两种方式保留相同规则、策略和优先级；缺少本地原始文件时需先同步，不静默回退其他来源。
- **PPanel 模板独立选择**：Clash（Mihomo 内核）、Mihomo、OpenClash、Stash 的原始 MRS 规则模板提供本机／上游变体，原共享模板地址保持原义。转换产物没有等价上游文件的客户端不提供该切换。
- **配置来源单独说明**：普通转换的 INI 本机镜像只代表配置文件已缓存。预设详情显示声明的本机、外部、缺失或未知规则依赖；不会自动下载全部嵌套依赖。

“本机”指 CoralBay 服务器。完成首次同步后，上游不可达不影响本机规则读取；节点订阅刷新和客户端访问 CoralBay 仍需网络。固定资源分别保存在 `data/rule-resources/666os` 与 `data/routing/rules/releases`，当前不自动清理已交付引用，备份需包含整个 `data/` 并关注磁盘用量。

旧方案缺少来源设置时继续内嵌；主动切换并成功保存后，已有分流交付链接保持不变。普通转换签名链接则须重新生成才能改变其参数。Stash 集合按官方格式输出并完成结构与引用检查，尚未完成 Stash 真机加载与命中验收。

## 本地图标镜像

PPanel 模板使用的 27 个 Qure 策略组图标已经固化在 Docker 镜像中。同步时会复制到 `/_assets/icons/`，并自动把生成模板里的 GitHub 图标地址替换为当前规则域名，例如：

```text
https://rules.example.com/_assets/icons/Auto.png
```

这些图标只用于 OpenClash/Mihomo 面板展示，不参与节点测速或流量分流。图标来源：[Koolson/Qure](https://github.com/Koolson/Qure)，固定于上游提交 `b16b260625f873266f6a6a9b88710132774997b8`。

## 客户端模板中心

控制台提供 Perfect Panel 当前 15 类客户端模板的在线链接、复制和下载，并显示名称、User-Agent、输出格式、URL Scheme 与模板地址。

- Clash/Mihomo/OpenClash 与 Stash 使用本地 33 个 MRS 镜像。
- Surge、Loon、Surfboard、Egern 使用由 666OS `geo` 可读源生成的原生文本 RULE-SET，并映射 Pro_cn 策略分组。
- Hiddify 与 sing-box 1.11–1.14 已将第三方核心规则 URL 替换为本项目生成的 source-format JSON；完整 Pro_cn 分组仍以独立状态显示，不会伪装成全部完成。
- Shadowrocket、Quantumult X、Quantumult 和通用订阅保留纯节点输出，因为 Perfect Panel 的这些模板本身没有完整路由层。

Stash 模板沿用 Perfect Panel 的节点渲染逻辑，并将策略分组、地区筛选、自动测速、负载均衡、规则路由与 providers 完整改造为 666OS Pro_cn 结构。Stash 官方文档确认支持 `include-all`、`filter`、`url-test`、`load-balance`，以及 `domain` 和 `ipcidr` 行为的 MRS 规则集。

模板来源代码按其 MIT 许可证保存在 `templates/clients/perfect-panel/`。

## 跨客户端规则转换器

控制台同时列出 666OS `release` 分支的三套原生产物：`/mihomo/`、`/singbox/` 和 `/surge/`。这些文件保持上游格式直接镜像；页面可按平台、名称和后缀筛选并复制链接。

OpenClash 的 `MihomoPro_overwrite.conf` 会在每次同步时从 666OS/YYDS 下载到 `/_templates/MihomoPro_overwrite.conf`，避免客户端运行时依赖 GitHub。

## 订阅链接转换

Compose 内置自托管的 `subconverter-ng` 后端，但不直接暴露其端口。控制台把“PPanel 模板”和“订阅转换”设为两个独立入口：前者下载或复制 `.gotmpl` 供 PPanel 管理后台使用；后者把现有订阅转换成客户端可定期更新的签名地址，二者不能互换。

订阅转换支持最多合并 5 个 HTTP/HTTPS 地址，并输出 Clash/Mihomo、ClashR、sing-box、Surge、Surfboard、Shadowrocket、Quantumult/Quantumult X、Loon、V2Ray、SS、SSR、Trojan 和混合 URI。可配置包含/排除正则、节点重命名、远程配置预设、更新间隔、Emoji、排序、去重、UDP/XUDP、TFO、TLS 1.3、证书校验、仅节点输出、规则展开、DoH 和客户端专属参数。CoralBay 会先实际转换并检查可用节点数量，验证通过才生成 HMAC 签名的 `/sub` 地址；同时提供二维码、签名链接反向解析和管理员生成历史。不兼容目标（例如全 VLESS Reality 订阅转 Surge）会明确拒绝，不发布空配置。

远程配置库收录 `sub-web-modify` 当前维护的 88 条配置，按“通用、ACL、全网搜集、各大机场、特殊”分组，其中全网搜集配置 32 条。默认调用原始链接，也可切换到 CoralBay 本机镜像。应用启动时会在后台并发刷新缓存，管理员也可手动更新；上游失效时保留最后一次成功文件并显示最近错误，不会用失败响应覆盖可用缓存。

配置库顶部另有 CoralBay 内置的 `MihomoPro · 666OS Pro_cn` 预设，用于把其他来源的节点订阅转换为带地区自动选择、故障转移、业务策略组和本机 666OS 规则镜像的 Mihomo 配置。该预设固定使用本机配置，不依赖第三方远程配置地址。

转换后端只在 Compose 内网提供服务，订阅不会发送给公共转换站；项目不提供公共短链接，也不执行用户提交的任意 JavaScript。

## 666OS 原生规则转换产物

同步服务会调用独立的 `coralbay-ruleconvert`，从同一次 666OS `geo` 快照生成两种可审计产物：

```text
/_converted/native/list/{site|ip}/*.list
/_converted/native/sing-box/{site|ip}/*.json
/_converted/manifest.json
```

转换过程会去除空行、注释与重复项，将 `+.` 域名转换为 `DOMAIN-SUFFIX`，将精确域名转换为 `DOMAIN`，并区分 IPv4 `IP-CIDR` 与 IPv6 `IP-CIDR6`。sing-box JSON 固定使用兼容 1.11–1.14 的 source rule-set version 3。

`release` 中少量只有 MRS、在 `geo` 分支没有公开可逆源的分类会生成零条目的合法占位文件，并在转换清单中明确显示 `0`，绝不会用空规则对象误匹配全部流量。
清单同时记录每个 `.list` 和 JSON 产物的 SHA-256，可用于下载完整性检查。

## 可读规则详情

同步 `release` 分支 MRS 的同时，项目会同步 666OS `geo` 分支的可读源。控制台可展开 Google 等存在映射的规则并搜索全部条目；未公开对应可读源的 MRS 会明确显示“暂无公开可读源”。

## PPanel 模板修改

每次规则同步都会同时生成一份已经替换为当前镜像域名的 PPanel 模板：

```text
https://rules.example.com/_templates/ppanel_openclash_pro_cn.gotmpl
```

管理菜单可以显示并检测该下载链接。下载文件后，由用户在 PPanel 客户端管理页面手动替换 `OpenClash Pro` 的订阅模板。

网页下载按钮会保存为 UTF-8 文件 `CoralBay_OpenClash_PPanel_Template.yaml`；它仍然是 PPanel 模板，需要由 PPanel 渲染后才是最终 OpenClash 配置。

将：

```text
https://github.com/666OS/rules/raw/release/
```

替换为：

```text
https://rules.example.com/
```

建议继续保留代理下载兜底：

```yaml
x-rule-set-domain: &rule-set-domain
  type: http
  behavior: domain
  format: mrs
  interval: 86400
  proxy: 全球手动

x-rule-set-ipcidr: &rule-set-ipcidr
  type: http
  behavior: ipcidr
  format: mrs
  interval: 86400
  proxy: 全球手动
```

## 开发与镜像发布

直接部署公开镜像不需要下面的配置。以下步骤用于维护本项目或发布自己的 Fork：

- `main` 推送与 Pull Request 运行 GitHub CI，包括 Go 竞态测试、格式和静态检查、Shell 回归、前端语法及 Docker 构建。
- Docker 工作流由 `v*` 标签或手动触发。发布者需要设置 `DOCKERHUB_USERNAME` 和具有目标仓库写入权限的 `DOCKERHUB_TOKEN`；不要把凭据写进代码或 `.env` 示例。
- Fork 发布时，修改 `.github/workflows/docker.yml` 中的镜像命名空间，并相应调整安装器默认镜像或通过 `CORALBAY_IMAGE` 指定自己的镜像。
- 版本标签必须匹配 Dockerfile 的 `ARG VERSION`，例如 `v4.13.0`。工作流支持 Linux amd64 / arm64，并生成 SBOM、provenance 与 OCI 来源标签。
- Docker 工作流不会创建 GitHub Release。完成镜像发布后另外创建正式 Release，控制台通过 GitHub Releases 查询新版本。

本地开发验证：

```bash
go test -race ./...
go vet ./...
bash -n install.sh
sh -n sync.sh
bash tests/install_test.sh
bash tests/sync_test.sh
node --check web/app.js
node --check web/login.js
node --check web/routing.js
```

`sync_test.sh` 需要 Linux 的 `flock`；macOS 上的跳过不能代替 Linux 回归。当前实现与运行边界见各版本记录及[规则来源说明](docs/reference/metacubex-routing-source-notice.md)。

## 可靠性

### v4.11.4 Stash TLS 修复

修复 Stash 模板的 VLESS 分支漏掉 `tls: true`：TLS 和 Reality 节点会显式启用 TLS，普通 VLESS 保持原样。原始模板和 CoralBay 适配版都会在同步后更新。详见 [v4.11.4 实施报告](REVIEW-4.11.4.md)。

### v4.11.3 修复

- 同步和回滚共用内核文件锁；进程异常退出后自动释放，旧 `.sync.lock` 目录不再阻塞更新。
- 修复登录限流可通过更换 TCP 连接绕过的问题；不会信任未经验证的转发 IP 请求头。
- 已停用或删除的订阅重新生成相同参数时返回明确提示，要求先恢复，不交付无法拉取的链接。
- Clash/Stash 节点计数使用 YAML 解析，兼容行内列表、无缩进列表和不同字段顺序。
- 独立同步循环在上游下载失败时立即中止该次发布；版本或域名变化后主动同步。
- 安装脚本不再执行 `.env` 内容，支持安全重配置、目录记忆和实际启动检查。
- 详细验证和发布边界见 [v4.11.3 实施报告](REVIEW-4.11.3.md)。

### v4.11 链接管理与安全升级

- 订阅管理 → 普通转换：查看拉取次数、筛选分页、停用/恢复、更新签名和服务器连通性抽测。
- 清除历史不会撤销订阅；停用阻止未来拉取，不能撤回已下载配置。
- 新链接独立 v2 签名；升级前旧链接兼容 90 天，请在管理页“更新签名”后复制到客户端。
- 新的出站隔离需要升级安装脚本及 Compose，不是只更新 app 镜像。
- 详细迁移、验证边界及备份说明见 [v4.11.0 实施报告](REVIEW-4.11.0.md)。

- 验证 PPanel Pro_cn 使用的全部 33 个 MRS 文件。
- 发布 ID 由 release 提交、geo 提交、生成器版本和域名配置摘要组成；历史产物不可变。
- 新版本在独立 staging 中完整通过校验后，通过临时链接原子切换。
- 面板、计划任务和命令行共享跨进程同步锁，避免并发写入。
- GitHub 同步失败时保留旧版本。
- 保留当前版本和最近两个历史版本。
- 默认每六小时同步，可在安装菜单中修改。
- 支持 Linux x86_64 和 ARM64。
- 活动日志和管理操作审计持久化到 `/data`，容器重启后仍可追踪。
