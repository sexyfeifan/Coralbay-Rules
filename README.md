# CoralBay Rules

稳定、自托管的 666OS Mihomo MRS 规则镜像。项目定时同步
[`666OS/rules`](https://github.com/666OS/rules) 的 `release` 分支，在全部规则通过校验后才原子切换上线版本；同步失败时继续提供上一次有效规则。

Docker Hub：`sexyfeifan/coralbay-rules:latest`

## Web 管理界面

- `/`：公开状态首页，显示规则版本、校验数量和同步时间。
- `/admin/`：免登录运维控制台，可立即同步、调整同步频率、查看证书与图标缓存、浏览规则资源、对比版本、升级容器、查看日志以及回滚规则版本。
- Docker Socket 只挂载给独立更新器；主程序无法直接操作 Docker，更新器也只匹配 `coralbay-rules` scope。

仓库包含手动触发的多架构 Docker 发布工作流。使用前在 GitHub 仓库中添加
`DOCKERHUB_USERNAME` 和 `DOCKERHUB_TOKEN` 两个 Actions secrets。

## 一键安装

服务器需要预先安装 Docker Engine 和 Docker Compose 插件。

```bash
curl -fsSL "https://raw.githubusercontent.com/sexyfeifan/Coralbay-Rules/main/install.sh?t=$(date +%s)" | sudo bash
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
0. 退出
```

首次安装完成后，脚本会注册系统快捷命令。以后通过 SSH 登录服务器，直接执行即可重新打开菜单：

```bash
sudo rules
# 或
sudo 666
```

管理快捷命令仅保留 `sudo rules` 和 `sudo 666`。

也可以直接执行子命令，例如 `sudo rules status`、`sudo rules sync`、`sudo rules logs` 和 `sudo rules update`。

重新配置前会自动备份 `.env`、Compose 和 Caddy 配置；脚本也会检测 80/443、避免重复安装时误判自己的 3999 端口，并在启动后执行健康检查。

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

同步服务会调用独立的 `coralbay-ruleconvert`，从同一次 666OS `geo` 快照生成两种可审计产物：

```text
/_converted/native/list/{site|ip}/*.list
/_converted/native/sing-box/{site|ip}/*.json
/_converted/manifest.json
```

转换过程会去除空行、注释与重复项，将 `+.` 域名转换为 `DOMAIN-SUFFIX`，将精确域名转换为 `DOMAIN`，并区分 IPv4 `IP-CIDR` 与 IPv6 `IP-CIDR6`。sing-box JSON 固定使用兼容 1.11–1.14 的 source rule-set version 3。

`release` 中少量只有 MRS、在 `geo` 分支没有公开可逆源的分类会生成零条目的合法占位文件，并在转换清单中明确显示 `0`，绝不会用空规则对象误匹配全部流量。

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

- 验证 PPanel Pro_cn 使用的全部 33 个 MRS 文件。
- 新版本完整通过校验后才切换。
- GitHub 同步失败时保留旧版本。
- 保留当前版本和最近两个历史版本。
- 默认每六小时同步，可在安装菜单中修改。
- 支持 Linux x86_64 和 ARM64。
