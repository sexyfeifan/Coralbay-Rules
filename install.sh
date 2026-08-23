#!/usr/bin/env bash
set -Eeuo pipefail

APP_NAME="CoralBay Rules"
IMAGE="${CORALBAY_IMAGE:-sexyfeifan/coralbay-rules:latest}"
DEFAULT_DIR="/opt/coralbay-rules"
DEFAULT_INTERVAL="21600"
DEFAULT_LOCAL_PORT="3999"
SOURCE_REPO="https://github.com/sexyfeifan/Coralbay-Rules"
MANAGER_URL="https://raw.githubusercontent.com/sexyfeifan/Coralbay-Rules/main/install.sh"
MANAGER_PATH="/usr/local/libexec/coralbay-rules-manager"

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

prompt_required() {
  local label="$1" value
  while :; do
    printf '%s: ' "$label" >&2
    IFS= read -r value < "$TTY_IN" || true
    [[ -n "$value" ]] && { printf '%s' "$value"; return; }
    warn "$label 不能为空。" >&2
  done
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

install_manager_command() {
  local temp_script
  temp_script="$(mktemp /tmp/coralbay-rules-manager.XXXXXX)"
  if curl -fsSL --retry 3 --connect-timeout 15 "$MANAGER_URL" -o "$temp_script"; then
    install -d -m 0755 "$(dirname "$MANAGER_PATH")"
    install -m 0755 "$temp_script" "$MANAGER_PATH"
    ln -sfn "$MANAGER_PATH" /usr/local/bin/rules
    ln -sfn "$MANAGER_PATH" /usr/local/bin/666
    rm -f -- /usr/local/bin/coralbay-rules /usr/local/bin/luse /usr/local/bin/六六六
    info "管理快捷命令已安装：rules、666"
  else
    warn "管理快捷命令下载失败；服务安装仍会继续。"
  fi
  rm -f -- "$temp_script"
}

backup_config() {
  local dir="$1" stamp
  [[ -f "$dir/.env" || -f "$dir/compose.yaml" ]] || return 0
  stamp="$(date +%Y%m%d-%H%M%S)"
  install -d -m 0700 "$dir/backups/$stamp"
  [[ -f "$dir/.env" ]] && cp -p "$dir/.env" "$dir/backups/$stamp/.env"
  [[ -f "$dir/compose.yaml" ]] && cp -p "$dir/compose.yaml" "$dir/backups/$stamp/compose.yaml"
  [[ -f "$dir/Caddyfile" ]] && cp -p "$dir/Caddyfile" "$dir/backups/$stamp/Caddyfile"
  info "原配置已备份到 $dir/backups/$stamp"
}

write_compose() {
  local dir="$1"
  cat > "$dir/compose.yaml" <<'EOF'
services:
  app:
    image: ${APP_IMAGE}
    container_name: coralbay-rules
    restart: unless-stopped
    environment:
      RULES_REPOSITORY: ${RULES_REPOSITORY}
      RULES_BRANCH: ${RULES_BRANCH}
      SYNC_INTERVAL: ${SYNC_INTERVAL}
      MIRROR_DOMAIN: ${MIRROR_DOMAIN}
      ADMIN_AUTH_DISABLED: ${ADMIN_AUTH_DISABLED}
      UPDATER_URL: http://updater:8080/v1/update
      UPDATER_TOKEN: ${UPDATER_TOKEN}
    labels:
      com.centurylinklabs.watchtower.scope: coralbay-rules
    volumes:
      - ./data:/data
    healthcheck:
      test: ["CMD-SHELL", "curl -fsS http://127.0.0.1:8080/healthz >/dev/null"]
      interval: 30s
      timeout: 5s
      retries: 5
      start_period: 60s
    ports:
      - "127.0.0.1:${LOCAL_PORT}:8080"

  updater:
    image: nickfedor/watchtower:1.20.3
    container_name: coralbay-rules-updater
    restart: unless-stopped
    command: --scope coralbay-rules --cleanup
    environment:
      WATCHTOWER_HTTP_API_TOKEN: ${UPDATER_TOKEN}
      WATCHTOWER_HTTP_API_ENDPOINTS: update
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
    labels:
      com.centurylinklabs.watchtower.enable: "false"
EOF
}

install_service() {
  need_root; need_docker
  local dir domain interval local_port updater_token
  dir="$(prompt '安装目录' "$DEFAULT_DIR")"
  load_env "$dir"
  domain="$(prompt_required '规则域名（例如 rules.example.com）')"
  valid_domain "$domain" || fail "域名格式不正确：$domain"
  while :; do
    local_port="$(prompt '本地监听端口' "${LOCAL_PORT:-$DEFAULT_LOCAL_PORT}")"
    [[ "$local_port" =~ ^[0-9]+$ && "$local_port" -ge 1024 && "$local_port" -le 65535 ]] || {
      warn "端口必须是 1024 到 65535 之间的数字。"
      continue
    }
    if port_is_busy "$local_port" && ! { [[ -f "$dir/compose.yaml" ]] && [[ "${LOCAL_PORT:-}" == "$local_port" ]]; }; then
      warn "端口 $local_port 已被系统进程或 Docker 容器占用，请换一个端口。"
      continue
    fi
    break
  done

  printf '\n同步间隔：\n  1) 1 小时\n  2) 6 小时（推荐）\n  3) 12 小时\n  4) 24 小时\n  5) 自定义秒数\n'
  case "$(prompt '请选择' "2")" in
    1) interval=3600 ;;
    3) interval=43200 ;;
    4) interval=86400 ;;
    5) interval="$(prompt '同步间隔（秒）' "$DEFAULT_INTERVAL")" ;;
    *) interval=21600 ;;
  esac
  [[ "$interval" =~ ^[0-9]+$ && "$interval" -ge 3600 ]] || fail "同步间隔至少为 3600 秒。"

  updater_token="${UPDATER_TOKEN:-$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')}"

  install_manager_command
  install -d -m 0755 "$dir/data" "$dir/caddy_data" "$dir/caddy_config"
  backup_config "$dir"
  cat > "$dir/.env" <<EOF
APP_IMAGE=$IMAGE
MIRROR_DOMAIN=$domain
SYNC_INTERVAL=$interval
RULES_REPOSITORY=https://github.com/666OS/rules.git
RULES_BRANCH=release
DEPLOY_MODE=proxy
LOCAL_PORT=$local_port
ADMIN_AUTH_DISABLED=true
UPDATER_TOKEN=$updater_token
EOF
  chmod 600 "$dir/.env"
  write_compose "$dir"

  docker compose -f "$dir/compose.yaml" --env-file "$dir/.env" config --quiet

  docker compose -f "$dir/compose.yaml" --env-file "$dir/.env" pull
  docker compose -f "$dir/compose.yaml" --env-file "$dir/.env" up -d

  local healthy="false"
  for _ in {1..30}; do
    if docker compose -f "$dir/compose.yaml" --env-file "$dir/.env" exec -T app curl -fsS http://127.0.0.1:8080/healthz >/dev/null 2>&1; then
      healthy="true"
      break
    fi
    sleep 2
  done
  [[ "$healthy" == "true" ]] || warn "容器尚未通过健康检查，请通过 coralbay-rules logs 查看原因。"

  info "服务已启动，规则首次同步可能需要几十秒。"
  info "本地首页：http://127.0.0.1:$local_port/"
  info "本地后台：http://127.0.0.1:$local_port/admin/"
  warn "请在现有 Nginx/PPanel/OpenResty 中把 $domain 反向代理到 127.0.0.1:$local_port，并在现有面板管理 HTTPS 证书。"
  info "今后在 SSH 中输入 rules 或 666 即可重新打开管理菜单。"
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
  docker compose -f "$dir/compose.yaml" --env-file "$dir/.env" exec -T app /usr/local/bin/coralbay-rules-sync once
  info "规则同步已完成。"
}

show_ppanel_template() {
  local dir="$(prompt '安装目录' "$DEFAULT_DIR")"
  [[ -f "$dir/.env" ]] || fail "未在 $dir 找到安装。"
  load_env "$dir"
  local url="https://${MIRROR_DOMAIN}/_templates/ppanel_openclash_pro_cn.gotmpl"
  printf '\nPPanel 订阅模板下载链接：\n%b%s%b\n\n' "$cyan" "$url" "$reset"
  info "下载后进入 PPanel → 订阅配置 → 客户端管理 → OpenClash Pro → 模板，手动全选替换。"
  if curl -fsSI --connect-timeout 10 "$url" >/dev/null 2>&1; then
    info "链接检测正常。"
  else
    warn "当前无法从公网访问该链接；请确认首次同步、DNS、证书和反向代理配置。"
  fi
}

certificate_menu() {
  local dir="$(prompt '安装目录' "$DEFAULT_DIR")"
  [[ -f "$dir/.env" && -f "$dir/compose.yaml" ]] || fail "未在 $dir 找到安装。"
  load_env "$dir"
  warn "HTTPS 证书由现有 Nginx/PPanel/OpenResty 管理，本项目不会占用 80/443 或单独申请证书。"
  info "本项目后端地址：http://127.0.0.1:${LOCAL_PORT:-$DEFAULT_LOCAL_PORT}"
  info "正在检测公网 HTTPS……"
  curl -fsSI --connect-timeout 15 "https://${MIRROR_DOMAIN}/_mirror/status.json" | head -n 5
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
  info "正在升级管理脚本……"
  install_manager_command
  info "正在拉取最新容器镜像……"
  docker compose -f "$dir/compose.yaml" --env-file "$dir/.env" pull
  docker compose -f "$dir/compose.yaml" --env-file "$dir/.env" up -d
  info "管理脚本、容器镜像和服务已经全部升级。"
}

verify_service() {
  local domain="$(prompt_required '规则域名（例如 rules.example.com）')"
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
  4. 获取 PPanel 订阅模板下载链接
  5. HTTPS 证书管理
  6. 查看最近日志
  7. 升级程序（管理脚本 + 容器镜像）
  8. 检测公网规则地址
  9. 卸载
 10. 查看项目信息
  0. 退出
EOF
    choice="$(prompt '请选择功能' "1")"
    case "$choice" in
      1) install_service; pause_menu ;;
      2) service_status; pause_menu ;;
      3) sync_now; pause_menu ;;
      4) show_ppanel_template; pause_menu ;;
      5) certificate_menu; pause_menu ;;
      6) show_logs; pause_menu ;;
      7) update_service; pause_menu ;;
      8) verify_service; pause_menu ;;
      9) uninstall_service; pause_menu ;;
      10) show_info; pause_menu ;;
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
  template) show_ppanel_template ;;
  certificate) certificate_menu ;;
  logs) show_logs ;;
  update) update_service ;;
  verify) verify_service ;;
  uninstall) uninstall_service ;;
  *) fail "未知命令。可用命令：menu/install/status/sync/template/certificate/logs/update/verify/uninstall" ;;
esac
