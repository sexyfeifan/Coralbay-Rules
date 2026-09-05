#!/usr/bin/env bash
set -Eeuo pipefail

IMAGE="${CORALBAY_IMAGE:-sexyfeifan/coralbay-rules:latest}"
DEFAULT_DIR="/opt/coralbay-rules"
DEFAULT_INTERVAL="21600"
DEFAULT_LOCAL_PORT="3999"
SOURCE_REPO="https://github.com/sexyfeifan/Coralbay-Rules"
MANAGER_URL="https://raw.githubusercontent.com/sexyfeifan/Coralbay-Rules/main/install.sh"
MANAGER_PATH="/usr/local/libexec/coralbay-rules-manager"
INSTALL_DIR_FILE="/etc/coralbay-rules/install-dir"
umask 077
[[ ! -f "$INSTALL_DIR_FILE" ]] || IFS= read -r DEFAULT_DIR < "$INSTALL_DIR_FILE"

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
  IFS= read -r value < "$TTY_IN" || fail "无法读取输入，请在交互终端运行安装脚本。"
  printf '%s' "${value:-$default}"
}

prompt_required() {
  local label="$1" value
  while :; do
    printf '%s: ' "$label" >&2
    IFS= read -r value < "$TTY_IN" || fail "无法读取输入，请在交互终端运行安装脚本。"
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
  [[ "$(uname -s)" == Linux ]] || fail "安装脚本用于 Linux 服务器。"
  local tool
  for tool in curl realpath od awk; do
    command -v "$tool" >/dev/null 2>&1 || fail "缺少系统命令：$tool，请先安装 curl 和 coreutils。"
  done
  command -v docker >/dev/null 2>&1 || fail "尚未安装 Docker Engine。请先安装 Docker。"
  docker compose version >/dev/null 2>&1 || fail "缺少 Docker Compose 插件。"
  docker info >/dev/null 2>&1 || fail "Docker 服务没有运行。"
}

valid_domain() {
  local label domain="$1"
  [[ ${#domain} -le 253 && "$domain" == *.* && "$domain" =~ ^[A-Za-z0-9.-]+\.[A-Za-z]{2,}$ ]] || return 1
  local -a labels
  IFS=. read -r -a labels <<< "$domain"
  for label in "${labels[@]}"; do
    [[ ${#label} -ge 1 && ${#label} -le 63 && "$label" =~ ^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?$ ]] || return 1
  done
}

installation_dir() {
  local dir="${CORALBAY_INSTALL_DIR:-}"
  [[ -n "$dir" ]] || dir="$(prompt '安装目录' "$DEFAULT_DIR")"
  [[ "$dir" == /* ]] || fail "安装目录必须是绝对路径。"
  dir="$(realpath -m -- "$dir")"
  case "$dir" in /|/opt|/usr|/usr/local|/etc|/var|/srv|/home|/root) fail "请使用独立的应用子目录。" ;; esac
  printf '%s' "$dir"
}

remember_installation() {
  install -d -m 0700 "$(dirname "$INSTALL_DIR_FILE")"
  printf '%s\n' "$1" > "$INSTALL_DIR_FILE"
  DEFAULT_DIR="$1"
}

prompt_password() {
  local password again
  while :; do
    printf '设置管理员密码（至少 12 位，输入隐藏）: ' >&2
    IFS= read -rs password < "$TTY_IN" || fail "无法读取密码。"
    printf '\n' >&2
    [[ ${#password} -ge 12 && "$password" =~ ^[A-Za-z0-9._@+-]+$ ]] || {
      warn "请使用至少 12 位字母、数字或 . _ @ + -。" >&2
      continue
    }
    printf '再次输入管理员密码: ' >&2
    IFS= read -rs again < "$TTY_IN" || fail "无法读取密码。"
    printf '\n' >&2
    [[ "$password" == "$again" ]] && { printf '%s' "$password"; return; }
    warn "两次密码不一致。" >&2
  done
}

port_owned_by_app() {
  local dir="$1" port="$2" owner binding
  owner="$(docker inspect -f '{{index .Config.Labels "com.docker.compose.project.working_dir"}}' coralbay-rules 2>/dev/null)" || return 1
  [[ "$owner" == "$dir" ]] || return 1
  binding="$(docker port coralbay-rules 8080/tcp 2>/dev/null)" || return 1
  [[ "$binding" == "127.0.0.1:$port" ]]
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
  local dir="${1:-$DEFAULT_DIR}" line key value
  local value_pattern='^[A-Za-z0-9_./:@%+?=&,-]*$'
  unset APP_IMAGE MIRROR_DOMAIN SYNC_INTERVAL RULES_REPOSITORY RULES_BRANCH DEPLOY_MODE LOCAL_PORT ADMIN_ACTION_TOKEN ADMIN_PASSWORD UPDATER_TOKEN
  if [[ -f "$dir/.env" ]]; then
    while IFS= read -r line || [[ -n "$line" ]]; do
      line="${line%$'\r'}"
      [[ -z "$line" || "$line" == \#* ]] && continue
      [[ "$line" == *=* ]] || fail "环境文件格式错误。"
      key="${line%%=*}"; value="${line#*=}"
      case "$key" in
        APP_IMAGE|MIRROR_DOMAIN|SYNC_INTERVAL|RULES_REPOSITORY|RULES_BRANCH|DEPLOY_MODE|LOCAL_PORT|ADMIN_ACTION_TOKEN|ADMIN_PASSWORD|UPDATER_TOKEN)
          [[ "$value" =~ $value_pattern ]] || fail "环境字段 $key 含有不支持的字符，未执行其内容。"
          printf -v "$key" '%s' "$value"
          ;;
      esac
    done < "$dir/.env"
  fi
}

install_manager_command() {
  local temp_script mode="${1:-local}"
  temp_script="$(mktemp /tmp/coralbay-rules-manager.XXXXXX)"
  if [[ "$mode" == local && -f "${BASH_SOURCE[0]:-}" ]]; then
    cp "${BASH_SOURCE[0]}" "$temp_script"
  elif ! curl -fsSL --retry 3 --connect-timeout 15 --max-time 120 "${MANAGER_URL}?t=$(date +%s)" -o "$temp_script"; then
    rm -f -- "$temp_script"
    fail "管理脚本下载失败，未替换现有脚本。"
  fi
  if [[ -s "$temp_script" ]] && bash -n "$temp_script"; then
    install -d -m 0755 "$(dirname "$MANAGER_PATH")"
    install -m 0755 "$temp_script" "$MANAGER_PATH.next"
    mv -f "$MANAGER_PATH.next" "$MANAGER_PATH"
    ln -sfn "$MANAGER_PATH" /usr/local/bin/rules
    ln -sfn "$MANAGER_PATH" /usr/local/bin/666
    local shortcut
    for shortcut in /usr/local/bin/coralbay-rules /usr/local/bin/luse /usr/local/bin/六六六; do
      if [[ -L "$shortcut" && "$(readlink "$shortcut")" == "$MANAGER_PATH" ]]; then
        rm -f -- "$shortcut"
      fi
    done
    info "管理快捷命令已安装：rules、666"
  else
    rm -f -- "$temp_script"
    fail "下载的管理脚本无效，未替换现有脚本。"
  fi
  rm -f -- "$temp_script"
}

backup_config() {
  local dir="$1" backup_dir
  [[ -f "$dir/.env" || -f "$dir/compose.yaml" ]] || return 0
  install -d -m 0700 "$dir/backups"
  backup_dir="$(mktemp -d "$dir/backups/$(date +%Y%m%d-%H%M%S).XXXXXX")"
  [[ -f "$dir/.env" ]] && cp -p "$dir/.env" "$backup_dir/.env"
  [[ -f "$dir/compose.yaml" ]] && cp -p "$dir/compose.yaml" "$backup_dir/compose.yaml"
  [[ -f "$dir/Caddyfile" ]] && cp -p "$dir/Caddyfile" "$backup_dir/Caddyfile"
  info "原配置已备份到 $backup_dir"
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
      ADMIN_ACTION_TOKEN: ${ADMIN_ACTION_TOKEN}
      ADMIN_PASSWORD: ${ADMIN_PASSWORD}
      SUBCONVERTER_URL: http://subconverter:25500
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
    networks: [default, converter_private]
    ports:
      - "127.0.0.1:${LOCAL_PORT}:8080"

  subconverter:
    image: ghcr.io/jungley8/subconverter-ng:latest
    container_name: coralbay-subconverter
    restart: unless-stopped
    networks: [converter_private]
    environment:
      SUBNG_PROXY: http://coralbay:${ADMIN_ACTION_TOKEN}@app:8081
      http_proxy: http://coralbay:${ADMIN_ACTION_TOKEN}@app:8081
      https_proxy: http://coralbay:${ADMIN_ACTION_TOKEN}@app:8081
      HTTP_PROXY: http://coralbay:${ADMIN_ACTION_TOKEN}@app:8081
      HTTPS_PROXY: http://coralbay:${ADMIN_ACTION_TOKEN}@app:8081
      NO_PROXY: ""
      no_proxy: ""

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
networks:
  converter_private:
    internal: true
EOF
}

write_env() {
  local dir="$1"
  valid_domain "${MIRROR_DOMAIN:-}" || fail "配置中的规则域名无效。"
  cat > "$dir/.env" <<EOF
APP_IMAGE=${CORALBAY_IMAGE:-${APP_IMAGE:-$IMAGE}}
MIRROR_DOMAIN=$MIRROR_DOMAIN
SYNC_INTERVAL=${SYNC_INTERVAL:-$DEFAULT_INTERVAL}
RULES_REPOSITORY=${RULES_REPOSITORY:-https://github.com/666OS/rules.git}
RULES_BRANCH=${RULES_BRANCH:-release}
DEPLOY_MODE=proxy
LOCAL_PORT=${LOCAL_PORT:-$DEFAULT_LOCAL_PORT}
ADMIN_ACTION_TOKEN=$ADMIN_ACTION_TOKEN
ADMIN_PASSWORD=$ADMIN_PASSWORD
UPDATER_TOKEN=$UPDATER_TOKEN
EOF
  chmod 600 "$dir/.env"
}

wait_for_service() {
  local dir="$1" attempt
  for ((attempt=0; attempt<30; attempt++)); do
    if docker compose -f "$dir/compose.yaml" --env-file "$dir/.env" exec -T app sh -c \
      'curl -fsS --max-time 3 http://127.0.0.1:8080/healthz >/dev/null && curl -fsS --max-time 3 http://subconverter:25500/version >/dev/null' >/dev/null 2>&1; then
      return 0
    fi
    sleep 2
  done
  docker compose -f "$dir/compose.yaml" --env-file "$dir/.env" ps >&2 || true
  return 1
}

deploy_config() {
  local dir="$1" stage="$2" interval="${3:-}" file
  # Resolve relative volumes against the real installation, not staging.
  if ! docker compose --project-directory "$dir" -f "$stage/compose.yaml" --env-file "$stage/.env" config --quiet ||
     ! docker compose --project-directory "$dir" -f "$stage/compose.yaml" --env-file "$stage/.env" pull; then
    rm -rf -- "$stage"
    fail "配置校验或镜像拉取失败，原配置和运行服务未变更。"
  fi
  backup_config "$dir"
  [[ ! -f "$dir/.env" ]] || cp -p "$dir/.env" "$stage/previous.env"
  [[ ! -f "$dir/compose.yaml" ]] || cp -p "$dir/compose.yaml" "$stage/previous.compose.yaml"
  if [[ -n "$interval" ]]; then
    for file in settings.json schedule.json; do
      [[ ! -f "$dir/data/$file" ]] || cp -p "$dir/data/$file" "$stage/previous.$file"
    done
    printf '{"interval_seconds":%s}\n' "$interval" > "$dir/data/settings.json.next"
    mv -f "$dir/data/settings.json.next" "$dir/data/settings.json"
    rm -f "$dir/data/schedule.json"
  fi
  mv -f "$stage/.env" "$dir/.env"
  mv -f "$stage/compose.yaml" "$dir/compose.yaml"
  if ! docker compose -f "$dir/compose.yaml" --env-file "$dir/.env" up -d --force-recreate || ! wait_for_service "$dir"; then
    if [[ -f "$stage/previous.env" && -f "$stage/previous.compose.yaml" ]]; then
      cp -p "$stage/previous.env" "$dir/.env"
      cp -p "$stage/previous.compose.yaml" "$dir/compose.yaml"
    fi
    if [[ -n "$interval" ]]; then
      for file in settings.json schedule.json; do
        if [[ -f "$stage/previous.$file" ]]; then
          cp -p "$stage/previous.$file" "$dir/data/$file"
        else
          rm -f "$dir/data/$file"
        fi
      done
    fi
    rm -rf -- "$stage"
    fail "服务启动检查失败；原有配置已保留或恢复，数据未删除。请执行 rules logs 排查；已拉取的镜像不会自动回退。"
  fi
  rm -rf -- "$stage"
}

install_service() {
  need_root; need_docker
  local dir domain interval local_port updater_token action_token admin_password
  dir="$(installation_dir)"
  local owner
  if owner="$(docker inspect -f '{{index .Config.Labels "com.docker.compose.project.working_dir"}}' coralbay-rules 2>/dev/null)"; then
    [[ "$owner" == "$dir" ]] || fail "已有 CoralBay 容器属于其他安装目录：$owner，请使用该目录管理。"
  fi
  load_env "$dir"
  domain="$(prompt_required '规则域名（例如 rules.example.com）')"
  valid_domain "$domain" || fail "域名格式不正确：$domain"
  while :; do
    local_port="$(prompt '本地监听端口' "${LOCAL_PORT:-$DEFAULT_LOCAL_PORT}")"
    [[ "$local_port" =~ ^[0-9]+$ && "$local_port" -ge 1024 && "$local_port" -le 65535 ]] || {
      warn "端口必须是 1024 到 65535 之间的数字。"
      continue
    }
    if port_is_busy "$local_port" && ! port_owned_by_app "$dir" "$local_port"; then
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
  [[ "$interval" =~ ^[0-9]+$ && "$interval" -ge 3600 && "$interval" -le 604800 ]] || fail "同步间隔必须在 3600 到 604800 秒之间。"

  updater_token="${UPDATER_TOKEN:-$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')}"
  action_token="${ADMIN_ACTION_TOKEN:-$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')}"
  admin_password="${ADMIN_PASSWORD:-}"
  [[ -n "$admin_password" ]] || admin_password="$(prompt_password)"

  install -d -m 0700 "$dir" "$dir/data"
  local stage
  stage="$(mktemp -d "$dir/.config.XXXXXX")"
  APP_IMAGE="${CORALBAY_IMAGE:-${APP_IMAGE:-$IMAGE}}"
  MIRROR_DOMAIN="$domain"; SYNC_INTERVAL="$interval"; LOCAL_PORT="$local_port"
  ADMIN_ACTION_TOKEN="$action_token"; ADMIN_PASSWORD="$admin_password"; UPDATER_TOKEN="$updater_token"
  write_env "$stage"
  write_compose "$stage"
  deploy_config "$dir" "$stage" "$interval"
  install_manager_command
  remember_installation "$dir"

  info "服务已启动，规则首次同步可能需要几十秒。"
  info "控制台：https://$domain/（配置反向代理和证书后可访问）"
  warn "请在现有 Nginx/PPanel/OpenResty 中把 $domain 反向代理到 127.0.0.1:$local_port，并在现有面板管理 HTTPS 证书。"
  info "今后在 SSH 中输入 rules 或 666 即可重新打开管理菜单。"
  info "控制台密码已设置；公网访问时请使用 HTTPS。"
}

service_status() {
  need_docker
  local dir
  dir="$(installation_dir)"
  [[ -f "$dir/compose.yaml" ]] || fail "未在 $dir 找到安装。"
  docker compose -f "$dir/compose.yaml" --env-file "$dir/.env" ps
  if [[ -s "$dir/data/current/_mirror/status.json" ]]; then
    printf '\n当前规则版本：\n'; cat "$dir/data/current/_mirror/status.json"; printf '\n'
  fi
}

sync_now() {
  need_root; need_docker
  local dir
  dir="$(installation_dir)"
  [[ -f "$dir/compose.yaml" ]] || fail "未在 $dir 找到安装。"
  docker compose -f "$dir/compose.yaml" --env-file "$dir/.env" exec -T app /usr/local/bin/coralbay-rules-sync once
  info "规则同步已完成。"
}

show_ppanel_template() {
  local dir
  dir="$(installation_dir)"
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
  local dir
  dir="$(installation_dir)"
  [[ -f "$dir/.env" && -f "$dir/compose.yaml" ]] || fail "未在 $dir 找到安装。"
  load_env "$dir"
  warn "HTTPS 证书由现有 Nginx/PPanel/OpenResty 管理，本项目不会占用 80/443 或单独申请证书。"
  info "本项目后端地址：http://127.0.0.1:${LOCAL_PORT:-$DEFAULT_LOCAL_PORT}"
  info "正在检测公网 HTTPS……"
  curl -fsSI --connect-timeout 15 "https://${MIRROR_DOMAIN}/_mirror/status.json" | head -n 5
}

show_logs() {
  need_docker
  local dir
  dir="$(installation_dir)"
  [[ -f "$dir/compose.yaml" ]] || fail "未在 $dir 找到安装。"
  docker compose -f "$dir/compose.yaml" --env-file "$dir/.env" logs --tail=100
}

update_service() {
  need_root; need_docker
  local dir stage
  dir="$(installation_dir)"
  [[ -f "$dir/compose.yaml" && -f "$dir/.env" ]] || fail "未在 $dir 找到安装。"
  if [[ "${CORALBAY_MANAGER_UPDATED:-}" != 1 ]]; then
    info "正在升级管理脚本……"
    install_manager_command download
    exec env CORALBAY_MANAGER_UPDATED=1 CORALBAY_INSTALL_DIR="$dir" "$MANAGER_PATH" update
  fi
  load_env "$dir"
  ADMIN_ACTION_TOKEN="${ADMIN_ACTION_TOKEN:-$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')}"
  UPDATER_TOKEN="${UPDATER_TOKEN:-$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')}"
  [[ -n "${ADMIN_PASSWORD:-}" ]] || ADMIN_PASSWORD="$(prompt_password)"
  stage="$(mktemp -d "$dir/.config.XXXXXX")"
  write_env "$stage"
  write_compose "$stage"
  deploy_config "$dir" "$stage"
  remember_installation "$dir"
  info "管理脚本、Compose 配置和容器镜像已升级，应用与订阅后端检查通过。"
}

change_password() {
  need_root; need_docker
  local dir password
  dir="$(installation_dir)"
  [[ -f "$dir/.env" ]] || fail "未在 $dir 找到安装。"
  password="$(prompt_password)"
  [[ ${#password} -ge 8 ]] || fail "密码至少需要 8 个字符。"
  [[ "$password" =~ ^[A-Za-z0-9._@+-]+$ ]] || fail "密码仅支持字母、数字和 . _ @ + -，避免 Compose 环境文件转义错误。"
  backup_config "$dir"
  if grep -q '^ADMIN_PASSWORD=' "$dir/.env"; then
    sed -i.bak "s|^ADMIN_PASSWORD=.*|ADMIN_PASSWORD=$password|" "$dir/.env"
  else
    printf '\nADMIN_PASSWORD=%s\n' "$password" >> "$dir/.env"
  fi
  chmod 600 "$dir/.env"
  docker compose -f "$dir/compose.yaml" --env-file "$dir/.env" up -d --force-recreate app
  wait_for_service "$dir" || fail "密码已保存，但服务检查失败，请执行 rules logs。"
  info "管理员密码已更新，已有网页登录会话将失效。"
}

verify_service() {
  local domain
  domain="$(prompt_required '规则域名（例如 rules.example.com）')"
  info "检测状态接口……"
  curl -fsSL --connect-timeout 10 "https://$domain/_mirror/status.json" && printf '\n'
  info "检测 AI.mrs……"
  curl -fsSI --connect-timeout 10 "https://$domain/mihomo/domain/AI.mrs" | head -n 1
}

uninstall_service() {
  need_root; need_docker
  local dir
  dir="$(installation_dir)"
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
 11. 修改管理员密码
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
      11) change_password; pause_menu ;;
      0) exit 0 ;;
      *) warn "无效选项"; sleep 1 ;;
    esac
  done
}

[[ "${BASH_SOURCE[0]:-}" != "$0" && -n "${BASH_SOURCE[0]:-}" ]] && return 0

action="${1:-}"
if [[ -z "$action" ]]; then
  if [[ -z "${BASH_SOURCE[0]:-}" || "${BASH_SOURCE[0]}" == "/dev/stdin" ]]; then
    action="install"
  else
    action="menu"
  fi
fi

case "$action" in
  menu) menu ;;
  install) install_service ;;
  status) service_status ;;
  sync) sync_now ;;
  template) show_ppanel_template ;;
  certificate) certificate_menu ;;
  logs) show_logs ;;
  update) update_service ;;
  password) change_password ;;
  verify) verify_service ;;
  uninstall) uninstall_service ;;
  *) fail "未知命令。可用命令：menu/install/status/sync/template/certificate/logs/update/password/verify/uninstall" ;;
esac
