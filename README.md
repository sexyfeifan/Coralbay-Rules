# CoralBay Rules

自托管的订阅转换、分流配置与规则镜像平台。提供原始订阅合并、客户端配置生成、固定订阅链接、PPanel 模板，以及相互独立的 **666OS 规则镜像**和 **MetaCubeX 自定义分流**。

**代码版本：4.15.0** · Linux amd64 / arm64 · Docker Compose 部署

本页描述 4.15.0 的代码能力。可用版本以 [GitHub Releases](https://github.com/sexyfeifan/Coralbay-Rules/releases) 与 Docker Hub 标签为准；发布新版本不会自动升级固定标签的现有服务器。

[搭建与升级指南](docs/deployment.md) · [GitHub Releases](https://github.com/sexyfeifan/Coralbay-Rules/releases) · [Docker Hub](https://hub.docker.com/r/sexyfeifan/coralbay-rules) · [更新记录](CHANGELOG.md)

项目使用你已有的代理订阅，不提供代理节点。部署和使用不依赖 PPanel 或 Nextin；PPanel 用户可另外使用模板功能。

## 功能选择

| 你想完成的事 | 使用入口 | 交付内容 |
| --- | --- | --- |
| 合并、筛选、重命名节点，转换为不同客户端格式 | 订阅服务 → 普通订阅转换 | 可更新的签名 `/sub` 链接 |
| 让 AI、视频、社交等服务使用不同节点或策略 | 订阅服务 → 自定义分流订阅 | Mihomo / OpenClash / Stash 的完整分流配置链接 |
| 查看已有链接、停用或恢复、查看拉取记录 | 订阅服务 → 订阅管理 | 普通转换与自定义分流分别管理 |
| 给 PPanel 配置客户端订阅模板 | 客户端配置 → PPanel 模板 | 供 PPanel 渲染的 `.gotmpl` 模板 |
| 为 OpenClash 使用 MihomoPro 覆写 | 客户端配置 → MihomoPro 覆写 | 来源与版本匹配的覆写文件、完整配置和预览 |
| 给妙妙屋X 导入 YYDS 模板或单项规则集 | 客户端配置 → 妙妙屋X 模板 | Clash V3 模板，以及供规则集管理手动导入的 payload YAML |
| 镜像 666OS 的规则文件、查看和同步可读源 | 规则与配置源 → 666OS 规则资源 | Mihomo、sing-box、Surge 原生产物和转换产物 |
| 查看新分流规则的内容、版本与上游来源 | 规则与配置源 → MetaCubeX 分流源 | 78 项独立规则目录与同步状态 |

桌面使用分组侧栏，手机使用抽屉。默认登录入口为 `/`；新分流页面为 `/routing`，规则源页面为 `/routing#sources`。原有 hash 页面入口继续有效，`/admin/` 跳转至 `/`。

**妙妙屋X 支持：** 独立页面 `/miaomiaowu` 提供两种入口：完整模板导入妙妙屋X“模板管理”，33 项 YYDS 规则集可按需转换后导入“规则集管理”。模板保留业务分流，节点由妙妙屋X 注入；两个入口分别选择本机镜像或上游源，均使用固定版本。这里的“本机”是 CoralBay 服务器。导入步骤、格式区别和更新方式见[妙妙屋X 使用指南](docs/miaomiaowux.md)，本版变更见 [4.15.0 发布说明](RELEASE-4.15.0.md)。

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

确认 Docker Hub 已提供 4.15.0 标签后，可使用固定版本升级命令：

```bash
sudo env CORALBAY_IMAGE=sexyfeifan/coralbay-rules:4.15.0 rules update
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

独立分流页面把原始订阅中的节点与自选策略重新组合，生成可持续更新的完整配置链接。4.14.0 统一了来源选择、资源健康状态及修复操作；版本边界见[更新记录](CHANGELOG.md)。

1. 打开 `/routing`，输入方案名称和 1–8 个原始 HTTP(S) 订阅链接。
2. 选择 Mihomo、OpenClash 或 Stash，在“分流规则来源”选择本机镜像或上游源（新方案默认本机镜像），勾选 AI、流媒体、社交、开发等规则。
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

## 四个入口统一选择分流规则来源

“本机镜像”指部署 CoralBay 的服务器，“上游源”指同一规则库、同一提交的原始文件。来源选择属于当前模板、转换请求或方案，不会修改原始节点订阅地址，也不会把 666OS 换成 MetaCubeX 的同名集合。

| 入口 | 本机镜像／上游源实际控制什么 | 切换后的操作 |
| --- | --- | --- |
| MihomoPro 覆写 | 完整配置内的 666OS MRS 下载地址；覆写下载与所选来源、版本配对的完整配置 | 复制、打开、下载或预览同一变体；重新导入所选覆写 |
| PPanel 模板 | Clash（Mihomo 内核）、Mihomo、OpenClash、Stash 改造模板内的原始 MRS 地址 | 替换 PPanel 中的模板，再更新客户端订阅；原始／改造模板版本独立选择 |
| 普通订阅转换 | 内置 MihomoPro 预设生成时读取的 666OS 原始 MRS，解码后内嵌到 Clash/Mihomo 或 Stash 结果 | 测试并生成新的签名链接；客户端随订阅取得规则正文 |
| 自定义分流订阅 | 客户端下载 MetaCubeX 固定版本 YAML 集合的位置 | 预览并保存方案，之后更新原固定订阅链接 |

普通内置转换先校验同版本 **33 份 MRS 原件**，按 MihomoPro 原有顺序完整展开实际路由引用的 **28 个集合与最终兜底**。其余集合保留为备用，不擅自加入流量规则。生成结果显示实际来源、版本、有效条目和未覆盖数量；两种来源保留相同路由语义。需要镜像内的 MRS 解码内核，缺失、损坏、解码失败或超限时明确失败，不改用其他来源。它不再依赖旧 geo 派生占位文件补足这些原始 MRS 的内容。

旧普通链接缺少 `rule_source` 时保持原来的转换、签名和规则来源；旧自定义方案缺少来源字段时继续内嵌，不自动迁移。页面保留明确的兼容选项，只有主动切换并成功生成或保存才会采用新模式。旧 MihomoPro 与 PPanel 共享地址也保持兼容含义。

支持范围有明确边界：

- PPanel 只有上述四种 MRS 模板提供等价双源；其他格式中的本机派生规则没有等价上游原件，页面会说明原因。纯节点输出不适用分流规则来源。
- 普通转换首批只为内置 MihomoPro 的 Clash/Mihomo、Stash 完整配置开放此选项，其他目标暂不支持。
- 88 份第三方 INI 的“配置文件来源”继续位于高级设置；本机缓存 INI 不代表所有嵌套规则已经镜像。页面列出已识别的本机、外部、缺失和未知依赖，未适配的第三方配置不提供虚假的双源切换。
- 本机原始规则读取不因缓存过期临时联网；节点刷新、客户端访问 CoralBay 和上游模式仍有各自的网络需求。Stash 的结构与字段检查不能代替原生客户端加载、连接和命中验收；本版尚无 Stash 真机验收结果。

## 资源状态、修复与留存

666OS 与 MetaCubeX 资源列表始终保留“本机镜像”“上游原文件”“详情”入口，缺失镜像显示同步或修复操作。详情可切换来源、搜索和分页。上游预览不改变活动本地版本；只有实际读取并校验过的两份原始文件才可比较摘要，旧规范化缓存不能充当原件。666OS 的关联 geo 可读内容单独标注，不冒充 MRS 的精确反向解析。

MetaCubeX 的“检查上游版本”“同步最新版本”“修复当前版本”分别用于检查、升级和补齐已知固定版本。修复不依赖上游分支版本检查成功，也不自动换到最新提交。资源页的“版本历史与磁盘维护”折叠区按需读取历史列表，每页 20 项，并显示规则占用、磁盘总量、可用空间和告警。历史列表是清单元信息，完整性仍须通过读取或显式修复验证。历史资源修复不切换当前版本，尚不提供完整的历史版本回退界面或离线包导入导出。

固定资源保存在 `data/rule-resources/666os/` 与 `data/routing/rules/releases/`，MRS 解码缓存位于 `data/rule-projections/`。旧 666OS 三版本清理不删除这些已交付资源，本版也不自动回收历史引用。MetaCubeX 统计包含历史与检查缓存，超过 1 GiB 或数据磁盘剩余空间低于 1 GiB／10% 时返回告警信息。需要关注整个数据目录的占用并完整备份；手动删除固定文件可能使旧配置失效。

## 本地图标镜像

PPanel 模板使用的 27 个 Qure 策略组图标已经固化在 Docker 镜像中。同步时会复制到 `/_assets/icons/`，并自动把生成模板里的 GitHub 图标地址替换为当前规则域名，例如：

```text
https://rules.example.com/_assets/icons/Auto.png
```

这些图标只用于 OpenClash/Mihomo 面板展示，不参与节点测速或流量分流。图标来源：[Koolson/Qure](https://github.com/Koolson/Qure)，固定于上游提交 `b16b260625f873266f6a6a9b88710132774997b8`。

## 客户端模板中心

控制台提供 Perfect Panel 当前 15 类客户端模板的在线链接、复制和下载，并显示名称、User-Agent、输出格式、URL Scheme 与模板地址。

- Clash/Mihomo/OpenClash 与 Stash 改造模板使用 33 个 MRS 原件，可选择本机镜像或同提交上游源；现有共享模板地址继续沿用原行为。
- Surge、Loon、Surfboard、Egern 使用由 666OS `geo` 可读源生成的原生文本 RULE-SET，并映射 Pro_cn 策略分组。
- Hiddify 与 sing-box 1.11–1.14 已将第三方核心规则 URL 替换为本项目生成的 source-format JSON；完整 Pro_cn 分组仍以独立状态显示，不会伪装成全部完成。
- Shadowrocket、Quantumult X、Quantumult 和通用订阅保留纯节点输出，因为 Perfect Panel 的这些模板本身没有完整路由层。

Stash 模板沿用 Perfect Panel 的节点渲染逻辑，并将策略分组、地区筛选、自动测速、负载均衡、规则路由与 providers 完整改造为 666OS Pro_cn 结构。Stash 官方文档确认支持 `include-all`、`filter`、`url-test`、`load-balance`，以及 `domain` 和 `ipcidr` 行为的 MRS 规则集。

模板来源代码按其 MIT 许可证保存在 `templates/clients/perfect-panel/`。

## 跨客户端规则转换器

控制台同时列出 666OS `release` 分支的三套原生产物：`/mihomo/`、`/singbox/` 和 `/surge/`。这些文件保持上游格式直接镜像；页面可按平台、名称和后缀筛选并复制链接。

MihomoPro 页面现在成对提供固定版本覆写和完整配置：`/_rule-templates/666os/{版本}/{local或upstream}/mihomopro-overwrite` 与 `mihomopro-config`。覆写只下载配对配置，并使用来源与版本专属的客户端文件名；切换后需重新导入覆写，避免继续使用旧文件。主、备用订阅按实际填写内容建立节点提供者；旧 `/_templates/MihomoPro_overwrite.conf` 与 `/_templates/MihomoPro.yaml` 地址仍保留。

## 订阅链接转换

Compose 内置自托管的 `subconverter-ng` 后端，但不直接暴露其端口。控制台把“PPanel 模板”和“订阅转换”设为两个独立入口：前者下载或复制 `.gotmpl` 供 PPanel 管理后台使用；后者把现有订阅转换成客户端可定期更新的签名地址，二者不能互换。

订阅转换支持最多合并 5 个 HTTP/HTTPS 地址，并输出 Clash/Mihomo、ClashR、sing-box、Surge、Surfboard、Shadowrocket、Quantumult/Quantumult X、Loon、V2Ray、SS、SSR、Trojan 和混合 URI。可配置包含/排除正则、节点重命名、远程配置预设、更新间隔、Emoji、排序、去重、UDP/XUDP、TFO、TLS 1.3、证书校验、仅节点输出、规则展开、DoH 和客户端专属参数。CoralBay 会先实际转换并检查可用节点数量，验证通过才生成 HMAC 签名的 `/sub` 地址；同时提供二维码、签名链接反向解析和管理员生成历史。不兼容目标（例如全 VLESS Reality 订阅转 Surge）会明确拒绝，不发布空配置。

远程配置库收录 `sub-web-modify` 当前维护的 88 条配置，按“通用、ACL、全网搜集、各大机场、特殊”分组，其中全网搜集配置 32 条。默认调用原始链接，也可切换到 CoralBay 本机镜像。应用启动时会在后台并发刷新缓存，管理员也可手动更新；上游失效时保留最后一次成功文件并显示最近错误，不会用失败响应覆盖可用缓存。

配置库顶部另有 CoralBay 内置的 `MihomoPro · 666OS Pro_cn` 预设，提供地区自动选择、故障转移和业务策略组。配置结构由本机提供；4.14.0 可为 Clash/Mihomo 或 Stash 明确选择生成时的分流规则来源，并把原始 MRS 的实际路由内容内嵌到结果。旧链接未指定来源时仍走原有规则链路。

转换后端只在 Compose 内网提供服务，订阅不会发送给公共转换站；项目不提供公共短链接，也不执行用户提交的任意 JavaScript。

普通转换后端响应上限为 16 MiB，远程 INI 缓存上限为 2 MiB；超过上限会拒绝，不能把截断后恰好可解析的片段当成完整结果。无效远程配置不会替换最后有效缓存，缓存同步避免并发临时文件覆盖。原始 MRS 的单份解码文本限制为 32 MiB，解码缓存按原件摘要保存并校验内容。完整内嵌规则可能明显增大订阅文件，仍受输出大小、客户端能力及网络超时限制。

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
- Docker 工作流由 `v*` 标签或手动触发。没有同时配置 `DOCKERHUB_USERNAME` 和 `DOCKERHUB_TOKEN` 时，仍执行校验及 Linux amd64 / arm64 构建，但跳过登录和镜像推送；工作流成功不等于镜像已发布。
- 两项凭据齐全时，工作流会登录并自动推送构建结果。Token 必须有目标仓库写入权限，不要把凭据写进代码或 `.env` 示例。未配置 Actions 发布凭据的仓库，由已认证维护者另外发布镜像并核对标签和摘要。
- Fork 发布时，修改 `.github/workflows/docker.yml` 中的镜像命名空间，并相应调整安装器默认镜像或通过 `CORALBAY_IMAGE` 指定自己的镜像。
- 版本标签必须匹配 Dockerfile 的 `ARG VERSION`，例如`v4.15.0`。工作流支持 Linux amd64 / arm64，并生成 SBOM、provenance 与 OCI 来源标签。
- Docker 工作流不会创建 GitHub Release。已认证维护者确认镜像实际发布、标签和摘要正确后，再手动创建正式 Release；控制台通过 GitHub Releases 查询新版本。

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
node --check web/miaomiaowu.js
node --check web/miaomiaowu-rules.js
node tests/source_ui_test.cjs
node tests/miaomiaowu_ui_test.cjs
node tests/miaomiaowu_rules_ui_test.cjs
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
- 666OS 发布 ID 包含 release 提交、geo 提交、实际 YYDS 配置摘要、生成器版本和域名配置摘要；配置单独变化也能产生新版本。
- 新版本在独立 staging 中完整通过校验后，通过临时链接原子切换。
- 面板、计划任务和命令行共享跨进程同步锁，避免并发写入。
- GitHub 同步失败时保留旧版本。
- 旧 666OS 活动产物目录保留当前版本和最近两个历史版本；独立固定资源继续留存，不受该清理影响。
- 默认每六小时同步，可在安装菜单中修改。
- 支持 Linux x86_64 和 ARM64。
- 活动日志和管理操作审计持久化到 `/data`，容器重启后仍可追踪。
