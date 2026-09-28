(() => {
  'use strict';
  const el = id => document.getElementById(id);
  const labels = {local:'本机镜像', upstream:'上游源'};
  const clients = {
    clash:{label:'Clash / Mihomo', extension:'.yaml', downloadLabel:'下载 YAML'},
    surge:{label:'Surge', extension:'.conf', downloadLabel:'下载 .conf'},
    loon:{label:'Loon', extension:'.lcf', downloadLabel:'下载 .lcf'}
  };
  const clientSources = {clash:'local', surge:'local', loon:'local'};
  let catalog = null, client = 'clash', selected = 'local', generation = 0, controller = null, preparing = false;
  let template = '', templateURL = '', ready = false, rulesModulePromise = null;

  async function loadRulesModule() {
    try {
      if (!rulesModulePromise) rulesModulePromise = window.CoralBayMiaomiaowuRules ? Promise.resolve(window.CoralBayMiaomiaowuRules) : new Promise((resolve, reject) => {
        const script = document.createElement('script');
        script.src = '/assets/miaomiaowu-rules.js?v=' + encodeURIComponent(typeof coralbayAssetVersion === 'undefined' ? '' : coralbayAssetVersion);
        script.onload = () => resolve(window.CoralBayMiaomiaowuRules);
        script.onerror = () => { script.remove(); rulesModulePromise = null; reject(new Error('规则集页面模块加载失败，请刷新状态重试')); };
        document.body.appendChild(script);
      });
      const module = await rulesModulePromise;
      if (!module?.activate) throw new Error('规则集页面模块未就绪，请刷新状态重试');
      await module.activate();
    } catch (error) {
      rulesModulePromise = null;
      el('mmRulesetsSection').innerHTML = '<p class="routing-feedback bad">' + escapeHTML(error.message) + '</p>';
    }
  }

  function localPreviewOrigin() {
    const origin = new URL(location.origin);
    return origin.protocol === 'http:' && ['localhost','127.0.0.1','[::1]'].includes(origin.hostname) ? origin : null;
  }

  function safeTemplateURL(raw) {
    if (typeof raw !== 'string' || !raw) return '';
    try {
      const url = new URL(raw, location.origin);
      const preview = localPreviewOrigin();
      if (preview && url.protocol === 'https:' && url.hostname === preview.hostname && (url.port || '443') === (preview.port || '80')) {
        url.protocol = preview.protocol;
        url.port = preview.port;
      }
      if (!['http:', 'https:'].includes(url.protocol) || url.origin !== location.origin || url.username || url.password || !url.pathname.startsWith('/_miaomiaowu/')) return '';
      return url.origin + url.pathname + url.search;
    } catch { return ''; }
  }

  function status(message, bad = false) {
    el('mmStatus').textContent = message;
    el('mmStatus').classList.toggle('bad', bad);
  }

  function clearArtifact(message = '请选择规则来源。') {
    template = '';
    templateURL = '';
    ready = false;
    el('mmPreview').textContent = message;
    el('mmTemplateURL').textContent = '尚无可用下载链接';
    el('mmCopyText').disabled = true;
    el('mmCopyURL').disabled = true;
    el('mmPreviewRefresh').disabled = true;
    el('mmDownload').removeAttribute('href');
    el('mmDownload').removeAttribute('download');
    el('mmDownload').setAttribute('aria-disabled', 'true');
    el('mmDownload').setAttribute('tabindex', '-1');
  }

  function beginRequest() {
    controller?.abort();
    controller = new AbortController();
    return {ticket:++generation, signal:controller.signal};
  }

  function chosen() {
    return catalog?.source_options?.find(item => item.id === selected);
  }

  function filename(option = chosen()) {
    const advertised = option?.filename;
    if (typeof advertised === 'string' && advertised.endsWith(clients[client].extension) && !/[/\\\0]/.test(advertised)) return advertised;
    return client === 'clash' ? 'CoralBay_MiaoMiaoWuX_YYDS_' + selected + '.yaml' : 'coralbay_yyds_' + selected + '__' + client + clients[client].extension;
  }

  function renderClient() {
    const info = clients[client], option = chosen();
    el('mmClient').value = client;
    el('mmFilename').textContent = filename(option);
    el('mmDownload').textContent = info.downloadLabel;
    el('mmImportType').textContent = client === 'clash' ? 'Clash V3 模板 · .yaml' : info.label + ' 模板 · ' + info.extension;
    el('mmGetTemplateStep').textContent = '下载 ' + info.extension + ' 文件，或复制上方模板正文。';
    el('mmImportStep').textContent = '在妙妙屋X 模板管理中选择 ' + (client === 'clash' ? 'Clash（V3）' : info.label) + ' 类型，再导入对应文件或粘贴正文并保存。';
    el('mmNodeHint').textContent = client === 'clash'
      ? '本页文件不包含真实节点。请先导入妙妙屋X，再使用它生成的订阅链接。'
      : '妙妙屋X 生成订阅时填入节点。节点协议还须同时受妙妙屋X 和 ' + info.label + ' 支持；模板生成成功不代表订阅中的每个节点都能使用。';
    el('mmClientBoundary').textContent = client === 'surge'
      ? '当前核对的妙妙屋 Surge 转换器会过滤 VLESS 节点；请确认订阅中有可转换的节点。'
      : client === 'loon' ? 'Loon 的域名与 IP 规则匹配顺序与 Clash 不同，分流行为不保证完全一致；请使用支持所选节点协议的 Loon 版本。' : '';
    el('mmClientBoundary').classList.toggle('hidden', client === 'clash');
    el('mmSourceDelivery').textContent = client === 'clash'
      ? '模板中的 MRS 规则由客户端从所选来源下载。'
      : '这里选择生成时读取的 MRS 原件来源。转换后的文本规则和模板都由 CoralBay 固定托管；选择上游源不表示客户端直接下载上游文本规则。本模板已引用对应文本规则，无需另导入下方的 Clash YAML 规则集。';
    el('mmPrepare').classList.toggle('hidden', client === 'clash' || option?.prepared === true);
    el('mmPrepare').disabled = preparing || !option?.available;
    el('mmPrepare').textContent = preparing ? '正在生成 ' + info.label + ' 模板…' : '生成 ' + info.label + ' 模板';
  }

  function setExternalLink(id, raw) {
    const link = el(id);
    link.removeAttribute('href');
    link.setAttribute('aria-disabled', 'true');
    try {
      const url = new URL(raw);
      if (!['https:', 'http:'].includes(url.protocol) || url.username || url.password) return;
      link.href = url.href;
      link.removeAttribute('aria-disabled');
    } catch { /* A missing source link must not reuse an earlier URL. */ }
  }

  function renderSource() {
    const option = chosen();
    renderClient();
    renderRuleSourceControl('mmRuleSource', {
      value:selected,
      options:catalog?.source_options || [],
      onchange:source => { selected = source; clientSources[client] = source; loadTemplate(); }
    });
    el('mmSourceName').textContent = catalog?.source_name || '666OS / YYDS Pro_cn';
    el('mmSourceMode').textContent = labels[selected];
    el('mmSourceDescription').textContent = option?.description || (selected === 'local' ? '模板中的规则文件从本机镜像下载。' : '模板中的规则文件从上游下载。');
    for (const [id, key] of [['mmProviderCount','provider_count'], ['mmGroupCount','group_count'], ['mmRuleCount','rule_count']]) {
      const value = option?.[key];
      el(id).textContent = typeof value === 'number' && Number.isFinite(value) ? value.toLocaleString('zh-CN') : '—';
    }
    el('mmRevision').textContent = option?.revision || '尚未生成';
    el('mmRuleRevision').textContent = option?.rule_revision || '尚未记录';
    el('mmSHA256').textContent = option?.sha256 || '尚未生成';
    setExternalLink('mmSourceLink', catalog?.source_url);
    setExternalLink('mmDocsLink', catalog?.docs_url || 'https://miaomiaowux.com/docs/templates/');
    const unavailable = !option?.available;
    el('mmSyncHint').classList.toggle('hidden', !unavailable);
    el('mmLocalError').textContent = catalog?.local_error ? '模板状态：' + catalog.local_error : '';
    el('mmLocalError').classList.toggle('hidden', !catalog?.local_error);
    if (typeof loadTemplateGrouping === 'function') loadTemplateGrouping('miaomiaowu',{host:el('mmGrouping'),client,source:selected,available:option?.available,baseline:()=>template});
  }

  async function loadTemplate() {
    const {ticket, signal} = beginRequest();
    preparing = false;
    const option = chosen();
    clearArtifact('正在读取所选模板…');
    renderSource();
    if (!option?.available) {
      const reason = option?.reason || '该来源尚未准备好，请先在 666OS 规则资源中完成同步。';
      clearArtifact(reason);
      status(reason, true);
      return;
    }
    if (client !== 'clash' && option.prepared !== true) {
      const message = '已选择 ' + clients[client].label + ' · ' + labels[selected] + '。点击“生成模板”准备固定版本；首次生成需要转换规则，请稍候。';
      clearArtifact('生成模板后显示完整正文。');
      status(message);
      return;
    }
    const url = safeTemplateURL(option.template_url);
    if (!url || !new URL(url).pathname.endsWith(clients[client].extension)) {
      clearArtifact('模板地址无效，请刷新状态后重试。');
      status('模板地址无效，暂时无法预览或下载。', true);
      return;
    }
    status('正在读取' + labels[selected] + '模板…');
    try {
      const response = await fetch(new URL(url).pathname + new URL(url).search, {cache:'no-store', credentials:'same-origin', redirect:'error', signal});
      if (!response.ok) throw new Error('模板读取失败（HTTP ' + response.status + '）');
      if (/text\/html/i.test(response.headers?.get('Content-Type') || '')) throw new Error('服务返回了网页，请重新登录后重试');
      const body = await response.text();
      if (!body.trim()) throw new Error('模板内容为空，请重新同步规则资源');
      if (ticket !== generation) return;
      template = body;
      templateURL = url;
      ready = true;
      el('mmPreview').textContent = body;
      el('mmTemplateURL').textContent = url;
      el('mmCopyText').disabled = false;
      el('mmCopyURL').disabled = false;
      el('mmPreviewRefresh').disabled = false;
      el('mmDownload').href = url;
      el('mmDownload').download = filename(option);
      el('mmDownload').removeAttribute('aria-disabled');
      el('mmDownload').removeAttribute('tabindex');
      status(clients[client].label + ' · ' + labels[selected] + '模板已就绪，可下载 ' + clients[client].extension + ' 或复制正文到妙妙屋X。');
    } catch (error) {
      if (ticket !== generation) return;
      clearArtifact(error.message);
      status(error.message + '；可点击“刷新状态”重试。', true);
    }
  }

  async function loadCatalog() {
    const {ticket, signal} = beginRequest();
    const requestedClient = client;
    preparing = false;
    catalog = null;
    clearArtifact('正在读取模板状态…');
    renderSource();
    status('正在读取模板状态…');
    el('mmRefresh').disabled = true;
    try {
      const response = await fetch('/api/templates/miaomiaowu' + (requestedClient === 'clash' ? '' : '?client=' + requestedClient), {cache:'no-store', credentials:'same-origin', signal});
      const data = await response.json().catch(() => ({}));
      if (ticket !== generation) return;
      if (response.status === 401) throw new Error('登录已失效，请重新登录');
      if (!response.ok) throw new Error(data.error || '状态读取失败（HTTP ' + response.status + '）');
      if (data.format !== 'miaomiaowu-v3' || !Array.isArray(data.source_options)) throw new Error('模板状态不完整，请刷新重试');
      if ((data.client && data.client !== requestedClient) || (requestedClient !== 'clash' && (data.client !== requestedClient || data.extension !== clients[requestedClient].extension))) throw new Error('返回的模板类型与当前选择不一致，请刷新重试');
      catalog = data;
      renderSource();
      el('mmRefresh').disabled = false;
      await loadTemplate();
    } catch (error) {
      if (ticket !== generation) return;
      clearArtifact(error.message);
      status(error.message, true);
      el('mmRefresh').disabled = false;
    }
  }

  async function prepareTemplate() {
    const option = chosen();
    if (preparing || client === 'clash' || !option?.available || option.prepared) return;
    const {ticket, signal} = beginRequest();
    const requested = {client, source:selected, revision:option.revision};
    preparing = true;
    clearArtifact('正在生成所选格式的模板与固定版本规则…');
    renderSource();
    status('正在生成 ' + clients[client].label + ' · ' + labels[selected] + ' 模板。首次需要转换规则，请稍候。');
    try {
      const response = await fetch('/api/templates/miaomiaowu/prepare', {method:'POST', headers:{'Content-Type':'application/json'}, cache:'no-store', credentials:'same-origin', signal, body:JSON.stringify(requested)});
      const data = await response.json().catch(() => ({}));
      if (ticket !== generation) return;
      if (response.status === 401) throw new Error('登录已失效，请重新登录');
      if (!response.ok) throw new Error(data.error || '模板生成失败（HTTP ' + response.status + '）');
      const preparedURL = safeTemplateURL(data.template_url);
      if (data.id !== requested.source || data.revision !== requested.revision || data.prepared !== true || data.available !== true || !preparedURL || !new URL(preparedURL).pathname.endsWith(clients[requested.client].extension)) throw new Error('生成结果与所选来源不一致，请刷新后重试');
      catalog.source_options = catalog.source_options.map(item => item.id === requested.source ? data : item);
      preparing = false;
      await loadTemplate();
    } catch (error) {
      if (ticket !== generation) return;
      clearArtifact(error.message);
      status(error.message, true);
    } finally {
      if (ticket === generation) { preparing = false; renderSource(); }
    }
  }

  async function copyContent(body) {
    if (!ready || !templateURL || !template) return;
    const ticket = generation;
    try {
      await navigator.clipboard.writeText(body ? template : templateURL);
      if (ticket === generation) status(body ? '模板正文已复制，请粘贴到妙妙屋X 的模板编辑器。' : '模板下载链接已复制。');
    } catch {
      if (ticket === generation) status('复制失败，请下载模板文件，或在预览中手动选择并复制。', true);
    }
  }

  function mount() {
    if (el('mmRefresh')) return;
    el('miaomiaowuPage').innerHTML = `
      <div class="page-heading"><div><span class="eyebrow">MIAOMIAOWUX · YYDS</span><h2>妙妙屋X 模板</h2><p>将 666OS / YYDS 分流规则作为妙妙屋X 的独立模板使用。</p></div><button id="mmRefresh" class="secondary" type="button">刷新状态</button></div>
      <div class="entry-notice accent"><strong>使用流程</strong><span>取得 YYDS 模板 → 导入妙妙屋X → 绑定自己的订阅 → 在妙妙屋X 生成客户端订阅</span></div>
      <p id="mmPreviewScope" class="field-hint hidden">当前为本机开发预览，页面下载链接使用 HTTP；正式部署仍使用 HTTPS。</p>
      <p class="field-hint miaomiaowu-adaptation">Clash / Mihomo 可在分组设置中生成含地区自动、均衡与手动的新模板。Surge、Loon 保留现有基础模板。</p>
      <section class="panel miaomiaowu-template"><div class="panel-head"><div><h3>选择模板客户端与来源</h3><p class="panel-copy">本页选择只用于这份妙妙屋X 模板。已有订阅、PPanel 模板和 MihomoPro 覆写保持各自设置。</p></div><span class="soft-badge">YYDS Pro_cn</span></div>
        <div class="miaomiaowu-client-control"><div><label class="field-label" for="mmClient">目标客户端</label><select id="mmClient"><option value="clash">Clash / Mihomo</option><option value="surge">Surge</option><option value="loon">Loon</option></select></div><div><span class="field-label">妙妙屋X 导入类型与文件后缀</span><strong id="mmImportType">Clash V3 模板 · .yaml</strong><p class="field-hint">保存文件名：<code id="mmFilename">CoralBay_MiaoMiaoWuX_YYDS_local.yaml</code></p></div></div>
        <div id="mmRuleSource" data-value="local"></div><p id="mmSourceDescription" class="field-hint"></p>
        <div id="mmGrouping"></div>
        <h4 class="grouping-legacy-title">基础模板 · 不含上方自定义设置</h4>
        <p id="mmSourceDelivery" class="field-hint"></p>
        <p id="mmClientBoundary" class="field-hint hidden"></p>
        <p id="mmStatus" class="routing-feedback" role="status" aria-live="polite"></p>
        <p id="mmLocalError" class="field-hint hidden"></p>
        <div id="mmSyncHint" class="entry-notice hidden"><span>本机资源尚未准备好。前往规则源同步，完成后回到本页刷新状态。</span><button id="mmGoRules" type="button" class="secondary">打开 666OS 规则资源</button></div>
        <div class="routing-preview-summary" aria-label="所选模板内容"><span><b id="mmProviderCount">—</b>规则集</span><span><b id="mmGroupCount">—</b>策略组</span><span><b id="mmRuleCount">—</b>分流规则</span></div>
        <div class="panel-actions miaomiaowu-actions"><button id="mmPrepare" type="button" class="hidden" disabled>生成模板</button><a id="mmDownload" class="button" aria-disabled="true" tabindex="-1">下载 YAML</a><button id="mmCopyText" type="button" class="secondary" disabled>复制模板正文</button><button id="mmCopyURL" type="button" class="secondary" disabled>复制下载链接</button></div>
        <code id="mmTemplateURL" class="miaomiaowu-url">尚无可用下载链接</code>
        <p class="field-hint">模板使用固定版本。新规则发布后，请重新获取并导入妙妙屋X；已导入的模板不会自动替换。</p>
        <details class="template-preview"><summary>基础模板内容预览</summary><div class="routing-yaml-tools"><span>这里的操作只用于基础模板；自定义分组请使用上方生成的新文件。</span><button id="mmPreviewRefresh" type="button" class="secondary" disabled>重新读取正文</button></div><pre id="mmPreview">正在读取模板状态…</pre></details>
      </section>
      <section id="mmRulesetsSection"></section>
      <div class="miaomiaowu-details"><section class="panel"><div class="panel-head"><h3>在妙妙屋X 中使用</h3><a id="mmDocsLink" href="https://miaomiaowux.com/docs/templates/" target="_blank" rel="noopener noreferrer">查看官方说明 ↗</a></div><ol class="miaomiaowu-steps"><li><strong>取得模板</strong><span id="mmGetTemplateStep">下载 YAML 文件，或复制上方模板正文。</span></li><li><strong>导入模板</strong><span id="mmImportStep">在妙妙屋X 的模板管理中新建或导入模板，上传 YAML 文件或粘贴正文并保存。</span></li><li><strong>绑定订阅并生成</strong><span>将模板绑定到自己的订阅，由妙妙屋X 填入节点并生成客户端订阅。</span></li></ol><p id="mmNodeHint" class="field-hint">本页文件不包含真实节点。请先导入妙妙屋X，再使用它生成的订阅链接。</p></section>
      <section class="panel"><div class="panel-head"><h3>所选来源详情</h3><a id="mmSourceLink" target="_blank" rel="noopener noreferrer">查看 YYDS 原配置 ↗</a></div><dl class="resource-meta miaomiaowu-meta"><dt>规则方案</dt><dd id="mmSourceName">666OS / YYDS Pro_cn</dd><dt>规则文件来源</dt><dd id="mmSourceMode">本机镜像</dd><dt>模板版本</dt><dd id="mmRevision">尚未生成</dd><dt>规则版本</dt><dd id="mmRuleRevision">尚未记录</dd></dl><details class="miaomiaowu-checksum"><summary>文件校验信息</summary><p class="field-hint">SHA-256</p><code id="mmSHA256">尚未生成</code></details></section></div>`;
    el('mmRefresh').onclick = () => Promise.all([loadCatalog(), loadRulesModule()]);
    el('mmPreviewRefresh').onclick = loadTemplate;
    el('mmClient').onchange = () => {
      if (!Object.hasOwn(clients, el('mmClient').value)) return;
      client = el('mmClient').value;
      selected = clientSources[client];
      return loadCatalog();
    };
    el('mmPrepare').onclick = prepareTemplate;
    el('mmCopyText').onclick = () => copyContent(true);
    el('mmCopyURL').onclick = () => copyContent(false);
    el('mmGoRules').onclick = () => activateTab('rules');
    el('mmPreviewScope').classList.toggle('hidden', !localPreviewOrigin());
  }

  async function activate() {
    mount();
    await Promise.all([loadCatalog(), loadRulesModule()]);
  }

  window.CoralBayMiaomiaowu = {activate};
})();
