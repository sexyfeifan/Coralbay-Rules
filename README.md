# CoralBay Rules

稳定、自托管的 666OS Mihomo MRS 规则镜像。项目定时同步
[`666OS/rules`](https://github.com/666OS/rules) 的 `release` 分支，在全部规则通过校验后才原子切换上线版本；同步失败时继续提供上一次有效规则。

Docker Hub：`sexyfeifan/coralbay-rules:latest`

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
4. 查看最近日志
5. 更新容器镜像
6. 检测公网规则地址
7. 卸载
8. 查看项目信息
0. 退出
```

## 两种部署模式

### 独立服务器

脚本直接监听 80/443，由 Caddy 自动签发和续期 HTTPS 证书。域名的 A/AAAA 记录需要提前指向服务器。

### 与 PPanel/Nginx 共存

容器只监听 `127.0.0.1:8088`，再由已有 Nginx 将规则域名反向代理到该端口，避免抢占 PPanel 的 80/443。

Nginx 示例：

```nginx
server {
    listen 443 ssl http2;
    server_name rules.coralbay.top;

    location / {
        proxy_pass http://127.0.0.1:8088;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
    }
}
```

## 验证

```bash
curl -fsS https://rules.coralbay.top/_mirror/status.json
curl -fsSI https://rules.coralbay.top/mihomo/domain/AI.mrs
```

## PPanel 模板修改

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
