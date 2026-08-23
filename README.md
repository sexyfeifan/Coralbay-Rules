# CoralBay Rules

稳定、自托管的 666OS Mihomo MRS 规则镜像。项目定时同步
[`666OS/rules`](https://github.com/666OS/rules) 的 `release` 分支，在全部规则通过校验后才原子切换上线版本；同步失败时继续提供上一次有效规则。

Docker Hub：`sexyfeifan/coralbay-rules:latest`

## Web 管理界面

- `/`：公开状态首页，显示规则版本、校验数量和同步时间。
- `/admin/`：密码保护的管理后台，可立即同步、查看日志、复制 PPanel 模板链接，以及在保留版本之间回滚。
- 管理密码在一键安装时设置，只保存 SHA-256 摘要；也可以通过命令行菜单修改。

仓库包含手动触发的多架构 Docker 发布工作流。使用前在 GitHub 仓库中添加
`DOCKERHUB_USERNAME` 和 `DOCKERHUB_TOKEN` 两个 Actions secrets。

## 一键安装

服务器需要预先安装 Docker Engine 和 Docker Compose 插件。

```bash
curl -fsSL https://raw.githubusercontent.com/sexyfeifan/Coralbay-Rules/main/install.sh | sudo bash
```

运行后显示中文管理菜单：

```text
1. 安装 / 重新配置
2. 查看运行状态
3. 立即同步规则
4. 获取 PPanel 订阅模板下载链接
5. HTTPS 证书管理
6. 查看最近日志
7. 更新容器镜像
8. 检测公网规则地址
9. 卸载
10. 查看项目信息
11. 修改 Web 管理密码
0. 退出
```

首次安装完成后，脚本会注册系统快捷命令。以后通过 SSH 登录服务器，直接执行即可重新打开菜单：

```bash
sudo coralbay-rules
```

推荐使用短命令 `sudo rules`。同时保留 `sudo luse`，或者在终端编码支持良好时使用 `sudo 六六六`。

也可以直接执行子命令，例如 `sudo coralbay-rules status`、`sudo coralbay-rules sync`、`sudo coralbay-rules logs` 和 `sudo coralbay-rules update`。

重新配置前会自动备份 `.env`、Compose 和 Caddy 配置；脚本也会检测 80/443、避免重复安装时误判自己的 3999 端口，并在启动后执行健康检查。

## 本地图标镜像

PPanel 模板使用的 27 个 Qure 策略组图标已经固化在 Docker 镜像中。同步时会复制到 `/_assets/icons/`，并自动把生成模板里的 GitHub 图标地址替换为当前规则域名，例如：

```text
https://rules.coralbay.top/_assets/icons/Auto.png
```

这些图标只用于 OpenClash/Mihomo 面板展示，不参与节点测速或流量分流。图标来源：[Koolson/Qure](https://github.com/Koolson/Qure)，固定于上游提交 `b16b260625f873266f6a6a9b88710132774997b8`。

## 两种部署模式

### 独立服务器

脚本直接监听 80/443，由 Caddy 自动签发和续期 HTTPS 证书。域名的 A/AAAA 记录需要提前指向服务器。

安装环节会要求确认 DNS 和防火墙条件；启动 Caddy 后即自动申请证书。菜单中的“HTTPS 证书管理”可以检测证书、重新加载 Caddy 配置以及查看 ACME/TLS 日志。

### 与 PPanel/Nginx 共存

容器默认只监听 `127.0.0.1:3999`，再由已有 Nginx 将规则域名反向代理到该端口，避免抢占 PPanel 的 80/443。安装脚本会同时检查系统监听端口和 Docker 端口映射；如果 3999 已占用，会提示选择其他 1024–65535 端口。

从 v1 升级到 v2 时重新执行一键脚本，并选择“安装 / 重新配置”。原有规则缓存会保留，安装过程会要求设置新的 Web 管理密码。

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
