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

const consoleTabs = new Set(['overview','templates','overwrite','rules','subscription','activity']);
function prepareTabs() {
  const groups = {
    overview: ['.hero', '#connectionAlert', '.metric-grid', '#operations'],
    templates: ['#templates'],
    overwrite: ['#overwritePanel'],
    rules: ['#nativeRules', '#ruleSection', '#detailPanel'],
    subscription: ['#subscriptionConverter'],
    activity: ['#activity']
  };
  document.querySelectorAll('.panel.section-space').forEach(panel => {
    if (panel.querySelector('#conversionRows')) groups.rules.push(panel);
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
let templatePreviewRequest = 0;
function selectTemplate() {
  const selected = templateItems.find(item => item.id === $('clientTemplate').value); if (!selected) return;
  const original = $('templateVariant').value === 'original';
  const templateURL = original ? selected.original_url : selected.online_url;
  $('downloadClientTemplate').href = original ? selected.original_download_url : selected.download_url;
  $('downloadClientTemplate').textContent = original ? '下载原始版' : '下载改造版';
  $('openClientTemplate').href = templateURL;
  $('openClientTemplate').textContent = original ? '打开原始版' : '打开改造版';
  $('copyClientTemplate').dataset.url = templateURL;
  const statusMap = {adapted:['adapted','● 已改造'],converted:['converted','◉ 已转换规则源'],convertible:['convertible','◐ 可改造'], 'nodes-only':['limited','— 仅节点 / 不适用']};
  const state = statusMap[selected.capability] || ['base','○ 官方基础'];
  const status = `<span class="template-status ${state[0]}">${state[1]}</span>`;
  const variantStatus = original ? '<span class="template-status base">○ Perfect Panel 原始版</span>' : status;
  const variantDescription = original ? '未经 CoralBay 分流改造的 Perfect Panel 原始模板，用于对照、排错或恢复。' : selected.description;
  $('clientTemplateHint').innerHTML = `${variantStatus}<span>${escapeHTML(variantDescription)}<br><small>${original?'节点：官方原始渲染 · 策略组与规则：保持原样':`节点：${selected.node_rendering?'可渲染':'不适用'} · 策略组：${escapeHTML(selected.policy_groups)} · 规则源：${escapeHTML(selected.rule_sources)} · ${escapeHTML(selected.validation)}`}</small></span>`;
  $('ppanelName').textContent = selected.ppanel_name || '';
  $('ppanelUA').textContent = selected.user_agent || '';
  $('ppanelFormat').textContent = selected.output_format || '';
  $('ppanelScheme').textContent = selected.url_scheme || '（留空）';
  $('ppanelTemplateURL').textContent = templateURL || '';
  const requestID = ++templatePreviewRequest;
  $('clientTemplatePreview').textContent = '正在读取模板…';
  fetch(templateURL,{cache:'no-store'}).then(response=>{if(!response.ok)throw new Error(`HTTP ${response.status}`);return response.text()}).then(content=>{if(requestID===templatePreviewRequest)$('clientTemplatePreview').textContent=content}).catch(error=>{if(requestID===templatePreviewRequest)$('clientTemplatePreview').textContent=`模板预览失败：${error.message}`});
}
async function loadTemplates() {
  const data = await json('/api/public/templates'); templateItems = data.templates || [];
  $('clientTemplate').innerHTML = templateItems.map(item => `<option value="${escapeHTML(item.id)}">${escapeHTML(item.name)}</option>`).join('');
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

const targetCompatibility = {
  clash: ['ok','适合现代节点：支持 VLESS Reality、Hysteria2、TUIC、AnyTLS。'],
  stash: ['ok','输出 Stash 兼容的 Clash.Meta YAML；VLESS Reality/Vision 需要 Stash 3.1.1 或更高版本。'],
  clashr: ['warning','旧版 ClashR 格式，不支持现代 VLESS Reality 等节点。'],
  singbox: ['ok','适合现代节点：支持 VLESS Reality、Hysteria2、TUIC、AnyTLS。'],
  surge: ['warning','Surge 无法表达 VLESS Reality。若订阅全部是 VLESS，验证会拒绝生成空配置。'],
  shadowrocket: ['ok','支持当前 VLESS Reality 节点；不支持的协议会由后端过滤并报告。'],
  quanx: ['ok','支持当前 VLESS Reality 节点；部分新协议可能无法输出。'],
  loon: ['ok','支持当前 VLESS Reality 节点，生成的是完整 Loon 配置。'],
  surfboard: ['warning','Surfboard 协议能力有限，现代节点可能被过滤。'],
  quan: ['warning','Quantumult 旧版格式能力有限，建议优先使用 Quantumult X。'],
  ss: ['warning','只输出 Shadowsocks 节点，其他协议会被过滤。'],
  ssr: ['warning','只输出 ShadowsocksR 节点，其他协议会被过滤。'],
  trojan: ['warning','只输出 Trojan 节点，其他协议会被过滤。'],
  mixed: ['ok','输出可用的混合 URI；不包含策略组和规则。'],
  v2ray: ['ok','输出 Base64 节点链接，不包含策略组与规则。']
};
function updateSubCompatibility(){const target=$('subTarget').value,[state,text]=targetCompatibility[target]||['warning','请先选择目标客户端'];$('subCompatibility').className=`template-note compatibility-${state}`;$('subCompatibility').textContent=text;$('subSurgeFields').classList.toggle('hidden',target!=='surge');$('subDeviceFields').classList.toggle('hidden',target!=='quanx')}
async function loadSubconverterStatus(){try{const data=await json('/api/admin/subconverter/status');$('subBackendState').textContent=data.ok?`本机后端 · ${data.version}`:'后端异常';$('subBackendState').classList.toggle('bad',!data.ok)}catch(error){$('subBackendState').textContent='后端不可用';$('subBackendState').classList.add('bad')}}

let subscriptionPresets=[];
function applySubscriptionPreset(){const preset=subscriptionPresets.find(item=>item.id===$('subPreset').value);if(!preset)return;if(preset.built_in){$('subPresetSource').value='local';$('subPresetSource').disabled=true;$('subConfig').value=preset.local_url;$('subPresetDetail').innerHTML='<span class="template-status adapted">● 内置 MihomoPro</span><span>使用本机 666OS/YYDS Pro_cn 风格分组与 CoralBay 规则镜像，可转换其他来源的节点订阅。</span>';return}$('subPresetSource').disabled=false;let local=$('subPresetSource').value==='local';let fallback='';if(local&&!preset.cached){$('subPresetSource').value='original';local=false;fallback='<span class="warning">本机尚无该配置，已自动回退原链接。</span><br>'}$('subConfig').value=preset.id==='none'?'':(local?preset.local_url:preset.original_url);const state=preset.id==='none'?'不使用远程配置':preset.cached?`<span class="ok">● 已缓存 · ${size(preset.bytes)}</span>`:'<span class="bad">○ 未缓存，仅可使用原链接</span>';$('subPresetDetail').innerHTML=`${fallback}${state} · 当前调用：${local?'CoralBay 本机镜像':'原链接'}${preset.updated_at?` · 更新于 ${new Date(preset.updated_at).toLocaleString('zh-CN',{hour12:false})}`:''}${preset.error?`<br><span class="bad">最近同步错误：${escapeHTML(preset.error)}</span>`:''}`}
async function loadSubscriptionPresets(){const data=await json('/api/admin/subscription-presets');subscriptionPresets=data.presets||[];const groups=[];for(const item of subscriptionPresets){let group=groups.find(value=>value.name===item.group);if(!group){group={name:item.group,items:[]};groups.push(group)}group.items.push(item)}$('subPreset').innerHTML=groups.map(group=>`<optgroup label="${escapeHTML(group.name)}">${group.items.map(item=>`<option value="${escapeHTML(item.id)}">${item.built_in?'◆':item.cached?'●':'○'} ${escapeHTML(item.name)}</option>`).join('')}</optgroup>`).join('');$('subPresetCache').textContent=`${data.cached||0} / ${data.total||0} 已缓存 · MihomoPro 内置`;$('subPreset').onchange=applySubscriptionPreset;$('subPresetSource').onchange=applySubscriptionPreset;applySubscriptionPreset()}
async function loadSubscriptionCapabilities(){const data=await json('/api/admin/subscription-capabilities');const modern=(data.targets||[]).filter(item=>item.modern).length;$('subCapabilities').textContent=`${(data.targets||[]).length} 种输出 · ${modern} 种现代协议`;}
function historySettings(item){const s=item.settings;if(!s)return '<small class="muted">旧记录 · 可通过复用解析原链接</small>';const enabled=[['emoji','Emoji'],['sort','排序'],['dedup','去重'],['udp','UDP'],['xudp','XUDP'],['tfo','TFO'],['scv','跳过证书'],['tls13','TLS 1.3'],['append_type','附加协议'],['list','仅节点'],['insert','插入节点'],['expand','展开规则'],['new_name','新命名'],['fdn','过滤节点'],['clash_doh','Clash DoH'],['surge_doh','Surge DoH'],['singbox_ipv6','IPv6']].filter(([key])=>s[key]).map(([,label])=>label);const filters=[s.include&&`包含：${s.include}`,s.exclude&&`排除：${s.exclude}`,s.rename&&`重命名：${s.rename}`].filter(Boolean);const detail=[`源订阅 ${item.source_count||1} 条`,`更新 ${s.interval||24} 小时`,s.config?'远程配置':'无远程配置',...filters,...enabled].map(escapeHTML).join(' · ');return `<details class="history-settings"><summary>查看 ${3+filters.length+enabled.length} 项设置</summary><small>${detail}</small></details>`}
async function reuseSubscriptionHistory(url){const data=await json('/api/admin/subscription-parse',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({url})});applyParsedSubscription(data.params||{});$('subPreset').value='none';$('subPresetSource').disabled=false;$('subPresetDetail').textContent='已从历史记录恢复设置；远程配置 URL 保持记录中的值。';$('subURLs').scrollIntoView({behavior:'smooth',block:'center'});$('message').textContent=`已复用历史设置：${data.source_count} 个源订阅`}
async function loadSubscriptionHistory(){const data=await json('/api/admin/subscription-history'),items=data.history||[];$('subHistoryRows').innerHTML=items.map(item=>`<tr><td>${new Date(item.created_at).toLocaleString('zh-CN',{hour12:false})}</td><td><strong>${escapeHTML(item.target)}</strong><br><small>${escapeHTML(item.filename||'未命名')}</small></td><td>${item.node_count}</td><td>${historySettings(item)}</td><td><div class="history-actions"><button class="link-button" data-history-reuse="${escapeHTML(item.url)}">复用设置</button><button class="link-button" data-history-copy="${escapeHTML(item.url)}">复制</button><a href="${escapeHTML(item.url)}" target="_blank" rel="noreferrer">打开</a><button class="link-button danger-link" data-history-delete="${escapeHTML(item.id)}">删除</button></div></td></tr>`).join('')||'<tr><td colspan="5" class="muted">暂无生成记录</td></tr>';document.querySelectorAll('[data-history-copy]').forEach(button=>button.onclick=async()=>{await navigator.clipboard.writeText(button.dataset.historyCopy);$('message').textContent='历史订阅链接已复制'});document.querySelectorAll('[data-history-reuse]').forEach(button=>button.onclick=async()=>{try{await reuseSubscriptionHistory(button.dataset.historyReuse)}catch(error){$('message').textContent=`复用失败：${error.message}`}});document.querySelectorAll('[data-history-delete]').forEach(button=>button.onclick=async()=>{if(!confirm('仅删除这条生成记录？已生成的签名链接仍可继续使用。'))return;await json(`/api/admin/subscription-history/${encodeURIComponent(button.dataset.historyDelete)}`,{method:'DELETE'});await loadSubscriptionHistory();$('message').textContent='单条历史记录已删除'})}
function subscriptionPayload(){return{target:$('subTarget').value,url:$('subURLs').value.split(/[\n|]+/).map(value=>value.trim()).filter(Boolean).join('|'),config:$('subConfig').value.trim(),filename:$('subFilename').value.trim(),include:$('subInclude').value.trim(),exclude:$('subExclude').value.trim(),rename:$('subRename').value.trim(),dev_id:$('subDeviceID').value.trim(),surge_version:Number($('subSurgeVersion').value)||4,interval:Number($('subInterval').value)||24,emoji:$('subEmoji').checked,sort:$('subSort').checked,dedup:$('subDedup').checked,udp:$('subUDP').checked,xudp:$('subXUDP').checked,tfo:$('subTFO').checked,scv:$('subSCV').checked,tls13:$('subTLS13').checked,append_type:$('subAppendType').checked,list:$('subListOnly').checked,insert:$('subInsert').checked,expand:$('subExpand').checked,new_name:$('subNewName').checked,fdn:$('subFDN').checked,clash_doh:$('subClashDoH').checked,surge_doh:$('subSurgeDoH').checked,singbox_ipv6:$('subSingboxIPv6').checked}}
function firstParam(params,key){const value=params[key];return Array.isArray(value)?(value[0]||''):value||''}
function applyParsedSubscription(params){const text=(key,id)=>{if($(id))$(id).value=firstParam(params,key)},check=(key,id)=>{if($(id))$(id).checked=firstParam(params,key)==='true'};text('target','subTarget');text('url','subURLs');text('config','subConfig');text('filename','subFilename');text('include','subInclude');text('exclude','subExclude');text('rename','subRename');text('dev_id','subDeviceID');const seconds=Number(firstParam(params,'interval'));if(seconds)$('subInterval').value=Math.max(1,Math.round(seconds/3600));const ver=firstParam(params,'ver');if(ver)$('subSurgeVersion').value=ver;[['emoji','subEmoji'],['sort','subSort'],['dedup','subDedup'],['udp','subUDP'],['xudp','subXUDP'],['tfo','subTFO'],['scv','subSCV'],['tls13','subTLS13'],['append_type','subAppendType'],['list','subListOnly'],['insert','subInsert'],['expand','subExpand'],['new_name','subNewName'],['fdn','subFDN'],['clash.doh','subClashDoH'],['surge.doh','subSurgeDoH']].forEach(([key,id])=>check(key,id));$('subSingboxIPv6').checked=firstParam(params,'singbox.ipv6')==='1';updateSubCompatibility()}

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
  $('clientTemplate').onchange=selectTemplate; $('templateVariant').onchange=selectTemplate; $('copyClientTemplate').onclick=async()=>{await navigator.clipboard.writeText($('copyClientTemplate').dataset.url);$('message').textContent='在线模板链接已复制'};
  document.querySelectorAll('[data-copy-field]').forEach(button=>button.onclick=async()=>{const value=$(button.dataset.copyField).textContent;if(value==='（留空）')return;$('message').textContent='字段已复制';await navigator.clipboard.writeText(value)});
  $('copyPPanelConfig').onclick=async()=>{const selected=templateItems.find(item=>item.id===$('clientTemplate').value);if(!selected)return;const templateURL=$('templateVariant').value==='original'?selected.original_url:selected.online_url;const content=[`名称: ${selected.ppanel_name}`,`User-Agent: ${selected.user_agent}`,`输出格式: ${selected.output_format}`,`URL Scheme: ${selected.url_scheme||'留空'}`,`模板: ${templateURL}`].join('\n');await navigator.clipboard.writeText(content);$('message').textContent='PPanel 客户端设置已复制'};
  $('conversionSearch').oninput=renderConversions; $('conversionKind').onchange=renderConversions;
	$('nativeSearch').oninput=renderNative; $('nativePlatform').onchange=renderNative;
	$('subTarget').onchange=updateSubCompatibility; updateSubCompatibility(); loadSubconverterStatus();
	$('generateSub').onclick=async()=>{const feedback=document.querySelector('.converter-actions .field-hint');try{$('generateSub').disabled=true;$('generateSub').textContent='正在拉取并验证…';feedback.textContent='正在由本机后端获取并解析订阅…';feedback.classList.remove('bad','ok');$('subResult').classList.add('hidden');const data=await json('/api/admin/subscription-link',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(subscriptionPayload())});$('subResultURL').textContent=data.url;$('subValidation').textContent=`✓ 已验证 ${data.node_count} 个可用节点`;$('openSubResult').href=data.url;$('downloadSubResult').href=data.url;$('subQR').src=`/api/admin/subscription-qr?url=${encodeURIComponent(data.url)}`;$('subResult').classList.remove('hidden');feedback.textContent=`转换验证通过：${data.node_count} 个可用节点`;feedback.classList.add('ok');loadSubscriptionHistory()}catch(error){feedback.textContent=`转换失败：${error.message}`;feedback.classList.add('bad')}finally{$('generateSub').disabled=false;$('generateSub').textContent='测试并生成'}};
	$('copySubResult').onclick=async()=>{await navigator.clipboard.writeText($('subResultURL').textContent);$('message').textContent='订阅链接已复制'};
  $('parseSub').onclick=async()=>{try{const data=await json('/api/admin/subscription-parse',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({url:$('parseSubURL').value.trim()})});applyParsedSubscription(data.params||{});$('parseSubStatus').textContent=`已解析并回填 ${data.source_count} 个源订阅`;$('parseSubStatus').className='field-hint ok'}catch(error){$('parseSubStatus').textContent=`解析失败：${error.message}`;$('parseSubStatus').className='field-hint bad'}};
  $('syncSubPresets').onclick=async()=>{const button=$('syncSubPresets');try{button.disabled=true;button.textContent='正在缓存 88 条配置…';const data=await json('/api/admin/subscription-presets/sync',{method:'POST'});$('message').textContent=`远程配置同步完成：${data.cached}/${data.total} 可用，${data.failed} 条上游失败`;await loadSubscriptionPresets()}catch(error){$('message').textContent=`远程配置同步失败：${error.message}`}finally{button.disabled=false;button.textContent='立即更新本机镜像'}};
  $('clearSubHistory').onclick=async()=>{if(!confirm('确认清空订阅转换历史？已生成的签名链接仍可使用。'))return;await json('/api/admin/subscription-history',{method:'DELETE'});loadSubscriptionHistory()};
  $('closeDetails').onclick=()=>$('detailPanel').classList.add('hidden'); $('detailSearch').oninput=()=>{clearTimeout(detailTimer);detailTimer=setTimeout(()=>showRuleDetails(detailPath,detailName,$('detailSearch').value),250)};
  async function refreshAll(){const results=await Promise.allSettled([loadAdmin(),loadRules(),loadTemplates(),loadConversions(),loadNative(),loadSubconverterStatus(),loadSubscriptionPresets(),loadSubscriptionCapabilities(),loadSubscriptionHistory()]);const failed=results.find(item=>item.status==='rejected');if(failed)connected(false,`部分数据加载失败：${failed.reason.message}`)}
  refreshAll();
}
publicStatus();
