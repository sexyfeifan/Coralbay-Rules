(() => {
  'use strict';
  const scopes = {miaomiaowu:'妙妙屋X', ppanel:'PPanel', overwrite:'MihomoPro 覆写'};
  const instances = new Map();
  const esc = value => String(value ?? '').replace(/[&<>"']/g, char => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[char]));
  const clone = value => JSON.parse(JSON.stringify(value));
  const tabs = [['categories','分类与默认'],['regions','地区'],['automatic','自动与均衡'],['advanced','规则与高级'],['preview','预览']];
  const fallbackRegions = [{code:'hk',name:'香港'},{code:'tw',name:'台湾'},{code:'jp',name:'日本'},{code:'us',name:'美国'},{code:'sg',name:'新加坡'},{code:'kr',name:'韩国'}];
  function supported(scope, client) { return scope === 'miaomiaowu' ? client === 'clash' : scope === 'ppanel' ? ['clash','mihomo','openclash'].includes(client) : scope === 'overwrite' && client === 'mihomo'; }
  function safeArtifactURL(raw) {
    try {
      if (typeof raw !== 'string' || !raw) return '';
      const url = new URL(raw, location.origin), origin = new URL(location.origin);
      if (origin.protocol === 'http:' && ['localhost','127.0.0.1','[::1]'].includes(origin.hostname) && url.protocol === 'https:' && url.hostname === origin.hostname && (url.port || '443') === (origin.port || '80')) { url.protocol = origin.protocol; url.port = origin.port; }
      if (!['http:','https:'].includes(url.protocol) || url.origin !== origin.origin || url.username || url.password || !/^\/_grouped-templates\/[a-f0-9]{64}\/[^/]+$/.test(url.pathname)) return '';
      return url.href;
    } catch { return ''; }
  }
  async function request(path, options = {}, retried = false) {
    const response = await fetch('/api/template-grouping/' + path, {cache:'no-store', credentials:'same-origin', ...options});
    if (response.status === 429 && options.method && !retried) { await new Promise(resolve => setTimeout(resolve,1100)); return request(path,options,true); }
    const data = await response.json().catch(() => ({}));
    if (response.status === 401) throw new Error('登录已失效，请重新登录。');
    if (!response.ok) throw new Error(data.error || `请求失败（HTTP ${response.status}）`);
    return data;
  }
  const body = value => ({headers:{'Content-Type':'application/json'}, body:JSON.stringify(value)});
  const choices = (values, selected) => values.map(item => { const value = typeof item === 'string' ? item : item.value, label = typeof item === 'string' ? item : item.label; return `<option value="${esc(value)}"${selected === value ? ' selected' : ''}>${esc(label)}</option>`; }).join('');
  const check = (field, text, checked) => `<label class="grouping-check"><input type="checkbox" data-grouping-field="${field}"${checked ? ' checked' : ''}><span>${text}</span></label>`;
  const field = (label, control, hint = '') => `<label class="grouping-field"><span>${label}</span>${control}${hint ? `<small>${hint}</small>` : ''}</label>`;
  const input = (key, value, attrs = '') => `<input data-grouping-field="${key}" value="${esc(value)}" ${attrs}>`;
  function contentDifference(before, after) {
    const names = content => {
      const block = String(content || '').match(/^proxy-groups:\s*\n([\s\S]*?)(?=^[a-zA-Z][\w-]*:|$(?![\s\S]))/m)?.[1] || '';
      return [...block.matchAll(/\bname:\s*["']?([^\n,"'}]+)/g)].map(match => match[1].trim());
    };
    const oldNames = names(before), newNames = names(after);
    return {beforeLines:String(before || '').split('\n').length,afterLines:String(after || '').split('\n').length,added:newNames.filter(name => !oldNames.includes(name)),removed:oldNames.filter(name => !newNames.includes(name))};
  }

  function create(scope, host) {
    const state = {scope,host,context:{},loaded:false,loading:false,busy:false,dirty:false,inherit:true,profile:null,catalog:{},defaults:null,activeTab:'categories',ticket:0,artifacts:[],artifactIndex:0,preview:null,names:'',feedback:''};
    const find = part => host.querySelector(`[data-grouping="${part}"]`);
    function notice(text, bad = false) { state.feedback = text; const box = find('feedback'); if (box) { box.textContent = text; box.classList.toggle('bad', bad); } }
    function invalidate() { state.ticket++; state.artifacts = []; state.generated = null; state.preview = null; state.dirty = true; renderArtifacts(); renderStatus(); notice('设置有修改，请保存后重新生成。'); }
    function defaultsOptions() {
      const regions = state.catalog.regions || fallbackRegions, macros = state.catalog.macros || [];
      const names = [...(state.profile.regions || []).map(code => regions.find(item => item.code === code)?.name).filter(Boolean),...macros.map(item => item.name)];
      return ['全球自动','全球手动','故障转移','DIRECT','REJECT','REJECT-DROP',...names.flatMap(name => ['自动','均衡','手动'].map(suffix => name+suffix))];
    }
    function policyChoices(selected, category = false) {
      const options = [...(category ? [{value:'auto',label:'沿用分类推荐'},'默认出口'] : []),...defaultsOptions()];
      if (selected && !options.some(item => (typeof item === 'string' ? item : item.value) === selected)) options.push({value:selected,label:selected+'（地区已移除，请重选）'});
      return choices(options,selected);
    }
    function saveFields() {
      if (!state.profile) return;
      host.querySelectorAll('[data-grouping-field]').forEach(element => {
        const key = element.dataset.groupingField;
        state.profile[key] = element.type === 'checkbox' ? element.checked : element.type === 'number' ? Number(element.value) : element.value;
      });
      const regions = Array.from(host.querySelectorAll('[data-grouping-region]:checked')).map(element => element.dataset.groupingRegion);
      state.profile.regions = [...(state.profile.regions || []).filter(code => regions.includes(code)),...regions.filter(code => !state.profile.regions?.includes(code))];
      host.querySelectorAll('[data-grouping-category]').forEach(element => {
        const item = state.profile.categories[Number(element.dataset.groupingCategory)];
        if (item) item[element.dataset.property] = element.type === 'checkbox' ? element.checked : element.value;
      });
      state.profile.media = Array.from(host.querySelectorAll('[data-grouping-media]:checked')).map(element => element.dataset.groupingMedia);
      state.names = find('names')?.value ?? state.names;
      const overrides = find('overrides')?.value;
      if (overrides != null) state.profile.overrides = overrides.split('\n').map(line => line.trim()).filter(Boolean).map(line => {
        const separator = line.lastIndexOf(' => ');
        if (separator < 1) throw new Error('归属修正请每行填写：节点名称正则 => 地区代码，例如 德国|Germany => europe。');
        return {pattern:line.slice(0, separator).trim(),region:line.slice(separator + 4).trim()};
      });
    }
    function contextReason() {
      if (!supported(scope, state.context.client)) return '当前客户端暂不支持这套分组设置；基础模板仍可按原流程下载。';
      if (state.context.original) return '当前查看 Perfect Panel 原始版。切换到 CoralBay 改造版后，可生成采用本方案的模板。';
      if (!['local','upstream'].includes(state.context.source)) return '请在上方明确选择“本机镜像”或“上游源”，再生成采用本方案的文件。';
      if (state.context.available === false) return '所选规则来源尚未就绪，请先同步资源或更换来源。';
      return '';
    }
    function renderStatus() {
      const reason = contextReason(), tag = find('state');
      if (tag) tag.textContent = state.dirty ? '有未保存的修改' : state.inherit ? '使用共用方案' : '当前页独立方案';
      if (find('boundary')) { find('boundary').textContent = reason || '此分组方案面向现代 Mihomo 内核。编辑并保存后生成新文件；下方基础模板不会自动变化。'; find('boundary').classList.toggle('bad', !!reason); }
      if (find('generate')) find('generate').disabled = state.busy || !state.loaded || !!reason || state.dirty;
      host.querySelectorAll('[data-grouping-write]').forEach(button => button.disabled = state.busy || !state.loaded);
      host.querySelectorAll('input,select,textarea').forEach(element => element.disabled = state.busy);
      host.querySelectorAll('[data-category-move], [data-region-move]').forEach(button => { const index = Number(button.dataset.categoryMove ?? button.dataset.regionMove), count = button.dataset.categoryMove != null ? state.profile.categories.length : state.profile.regions.length; button.disabled = state.busy || index + Number(button.dataset.direction) < 0 || index + Number(button.dataset.direction) >= count; });
    }
    function renderCategories() {
      const p = state.profile;
      return `<div class="grouping-grid"><div>${field('方案名称', input('name', p.name || '', 'maxlength="80"'))}${field('全局默认出口', `<select data-grouping-field="default">${policyChoices(p.default)}</select>`, '分类可分别覆盖默认出口；客户端已保存的手选结果可能继续保留。')}</div><div class="grouping-note"><strong>每个分类都能自由选节点</strong><p>默认出口、全球自动、全球手动，以及各地区的自动、均衡、手动共同组成候选。</p>${check('show_nodes','分类中直接展开全部节点',p.show_nodes)}<small>关闭后仍可进入“全球手动”或“地区手动”选择单个节点。</small></div></div><div class="grouping-category-list">${(p.categories || []).map((category,index) => `<div class="grouping-category"><label><input type="checkbox" data-grouping-category="${index}" data-property="enabled"${category.enabled ? ' checked' : ''}><span>${esc(category.name)}</span></label><select aria-label="${esc(category.name)}默认出口" data-grouping-category="${index}" data-property="default">${policyChoices(category.default || 'auto',true)}</select><div class="grouping-order"><button type="button" class="ghost" data-category-move="${index}" data-direction="-1" aria-label="上移${esc(category.name)}"${index === 0 ? ' disabled' : ''}>↑</button><button type="button" class="ghost" data-category-move="${index}" data-direction="1" aria-label="下移${esc(category.name)}"${index === p.categories.length - 1 ? ' disabled' : ''}>↓</button></div></div>`).join('')}</div><p class="field-hint">箭头调整分类展示顺序。关闭分类后保留规则，流量跟随默认出口；广告、国内与苹果服务保留各自的阻断或直连默认。</p>`;
    }
    function renderRegions() {
      const regions = state.catalog.regions || fallbackRegions, macros = state.catalog.macros || [];
      return `<p class="grouping-copy">优先划分独立地区，再把剩余节点放入大区。每个地区都有自动、均衡和手动选择；未识别节点进入“其他未识别”，不会丢失。</p><div class="grouping-region-order">${(state.profile.regions || []).map((code,index) => `<div><span>${esc(regions.find(item => item.code === code)?.name || code)}</span><button type="button" class="ghost" data-region-move="${index}" data-direction="-1" aria-label="前移${esc(code)}"${index === 0 ? ' disabled' : ''}>←</button><button type="button" class="ghost" data-region-move="${index}" data-direction="1" aria-label="后移${esc(code)}"${index === state.profile.regions.length - 1 ? ' disabled' : ''}>→</button></div>`).join('')}</div><p class="field-hint">上方顺序决定独立地区展示及多重匹配的优先级。</p><label class="grouping-field"><span>搜索独立地区</span><input data-grouping="region-search" placeholder="国家、地区或代码"></label><div class="grouping-regions">${regions.map(region => `<label class="grouping-check" data-region-label="${esc((region.name+' '+region.code).toLowerCase())}"><input type="checkbox" data-grouping-region="${esc(region.code)}"${state.profile.regions?.includes(region.code) ? ' checked' : ''}><span>${esc(region.flag || '')} ${esc(region.name)}<small>${esc(region.code)}</small></span></label>`).join('')}</div><div class="grouping-note"><strong>自动补齐的大区</strong><p>${esc(macros.map(item => item.name+' ('+item.code+')').join(' · ') || '亚洲其他 · 欧洲 · 北美其他 · 南美 · 大洋洲 · 非洲 · 其他未识别')}</p><small>大区排除已经分到独立地区的节点。名称无法确认实际出口时，可在预览中检查并手动修正。原生 Mihomo 的空地区会拒绝连接，请选择全球自动或有节点的地区；妙妙屋X 会移除空地区。</small></div>${field('归属修正（每行一条）', `<textarea data-grouping="overrides" rows="4" spellcheck="false" placeholder="德国|Germany => europe">${esc((state.profile.overrides || []).map(item => item.pattern+' => '+item.region).join('\n'))}</textarea>`, '只填写已单列地区或大区代码，不支持 other。人工修正会提升整个目标地区的优先级；同一目标合并，按首次出现排序。')}`;
    }
    function renderAutomatic() {
      const p = state.profile;
      return `<div class="grouping-grid">${field('测速地址',input('test_url',p.test_url,'type="url" placeholder="https://www.gstatic.com/generate_204"'))}${field('测速间隔（秒）',input('interval',p.interval,'type="number" min="60" step="1"'))}${field('切换容差（毫秒）',input('tolerance',p.tolerance,'type="number" min="0" step="1"'))}${field('均衡方式',`<select data-grouping-field="strategy">${choices([{value:'consistent-hashing',label:'相同网站优先使用同一节点（推荐）'},{value:'round-robin',label:'轮流分配新连接'},{value:'sticky-sessions',label:'同一来源与网站保持会话'}],p.strategy)}</select>`)}</div><div class="grouping-note"><strong>均衡是多节点分配连接</strong><p>不会把多个节点叠加成一条更快的连接。对登录、支付等业务，优先使用默认的一致性哈希或手动固定节点。</p></div><div class="grouping-check-row">${check('hide_auto','折叠辅助自动组',p.hide_auto)}${check('icons','使用本机缓存图标',p.icons)}</div>`;
    }
    function renderAdvanced() {
      const p = state.profile, inherit = [{value:'inherit',label:'沿用当前模板'},{value:'on',label:'开启'},{value:'off',label:'关闭'}];
      return `<h4>媒体独立分流</h4><p class="grouping-copy">启用后增加独立分类，优先于国际媒体、谷歌服务等综合规则匹配。</p><div class="grouping-check-row">${(state.catalog.media || ['YouTube','Netflix','Disney','Spotify']).map(name => `<label class="grouping-check"><input type="checkbox" data-grouping-media="${esc(name)}"${p.media?.includes(name) ? ' checked' : ''}><span>${esc(name)}</span></label>`).join('')}</div><div class="grouping-grid">${field('DNS 模式',`<select data-grouping-field="dns_mode">${choices([{value:'inherit',label:'沿用当前模板'},{value:'fake-ip',label:'Fake-IP'},{value:'redir-host',label:'Redir-Host'}],p.dns_mode || 'inherit')}</select>`)}${field('IPv6',`<select data-grouping-field="ipv6">${choices(inherit,p.ipv6 || 'inherit')}</select>`)}${field('流量嗅探',`<select data-grouping-field="sniffer">${choices(inherit,p.sniffer || 'inherit')}</select>`)}</div><p class="field-hint">规则来源沿用本页上方的选择。生成会固定本次规则版本；保留原始节点的服务器、认证与传输参数。</p>`;
    }
    function renderPreview() {
      return `<p class="grouping-copy">粘贴节点名称即可检查分区。这里只需要名称，不需要订阅链接或节点密码。</p><label class="grouping-field"><span>节点名称（每行一个，可选）</span><textarea data-grouping="names" rows="5" placeholder="香港 01&#10;德国 01&#10;越南 01">${esc(state.names)}</textarea></label><button data-grouping="preview" data-grouping-write type="button" class="secondary">检查分组</button><div data-grouping="diagnostics"></div>`;
    }
    function renderDiagnostics() {
      const result = state.preview, target = find('diagnostics');
      if (!target) return;
      if (!result) { target.innerHTML = '<p class="field-hint">预览用于核对名称归属与分组；不代表节点已连通。</p>'; return; }
      target.innerHTML = `<div class="grouping-summary"><span><b>${Number(result.total || 0)}</b>个名称</span><span><b>${Number(result.covered || 0)}</b>已归组</span><span><b>${Number(result.unknown || 0)}</b>未识别</span><span><b>${Number(result.ambiguous || 0)}</b>多重匹配</span></div>${(result.warnings || []).map(item => `<p class="field-hint warning">${esc(item)}</p>`).join('')}<div class="grouping-region-results">${(result.regions || []).map(region => `<details><summary>${esc(region.name)}<span>${Number(region.count || 0)} 个节点</span></summary><pre>${esc((region.nodes || []).join('\n') || '当前没有匹配节点')}</pre></details>`).join('')}</div>${(result.nodes || []).some(node => node.ambiguous) ? `<details class="grouping-note"><summary>需要留意的多重匹配</summary>${result.nodes.filter(node => node.ambiguous).map(node => `<p>${esc(node.name)} → ${esc(node.region_name || node.region)}<small>匹配：${esc((node.matches || []).join(' / '))}</small></p>`).join('')}</details>` : ''}<details class="grouping-note"><summary>分组与候选顺序</summary>${(result.groups || []).map(group => `<p><strong>${esc(group.name)}</strong><small>${esc((group.proxies || []).join(' → '))}</small></p>`).join('')}</details>`;
    }
    function renderArtifacts() {
      const target = find('artifacts'); if (!target) return;
      if (!state.generated || !state.artifacts.length) { target.innerHTML = '<div class="grouping-artifact-empty"><strong>采用本方案的新文件</strong><span>保存方案并生成后，在这里打开、下载或复制。基础模板仍保留在下方。</span></div>'; return; }
      const data = state.generated, item = state.artifacts[state.artifactIndex] || state.artifacts[0];
      target.innerHTML = `<div class="grouping-artifact-head"><div><span class="eyebrow">GENERATED WITH YOUR SETTINGS</span><h4>采用本方案的新文件</h4></div><span class="template-status adapted">● 已生成</span></div><div class="grouping-summary"><span><b>${Number(data.group_count || 0)}</b>策略组</span><span><b>${Number(data.provider_count || 0)}</b>规则集</span><span><b>${Number(data.rule_count || 0)}</b>路由规则</span></div>${(data.warnings || []).map(warning => `<p class="field-hint warning">${esc(warning)}</p>`).join('')}<div class="grouping-artifact-list">${state.artifacts.map((artifact,index) => `<button type="button" class="secondary${index === state.artifactIndex ? ' active' : ''}" data-grouping-artifact="${index}">${esc(artifact.name)}</button>`).join('')}</div><div class="panel-actions"><a class="button secondary" href="${esc(item.url)}" target="_blank" rel="noopener noreferrer">打开</a><a class="button" href="${esc(item.download_url)}" download="${esc(item.name)}">下载</a><button type="button" class="secondary" data-grouping="copy-content">复制正文</button><button type="button" class="secondary" data-grouping="copy-url">复制链接</button></div><code class="grouping-artifact-url">${esc(item.url)}</code><details class="template-preview" open><summary>新文件正文</summary><pre data-grouping="content">${esc(item.content || '正在读取正文…')}</pre></details><details class="grouping-provenance"><summary>版本与校验信息</summary><p>配置版本：${esc(data.input_revision || '—')}<br>规则版本：${esc(data.rule_revision || '—')}<br>方案：${esc(data.profile_hash || '—')}<br>SHA-256：${esc(item.sha256 || '—')}</p></details>`;
      const baseline = state.context.baseline?.() || '';
      if (baseline && item.content && /\.ya?ml$/i.test(item.name)) {
        const diff = contentDifference(baseline,item.content);
        target.innerHTML += `<details class="grouping-provenance"><summary>与本页基础模板对比</summary><p>正文行数：${diff.beforeLines} → ${diff.afterLines}<br>新增分组：${esc(diff.added.join('、') || '无')}<br>移除分组：${esc(diff.removed.join('、') || '无')}</p><small>展示分组名称与行数变化，不表示节点或规则内容已逐项比较。</small></details>`;
      }
      target.querySelectorAll('[data-grouping-artifact]').forEach(button => button.onclick = () => { state.artifactIndex = Number(button.dataset.groupingArtifact); renderArtifacts(); loadContent(); });
      find('copy-content').onclick = () => copy(true); find('copy-url').onclick = () => copy(false);
      find('copy-content').disabled = !item.content;
    }
    async function loadContent() {
      const item = state.artifacts[state.artifactIndex], ticket = state.ticket;
      if (!item || item.content) return;
      try {
        const url = new URL(item.url), response = await fetch(url.pathname + url.search, {cache:'no-store',credentials:'same-origin',redirect:'error'});
        if (!response.ok || /text\/html/i.test(response.headers.get('Content-Type') || '')) throw new Error('无法读取生成文件，请重新生成。');
        const content = await response.text(); if (!content.trim()) throw new Error('生成文件正文为空。');
        if (ticket !== state.ticket || state.artifacts[state.artifactIndex] !== item) return;
        item.content = content; renderArtifacts();
      } catch (error) { if (ticket === state.ticket) notice(error.message, true); }
    }
    async function copy(text) {
      const item = state.artifacts[state.artifactIndex]; if (!item || text && !item.content) return;
      try { await navigator.clipboard.writeText(text ? item.content : item.url); notice(text ? '新文件正文已复制。' : '新文件链接已复制。'); } catch { notice('剪贴板不可用，请在正文中手动复制或下载文件。', true); }
    }
    function render() {
      if (!state.loaded) { host.innerHTML = `<p class="routing-feedback${state.error ? ' bad' : ''}">${esc(state.error || '正在读取分组设置…')}</p>${state.error ? '<button type="button" data-grouping="retry" class="secondary">重试</button>' : ''}`; if (find('retry')) find('retry').onclick = load; return; }
      host.className = 'template-grouping';
      host.innerHTML = `<div class="grouping-heading"><div><span class="eyebrow">GROUPING PROFILE</span><h3>分组与分流设置</h3><p>同一套设置可用于妙妙屋X、PPanel 与 MihomoPro 覆写。</p></div><span data-grouping="state" class="soft-badge"></span></div><div class="grouping-profile-bar"><label><span>方案来源</span><select data-grouping="inherit"><option value="common"${state.inherit ? ' selected' : ''}>使用共用方案</option><option value="independent"${!state.inherit ? ' selected' : ''}>${scopes[scope]}独立方案</option></select></label><button type="button" class="secondary" data-grouping="save-common" data-grouping-write>保存为共用</button><button type="button" class="secondary" data-grouping="save-current" data-grouping-write>另存当前页</button><button type="button" class="ghost" data-grouping="copy-profile" data-grouping-write>复制方案</button><button type="button" class="ghost" data-grouping="defaults" data-grouping-write>恢复默认</button></div><p data-grouping="boundary" class="field-hint"></p><div class="grouping-tabs" role="tablist" aria-label="分组设置">${tabs.map(([key,label]) => `<button type="button" role="tab" id="grouping-${scope}-tab-${key}" aria-controls="grouping-${scope}-panel-${key}" aria-selected="${state.activeTab === key}" data-grouping-tab="${key}" class="${state.activeTab === key ? 'active' : ''}">${label}</button>`).join('')}</div><div class="grouping-tab-body">${tabs.map(([key]) => `<section id="grouping-${scope}-panel-${key}" role="tabpanel" aria-labelledby="grouping-${scope}-tab-${key}" data-grouping-panel="${key}"${state.activeTab === key ? '' : ' hidden'}>${({categories:renderCategories,regions:renderRegions,automatic:renderAutomatic,advanced:renderAdvanced,preview:renderPreview})[key]()}</section>`).join('')}</div><div class="grouping-publish"><div><strong>保存设置 → 检查分组 → 生成文件</strong><p data-grouping="feedback" class="field-hint" role="status" aria-live="polite">${esc(state.feedback || '已有订阅不会自动替换。请导入生成的新文件。')}</p></div><button type="button" data-grouping="generate">生成新文件</button></div><div data-grouping="artifacts"></div>`;
      bind(); renderStatus(); renderDiagnostics(); renderArtifacts();
    }
    function bind() {
      host.querySelectorAll('[data-grouping-tab]').forEach(button => button.onclick = () => { state.activeTab = button.dataset.groupingTab; host.querySelectorAll('[data-grouping-tab]').forEach(tab => { const active = tab === button; tab.setAttribute('aria-selected', String(active)); tab.classList.toggle('active',active); }); host.querySelectorAll('[data-grouping-panel]').forEach(panel => panel.hidden = panel.dataset.groupingPanel !== state.activeTab); });
      host.querySelectorAll('[data-grouping-field], [data-grouping-region], [data-grouping-category], [data-grouping-media]').forEach(element => element.onchange = () => { try { saveFields(); invalidate(); } catch (error) { invalidate(); notice(error.message,true); } });
      host.querySelectorAll('[data-category-move], [data-region-move]').forEach(button => button.onclick = () => {
        try { saveFields(); const items = button.dataset.categoryMove != null ? state.profile.categories : state.profile.regions, index = Number(button.dataset.categoryMove ?? button.dataset.regionMove), next = index + Number(button.dataset.direction); if (next < 0 || next >= items.length) return; [items[index],items[next]] = [items[next],items[index]]; invalidate(); render(); } catch (error) { notice(error.message,true); }
      });
      host.querySelectorAll('[data-grouping-region]').forEach(element => element.onchange = () => { try { saveFields(); invalidate(); render(); } catch (error) { invalidate(); notice(error.message,true); } });
      host.querySelectorAll('[data-grouping-media]').forEach(element => element.onchange = () => {
        try { saveFields(); const media = state.catalog.media || ['YouTube','Netflix','Disney','Spotify']; state.profile.categories = state.profile.categories.filter(item => !media.includes(item.name) || state.profile.media.includes(item.name)); state.profile.media.forEach(name => { if (!state.profile.categories.some(item => item.name === name)) state.profile.categories.push({name,enabled:true,default:'auto'}); }); invalidate(); render(); notice('媒体独立分类已更新，可在“分类与默认”中设置出口。'); } catch (error) { invalidate(); notice(error.message,true); }
      });
      find('overrides').oninput = () => { invalidate(); };
      find('region-search').oninput = event => { const query = event.target.value.toLowerCase().trim(); host.querySelectorAll('[data-region-label]').forEach(label => label.hidden = !label.dataset.regionLabel.includes(query)); };
      find('names').oninput = event => { state.names = event.target.value; state.preview = null; renderDiagnostics(); };
      find('inherit').onchange = async event => {
        try {
          saveFields(); invalidate(); state.inherit = event.target.value === 'common'; state.busy = true; renderStatus();
          if (state.inherit) { const common = await request('profiles/common'); state.profile = clone(common.profile); }
          render(); notice('方案来源已切换；保存共用或另存当前页后再生成。');
        } catch (error) { notice(error.message,true); } finally { state.busy = false; renderStatus(); }
      };
      find('save-common').onclick = () => save(true); find('save-current').onclick = () => save(false);
      find('copy-profile').onclick = async () => { try { saveFields(); await navigator.clipboard.writeText(JSON.stringify(state.profile,null,2)); notice('方案已复制为 JSON，可保存留档。'); } catch (error) { notice(error.message || '复制失败。',true); } };
      find('defaults').onclick = () => { if (!state.defaults) return; state.profile = clone(state.defaults); invalidate(); render(); notice('已恢复默认草稿，保存后生效。'); };
      find('preview').onclick = preview; find('generate').onclick = generate;
    }
    async function load() {
      if (state.loading) return;
      state.loading = true; state.busy = true; state.error = ''; render();
      try {
        const data = await request('profiles/' + scope); if (!data.profile) throw new Error('分组设置不完整。');
        if (state.revision !== data.revision) { state.ticket++; state.artifacts = []; state.generated = null; state.preview = null; }
        state.profile = clone(data.profile); state.defaults = clone(data.defaults || data.profile); state.catalog = data.catalog || {}; state.inherit = data.inherit !== false; state.revision = data.revision; state.loaded = true; state.dirty = false;
        const last = data.last_generation;
        if (last && last.profile_hash === data.revision && last.scope === scope && last.client === state.context.client && last.source === state.context.source && !contextReason()) {
          const artifacts = (last.artifacts || []).map(item => ({...item,url:safeArtifactURL(item.url),download_url:safeArtifactURL(item.download_url || item.url)}));
          if (artifacts.length && artifacts.every(item => item.url && item.download_url && item.name && !/[/\\\0]/.test(item.name))) { state.generated = last; state.artifacts = artifacts; state.artifactIndex = 0; state.feedback = '已恢复当前方案上次生成的文件；规则版本见下方。'; }
        }
        render(); if (state.artifacts.length) await loadContent();
      }
      catch (error) { state.ticket++; state.error = error.message; state.loaded = false; state.generated = null; state.artifacts = []; render(); }
      finally { state.loading = false; state.busy = false; renderStatus(); }
    }
    async function save(common) {
      if (state.busy) return;
      try {
        saveFields(); state.busy = true; renderStatus();
        const profile = clone(state.profile), data = await request('profiles/' + (common ? 'common' : scope), {method:'PUT',...body({inherit:false,profile,...(common ? {apply_scope:scope} : {})})});
        state.profile = clone(data.profile || profile); state.inherit = common; state.dirty = false; state.revision = data.revision;
        if (common) instances.forEach(instance => { if (instance.state.scope !== scope && instance.state.inherit) instance.state.stale = true; });
        state.ticket++; state.artifacts = []; state.generated = null; render(); notice(common ? '已保存共用方案，当前页已使用共用设置。其他继承页面将在下次打开时读取。' : '已保存当前页独立方案。可以检查分组并生成新文件。');
      } catch (error) { notice(error.message,true); } finally { state.busy = false; renderStatus(); }
    }
    async function preview() {
      if (state.busy) return;
      try { saveFields(); state.busy = true; renderStatus(); const ticket = ++state.ticket, profile = clone(state.profile); const data = await request('preview',{method:'POST',...body({profile,names:state.names.split('\n').map(name => name.trim()).filter(Boolean)})}); if (ticket !== state.ticket) return; state.preview = data; renderDiagnostics(); notice('分组检查完成。未识别与多重匹配可在“地区”中修正。'); }
      catch (error) { notice(error.message,true); } finally { state.busy = false; renderStatus(); }
    }
    async function generate() {
      if (state.busy || state.dirty || contextReason()) return;
      const ticket = ++state.ticket, context = {...state.context};
      try {
        state.busy = true; state.artifacts = []; state.generated = null; renderStatus(); renderArtifacts(); notice('正在生成本方案的新文件…');
        const data = await request('generate',{method:'POST',...body({scope,client:context.client,source:context.source,profile:clone(state.profile)})});
        if (ticket !== state.ticket) return;
        if (data.scope && data.scope !== scope || data.client && data.client !== context.client || data.source && data.source !== context.source) throw new Error('生成结果与当前选择不一致，请重新生成。');
        if (data.profile_hash && state.revision && data.profile_hash !== state.revision) throw new Error('已保存的方案在另一处发生变化，请重新打开此页面确认设置，再生成文件。');
        const artifacts = (data.artifacts || []).map(item => ({...item,url:safeArtifactURL(item.url),download_url:safeArtifactURL(item.download_url || item.url)}));
        if (!artifacts.length || artifacts.some(item => !item.url || !item.download_url || !item.name || /[/\\\0]/.test(item.name))) throw new Error('生成文件地址无效，请刷新并重新生成。');
        state.generated = data; state.artifacts = artifacts; state.artifactIndex = 0; renderArtifacts(); await loadContent(); notice('新文件已生成。请使用此处的打开、下载或复制操作。');
      } catch (error) { if (ticket === state.ticket) notice(error.message,true); } finally { state.busy = false; renderStatus(); }
    }
    async function activate(context) {
      const changed = state.context.client !== context.client || state.context.source !== context.source || state.context.original !== context.original || state.context.available !== context.available;
      state.context = {...context};
      if (changed) { state.ticket++; state.artifacts = []; state.generated = null; renderArtifacts(); }
      if (!state.loaded || state.stale && !state.dirty) { state.stale = false; await load(); } else renderStatus();
      return state;
    }
    return {activate,load,state};
  }
  async function activate(scope, context = {}) {
    if (!Object.hasOwn(scopes,scope)) return;
    const host = typeof context.host === 'string' ? document.getElementById(context.host) : context.host;
    if (!host) return;
    let instance = instances.get(scope);
    if (!instance || instance.state.host !== host) { instance = create(scope,host); instances.set(scope,instance); }
    return instance.activate(context);
  }
  window.CoralBayTemplateGrouping = {activate};
})();
