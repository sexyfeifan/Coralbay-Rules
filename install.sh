#!/usr/bin/env bash
set -Eeuo pipefail

APP_NAME="CoralBay Rules"
IMAGE="${CORALBAY_IMAGE:-sexyfeifan/coralbay-rules:latest}"
DEFAULT_DIR="/opt/coralbay-rules"
DEFAULT_DOMAIN="rules.coralbay.top"
DEFAULT_EMAIL="admin@coralbay.top"
DEFAULT_INTERVAL="21600"
DEFAULT_LOCAL_PORT="3999"
SOURCE_REPO="https://github.com/sexyfeifan/Coralbay-Rules"

if [[ -t 0 ]]; then TTY_IN="/dev/stdin"; else TTY_IN="/dev/tty"; fi

green='\033[0;32m'; yellow='\033[0;33m'; red='\033[0;31m'; cyan='\033[0;36m'; reset='\033[0m'
info() { printf '%b[信息]%b %s\n' "$green" "$reset" "$*"; }
warn() { printf '%b[注意]%b %s\n' "$yellow" "$reset" "$*"; }
fail() { printf '%b[错误]%b %s\n' "$red" "$reset" "$*" >&2; exit 1; }

pause_menu() {
  printf '\n按回车键返回菜单...'
  IFS= read -r _ < "$TTY_IN" || true
}

prompt() {
  local label="$1" default="$2" value
  printf '%s [%s]: ' "$label" "$default" >&2
  IFS= read -r value < "$TTY_IN" || true
  printf '%s' "${value:-$default}"
}

confirm() {
  local text="$1" answer
  printf '%s [y/N]: ' "$text"
  IFS= read -r answer < "$TTY_IN" || true
  [[ "$answer" =~ ^[Yy]$ ]]
}

need_root() {
  [[ "$(id -u)" -eq 0 ]] || fail "请使用 sudo 运行。"
}

need_docker() {
  command -v docker >/dev/null 2>&1 || fail "尚未安装 Docker Engine。请先安装 Docker。"
  docker compose version >/dev/null 2>&1 || fail "缺少 Docker Compose 插件。"
  docker info >/dev/null 2>&1 || fail "Docker 服务没有运行。"
}

valid_domain() {
  [[ "$1" =~ ^[A-Za-z0-9.-]+\.[A-Za-z]{2,}$ ]]
}

port_is_busy() {
  local port="$1"
  if command -v ss >/dev/null 2>&1 && ss -ltnH 2>/dev/null | awk '{print $4}' | grep -Eq ":${port}$"; then
    return 0
  fi
  if command -v netstat >/dev/null 2>&1 && netstat -lnt 2>/dev/null | awk 'NR > 2 {print $4}' | grep -Eq ":${port}$"; then
    return 0
  fi
  if docker ps --format '{{.Ports}}' 2>/dev/null | grep -Eq ":${port}->"; then
    return 0
  fi
  return 1
}

load_env() {
  local dir="${1:-$DEFAULT_DIR}"
  if [[ -f "$dir/.env" ]]; then
    # shellcheck disable=SC1090
    set -a; source "$dir/.env"; set +a
  fi
}

write_compose() {
  local dir="$1" mode="$2" bind_port="$3"
  if [[ "$mode" == "direct" ]]; then
    cat > "$dir/compose.yaml" <<'EOF'
services:
  sync:
    image: ${SYNC_IMAGE}
    container_name: coralbay-rules-sync
    restart: unless-stopped
    environment:
      RULES_REPOSITORY: ${RULES_REPOSITORY}
      RULES_BRANCH: ${RULES_BRANCH}
      SYNC_INTERVAL: ${SYNC_INTERVAL}
    volumes:
      - ./data:/data
    healthcheck:
      test: ["CMD-SHELL", "test -s /data/current/_mirror/status.json"]
      interval: 30s
      timeout: 5s
      retries: 5
      start_period: 60s

  web:
    image: caddy:2-alpine
    container_name: coralbay-rules-web
    restart: unless-stopped
    depends_on: [sync]
    environment:
      MIRROR_DOMAIN: ${MIRROR_DOMAIN}
      ACME_EMAIL: ${ACME_EMAIL}
    ports:
      - "80:80"
      - "443:443"
      - "443:443/udp"
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      - ./data:/srv/rules:ro
      - ./caddy_data:/data
      - ./caddy_config:/config
EOF
    cat > "$dir/Caddyfile" <<'EOF'
{
  email {$ACME_EMAIL}
}

{$MIRROR_DOMAIN} {
  root * /srv/rules/current
  encode zstd gzip
  file_server
  @status path /_mirror/status.json
  @rules not path /_mirror/status.json
  header @status Cache-Control "no-store"
  header @rules Cache-Control "public, max-age=3600, stale-if-error=86400"
  log {
    output stdout
  }
}
EOF
  else
    cat > "$dir/compose.yaml" <<'EOF'
services:
  sync:
    image: ${SYNC_IMAGE}
    container_name: coralbay-rules-sync
    restart: unless-stopped
    environment:
      RULES_REPOSITORY: ${RULES_REPOSITORY}
      RULES_BRANCH: ${RULES_BRANCH}
      SYNC_INTERVAL: ${SYNC_INTERVAL}
    volumes:
      - ./data:/data
    healthcheck:
      test: ["CMD-SHELL", "test -s /data/current/_mirror/status.json"]
      interval: 30s
      timeout: 5s
      retries: 5
      start_period: 60s

  web:
    image: caddy:2-alpine
    container_name: coralbay-rules-web
    restart: unless-stopped
    depends_on: [sync]
    ports:
      - "127.0.0.1:${LOCAL_PORT}:8080"
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      - ./data:/srv/rules:ro
EOF
    cat > "$dir/Caddyfile" <<'EOF'
:8080 {
  root * /srv/rules/current
  encode zstd gzip
  file_server
  @status path /_mirror/status.json
  @rules not path /_mirror/status.json
  header @status Cache-Control "no-store"
  header @rules Cache-Control "public, max-age=3600, stale-if-error=86400"
}
EOF
  fi
}

install_service() {
  need_root; need_docker
  local dir domain email interval mode_choice mode local_port
  dir="$(prompt '安装目录' "$DEFAULT_DIR")"
  domain="$(prompt '规则域名' "${MIRROR_DOMAIN:-$DEFAULT_DOMAIN}")"
  valid_domain "$domain" || fail "域名格式不正确：$domain"
  email="$(prompt 'HTTPS 证书邮箱' "${ACME_EMAIL:-$DEFAULT_EMAIL}")"

  printf '\n部署模式：\n  1) 独立服务器，自动 HTTPS（占用 80/443）\n  2) 已有 Nginx/PPanel，监听 127.0.0.1 端口\n'
  mode_choice="$(prompt '请选择' "1")"
  if [[ "$mode_choice" == "2" ]]; then
    mode="proxy"
    while :; do
      local_port="$(prompt '本地监听端口' "$DEFAULT_LOCAL_PORT")"
      [[ "$local_port" =~ ^[0-9]+$ && "$local_port" -ge 1024 && "$local_port" -le 65535 ]] || {
        warn "端口必须是 1024 到 65535 之间的数字。"
        continue
      }
      if port_is_busy "$local_port"; then
        warn "端口 $local_port 已被系统进程或 Docker 容器占用，请换一个端口。"
        continue
      fi
      break
    done
  else
    mode="direct"; local_port="$DEFAULT_LOCAL_PORT"
  fi

  printf '\n同步间隔：\n  1) 1 小时\n  2) 6 小时（推荐）\n  3) 12 小时\n  4) 24 小时\n  5) 自定义秒数\n'
  case "$(prompt '请选择' "2")" in
    1) interval=3600 ;;
    3) interval=43200 ;;
    4) interval=86400 ;;
    5) interval="$(prompt '同步间隔（秒）' "$DEFAULT_INTERVAL")" ;;
    *) interval=21600 ;;
  esac
  [[ "$interval" =~ ^[0-9]+$ && "$interval" -ge 3600 ]] || fail "同步间隔至少为 3600 秒。"

  install -d -m 0755 "$dir/data" "$dir/caddy_data" "$dir/caddy_config"
  cat > "$dir/.env" <<EOF
SYNC_IMAGE=$IMAGE
MIRROR_DOMAIN=$domain
ACME_EMAIL=$email
SYNC_INTERVAL=$interval
RULES_REPOSITORY=https://github.com/666OS/rules.git
RULES_BRANCH=release
DEPLOY_MODE=$mode
LOCAL_PORT=$local_port
EOF
  write_compose "$dir" "$mode" "$local_port"

  docker compose -f "$dir/compose.yaml" --env-file "$dir/.env" pull
  docker compose -f "$dir/compose.yaml" --env-file "$dir/.env" up -d

  info "服务已启动，规则首次同步可能需要几十秒。"
  if [[ "$mode" == "direct" ]]; then
    info "状态地址：https://$domain/_mirror/status.json"
  else
    info "本地地址：http://127.0.0.1:$local_port/_mirror/status.json"
    warn "请在现有 Nginx/PPanel 中把 $domain 反向代理到 127.0.0.1:$local_port。"
  fi
}

service_status() {
  need_docker
  local dir="$(prompt '安装目录' "$DEFAULT_DIR")"
  [[ -f "$dir/compose.yaml" ]] || fail "未在 $dir 找到安装。"
  docker compose -f "$dir/compose.yaml" --env-file "$dir/.env" ps
  if [[ -s "$dir/data/current/_mirror/status.json" ]]; then
    printf '\n当前规则版本：\n'; cat "$dir/data/current/_mirror/status.json"; printf '\n'
  fi
}

sync_now() {
  need_root; need_docker
  local dir="$(prompt '安装目录' "$DEFAULT_DIR")"
  [[ -f "$dir/compose.yaml" ]] || fail "未在 $dir 找到安装。"
  docker compose -f "$dir/compose.yaml" --env-file "$dir/.env" restart sync
  info "同步容器已重启，正在立即同步。"
}

show_logs() {
  need_docker
  local dir="$(prompt '安装目录' "$DEFAULT_DIR")"
  [[ -f "$dir/compose.yaml" ]] || fail "未在 $dir 找到安装。"
  docker compose -f "$dir/compose.yaml" --env-file "$dir/.env" logs --tail=100
}

update_service() {
  need_root; need_docker
  local dir="$(prompt '安装目录' "$DEFAULT_DIR")"
  [[ -f "$dir/compose.yaml" ]] || fail "未在 $dir 找到安装。"
  docker compose -f "$dir/compose.yaml" --env-file "$dir/.env" pull
  docker compose -f "$dir/compose.yaml" --env-file "$dir/.env" up -d
  info "镜像和服务已经更新。"
}

verify_service() {
  local domain="$(prompt '规则域名' "$DEFAULT_DOMAIN")"
  info "检测状态接口……"
  curl -fsSL --connect-timeout 10 "https://$domain/_mirror/status.json" && printf '\n'
  info "检测 AI.mrs……"
  curl -fsSI --connect-timeout 10 "https://$domain/mihomo/domain/AI.mrs" | head -n 1
}

uninstall_service() {
  need_root; need_docker
  local dir="$(prompt '安装目录' "$DEFAULT_DIR")"
  [[ -f "$dir/compose.yaml" ]] || fail "未在 $dir 找到安装。"
  docker compose -f "$dir/compose.yaml" --env-file "$dir/.env" down
  info "容器已停止并移除，规则数据仍保留在 $dir。"
  if confirm "是否同时永久删除配置、证书和规则缓存？"; then
    local typed
    printf '请输入 PURGE 确认永久删除: '
    IFS= read -r typed < "$TTY_IN" || true
    if [[ "$typed" == "PURGE" && "$dir" == /opt/* && "$dir" != "/opt" ]]; then
      rm -rf -- "$dir"
      info "已永久删除 $dir。"
    else
      warn "确认文字或目录安全检查未通过，数据没有删除。"
    fi
  fi
}

show_info() {
  cat <<EOF
项目：$SOURCE_REPO
镜像：$IMAGE

PPanel 模板规则前缀：
  https://rules.coralbay.top/

建议同时在 rule-provider 锚点增加：
  proxy: 全球手动
EOF
}

menu() {
  while :; do
    clear 2>/dev/null || true
    printf '%b\n' "${cyan}========================================${reset}"
    printf '%b\n' "${cyan}        CoralBay Rules 管理菜单${reset}"
    printf '%b\n' "${cyan}========================================${reset}"
    cat <<'EOF'
  1. 安装 / 重新配置
  2. 查看运行状态
  3. 立即同步规则
  4. 查看最近日志
  5. 更新容器镜像
  6. 检测公网规则地址
  7. 卸载
  8. 查看项目信息
  0. 退出
EOF
    choice="$(prompt '请选择功能' "1")"
    case "$choice" in
      1) install_service; pause_menu ;;
      2) service_status; pause_menu ;;
      3) sync_now; pause_menu ;;
      4) show_logs; pause_menu ;;
      5) update_service; pause_menu ;;
      6) verify_service; pause_menu ;;
      7) uninstall_service; pause_menu ;;
      8) show_info; pause_menu ;;
      0) exit 0 ;;
      *) warn "无效选项"; sleep 1 ;;
    esac
  done
}

case "${1:-menu}" in
  menu) menu ;;
  install) install_service ;;
  status) service_status ;;
  sync) sync_now ;;
  logs) show_logs ;;
  update) update_service ;;
  verify) verify_service ;;
  uninstall) uninstall_service ;;
  *) fail "未知命令。可用命令：menu/install/status/sync/logs/update/verify/uninstall" ;;
esac
