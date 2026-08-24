const $ = id => document.getElementById(id);
async function json(url, opt = {}) { const response = await fetch(url, {...opt, cache:'no-store'}); const data = await response.json().catch(() => ({})); if (response.status===401){location.replace('/');throw new Error('登录已失效')} if (!response.ok) throw new Error(data.error || `HTTP ${response.status}`); return data; }
function actionHeaders(extra = {}) { return extra; }
const short = value => value ? value.slice(0, 10) : '—';
const size = bytes => bytes == null ? '—' : bytes < 1024 ? `${bytes} B` : bytes < 1048576 ? `${(bytes/1024).toFixed(1)} KB` : `${(bytes/1048576).toFixed(1)} MB`;
const escapeHTML = value => String(value ?? '').replace(/[&<>'"]/g, char => ({'&':'&amp;','<':'&lt;','>':'&gt;',"'":'&#39;','"':'&quot;'}[char]));
function setState(id, text, ok) { $(id).textContent = text; $(id).classList.remove('ok','bad'); $(id).classList.add(ok ? 'ok' : 'bad'); }
function connected(ok, error = '') {
  if ($('headerState')) $('headerState').textContent = ok ? '服务在线' : '连接异常';
  if ($('connectionAlert')) $('connectionAlert').classList.toggle('hidden', ok);
  if ($('connectionError') && error) $('connectionError').textContent = error;
  if ($('lastRefresh')) $('lastRefresh').textContent = new Date().toLocaleString('zh-CN', {hour12:false});
}

const consoleTabs = new Set(['overview','templates','rules','subscription','activity']);
function prepareTabs() {
  const groups = {
    overview: ['.hero', '#connectionAlert', '.metric-grid', '#operations'],
    templates: ['#templates'],
    rules: ['#nativeRules', '#ruleSection', '#detailPanel'],
    subscription: ['#subscriptionConverter'],
    activity: ['#activity']
  };
  document.querySelectorAll('.panel.section-space').forEach(panel => {
    if (panel.querySelector('#conversionRows')) groups.rules.push(panel);
    if (panel.querySelector('a[href="/_templates/MihomoPro_overwrite.conf"]')) groups.subscription.push(panel);
  });
  Object.entries(groups).forEach(([name, selectors]) => selectors.forEach(selector => {
    const panel = typeof selector === 'string' ? document.querySelector(selector) : selector;
    if (panel) { panel.dataset.panel = name; panel.classList.add('tab-panel'); }
  }));
}
function activateTab(name, options = {}) {
  const selected = consoleTabs.has(name) ? name : 'overview';
  document.querySelectorAll('[data-tab]').forEach(button => {
    const active = button.dataset.tab === selected;
    button.classList.toggle('active', active);
    button.setAttribute('aria-selected', String(active));
  });
  document.querySelectorAll('[data-panel]').forEach(panel => panel.classList.toggle('active', panel.dataset.panel === selected));
  if (options.updateHash !== false && location.hash !== `#${selected}`) history.replaceState(null, '', `#${selected}`);
}

async function publicStatus() {
  if (!$('state')) return;
  try { const data=await json('/api/public/status'), status=data.status||{}; setState('state',data.syncing?'同步中':'正常',true); $('commit').textContent=short(status.commit); $('files').textContent=status.validated_files??'—'; $('synced').textContent=status.synced_at||'—'; }
  catch { setState('state','异常',false); }
}

let detailItems = [], detailPath = '', detailName = '', detailTimer;
async function showRuleDetails(path, name, query = '') {
  try {
    activateTab('rules');
    detailPath=path; detailName=name; const data = await json(`/api/public/rule-details?path=${encodeURIComponent(path)}&q=${encodeURIComponent(query)}&page_size=500`);
    detailItems = data.entries || []; $('detailTitle').textContent = `${name} 规则详情`;
    $('detailMeta').textContent = `${data.count} 条匹配 · 当前显示 ${detailItems.length} 条 · ${data.source_path}`; if(!query)$('detailSearch').value = '';
    $('detailEntries').textContent = detailItems.join('\n'); $('detailPanel').classList.remove('hidden');
    $('detailPanel').scrollIntoView({behavior:'smooth'});
  } catch (error) { $('message').textContent = error.message; }
}

async function loadRules() {
  if (!$('ruleRows')) return;
  try {
    const data = await json('/api/public/rules');
    if ($('ruleCount')) $('ruleCount').textContent = `${data.count || 0} 项资源`;
    $('ruleRows').innerHTML = data.rules.map(rule => `<tr><td><div class="rule-name"><img src="${escapeHTML(rule.icon_url)}" alt=""><div><strong>${escapeHTML(rule.name)}</strong><br><small>${escapeHTML(rule.path)}</small></div></div></td><td>${escapeHTML(rule.behavior)}<br><small>${escapeHTML(rule.format)}</small></td><td class="${rule.cached?'ok':'bad'}">${rule.cached?'● 已缓存':'● 缺失'}</td><td>${size(rule.bytes)}</td><td><a href="${escapeHTML(rule.original_url)}" target="_blank" rel="noreferrer">原链接</a> · <a href="${escapeHTML(rule.mirror_url)}" target="_blank" rel="noreferrer">镜像</a> · <a href="${escapeHTML(rule.mirror_url)}" download>下载</a>${rule.readable?` · <button class="link-button" data-detail="${escapeHTML(rule.path)}" data-name="${escapeHTML(rule.name)}">查看条目</button>`:`<br><small class="muted">暂无公开可读源</small>`}</td></tr>`).join('');
    document.querySelectorAll('[data-detail]').forEach(button => button.onclick = () => showRuleDetails(button.dataset.detail, button.dataset.name));
  } catch (error) { $('ruleRows').innerHTML = `<tr><td colspan="5" class="bad">规则目录加载失败：${escapeHTML(error.message)}</td></tr>`; throw error; }
}

let templateItems = [];
function selectTemplate() {
  const selected = templateItems.find(item => item.id === $('clientTemplate').value); if (!selected) return;
  $('downloadClientTemplate').href = selected.download_url;
  $('downloadClientTemplate').textContent = `下载 .${selected.extension}`;
  $('copyClientTemplate').dataset.url = selected.online_url;
  const statusMap = {adapted:['adapted','● 已改造'],converted:['converted','◉ 已转换规则源'],convertible:['convertible','◐ 可改造'], 'nodes-only':['limited','— 仅节点 / 不适用']};
  const state = statusMap[selected.capability] || ['base','○ 官方基础'];
  const status = `<span class="template-status ${state[0]}">${state[1]}</span>`;
  $('clientTemplateHint').innerHTML = `${status}<span>${escapeHTML(selected.description)}<br><small>节点：${selected.node_rendering?'可渲染':'不适用'} · 策略组：${escapeHTML(selected.policy_groups)} · 规则源：${escapeHTML(selected.rule_sources)} · ${escapeHTML(selected.validation)}</small></span>`;
  $('ppanelName').textContent = selected.ppanel_name || '';
  $('ppanelUA').textContent = selected.user_agent || '';
  $('ppanelFormat').textContent = selected.output_format || '';
  $('ppanelScheme').textContent = selected.url_scheme || '（留空）';
  $('ppanelTemplateURL').textContent = selected.online_url || '';
}
async function loadTemplates() {
  const data = await json('/api/public/templates'); templateItems = data.templates || [];
  const label = item => item.capability === 'adapted' ? '🟢 已改造' : item.capability === 'converted' ? '🔵 已转换规则源' : item.capability === 'convertible' ? '🟡 可改造' : '⚪ 仅节点 / 不适用';
  $('clientTemplate').innerHTML = templateItems.map(item => `<option value="${escapeHTML(item.id)}">${label(item)} · ${escapeHTML(item.name)}</option>`).join('');
  selectTemplate();
}

let conversionItems = [];
function renderConversions() {
  if (!$('conversionRows')) return;
  const query = ($('conversionSearch').value || '').trim().toLowerCase(), kind = $('conversionKind').value;
  const items = conversionItems.filter(item => (!kind || item.kind === kind) && (!query || item.id.toLowerCase().includes(query) || item.source.toLowerCase().includes(query)));
  $('conversionRows').innerHTML = items.map(item => `<tr class="${item.entries===0?'empty-rule':''}"><td><strong>${escapeHTML(item.id.replace(/^(site|ip)-/,''))}</strong>${item.entries===0?'<br><small class="warning">安全空占位：上游没有公开可读源</small>':''}</td><td>${item.kind==='site'?'域名':'IP/CIDR'}</td><td>${Number(item.entries).toLocaleString()}</td><td><code>${escapeHTML(item.source)}</code></td><td><a href="${escapeHTML(item.list_url)}" target="_blank" rel="noreferrer">RULE-SET</a> · <a href="${escapeHTML(item.singbox_url)}" target="_blank" rel="noreferrer">sing-box JSON</a> · <button class="link-button" data-copy-url="${escapeHTML(item.list_url)}">复制链接</button></td></tr>`).join('') || '<tr><td colspan="5" class="muted">没有匹配的转换产物</td></tr>';
  document.querySelectorAll('[data-copy-url]').forEach(button=>button.onclick=async()=>{await navigator.clipboard.writeText(button.dataset.copyUrl);$('message').textContent='转换产物链接已复制'});
}
async function loadConversions() {
  if (!$('conversionRows')) return;
  try { const data=await json('/api/public/conversions'); conversionItems=data.sets||[]; $('conversionCount').textContent=`${data.count||0} 套产物`; renderConversions(); }
  catch(error) { $('conversionRows').innerHTML=`<tr><td colspan="5" class="bad">${escapeHTML(error.message)}</td></tr>`; }
}

let nativeItems=[];
function renderNative(){const query=($('nativeSearch').value||'').toLowerCase(),platform=$('nativePlatform').value;const items=nativeItems.filter(item=>(!platform||item.platform===platform)&&(!query||item.path.toLowerCase().includes(query)));$('nativeRows').innerHTML=items.slice(0,500).map(item=>`<tr><td><strong>${escapeHTML(item.platform)}</strong></td><td><code>${escapeHTML(item.path)}</code></td><td>${escapeHTML(item.format)}</td><td>${size(item.bytes)}</td><td><a href="${escapeHTML(item.url)}" target="_blank">打开</a> · <button class="link-button" data-native-url="${escapeHTML(item.url)}">复制</button></td></tr>`).join('')||'<tr><td colspan="5">没有匹配文件</td></tr>';document.querySelectorAll('[data-native-url]').forEach(button=>button.onclick=async()=>navigator.clipboard.writeText(button.dataset.nativeUrl));}
async function loadNative(){const data=await json('/api/public/native-rules');nativeItems=data.rules||[];$('nativeCount').textContent=`${data.count||0} 个文件`;renderNative()}

async function loadAdmin() {
  try {
    const data=await json('/api/admin/status'), status=data.status||{}, certificate=data.certificate||{}, icons=data.icons||{};
    setState('adminState',data.syncing?'同步中':'正常',!data.last_error); $('adminCommit').textContent=`提交 ${short(status.commit)}`;
    $('releaseID').textContent=short(status.release_id||status.commit); $('geoCommit').textContent=`Geo ${short(status.geo_commit)} · 生成器 ${status.generator_version||'—'}`;
    $('runningVersion').textContent=`v${data.version}`; $('latestVersion').textContent=data.latest_version?`最新 ${data.latest_version}${data.update_available?' · 可升级':' · 已是最新'}`:'暂时无法检查最新版';
    setState('certificate',certificate.ok?`${certificate.days_remaining} 天`:'异常',certificate.ok&&certificate.days_remaining>14); $('certificateIssuer').textContent=certificate.ok?`${certificate.issuer} · ${new Date(certificate.not_after).toLocaleDateString()}`:(certificate.error||'无法读取');
    setState('iconCache',`${icons.cached||0} / ${icons.expected||27}`,icons.ok); $('iconBytes').textContent=size(icons.bytes); const diagnostics=await json('/api/public/diagnostics'); $('adminFiles').textContent=`${diagnostics.rules.converted_real} / ${diagnostics.rules.total}`; $('ruleCard').querySelector('small').textContent=`${status.validated_files||0}/33 MRS · ${diagnostics.rules.safe_empty} 个安全空占位`;
    $('interval').value=String(data.interval_seconds); $('intervalHint').textContent=`当前每 ${Math.round(data.interval_seconds/3600)} 小时`;
    const job=data.job||{}, progress={"准备同步":8,"开始同步":18,"校验":35,"可读规则源":48,"生成跨客户端":68,"生成客户端模板":78,"发布":92,"发布完成":100}[job.stage]||0; $('jobProgress').style.width=`${progress}%`; if(job.state==='running')$('message').textContent=`任务 ${job.id}：${job.stage}`;
    $('updateApp').disabled=!data.update_available; $('updateApp').textContent=data.update_available?`升级到 ${data.latest_version}`:'已是最新版本';
    const [logs,audit]=await Promise.all([json('/api/admin/logs'),json('/api/admin/audit')]); const auditLines=(audit.entries||[]).slice(-20).map(line=>{try{const item=JSON.parse(line);return `${item.time} [审计] ${item.action} ${item.result} ${item.detail||''}`}catch{return line}}); $('logs').textContent=[...(logs.logs||[]),...auditLines].join('\n')||'暂无运行及审计日志';
    const releases=await json('/api/admin/releases'); $('releases').innerHTML=(releases.releases||[]).map(item=>`<div class="release"><code>${short(item.commit)} ${item.active?'（当前）':''}</code>${item.active?'':`<button data-rollback="${item.commit}" class="secondary">回滚</button>`}</div>`).join('')||'暂无历史版本';
    document.querySelectorAll('[data-rollback]').forEach(button=>button.onclick=()=>rollback(button.dataset.rollback));
    connected(true);
  } catch(error) { setState('adminState','连接异常',false); $('message').textContent=`状态读取失败：${error.message}`; connected(false, `API 请求失败：${error.message}`); }
}
async function rollback(commit) { if(!confirm(`确认回滚到 ${short(commit)}？`)) return; await json('/api/admin/rollback',{method:'POST',headers:actionHeaders({'Content-Type':'application/json'}),body:JSON.stringify({commit})}); $('message').textContent='回滚完成'; loadAdmin(); }

if ($('sync')) {
	prepareTabs();
	document.querySelectorAll('[data-tab]').forEach(button => button.onclick=()=>activateTab(button.dataset.tab));
	window.addEventListener('hashchange',()=>activateTab(location.hash.slice(1),{updateHash:false}));
	activateTab(location.hash.slice(1),{updateHash:false});
	$('logout').onclick=async()=>{await fetch('/api/logout',{method:'POST'});location.replace('/')};
  $('sync').onclick=async()=>{try{await json('/api/admin/sync',{method:'POST',headers:actionHeaders()});$('message').textContent='同步已启动';setTimeout(()=>{loadAdmin();loadRules();loadTemplates()},1500)}catch(error){if(error.message.includes('令牌'))sessionStorage.removeItem('coralbayActionToken');$('message').textContent=error.message}};
  $('updateApp').onclick=async()=>{if(!confirm('确认拉取最新镜像并重启 CoralBay Rules？页面可能短暂断开。'))return;await json('/api/admin/update',{method:'POST',headers:actionHeaders()});$('message').textContent='更新器已启动，请约一分钟后刷新页面'};
  $('saveInterval').onclick=async()=>{await json('/api/admin/settings',{method:'PUT',headers:actionHeaders({'Content-Type':'application/json'}),body:JSON.stringify({interval_seconds:Number($('interval').value)})});$('message').textContent='同步频率已保存';loadAdmin()};
  $('refresh').onclick=()=>refreshAll(); $('ruleCard').onclick=()=>{activateTab('rules');requestAnimationFrame(()=>$('ruleSection').scrollIntoView({behavior:'smooth'}))};
  $('clientTemplate').onchange=selectTemplate; $('copyClientTemplate').onclick=async()=>{await navigator.clipboard.writeText($('copyClientTemplate').dataset.url);$('message').textContent='在线模板链接已复制'};
  document.querySelectorAll('[data-copy-field]').forEach(button=>button.onclick=async()=>{const value=$(button.dataset.copyField).textContent;if(value==='（留空）')return;$('message').textContent='字段已复制';await navigator.clipboard.writeText(value)});
  $('copyPPanelConfig').onclick=async()=>{const selected=templateItems.find(item=>item.id===$('clientTemplate').value);if(!selected)return;const content=[`名称: ${selected.ppanel_name}`,`User-Agent: ${selected.user_agent}`,`输出格式: ${selected.output_format}`,`URL Scheme: ${selected.url_scheme||'留空'}`,`模板: ${selected.online_url}`].join('\n');await navigator.clipboard.writeText(content);$('message').textContent='PPanel 客户端设置已复制'};
  $('conversionSearch').oninput=renderConversions; $('conversionKind').onchange=renderConversions;
	$('nativeSearch').oninput=renderNative; $('nativePlatform').onchange=renderNative;
	$('generateSub').onclick=async()=>{try{const data=await json('/api/admin/subscription-link',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({target:$('subTarget').value,url:$('subURLs').value.trim(),emoji:$('subEmoji').checked,rename:$('subRename').checked})});$('subResultURL').textContent=data.url;$('subResult').classList.remove('hidden');$('message').textContent='带签名订阅链接已生成'}catch(error){$('message').textContent=error.message}};
	$('copySubResult').onclick=async()=>{await navigator.clipboard.writeText($('subResultURL').textContent);$('message').textContent='订阅链接已复制'};
  $('closeDetails').onclick=()=>$('detailPanel').classList.add('hidden'); $('detailSearch').oninput=()=>{clearTimeout(detailTimer);detailTimer=setTimeout(()=>showRuleDetails(detailPath,detailName,$('detailSearch').value),250)};
  async function refreshAll(){const results=await Promise.allSettled([loadAdmin(),loadRules(),loadTemplates(),loadConversions(),loadNative()]);const failed=results.find(item=>item.status==='rejected');if(failed)connected(false,`部分数据加载失败：${failed.reason.message}`)}
  refreshAll();
}
publicStatus();
