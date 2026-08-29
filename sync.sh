#!/bin/sh
set -eu

repository="${RULES_REPOSITORY:-https://github.com/666OS/rules.git}"
allowed_repository="${ALLOWED_RULES_REPOSITORY:-https://github.com/666OS/rules.git}"
branch="${RULES_BRANCH:-release}"
interval="${SYNC_INTERVAL:-21600}"
mirror_domain="${MIRROR_DOMAIN:-rules.coralbay.top}"
expected_file="/app/expected-files.txt"
generator_version="${GENERATOR_VERSION:-4.11.2}"
generator_version="${generator_version#v}"
lock_dir="/data/.sync.lock"

log() {
  printf '%s [sync] %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"
}

if [ "$repository" != "$allowed_repository" ]; then
  log "拒绝未授权的上游仓库：$repository" >&2
  exit 64
fi

validate_release() {
  release_dir="$1"
  count=0
  while IFS= read -r relative_path; do
    [ -n "$relative_path" ] || continue
    if [ ! -s "$release_dir/$relative_path" ]; then
      log "校验失败：缺少或为空 $relative_path" >&2
      return 1
    fi
    count=$((count + 1))
  done < "$expected_file"

  expected_count="$(grep -cve '^[[:space:]]*$' "$expected_file")"
  [ "$count" -eq "$expected_count" ] || return 1
  VALIDATED_COUNT="$count"
}

sync_once() {
  mkdir -p /data/releases
  if ! mkdir "$lock_dir" 2>/dev/null; then
    log "已有同步任务正在运行" >&2
    return 75
  fi
  staging="/data/.staging.$$"
  geo_staging="/data/.geo-staging.$$"
  build_dir="/data/.release-build.$$"
  rm -rf "$staging"
  rm -rf "$geo_staging"
  rm -rf "$build_dir"
  trap 'rm -rf "$staging" "$geo_staging" "$build_dir"; rmdir "$lock_dir" 2>/dev/null || true' EXIT INT TERM

  log "开始同步 $repository ($branch)"
  git clone --quiet --depth 1 --single-branch --branch "$branch" "$repository" "$staging"
  commit="$(git -C "$staging" rev-parse HEAD)"
  validate_release "$staging"

  log "开始同步可读规则源 ($repository geo)"
  git clone --quiet --depth 1 --single-branch --branch geo "$repository" "$geo_staging"
  geo_commit="$(git -C "$geo_staging" rev-parse HEAD)"

  geo_short="$(printf '%s' "$geo_commit" | cut -c1-12)"
  config_hash="$(printf '%s' "$mirror_domain" | sha256sum | cut -c1-12)"
  release_id="${commit}-${geo_short}-v${generator_version}-${config_hash}"
  release_dir="/data/releases/$release_id"
  rm -rf "$staging/.git"
  mv "$staging" "$build_dir"

  # Regenerate deployment-specific files even if the upstream commit is the
  # same, so image upgrades and domain changes take effect immediately.
  mkdir -p "$build_dir/_mirror" "$build_dir/_templates/clients/original" "$build_dir/_assets" "$build_dir/_sources"
  cp -R /app/assets/icons "$build_dir/_assets/icons"
  mkdir -p "$build_dir/_sources/geo"
  cp -R "$geo_staging/site" "$geo_staging/ip" "$build_dir/_sources/geo/"
  cp "$geo_staging/LICENSE.txt" "$build_dir/_sources/geo/LICENSE.txt"
  log "生成跨客户端原生规则集"
  coralbay-ruleconvert -geo "$geo_staging" -out "$build_dir/_converted"
  log "生成客户端模板"
  rules_base_url="https://$mirror_domain/"
  assets_base_url="https://$mirror_domain/_assets/icons/"
  native_list_base_url="https://$mirror_domain/_converted/native/list/"
  # Publish a self-contained MihomoPro artifact. The filename referenced by
  # the original overwrite has been removed upstream; use the current Pro_cn
  # source and fall back to the snapshot shipped inside the image.
  mihomopro_source="$build_dir/_templates/MihomoPro.upstream.yaml"
  if curl -fsSL --connect-timeout 10 --max-time 30 \
    "https://raw.githubusercontent.com/666OS/YYDS/main/mihomo/config/cn/Pro_cn.yaml" \
    -o "$mihomopro_source.next" && [ -s "$mihomopro_source.next" ]; then
    mv -f "$mihomopro_source.next" "$mihomopro_source"
    mihomopro_origin="upstream"
  else
    rm -f "$mihomopro_source.next"
    cp /app/templates/openclash/Pro_cn.upstream.yaml "$mihomopro_source"
    mihomopro_origin="bundled-fallback"
    log "MihomoPro 上游不可用，使用镜像内置快照"
  fi
  sed \
    -e "s|https://github.com/666OS/rules/raw/release/|$rules_base_url|g" \
    -e "s|https://github.com/Koolson/Qure/raw/master/IconSet/Color/|$assets_base_url|g" \
    -e '/^external-ui-url:/d' \
    "$mihomopro_source" > "$build_dir/_templates/MihomoPro.yaml.next"
  mv -f "$build_dir/_templates/MihomoPro.yaml.next" "$build_dir/_templates/MihomoPro.yaml"
  sed "s|__MIHOMOPRO_CONFIG_URL__|https://$mirror_domain/_templates/MihomoPro.yaml|g" \
    /app/templates/openclash/MihomoPro_overwrite.conf \
    > "$build_dir/_templates/MihomoPro_overwrite.conf.next"
  mv -f "$build_dir/_templates/MihomoPro_overwrite.conf.next" "$build_dir/_templates/MihomoPro_overwrite.conf"
  sed -e "s|__RULES_BASE_URL__|$rules_base_url|g" \
    -e "s|https://github.com/Koolson/Qure/raw/master/IconSet/Color/|$assets_base_url|g" \
    /app/templates/ppanel_openclash_pro_cn.gotmpl \
    > "$build_dir/_templates/ppanel_openclash_pro_cn.gotmpl.next"
  mv -f "$build_dir/_templates/ppanel_openclash_pro_cn.gotmpl.next" \
    "$build_dir/_templates/ppanel_openclash_pro_cn.gotmpl"

  # Template center: preserve every Perfect Panel client template locally.
  for source_template in /app/templates/clients/perfect-panel/*.gotmpl; do
    client="$(basename "$source_template" .gotmpl)"
    cp "$source_template" "$build_dir/_templates/clients/original/$client.gotmpl"
    [ "$client" = "clash" ] && continue
    [ "$client" = "stash" ] && continue
    sed "s|https://github.com/Koolson/Qure/raw/master/IconSet/Color/|$assets_base_url|g" \
      "$source_template" > "$build_dir/_templates/clients/$client.gotmpl.next"
    mv -f "$build_dir/_templates/clients/$client.gotmpl.next" "$build_dir/_templates/clients/$client.gotmpl"
  done
  cp "$build_dir/_templates/ppanel_openclash_pro_cn.gotmpl" "$build_dir/_templates/clients/clash.gotmpl"
  cp "$build_dir/_templates/ppanel_openclash_pro_cn.gotmpl" "$build_dir/_templates/clients/mihomo.gotmpl"
  cp "$build_dir/_templates/ppanel_openclash_pro_cn.gotmpl" "$build_dir/_templates/clients/openclash.gotmpl"
  cp /app/templates/clients/perfect-panel/clash.gotmpl "$build_dir/_templates/clients/original/mihomo.gotmpl"
  cp /app/templates/clients/perfect-panel/clash.gotmpl "$build_dir/_templates/clients/original/openclash.gotmpl"

  # Keep Perfect Panel's node renderer, then map the complete 666OS Pro_cn
  # policy group and MRS rule layers to Stash-native fields.
  awk '/^proxy-groups:/{exit} {print}' /app/templates/clients/perfect-panel/stash.gotmpl \
    > "$build_dir/_templates/clients/stash.gotmpl.next"
  sed "s|__ASSETS_BASE_URL__|$assets_base_url|g" /app/templates/clients/stash-proxy-groups.yaml \
    >> "$build_dir/_templates/clients/stash.gotmpl.next"
  printf '\nrules:\n' >> "$build_dir/_templates/clients/stash.gotmpl.next"
  while IFS= read -r relative_path; do
    [ -n "$relative_path" ] || continue
    base="$(basename "$relative_path" .mrs)"
    suffix=""
    [ "${relative_path#mihomo/ip/}" != "$relative_path" ] && suffix="IP"
    provider="${base}${suffix}"
    case "$base" in
      Tracking|Advertising) target="广告拦截" ;;
      Private) target="DIRECT" ;;
      Speedtest) target="网络测试" ;;
      TM|Telegram) target="即时通讯" ;;
      SocialMedia) target="社交平台" ;;
      AI) target="人工智能" ;;
      Dev) target="开发服务" ;;
      Emby) target="EMBY" ;;
      Netflix|Disney|Streaming|YouTube|Spotify) target="国际媒体" ;;
      Games) target="游戏平台" ;;
      Crypto) target="货币平台" ;;
      Google) target="谷歌服务" ;;
      Facebook) target="脸书服务" ;;
      Microsoft) target="微软服务" ;;
      Apple) target="苹果服务" ;;
      Proxy) target="国外流量" ;;
      China) target="国内流量" ;;
      *) target="国外流量" ;;
    esac
    printf '  - RULE-SET,%s,%s\n' "$provider" "$target" >> "$build_dir/_templates/clients/stash.gotmpl.next"
  done < "$expected_file"
  printf '  - MATCH,漏网之鱼\n\nrule-providers:\n' >> "$build_dir/_templates/clients/stash.gotmpl.next"
  while IFS= read -r relative_path; do
    [ -n "$relative_path" ] || continue
    base="$(basename "$relative_path" .mrs)"
    behavior="domain"; suffix=""
    if [ "${relative_path#mihomo/ip/}" != "$relative_path" ]; then behavior="ipcidr"; suffix="IP"; fi
    provider="${base}${suffix}"
    printf '  %s: { type: http, behavior: %s, format: mrs, path: ./rules/%s.mrs, url: %s%s, interval: 86400 }\n' \
      "$provider" "$behavior" "$provider" "$rules_base_url" "$relative_path" \
      >> "$build_dir/_templates/clients/stash.gotmpl.next"
  done < "$expected_file"
  mv -f "$build_dir/_templates/clients/stash.gotmpl.next" "$build_dir/_templates/clients/stash.gotmpl"

  # Native text-rule clients share the same audited 666OS geo conversion
  # output, while retaining Perfect Panel's protocol-specific node renderer.
  for client in surge surfboard; do
    awk '/^\[Proxy Group\]/{exit} {print}' "/app/templates/clients/perfect-panel/$client.gotmpl" \
      > "$build_dir/_templates/clients/$client.gotmpl.next"
    printf '\n' >> "$build_dir/_templates/clients/$client.gotmpl.next"
    sed "s|__NATIVE_LIST_BASE_URL__|$native_list_base_url|g" \
      "/app/templates/clients/native/$client-tail.conf" \
      >> "$build_dir/_templates/clients/$client.gotmpl.next"
    mv -f "$build_dir/_templates/clients/$client.gotmpl.next" "$build_dir/_templates/clients/$client.gotmpl"
  done
  awk '/^\[Remote Filter\]/{exit} {print}' /app/templates/clients/perfect-panel/loon.gotmpl \
    > "$build_dir/_templates/clients/loon.gotmpl.next"
  printf '\n' >> "$build_dir/_templates/clients/loon.gotmpl.next"
  sed -e "s|__NATIVE_LIST_BASE_URL__|$native_list_base_url|g" \
    -e "s|__RULES_BASE_URL__|$rules_base_url|g" \
    -e "s|__ASSETS_BASE_URL__|$assets_base_url|g" \
    /app/templates/clients/native/loon-tail.conf \
    >> "$build_dir/_templates/clients/loon.gotmpl.next"
  mv -f "$build_dir/_templates/clients/loon.gotmpl.next" "$build_dir/_templates/clients/loon.gotmpl"
  awk '/^policy_groups:/{exit} {print}' /app/templates/clients/perfect-panel/egern.gotmpl \
    > "$build_dir/_templates/clients/egern.gotmpl.next"
  printf '\n' >> "$build_dir/_templates/clients/egern.gotmpl.next"
  sed "s|__NATIVE_LIST_BASE_URL__|$native_list_base_url|g" \
    /app/templates/clients/native/egern-tail.yaml \
    >> "$build_dir/_templates/clients/egern.gotmpl.next"
  mv -f "$build_dir/_templates/clients/egern.gotmpl.next" "$build_dir/_templates/clients/egern.gotmpl"

  # sing-box family: keep each version's node schema, but move its remote
  # routing dependencies from third-party SRS URLs to CoralBay's generated,
  # auditable source-format JSON. Version 3 rule sets work on 1.11-1.14.
  native_singbox_base_url="https://$mirror_domain/_converted/native/sing-box/"
  for client in hiddify sing-box-1.11 sing-box-1.12 sing-box-1.13 sing-box-1.14; do
    template="$build_dir/_templates/clients/$client.gotmpl"
    sed \
      -e 's|"format": "binary"|"format": "source"|g' \
      -e "s|https://anti-ad.net/anti-ad-sing-box.srs|${native_singbox_base_url}site/advertising.json|g" \
      -e "s|https://cdn.jsdmirror.com/gh/perfect-panel/rules/geo/geosite/category-ads-all.srs|${native_singbox_base_url}site/advertising.json|g" \
      -e "s|https://cdn.jsdmirror.com/gh/perfect-panel/rules/geo/geosite/github.srs|${native_singbox_base_url}site/github.json|g" \
      -e "s|https://cdn.jsdmirror.com/gh/perfect-panel/rules/geo/geoip/google.srs|${native_singbox_base_url}ip/google.json|g" \
      -e "s|https://cdn.jsdmirror.com/gh/perfect-panel/rules/geo/geosite/google.srs|${native_singbox_base_url}site/google.json|g" \
      -e "s|https://cdn.jsdmirror.com/gh/perfect-panel/rules/geo/geosite/microsoft.srs|${native_singbox_base_url}site/microsoft.json|g" \
      -e "s|https://cdn.jsdmirror.com/gh/perfect-panel/rules/geo/geosite/openai.srs|${native_singbox_base_url}site/openai.json|g" \
      -e "s|https://cdn.jsdmirror.com/gh/perfect-panel/rules/geo/geoip/telegram.srs|${native_singbox_base_url}ip/telegram.json|g" \
      -e "s|https://cdn.jsdmirror.com/gh/perfect-panel/rules/geo/geosite/telegram.srs|${native_singbox_base_url}site/telegram.json|g" \
      -e "s|https://cdn.jsdmirror.com/gh/perfect-panel/rules/geo/geoip/twitter.srs|${native_singbox_base_url}ip/twitter.json|g" \
      -e "s|https://cdn.jsdmirror.com/gh/perfect-panel/rules/geo/geosite/twitter.srs|${native_singbox_base_url}site/twitter.json|g" \
      -e "s|https://cdn.jsdmirror.com/gh/perfect-panel/rules/geo/geosite/youtube.srs|${native_singbox_base_url}site/youtube.json|g" \
      -e "s|https://cdn.jsdmirror.com/gh/perfect-panel/rules/geo/geoip/netflix.srs|${native_singbox_base_url}ip/netflix.json|g" \
      -e "s|https://cdn.jsdmirror.com/gh/perfect-panel/rules/geo/geosite/netflix.srs|${native_singbox_base_url}site/netflix.json|g" \
      -e "s|https://cdn.jsdmirror.com/gh/perfect-panel/rules/geo/geosite/disney.srs|${native_singbox_base_url}site/disney.json|g" \
      -e "s|https://cdn.jsdmirror.com/gh/perfect-panel/rules/geo/geosite/spotify.srs|${native_singbox_base_url}site/spotify.json|g" \
      -e "s|https://cdn.jsdmirror.com/gh/perfect-panel/rules/geo/geosite/apple.srs|${native_singbox_base_url}site/apple.json|g" \
      -e "s|https://cdn.jsdmirror.com/gh/perfect-panel/rules/geo-lite/geoip/apple.srs|${native_singbox_base_url}ip/apple.json|g" \
      -e "s|https://cdn.jsdmirror.com/gh/perfect-panel/rules/geo/geosite/tiktok.srs|${native_singbox_base_url}site/tiktok.json|g" \
      -e "s|https://cdn.jsdmirror.com/gh/perfect-panel/rules/geo/geosite/private.srs|${native_singbox_base_url}site/private.json|g" \
      -e "s|https://cdn.jsdmirror.com/gh/perfect-panel/rules/geo/geosite/geolocation-!cn.srs|${native_singbox_base_url}site/geolocation-!cn.json|g" \
      -e "s|https://cdn.jsdmirror.com/gh/perfect-panel/rules/geo/geosite/cn.srs|${native_singbox_base_url}site/cn.json|g" \
      -e "s|https://cdn.jsdmirror.com/gh/perfect-panel/rules/geo/geoip/cn.srs|${native_singbox_base_url}ip/cn.json|g" \
      "$template" > "$template.next"
    mv -f "$template.next" "$template"
  done
  log "校验生成产物"
  [ -s "$build_dir/_converted/manifest.json" ]
  [ -s "$build_dir/_templates/clients/clash.gotmpl" ]
  [ -s "$build_dir/_templates/clients/stash.gotmpl" ]
  [ -s "$build_dir/_templates/MihomoPro.yaml" ]
  [ -s "$build_dir/_templates/MihomoPro_overwrite.conf" ]
  if grep -Eq 'git\.imee\.me|github\.com/666OS/rules/raw|github\.com/Koolson/Qure/raw' \
    "$build_dir/_templates/MihomoPro.yaml" "$build_dir/_templates/MihomoPro_overwrite.conf"; then
    log "校验失败：MihomoPro 产物仍包含应本地化的外链" >&2
    return 1
  fi
  validate_ini_template() {
    template="$1"; shift
    previous=0
    for section in "$@"; do
      count="$(grep -Fxc "[$section]" "$template")"
      [ "$count" -eq 1 ] || { log "校验失败：$(basename "$template") 的 [$section] 出现 $count 次" >&2; return 1; }
      line="$(grep -Fn "[$section]" "$template" | cut -d: -f1)"
      [ "$line" -gt "$previous" ] || { log "校验失败：$(basename "$template") 的 [$section] 顺序错误" >&2; return 1; }
      previous="$line"
    done
  }
  validate_ini_template "$build_dir/_templates/clients/loon.gotmpl" "Proxy" "Remote Filter" "Proxy Group" "URL Rewrite" "Remote Rule" "Rule"
  if grep -Eq 'reality-public-key|reality-short-id|tls-name=' "$build_dir/_templates/clients/loon.gotmpl"; then
    log "ERROR" "Loon 模板包含不兼容的 Reality 字段"
    exit 1
  fi
  validate_ini_template "$build_dir/_templates/clients/surge.gotmpl" "Proxy" "Proxy Group" "Rule"
  validate_ini_template "$build_dir/_templates/clients/surfboard.gotmpl" "Proxy" "Proxy Group" "Rule"
  if awk '/^\[Proxy\]/{proxy=1;next} /^\[/{proxy=0} proxy && /rules\.coralbay\.top|__RULES_BASE_URL__|__NATIVE_LIST_BASE_URL__/{exit 1}' "$build_dir/_templates/clients/loon.gotmpl"; then :; else
    log "校验失败：Loon 规则 URL 混入 [Proxy] 节点区" >&2; return 1
  fi
  if find "$build_dir" -type l -print | grep -q .; then
    log "校验失败：发布产物中不允许符号链接" >&2
    return 1
  fi
  cat > "$build_dir/_mirror/status.json.next" <<EOF
{"ok":true,"repository":"$repository","branch":"$branch","commit":"$commit","geo_commit":"$geo_commit","release_id":"$release_id","generator_version":"$generator_version","synced_at":"$(date -u +%Y-%m-%dT%H:%M:%SZ)","validated_files":$VALIDATED_COUNT,"mihomopro_origin":"$mihomopro_origin"}
EOF
  mv -f "$build_dir/_mirror/status.json.next" "$build_dir/_mirror/status.json"
  cat > "$build_dir/index.html.next" <<EOF
<!doctype html>
<html lang="zh-CN">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>CoralBay Rules</title></head>
<body style="font-family:system-ui,sans-serif;max-width:760px;margin:60px auto;padding:0 20px;line-height:1.7">
<h1>CoralBay Rules</h1>
<p>666OS Mihomo MRS 自托管规则镜像。</p>
<ul>
  <li><a href="/_mirror/status.json">同步状态</a></li>
  <li><a href="/_templates/ppanel_openclash_pro_cn.gotmpl" download>PPanel OpenClash Pro_cn 订阅模板</a></li>
  <li><a href="/mihomo/domain/AI.mrs">规则文件示例：AI.mrs</a></li>
</ul>
<p>当前上游版本：<code>$commit</code></p>
</body></html>
EOF
  mv -f "$build_dir/index.html.next" "$build_dir/index.html"

  log "发布不可变版本 $release_id"
  if [ -d "$release_dir" ]; then
    rm -rf "$build_dir"
  else
    mv "$build_dir" "$release_dir"
  fi
  current_next="/data/.current.$$"
  ln -s "releases/$release_id" "$current_next"
  # -T is essential on BusyBox: without it an existing directory symlink is
  # followed and the temporary link is moved inside the active release.
  mv -Tf "$current_next" /data/current

  # 保留当前版本以及最近两个历史版本。
  ls -1dt /data/releases/* 2>/dev/null | awk 'NR > 3' | while IFS= read -r old_release; do
    [ "$old_release" = "$release_dir" ] || rm -rf "$old_release"
  done

  rm -rf "$geo_staging"
  rmdir "$lock_dir" 2>/dev/null || true
  trap - EXIT INT TERM
  log "同步完成，当前版本 $release_id，共校验 $VALIDATED_COUNT 个文件"
}

run_once="false"
[ "${1:-}" = "once" ] && run_once="true"

if [ "$run_once" = "true" ]; then
  sync_once
  exit 0
fi

while :; do
  if ! sync_once; then
    log "同步失败，继续保留上一次有效版本" >&2
  fi
  sleep "$interval"
done
