# CoralBay Rules 搭建与维护指南

本指南适用于 Linux x86_64（amd64）和 ARM64 服务器。应用镜像为 `sexyfeifan/coralbay-rules`；可选择跟随 `latest`，或固定版本。下文以 `4.13.0` 为例，固定版本安装前请确认对应镜像已发布。

使用公开镜像安装不需要 Fork 仓库、配置 GitHub Actions Secrets，也不需要安装 PPanel。PPanel、Nginx 或 OpenResty 只是可以使用的 HTTPS 反向代理方式。

## 安装前准备

准备以下条件：

- 一台 Linux 服务器，以及可使用 `sudo` 的交互式 SSH 终端；脚本不支持 macOS 或 Windows 宿主机安装。
- 已安装并启动的 Docker Engine，以及 `docker compose` 插件。可按 Docker 官方的 [Ubuntu 安装指南](https://docs.docker.com/engine/install/ubuntu/) 或 [Debian 安装指南](https://docs.docker.com/engine/install/debian/) 完成安装。
- Bash、curl、coreutils、awk 等基础工具；备份示例还需要 tar。脚本会检查 `curl`、`realpath`、`od`、`awk`、Docker 和 Compose 是否可用。
- 自己的域名、正确的 DNS 解析，以及已有的 HTTPS 站点和证书。域名应指向这台服务器；若填写了 AAAA 记录，也要确保 IPv6 可达。
- 服务器能够访问 Docker Hub、GHCR、GitHub，以及准备使用的订阅来源。规则同步需要访问 GitHub。

安装脚本不安装 Docker，不申请证书，不修改已有反代站点，也不占用宿主机的 80/443 端口。数据目录应位于支持 Linux 文件锁的本地文件系统；不要把未经验证的网络文件系统或 macOS Docker 共享目录作为生产数据目录。

检查 Docker：

```bash
docker --version
docker compose version
sudo docker info
```

## 安装应用

以下命令在下载、语法检查或安装失败时立即停止当前子 shell，并清理下载的临时脚本。请在服务器的交互终端中执行，不要将整段安装过程放进没有终端输入的后台任务。

### 使用 latest

```bash
(
  set -Eeuo pipefail
  coralbay_installer="$(mktemp)"
  trap 'rm -f -- "$coralbay_installer"' EXIT
  curl -fsSL --retry 3 --connect-timeout 15 --max-time 120 \
    'https://raw.githubusercontent.com/sexyfeifan/Coralbay-Rules/main/install.sh' \
    -o "$coralbay_installer"
  test -s "$coralbay_installer"
  bash -n "$coralbay_installer"
  sudo env CORALBAY_IMAGE=sexyfeifan/coralbay-rules:latest \
    bash "$coralbay_installer" install
)
```

### 固定安装 4.13.0

使用下面这一组命令替代上面的安装命令，不需要执行两遍：

```bash
(
  set -Eeuo pipefail
  coralbay_installer="$(mktemp)"
  trap 'rm -f -- "$coralbay_installer"' EXIT
  curl -fsSL --retry 3 --connect-timeout 15 --max-time 120 \
    'https://raw.githubusercontent.com/sexyfeifan/Coralbay-Rules/main/install.sh' \
    -o "$coralbay_installer"
  test -s "$coralbay_installer"
  bash -n "$coralbay_installer"
  sudo env CORALBAY_IMAGE=sexyfeifan/coralbay-rules:4.13.0 \
    bash "$coralbay_installer" install
)
```

`bash -n` 检查脚本语法，不是签名或文件摘要验证。两个示例都从项目的 GitHub `main` 分支取得管理脚本，应用镜像版本由 `CORALBAY_IMAGE` 决定。

### 安装时会询问什么

`install` 直接进入安装问答，不会先显示主菜单：

| 输入 | 实际行为 |
| --- | --- |
| 安装目录 | 默认 `/opt/coralbay-rules`；已有安装可能显示上次保存的目录。必须是独立的绝对子目录。 |
| 规则域名 | 必填，无默认值，例如 `rules.example.com`；只填域名，不包含 `https://`、端口或路径。 |
| 本地端口 | 默认 `3999`，可选 `1024–65535`；已被其他服务占用时需换端口。 |
| 666OS 同步周期 | 默认每 6 小时，可选 1/6/12/24 小时或自定义 `3600–604800` 秒。 |
| 管理员密码 | 首次安装要求至少 12 位，仅支持字母、数字及 `._@+-`，隐藏输入并再次确认。已有安装保留原密码。 |

脚本只支持上述交互问答，没有 `--domain`、`--password` 之类的无人值守安装参数。需要预先选定安装目录时，可在 `sudo env` 后增加 `CORALBAY_INSTALL_DIR=/你的/安装目录`；这只跳过目录问答。

安装会启动以下服务：

- `coralbay-rules`：Web 控制台、规则服务与订阅服务。
- `coralbay-subconverter`：普通订阅转换后端，只在 Compose 内网提供服务。
- `coralbay-rules-updater`：独立镜像更新器；Docker Socket 仅挂载给它。

默认主机映射为 `127.0.0.1:3999 → app:8080`，数据通过 `安装目录/data → /data` 持久化。安装成功只说明应用及转换后端启动检查通过；首次规则同步和公网 HTTPS 仍需下面的验证。

## 配置域名与 HTTPS 反代

在已有的 HTTPS 站点中，把整个域名的请求转发到应用。以下是供**宿主机上的 Nginx/OpenResty**使用的 `location` 片段；站点的 `server_name`、证书和 443 监听应已配置好，不要将它当成完整的 TLS 站点配置。

```nginx
location / {
    proxy_pass http://127.0.0.1:3999;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_connect_timeout 10s;
    proxy_read_timeout 240s;
    proxy_send_timeout 240s;
}
```

如果安装时更换了端口，将示例中的 `3999` 一并替换。新分流构建最长可持续约 3 分钟，建议将 [Nginx 读取超时](https://nginx.org/en/docs/http/ngx_http_proxy_module.html#proxy_read_timeout)设为 `240s`；使用面板反代时，在面板中设置对应超时。如果前面还有 CDN 或其他网关，也要核对其请求超时限制。配置完成后先检查 Nginx 配置，再按已有站点的管理方式重新加载。

如果反代运行在另一 Docker 容器内，该容器中的 `127.0.0.1` 指向反代容器自身，不能直接使用上面的宿主机地址。需要由该反代的部署方式提供到应用的网络连接，例如明确配置共享 Docker 网络并以服务名访问。不要为解决这一问题直接把应用、转换器或 Docker Socket 公开到公网。管理脚本升级会重新生成 Compose，额外的网络定制需要自行维护。

证书继续由现有反代或面板管理。`sudo rules certificate` 只显示后端地址并探测公网 HTTPS，不申请或续期证书。正式访问请使用 HTTPS，登录 Cookie 带有 `Secure` 属性。

## 验证并开始使用

先在服务器上检查本机应用。若使用其他端口，请修改命令：

```bash
curl -fsS --connect-timeout 5 --max-time 10 http://127.0.0.1:3999/healthz
```

应返回 `ok`。再把下面的示例域名替换为自己的域名，检查 HTTPS 与 666OS 规则：

```bash
coralbay_domain='rules.example.com'
curl -fsS --connect-timeout 10 --max-time 30 "https://${coralbay_domain}/_mirror/status.json"
curl -fsSI --connect-timeout 10 --max-time 30 "https://${coralbay_domain}/mihomo/domain/AI.mrs"
```

首次同步未完成时，规则文件可能暂不可用；通过 `sudo rules logs` 查看同步进度。健康接口返回 `ok` 不等于规则同步已经完成。

浏览器打开 `https://你的域名/`，使用安装时的密码登录：

1. 在“运行概览”查看程序版本、666OS 同步状态及规则完整度。
2. 在“普通订阅转换”转换已有订阅；其远程 `.ini` 配置预设仍位于普通转换页。
3. 使用自定义分流时，先打开“MetaCubeX 分流源”，确认本地原始规则达到 78 / 78（首次启动自动同步，也可手动同步）；再到 `/routing` 创建方案、选择本机或上游来源、预览并保存固定订阅地址。
4. 将生成的地址导入目标客户端，实际验证连接、规则命中和订阅更新。Mihomo/OpenClash 与 Stash 的完整分流输出范围及协议边界见 [4.13.0 发布说明](../RELEASE-4.13.0.md)。

管理 API 需要登录。匿名访问 `/api/routing/catalog` 等接口返回 401 是正常行为。

## 升级、固定版本与重新配置

安装成功后，执行 `sudo rules` 或 `sudo 666` 打开主菜单。快捷命令指向 `/usr/local/libexec/coralbay-rules-manager`；默认安装目录保存在 `/etc/coralbay-rules/install-dir`。

普通升级使用：

```bash
sudo rules update
```

该命令从 GitHub `main` 下载新管理脚本，检查语法后由新脚本执行升级；保留已有密码和数据，生成候选 Compose，先验证并拉取镜像，再替换配置、重建服务和检查启动状态。它会拉取 Compose 中的服务镜像，不只处理主应用。

**升级保留 `.env` 中已记录的 `APP_IMAGE`。** 如果原来固定为 `4.11.4`，直接执行 `rules update` 仍然使用该固定版本。需要切换镜像时明确指定：

```bash
# 跟随 latest；安装目录不是默认值时请修改。
sudo env CORALBAY_INSTALL_DIR=/opt/coralbay-rules \
  CORALBAY_IMAGE=sexyfeifan/coralbay-rules:latest rules update
```

```bash
# 固定到 4.13.0。
sudo env CORALBAY_INSTALL_DIR=/opt/coralbay-rules \
  CORALBAY_IMAGE=sexyfeifan/coralbay-rules:4.13.0 rules update
```

`CORALBAY_IMAGE` 也支持已核实的镜像摘要地址。固定应用版本不等于固定整个 Compose 中其他服务的版本；应保留升级前的配置和镜像信息。

已有安装不要用 `install` 代替日常升级。`install` 用于首次安装或有意重新配置域名、端口、同步周期；重新配置会覆盖之前在网页中保存的 666OS 同步周期。若旧安装缺少 `rules` 快捷命令，可按前面的临时文件下载流程取得脚本，将执行的子命令改为 `update`，并用 `CORALBAY_INSTALL_DIR` 指定已有安装目录。

应用或生成器版本变化后，666OS 会重新生成对应版本的产物；不要把这一过程与 MetaCubeX 的独立同步混为一谈。升级后重新检查容器、控制台版本和已有订阅。4.13.0 首次启动会补齐 MetaCubeX 原始 YAML，并为 666OS 保存独立固定资源；旧分流方案保持内嵌，只有主动编辑来源后才改变交付方式。

### 配置回退不等于服务回退

候选配置校验或镜像拉取失败时，原配置不被替换。启动检查失败时，脚本会恢复之前的配置文件并报错，但不会自动切回旧镜像或重新启动旧版本。它的 `backups/` 只包含部署配置，不是完整数据备份。

不要仅凭安装目录中存在 `backups/` 就认为可以无损降级。特别是涉及数据库版本时，应使用升级前一致的镜像、配置和完整数据备份制定恢复步骤。

## 备份与回退

重要升级前应备份 `.env`、`compose.yaml` 和整个 `data/`。`data/` 包含旧订阅签名密钥、SQLite/WAL、历史、调度信息、666OS 产物，以及新的 `routing/routing.sqlite` 和规则快照。在线复制单个 `.sqlite` 文件不保证一致性。

下面的示例短暂停止应用及原先运行的更新器，不停止转换后端。备份位于 `/var/backups/coralbay-rules`，只归档指定文件和 `data/`，不会递归打包安装目录下的 `backups/`。退出或常见中断信号会触发恢复；仅重新启动备份前处于运行状态的 `app` 和 `updater`，不会启动原本停止的服务。

先修改安装目录，再执行：

```bash
sudo env CORALBAY_INSTALL_DIR=/opt/coralbay-rules bash <<'BASH'
set -Eeuo pipefail
umask 077
coralbay_install_dir="$(realpath "$CORALBAY_INSTALL_DIR")"
coralbay_backup_root=/var/backups/coralbay-rules
[[ -f "$coralbay_install_dir/.env" ]]
[[ -f "$coralbay_install_dir/compose.yaml" ]]
if [[ ! -d "$coralbay_install_dir/data" || -L "$coralbay_install_dir/data" ]]; then
  printf '%s\n' '此示例要求 data 为普通目录；外部卷或符号链接请按实际存储位置备份。' >&2
  exit 1
fi
install -d -m 0700 "$coralbay_backup_root"
coralbay_backup_root="$(realpath "$coralbay_backup_root")"
case "$coralbay_backup_root/" in
  "$coralbay_install_dir/"*)
    printf '%s\n' '备份目录必须位于安装目录之外。' >&2
    exit 1
    ;;
esac
coralbay_compose=(docker compose --project-directory "$coralbay_install_dir"
  -f "$coralbay_install_dir/compose.yaml" --env-file "$coralbay_install_dir/.env")
coralbay_running_services="$("${coralbay_compose[@]}" ps --services --status running)"
coralbay_resume_app=false
coralbay_resume_updater=false
while IFS= read -r coralbay_service; do
  case "$coralbay_service" in
    app) coralbay_resume_app=true ;;
    updater) coralbay_resume_updater=true ;;
  esac
done <<< "$coralbay_running_services"
coralbay_restore_services() {
  local coralbay_result=$?
  trap - EXIT INT TERM
  if "$coralbay_resume_app"; then
    "${coralbay_compose[@]}" start app || coralbay_result=1
  fi
  if "$coralbay_resume_updater"; then
    "${coralbay_compose[@]}" start updater || coralbay_result=1
  fi
  exit "$coralbay_result"
}
trap coralbay_restore_services EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
if "$coralbay_resume_updater"; then "${coralbay_compose[@]}" stop updater; fi
if "$coralbay_resume_app"; then "${coralbay_compose[@]}" stop app; fi
coralbay_backup_dir="$(mktemp -d "$coralbay_backup_root/$(date +%Y%m%d-%H%M%S).XXXXXX")"
tar -C "$coralbay_install_dir" -czf "$coralbay_backup_dir/state.tar.gz.partial" \
  -- .env compose.yaml data
mv "$coralbay_backup_dir/state.tar.gz.partial" "$coralbay_backup_dir/state.tar.gz"
printf '备份文件：%s\n' "$coralbay_backup_dir/state.tar.gz"
BASH
```

结束后执行 `sudo rules status` 检查应用恢复情况。只有成功生成的 `state.tar.gz` 才是完成的备份；失败过程中留下的 `.partial` 不能当作完整备份。断电或 `SIGKILL` 不会执行 trap，这类情况下需要人工检查并恢复服务。

备份中包含密码和订阅凭据，应限制访问并保存到受保护的位置。外部反代配置、证书以及备份前使用的镜像版本/摘要也应单独记录。恢复前先确认目标版本和数据兼容性，不要把归档直接覆盖到正在运行的数据目录。

## 日常维护与卸载

| 命令或入口 | 功能 |
| --- | --- |
| `sudo rules` / `sudo 666` | 打开主菜单 |
| `sudo rules status` | 查看 Compose 服务和已有 666OS 产物状态 |
| `sudo rules logs` | 查看最近容器日志 |
| `sudo rules sync` | 立即同步 **666OS** 规则与模板 |
| 控制台“MetaCubeX 分流源” | 查看并独立同步自定义分流规则 |
| `sudo rules update` | 更新管理脚本、Compose 和当前选择的镜像 |
| `sudo rules password` | 修改管理员密码；已有网页登录会话会失效 |
| `sudo rules template` | 查看 PPanel 模板下载链接 |
| `sudo rules certificate` | 查看反代后端并检测公网 HTTPS |
| `sudo rules verify` | 输入域名，检测公网 666OS 状态和示例规则 |
| `sudo rules uninstall` | 停止并移除 Compose 服务；默认保留数据 |

`rules uninstall` 之后会另外询问是否永久删除数据。只有确认并输入 `PURGE`，且安装目录位于 `/opt/` 子目录时，脚本才会删除整个安装目录。不在 `/opt/` 下的自定义目录会保留。不要为了绕过目录保护而移动现有数据。

卸载不会删除外部 Nginx/PPanel 站点或其证书，也不会清理系统中的 `rules` / `666` 快捷命令及安装目录记忆文件；需要清理这些内容时，应先确认不再使用该安装。

## 安全与常见故障

应用的管理界面由密码保护，公开规则及订阅交付地址按各自权限提供服务。不要公开 `.env`、数据库或备份，不要额外映射转换后端、出站代理或 Docker Socket。生成的订阅链接包含交付凭据，应只提供给预期使用者。

| 现象 | 检查方法 |
| --- | --- |
| 提示未安装 Docker、缺少 Compose 或 Docker 未运行 | 按官方系统指南安装并启动 Docker，先通过 `sudo docker info`。 |
| 提示无法读取输入 | 在有交互终端的 SSH 会话执行安装；目录环境变量不会代替域名和密码输入。 |
| 提示端口被占用或容器属于其他目录 | 使用原安装目录管理已有服务；新安装另选未占用端口，不停止不相关容器。 |
| 公网出现 502 | 先测本机 `/healthz`，再检查反代端口；容器内反代不能把自身 `127.0.0.1` 当作宿主机。 |
| 登录后仍回到密码页 | 使用正确的 HTTPS 域名，检查反代的 Host/协议和浏览器 Cookie；不要把 HTTP 后端地址当作公网登录入口。 |
| 规则路径返回 404 | 查看首次 666OS 同步日志及上游网络；健康检查成功不代表规则已生成。 |
| MetaCubeX 目录有名称但没有缓存 | 在独立源页同步，检查服务器到 GitHub 的 DNS/TLS 与网络；666OS 同步不会填充该目录。 |
| 构建请求在反代处超时 | 将读取超时设为建议的 240 秒，并检查订阅来源响应速度；不要反复并发点击生成。 |
| 订阅来源提示非公网地址或域名解析失败 | 检查真实 DNS；内网、回环和 Fake-IP 地址会被拒绝，不要关闭网络约束来绕过。 |
| 更新后版本未变化 | 检查 `.env` 的 `APP_IMAGE` 是否固定在旧 tag，以及实际运行镜像；使用 `CORALBAY_IMAGE` 明确选择目标。 |
| 方案保存失败、空组或协议字段不兼容 | 先查看节点预览及错误，调整筛选条件或选择支持相应协议的客户端。 |
| 升级启动检查失败 | 查看 `rules logs`；脚本可能已恢复配置，但不会自动退回旧镜像。先保留备份，再按实际错误排查。 |

维护者自行发布镜像时，才需要在仓库配置 `DOCKERHUB_USERNAME` 与 `DOCKERHUB_TOKEN`。当前 Docker 工作流使用 `v*` 标签或手动触发，并要求发布标签匹配 Dockerfile 版本；Fork 仓库还需修改镜像命名空间。安装官方公开镜像不需要进行这些操作。

## 规则来源与本地存储

在两个规则资源页使用“检查上游”查看新版本，用“同步到本地”更新本机副本。详情中的上游读取使用独立缓存，不改变已发布资源；本机模式生成时不临时联网补规则。

新分流方案默认通过 CoralBay 的固定版本 URL 下载规则，可改为同一提交的上游 URL。上游模式要求客户端能访问 GitHub 原始文件；换来源后应在客户端更新订阅。旧方案保持兼容，可通过预览后保存主动升级，原分流链接不变。

固定资源存储在 `data/rule-resources/666os/` 和 `data/routing/rules/releases/`。这两处不会被旧 666OS 的三版本清理删除；当前没有自动清理已交付版本。需要监控安装目录的可用空间，并按前文备份整个 `data/`。手动删除历史资源可能令尚未更新订阅的客户端无法下载对应规则。

普通转换预设中的“配置文件来源”不代表它引用的全部规则已经镜像。详情会列出本机、外部、缺失与未知依赖；只有本项目生成的派生文件目前仍由本机交付。
