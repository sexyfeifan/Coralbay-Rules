(() => {
  'use strict';
  const el = id => document.getElementById(id);
  const labels = {local:'本机镜像', upstream:'上游源'};
  let catalog = null, itemID = '', source = 'local', generation = 0, controller = null;
  let pending = false, result = null, yaml = '', yamlURL = '';

  function currentItem() { return catalog?.items?.find(item => item.id === itemID); }
  function usable() { return catalog?.available === true && !!currentItem(); }
  function status(message, bad = false) {
    el('mmRulesStatus').textContent = message;
    el('mmRulesStatus').classList.toggle('bad', bad);
  }
  function beginRequest() {
    controller?.abort();
    controller = new AbortController();
    return {ticket:++generation, signal:controller.signal};
  }
  function localPreviewOrigin() {
    const origin = new URL(location.origin);
    return origin.protocol === 'http:' && ['localhost','127.0.0.1','[::1]'].includes(origin.hostname) ? origin : null;
  }
  function safeYAMLURL(raw) {
    if (typeof raw !== 'string' || !raw) return '';
    try {
      const url = new URL(raw, location.origin);
      const preview = localPreviewOrigin();
      if (preview && url.protocol === 'https:' && url.hostname === preview.hostname && (url.port || '443') === (preview.port || '80')) {
        url.protocol = preview.protocol;
        url.port = preview.port;
      }
      if (!['http:','https:'].includes(url.protocol) || url.origin !== location.origin || url.username || url.password || !url.pathname.startsWith('/_miaomiaowu/rulesets/v1/') || !url.pathname.endsWith('.yaml')) return '';
      return url.origin + url.pathname + url.search;
    } catch { return ''; }
  }
  function clearResult(message = '选择规则集与来源后，点击“生成并预览”。') {
    result = null;
    yaml = '';
    yamlURL = '';
    el('mmRulesPreview').textContent = message;
    el('mmRulesPreviewHint').textContent = '复制与下载始终包含完整规则集。';
    el('mmRulesProvider').textContent = '生成成功后显示 rule-providers 引用片段。';
    el('mmRulesResultURL').textContent = '尚无可用 YAML 链接';
    el('mmRulesResultMeta').textContent = '';
    el('mmRulesCopyText').disabled = true;
    el('mmRulesCopyURL').disabled = true;
    el('mmRulesCopyProvider').disabled = true;
    el('mmRulesDownload').removeAttribute('href');
    el('mmRulesDownload').removeAttribute('download');
    el('mmRulesDownload').setAttribute('aria-disabled','true');
    el('mmRulesDownload').setAttribute('tabindex','-1');
  }
  function setRawLink(id, raw) {
    const link = el(id);
    link.removeAttribute('href');
    link.setAttribute('aria-disabled','true');
    try {
      const url = new URL(raw, location.origin);
      if (!raw || !['http:','https:'].includes(url.protocol) || url.username || url.password) return;
      link.href = url.href;
      link.removeAttribute('aria-disabled');
    } catch { /* Keep absent links disabled. */ }
  }
  function renderChoice() {
    const item = currentItem();
    el('mmRulesGenerate').disabled = pending || !usable();
    el('mmRulesGenerate').textContent = pending ? '正在生成…' : '生成并预览';
    el('mmRulesFilename').textContent = item?.filename || '请先选择规则集';
    el('mmRulesRevision').textContent = catalog?.rule_revision || catalog?.revision || '尚未记录';
    setRawLink('mmRulesRawLocal', item?.local_mrs_url);
    setRawLink('mmRulesRawUpstream', item?.upstream_mrs_url);
    renderRuleSourceControl('mmRulesSource', {
      value:source,
      options:['local','upstream'].map(id => ({id,available:usable(),reason:catalog?.reason || '规则目录尚未准备好'})),
      onchange:value => { source = value; invalidateSelection(); }
    });
  }
  function invalidateSelection() {
    beginRequest();
    pending = false;
    clearResult();
    renderChoice();
    status('已选择 ' + (currentItem()?.name || itemID) + ' · ' + labels[source] + '，点击“生成并预览”取得可导入的 YAML。');
  }
  function renderCatalog() {
    const items = Array.isArray(catalog?.items) ? catalog.items : [];
    if (catalog && !items.some(item => item.id === itemID)) itemID = items[0]?.id || '';
    el('mmRulesSelect').innerHTML = items.length ? items.map(item => `<option value="${escapeHTML(item.id)}">${escapeHTML(item.name)} · ${item.behavior === 'ipcidr' ? 'IP 规则' : '域名规则'}</option>`).join('') : '<option value="">暂无可用规则集</option>';
    el('mmRulesSelect').value = itemID;
    el('mmRulesSelect').disabled = catalog?.available !== true || !items.length;
    el('mmRulesTotal').textContent = items.length + ' 个规则集';
    el('mmRulesSyncHint').classList.toggle('hidden', catalog?.available === true);
    renderChoice();
  }
  async function loadCatalog() {
    const {ticket, signal} = beginRequest();
    catalog = null;
    pending = false;
    clearResult('正在读取规则集目录…');
    renderCatalog();
    status('正在读取规则集目录…');
    el('mmRulesRefresh').disabled = true;
    try {
      const response = await fetch('/api/templates/miaomiaowu/rulesets', {cache:'no-store', credentials:'same-origin', signal});
      const data = await response.json().catch(() => ({}));
      if (ticket !== generation) return;
      if (response.status === 401) throw new Error('登录已失效，请重新登录');
      if (!response.ok) throw new Error(data.error || '目录读取失败（HTTP ' + response.status + '）');
      if (!Array.isArray(data.items)) throw new Error('规则集目录不完整，请刷新重试');
      catalog = data;
      renderCatalog();
      clearResult();
      status(usable() ? '选择一个规则集后生成 YAML；首次转换可能需要约 20 秒。' : data.reason || '请先同步 666OS 规则资源。', !usable());
    } catch (error) {
      if (ticket !== generation) return;
      clearResult(error.message);
      status(error.message, true);
    } finally {
      if (ticket === generation) el('mmRulesRefresh').disabled = false;
    }
  }
  function limitedPreview(body) {
    let end = 0, shown = 0;
    while (end < body.length && end < 64 * 1024 && shown < 200) {
      const newline = body.indexOf('\n', end);
      const next = newline < 0 ? body.length : newline + 1;
      if (next > 64 * 1024) break;
      if (/^\s*-\s/.test(body.slice(end, next))) shown++;
      end = next;
    }
    return {text:body.slice(0, end), shown, truncated:end < body.length};
  }
  async function generate() {
    if (pending || !usable()) return;
    const {ticket, signal} = beginRequest();
    const requested = {id:itemID, source, revision:catalog.revision};
    pending = true;
    clearResult('正在将选中的规则集转换为 payload YAML…');
    renderChoice();
    status('正在生成 ' + currentItem().name + ' · ' + labels[source] + '，首次转换可能需要约 20 秒。');
    try {
      const response = await fetch('/api/templates/miaomiaowu/rulesets/' + encodeURIComponent(requested.id), {method:'POST', headers:{'Content-Type':'application/json'}, cache:'no-store', credentials:'same-origin', signal, body:JSON.stringify({revision:requested.revision, source:requested.source})});
      const data = await response.json().catch(() => ({}));
      if (ticket !== generation) return;
      if (response.status === 401) throw new Error('登录已失效，请重新登录');
      if (!response.ok) throw new Error(data.error || '生成失败（HTTP ' + response.status + '）');
      if (data.id !== requested.id || data.source !== requested.source || data.revision !== requested.revision || data.format !== 'yaml' || data.behavior !== 'classical') throw new Error('返回的规则集与当前选择不一致，请刷新目录重试');
      const url = safeYAMLURL(data.yaml_url);
      if (!url) throw new Error('生成的 YAML 地址无效，请刷新目录重试');
      const parsed = new URL(url);
      const fileResponse = await fetch(parsed.pathname + parsed.search, {cache:'no-store', credentials:'same-origin', redirect:'error', signal});
      if (!fileResponse.ok) throw new Error('YAML 读取失败（HTTP ' + fileResponse.status + '）');
      if (/text\/html/i.test(fileResponse.headers?.get('Content-Type') || '')) throw new Error('服务返回了网页，请重新登录后重试');
      const body = await fileResponse.text();
      if (ticket !== generation) return;
      if (!/^payload\s*:/m.test(body)) throw new Error('返回的文件不包含 payload，无法作为规则集导入');
      result = data;
      yaml = body;
      yamlURL = url;
      const preview = limitedPreview(body);
      el('mmRulesPreview').textContent = preview.text || '单条规则较长，请复制或下载完整内容。';
      el('mmRulesPreviewHint').textContent = preview.truncated
        ? '仅预览前 ' + preview.shown.toLocaleString('zh-CN') + ' 条，复制和下载包含完整 ' + Number(data.count).toLocaleString('zh-CN') + ' 条。'
        : '已显示全部 ' + Number(data.count).toLocaleString('zh-CN') + ' 条；复制和下载包含完整规则集。';
      el('mmRulesResultURL').textContent = url;
      el('mmRulesProvider').textContent = data.provider_yaml || '暂未提供引用片段。';
      el('mmRulesResultMeta').textContent = [labels[data.source], Number(data.count).toLocaleString('zh-CN') + ' 条规则', '版本 ' + data.revision, data.sha256 && 'SHA-256 ' + data.sha256].filter(Boolean).join(' · ');
      el('mmRulesCopyText').disabled = false;
      el('mmRulesCopyURL').disabled = false;
      el('mmRulesCopyProvider').disabled = !data.provider_yaml;
      parsed.searchParams.set('download','1');
      el('mmRulesDownload').href = parsed.href;
      el('mmRulesDownload').download = data.filename || currentItem().filename;
      el('mmRulesDownload').removeAttribute('aria-disabled');
      el('mmRulesDownload').removeAttribute('tabindex');
      status('YAML 已生成，可粘贴到妙妙屋X“新建规则集”的内容区，或使用“从文件导入”。');
    } catch (error) {
      if (ticket !== generation) return;
      clearResult(error.message);
      status(error.message, true);
    } finally {
      if (ticket === generation) { pending = false; renderChoice(); }
    }
  }
  async function copy(kind) {
    if (!result || !yaml || !yamlURL) return;
    const value = kind === 'provider' ? result.provider_yaml : kind === 'url' ? yamlURL : yaml;
    if (!value) return;
    const ticket = generation;
    try {
      await navigator.clipboard.writeText(value);
      if (ticket === generation) status(kind === 'provider' ? '引用片段已复制。若使用妙妙屋X 托管文件，请将 url 改为它生成的公开地址。' : kind === 'url' ? 'YAML 链接已复制。' : '规则集正文已复制，请粘贴到妙妙屋X 的规则集内容区。');
    } catch {
      if (ticket === generation) status('复制失败，请下载 YAML 文件，或在预览中手动选择并复制。', true);
    }
  }
  function mount() {
    if (el('mmRulesRefresh')) return;
    el('mmRulesetsSection').innerHTML = `<section class="panel miaomiaowu-rules-panel"><div class="panel-head"><div><h3>单独导入 YYDS 规则集</h3><p class="panel-copy">用于妙妙屋X 的“规则集管理”。选择一项，生成可直接导入的 payload YAML。</p></div><span id="mmRulesTotal" class="soft-badge">读取目录中</span></div>
      <div class="miaomiaowu-rules-select"><div><label class="field-label" for="mmRulesSelect">规则集</label><select id="mmRulesSelect" disabled><option value="">正在读取目录…</option></select></div><button id="mmRulesRefresh" type="button" class="secondary">刷新目录</button></div>
      <div id="mmRulesSource" data-value="local"></div><p class="field-hint">这里的来源仅用于当前规则集，独立于上方模板的来源选择。每次只转换所选的一项。</p>
      <p id="mmRulesPreviewScope" class="field-hint hidden">本机开发预览的页面下载链接使用 HTTP；正式部署仍使用 HTTPS。</p>
      <p id="mmRulesStatus" class="routing-feedback" role="status" aria-live="polite"></p><div id="mmRulesSyncHint" class="entry-notice hidden"><span>规则目录尚未就绪，请先完成 666OS 规则资源同步。</span><button id="mmRulesGoSource" type="button" class="secondary">打开 666OS 规则资源</button></div>
      <div class="panel-actions miaomiaowu-actions"><button id="mmRulesGenerate" type="button" disabled>生成并预览</button><a id="mmRulesDownload" class="button secondary" aria-disabled="true" tabindex="-1">下载 YAML</a><button id="mmRulesCopyText" type="button" class="secondary" disabled>复制规则集正文</button><button id="mmRulesCopyURL" type="button" class="secondary" disabled>复制 YAML 链接</button></div>
      <p class="field-hint">导入文件名：<code id="mmRulesFilename">请先选择规则集</code></p><p id="mmRulesResultMeta" class="field-hint miaomiaowu-rules-result-meta"></p><code id="mmRulesResultURL" class="miaomiaowu-url">尚无可用 YAML 链接</code>
      <details class="template-preview" open><summary>可导入的规则集正文</summary><div class="routing-yaml-tools"><span id="mmRulesPreviewHint">复制与下载始终包含完整规则集。</span></div><pre id="mmRulesPreview">选择规则集与来源后，点击“生成并预览”。</pre></details>
      <div class="entry-notice"><strong>导入方式</strong><span>妙妙屋X → 规则集管理 → 新建规则集 → 来源选择“手动维护” → 填写上方文件名 → 粘贴正文或“从文件导入” → 保存。</span></div>
      <p class="field-hint">规则集与上方模板均使用固定版本。导入的是当前规则的一份快照，新规则发布后需要重新生成并导入；它不会自动同步，也不会修改已有模板。</p>
      <details class="template-preview"><summary>在模板中引用这个规则集</summary><div class="routing-yaml-tools"><span>此片段使用本站 YAML 地址。若文件已导入妙妙屋X，请将 url 换成妙妙屋X 提供的公开地址，并在模板的 rules 中添加相应 RULE-SET 规则。替换原 MRS 时，还须同时采用 format: yaml、behavior: classical，并将缓存 path 改为 .yaml；不能只改 url。</span><button id="mmRulesCopyProvider" type="button" class="secondary" disabled>复制引用片段</button></div><pre id="mmRulesProvider">生成成功后显示 rule-providers 引用片段。</pre></details>
      <details class="miaomiaowu-rules-origin"><summary>原始规则来源</summary><p class="field-hint">规则版本：<code id="mmRulesRevision">尚未记录</code></p><div class="resource-links"><a id="mmRulesRawLocal" target="_blank" rel="noopener noreferrer">本机原始 MRS</a><a id="mmRulesRawUpstream" target="_blank" rel="noopener noreferrer">上游原始 MRS</a></div><p class="field-hint">以上是二进制原文件，仅供核对来源。导入妙妙屋X 请使用本页生成的 YAML 正文或下载文件。</p></details>
    </section>`;
    el('mmRulesRefresh').onclick = loadCatalog;
    el('mmRulesGenerate').onclick = generate;
    el('mmRulesSelect').onchange = () => { itemID = el('mmRulesSelect').value; invalidateSelection(); };
    el('mmRulesCopyText').onclick = () => copy('body');
    el('mmRulesCopyURL').onclick = () => copy('url');
    el('mmRulesCopyProvider').onclick = () => copy('provider');
    el('mmRulesGoSource').onclick = () => activateTab('rules');
    el('mmRulesPreviewScope').classList.toggle('hidden', !localPreviewOrigin());
  }
  async function activate() { mount(); await loadCatalog(); }
  window.CoralBayMiaomiaowuRules = {activate};
})();
