# CoralBay Rules

稳定、自托管的 666OS Mihomo MRS 规则镜像。项目定时同步
[`666OS/rules`](https://github.com/666OS/rules) 的 `release` 分支，在全部规则通过校验后才原子切换上线版本；同步失败时继续提供上一次有效规则。

Docker Hub：`sexyfeifan/coralbay-rules:latest`

## Web 管理界面

- `/`：唯一的管理入口。未登录时显示密码页，登录后进入全功能控制台；首次安装要求设置至少 12 位管理员密码，隐藏输入并二次确认。已有安装保留原密码，可执行 `sudo rules password` 修改。
- `/admin/`：永久跳转到 `/`，不再保留第二套页面或公开状态首页。
- Docker Socket 只挂载给独立更新器；主程序无法直接操作 Docker，更新器也只匹配 `coralbay-rules` scope。

仓库包含 Push/PR 持续集成以及 Tag/手动触发的多架构 Docker 发布工作流。发布镜像包含 OCI 来源标签、SBOM 和 provenance。使用前在 GitHub 仓库中添加
`DOCKERHUB_USERNAME` 和 `DOCKERHUB_TOKEN` 两个 Actions secrets。

## 一键安装

用于 Linux x86_64 / ARM64 服务器，需要预先安装 Docker Engine、Docker Compose 插件、curl 和 coreutils，并准备已有的 Nginx/PPanel/OpenResty 反向代理和 HTTPS 证书。本脚本负责部署 CoralBay 服务，不自动安装 Docker 或接管服务器的 80/443 端口。

```bash
script=$(mktemp)
curl -fsSL --connect-timeout 15 --max-time 120 "https://raw.githubusercontent.com/sexyfeifan/Coralbay-Rules/main/install.sh?t=$(date +%s)" -o "$script" && sudo bash "$script" install
rm -f "$script"
```

运行后显示中文管理菜单：

```text
1. 安装 / 重新配置
2. 查看运行状态
3. 立即同步规则
4. 获取 PPanel 订阅模板下载链接
5. HTTPS 证书管理
6. 查看最近日志
7. 升级程序（管理脚本 + 容器镜像）
8. 检测公网规则地址
9. 卸载
  10. 查看项目信息
  11. 修改管理员密码
0. 退出
```

首次安装完成后，脚本会注册系统快捷命令。以后通过 SSH 登录服务器，直接执行即可重新打开菜单：

```bash
sudo rules
# 或
sudo 666
```

管理快捷命令仅保留 `sudo rules` 和 `sudo 666`。

执行 `sudo rules update` 会下载并校验新管理脚本，再由新脚本完成升级，备份并刷新 Compose 配置，为旧安装补齐必要设置。自定义安装目录会保存在 `/etc/coralbay-rules/install-dir`，后续菜单默认使用该目录。也可通过 `sudo env CORALBAY_INSTALL_DIR=/srv/coralbay rules update` 指定已有安装。

从旧版管理脚本升级到 v4.11.3 时，推荐先按上面的下载方式获取最新脚本，把最后的 `install` 改成 `update`，以便本次升级直接使用新的检查逻辑。

也可以直接执行子命令，例如 `sudo rules status`、`sudo rules sync`、`sudo rules logs` 和 `sudo rules update`。

脚本先校验候选 Compose 并成功拉取镜像，再替换配置；失败时保留原配置。它会核对监听端口所属容器和安装目录，并检查应用及订阅后端能否响应；启动检查失败会报错并恢复已有配置，不会显示升级成功。备份保存在安装目录的 `backups/`，不包含完整规则与订阅数据；失败后已拉取的镜像不会自动回退。

重新配置时选择的同步间隔会覆盖以前在网页保存的设置。应用版本或规则域名变化后会立即触发规则同步；上游暂时不可用时继续保留旧规则。

## 本地图标镜像

PPanel 模板使用的 27 个 Qure 策略组图标已经固化在 Docker 镜像中。同步时会复制到 `/_assets/icons/`，并自动把生成模板里的 GitHub 图标地址替换为当前规则域名，例如：

```text
https://rules.coralbay.top/_assets/icons/Auto.png
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

## 与 PPanel/Nginx 共存部署

容器默认只监听 `127.0.0.1:3999`，再由已有 Nginx 将规则域名反向代理到该端口，避免抢占 PPanel 的 80/443。安装脚本会同时检查系统监听端口和 Docker 端口映射；如果 3999 已占用，会提示选择其他 1024–65535 端口。

安装过程不再询问部署模式或证书邮箱。规则域名必须由使用者输入且没有预设值；HTTPS 证书继续由现有的 Nginx、PPanel 或 OpenResty 管理。

菜单“升级程序”会同时更新本机管理脚本与 Docker 镜像。也可以直接执行 `sudo rules update`。

Nginx 示例：

```nginx
server {
    listen 443 ssl http2;
    server_name rules.coralbay.top;

    location / {
        proxy_pass http://127.0.0.1:3999;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

## 验证

```bash
curl -fsS https://rules.coralbay.top/_mirror/status.json
curl -fsSI https://rules.coralbay.top/mihomo/domain/AI.mrs
```

## PPanel 模板修改

每次规则同步都会同时生成一份已经替换为当前镜像域名的 PPanel 模板：

```text
https://rules.coralbay.top/_templates/ppanel_openclash_pro_cn.gotmpl
```

管理菜单可以显示并检测该下载链接。下载文件后，由用户在 PPanel 客户端管理页面手动替换 `OpenClash Pro` 的订阅模板。

网页下载按钮会保存为 UTF-8 文件 `CoralBay_OpenClash_PPanel_Template.yaml`；它仍然是 PPanel 模板，需要由 PPanel 渲染后才是最终 OpenClash 配置。

将：

```text
https://github.com/666OS/rules/raw/release/
```

替换为：

```text
https://rules.coralbay.top/
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

## 可靠性

### v4.11.3 修复

- 同步和回滚共用内核文件锁；进程异常退出后自动释放，旧 `.sync.lock` 目录不再阻塞更新。
- 修复登录限流可通过更换 TCP 连接绕过的问题；不会信任未经验证的转发 IP 请求头。
- 已停用或删除的订阅重新生成相同参数时返回明确提示，要求先恢复，不交付无法拉取的链接。
- Clash/Stash 节点计数使用 YAML 解析，兼容行内列表、无缩进列表和不同字段顺序。
- 独立同步循环在上游下载失败时立即中止该次发布；版本或域名变化后主动同步。
- 安装脚本不再执行 `.env` 内容，支持安全重配置、目录记忆和实际启动检查。
- 详细验证和发布边界见 [v4.11.3 实施报告](REVIEW-4.11.3.md)。

### v4.11 链接管理与安全升级

- 订阅转换 → 链接管理：查看拉取次数、筛选分页、停用/恢复、更新签名和服务器连通性抽测。
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
