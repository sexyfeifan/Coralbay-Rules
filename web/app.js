const coralbayAssetVersion=new URL(document.currentScript?.src||location.href).searchParams.get('v')||'4.13.0';
const $ = id => document.getElementById(id);
let usagePage=1;
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

const consoleTabs = new Set(['overview','templates','overwrite','rules','subscription','activity','management','routing','sources']);
let routingModulePromise, consoleToastTimer;
let legacyUIReady=false, legacyDataStarted=false;
function ensureLegacyData(){if(!legacyUIReady||legacyDataStarted)return;legacyDataStarted=true;refreshAll()}
function consoleNotice(message){const toast=$('consoleToast');if(!toast)return;toast.textContent=message;toast.classList.remove('hidden');clearTimeout(consoleToastTimer);consoleToastTimer=setTimeout(()=>toast.classList.add('hidden'),6500)}
function setNavigationOpen(open){const wasOpen=$('consoleNavigation').classList.contains('open');$('consoleNavigation').classList.toggle('open',open);$('navBackdrop').classList.toggle('hidden',!open);$('navToggle').setAttribute('aria-expanded',String(open));document.body.classList.toggle('nav-open',open);if(open)$('navClose').focus();else if(wasOpen)$('navToggle').focus()}
function currentConsolePage(){return location.pathname.replace(/\/$/,'')==='/routing'?(location.hash==='#sources'?'sources':'routing'):location.hash.slice(1)}
async function loadRoutingModule(view){try{if(!routingModulePromise)routingModulePromise=new Promise((resolve,reject)=>{const script=document.createElement('script');script.src='/assets/routing.js?v='+encodeURIComponent(coralbayAssetVersion);script.onload=()=>resolve(window.CoralBayRouting);script.onerror=()=>{script.remove();routingModulePromise=null;reject(new Error('分流页面模块加载失败，请刷新重试'))};document.body.appendChild(script)});const module=await routingModulePromise;await module.activate(view)}catch(error){consoleNotice(error.message)}}
function prepareTabs() {
  const groups = {
    overview: ['.hero', '#connectionAlert', '.metric-grid', '#operations'],
    templates: ['#templates'],
    overwrite: ['#overwritePanel'],
    rules: ['#nativeRules', '#ruleSection'],
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
    if(active)button.setAttribute('aria-current','page');else button.removeAttribute('aria-current');
  });
  document.querySelectorAll('[data-panel]').forEach(panel => panel.classList.toggle('active', panel.dataset.panel === selected));
  const destination=selected==='routing'?'/routing':selected==='sources'?'/routing#sources':'/#'+selected;
  if (options.updateHash !== false && location.pathname+location.hash!==destination) history.pushState(null, '', destination);
  if($('navToggle'))setNavigationOpen(false);
  if(selected==='routing'||selected==='sources')loadRoutingModule(selected);
  if(selected==='management'&&$('manageRouting').classList.contains('active'))loadRoutingModule('management');
  else if(selected!=='routing'&&selected!=='sources')ensureLegacyData();
}

async function publicStatus() {
  if (!$('state')) return;
  try { const data=await json('/api/public/status'), status=data.status||{}; setState('state',data.syncing?'同步中':'正常',true); $('commit').textContent=short(status.commit); $('files').textContent=status.validated_files??'—'; $('synced').textContent=status.synced_at||'—'; }
  catch { setState('state','异常',false); }
}

function resourceURL(raw){if(!raw)return '';try{const url=new URL(raw,location.origin);return ['http:','https:'].includes(url.protocol)?url.href:''}catch{return ''}}
function resourceDate(value){return value?new Date(value).toLocaleString('zh-CN',{hour12:false}):'尚未记录'}
function resourceLink(url,label){const safe=resourceURL(url);return safe?`<a href="${escapeHTML(safe)}" target="_blank" rel="noopener noreferrer">${escapeHTML(label)}</a>`:'<span class="muted">暂无链接</span>'}
// Shared inspector is lazy: opening local content never asks for upstream state.
window.CoralBayResourceDrawer=(()=>{
  let dialog,config,source='local',page=1,request=0,timer,busy=false,snapshots={},opener;
  const pageSize=200;
  function mount(){
    if(dialog)return;
    dialog=document.createElement('dialog');dialog.id='resourceDrawer';dialog.className='resource-drawer';dialog.setAttribute('aria-labelledby','resourceTitle');
    dialog.innerHTML=`<div class="resource-drawer-head"><div><span id="resourceLibrary" class="eyebrow"></span><h2 id="resourceTitle"></h2></div><button id="resourceClose" class="ghost" aria-label="关闭资源详情" type="button">×</button></div><div class="resource-drawer-body"><div class="local-tabs resource-tabs" role="group" aria-label="详情来源"><button type="button" data-resource-tab="local">本地镜像</button><button type="button" data-resource-tab="upstream">上游原版</button><button type="button" data-resource-tab="diff">差异摘要</button></div><p id="resourceScope" class="field-hint"></p><div id="resourceStatus" class="routing-feedback" role="status" aria-live="polite"></div><dl id="resourceMeta" class="resource-meta"></dl><div id="resourceLinks" class="resource-links"></div><div id="resourceSearchRow" class="resource-search-row"><input id="resourceSearch" aria-label="搜索资源条目" placeholder="搜索域名、IP 或规则文本"><button id="resourceReload" type="button" class="secondary">重新读取</button></div><p id="resourceCount" class="field-hint"></p><pre id="resourceEntries" class="resource-entries"></pre><div id="resourcePagination" class="resource-pagination"><button id="resourcePrev" type="button" class="secondary">上一页</button><span id="resourcePage" class="field-hint"></span><button id="resourceNext" type="button" class="secondary">下一页</button></div><div id="resourceCreateRow" class="resource-create-row hidden"><button id="resourceCreate" type="button">用此规则新建分流方案</button><p class="field-hint">会新建草稿；订阅地址与保存仍需你填写确认。</p></div></div>`;
    document.body.append(dialog);
    $('resourceClose').onclick=()=>dialog.close();
    dialog.addEventListener('close',()=>{request++;clearTimeout(timer);opener?.focus()});
    dialog.addEventListener('click',event=>{if(event.target===dialog)dialog.close()});
    dialog.querySelectorAll('[data-resource-tab]').forEach(button=>button.onclick=()=>{source=button.dataset.resourceTab;page=1;$('resourceSearch').value='';clearTimeout(timer);load()});
    $('resourceSearch').oninput=()=>{clearTimeout(timer);request++;busy=true;page=1;$('resourcePrev').disabled=true;$('resourceNext').disabled=true;timer=setTimeout(load,300)};
    $('resourceReload').onclick=load;
    $('resourcePrev').onclick=()=>{if(!busy&&page>1){page--;load()}};
    $('resourceNext').onclick=()=>{if(!busy){page++;load()}};
    $('resourceCreate').onclick=async()=>{const create=config.create,chosen=source==='upstream'?'upstream':'local';dialog.close();await create(chosen)};
  }
  function metaRow(label,value){return `<div><dt>${escapeHTML(label)}</dt><dd>${escapeHTML(value??'—')}</dd></div>`}
  function renderLinks(data){
    const active=source==='local'?(data.local_url||data.url):(data.url||data.source_url);
    const links=[['当前来源文件',active],['本机固定文件',data.local_url],[data.content_kind==='associated_geo'?'关联 geo 可读源':'上游文件',data.source_url]];
    const used=new Set();$('resourceLinks').innerHTML=links.filter(([,url])=>{const safe=resourceURL(url);if(!safe||used.has(safe))return false;used.add(safe);return true}).map(([label,url])=>`<div><span>${escapeHTML(label)}</span>${resourceLink(url,'打开 / 下载')}<button type="button" class="link-button" data-resource-copy="${escapeHTML(resourceURL(url))}">复制 URL</button><code>${escapeHTML(resourceURL(url))}</code></div>`).join('');
    $('resourceLinks').querySelectorAll('[data-resource-copy]').forEach(button=>button.onclick=async()=>{try{await navigator.clipboard.writeText(button.dataset.resourceCopy);consoleNotice('资源 URL 已复制')}catch{consoleNotice('剪贴板不可用，请手动复制下方 URL。')}});
  }
  function renderComparison(){
    const local=snapshots.local,upstream=snapshots.upstream;
    $('resourceMeta').innerHTML=metaRow('本地版本',local?.revision||'尚未读取')+metaRow('上游版本',upstream?.revision||'尚未读取')+metaRow('本地 SHA-256',local?.sha256||'—')+metaRow('上游 SHA-256',upstream?.sha256||'—');
    $('resourceStatus').textContent=!local||!upstream?'先分别打开本地镜像与上游原版，再比较已读取的版本。':local.sha256&&upstream.sha256?(local.sha256===upstream.sha256?'已读取文件的 SHA-256 相同。':'已读取文件的 SHA-256 不同。'):'当前元数据不足以判断文件内容是否相同。';
    $('resourceScope').textContent='此处比较已明确读取的文件元数据；不表示所有规则条目相同，也不会自动检查上游。';
    $('resourceCount').textContent='条目级新增与删除比较尚未提供。';
    $('resourceEntries').textContent='';$('resourceLinks').replaceChildren();$('resourcePagination').classList.add('hidden');$('resourceSearchRow').classList.add('hidden');
  }
  async function load(){
    const id=++request;busy=true;
    dialog.querySelectorAll('[data-resource-tab]').forEach(button=>{const active=button.dataset.resourceTab===source;button.classList.toggle('active',active);button.setAttribute('aria-pressed',String(active))});
    $('resourceStatus').classList.remove('bad');$('resourceStatus').textContent=source==='local'?'正在读取本地已发布内容…':source==='upstream'?'正在读取上游内容；不会发布到本地…':'正在比较规则内容…';
    $('resourceScope').textContent=source==='local'?'只读取本机已有文件，不触发上游检查或同步。':source==='upstream'?'上游预览独立于本地发布；读取成功不会改变已发布版本。':'按当前明确记录的本地与上游版本比较。';
    $('resourceMeta').replaceChildren();$('resourceLinks').replaceChildren();$('resourceEntries').textContent='';$('resourceCount').textContent='';
    $('resourcePrev').disabled=true;$('resourceNext').disabled=true;$('resourceReload').disabled=true;
    $('resourcePagination').classList.remove('hidden');$('resourceSearchRow').classList.remove('hidden');
    $('resourceCreateRow').classList.toggle('hidden',!config.create||source==='diff');
    if(source==='diff'&&!config.serverDiff){renderComparison();busy=false;$('resourceReload').disabled=false;return}
    try{
      const data=await config.load({source,q:$('resourceSearch').value.trim(),page:String(page),page_size:String(pageSize)});
      if(id!==request||!dialog.open)return;
      if(source!=='diff')snapshots[source]=data;
      const entries=Array.isArray(data.entries)?data.entries:[];
      const matching=Number(data.count??data.total??entries.length),total=Number(data.total_count??matching),actualPage=Number(data.page||page),limit=Number(data.page_size||pageSize),pages=Math.max(1,Math.ceil(matching/limit));
      const note=[data.note,data.local_error&&'本地原始文件不可用：'+data.local_error,data.last_error&&'本次检查失败，显示已有缓存：'+data.last_error].filter(Boolean).join('；')||(data.readable===false?'该资源仅提供元数据；不会把二进制文件或关联文件伪装成可读原文。':'');
      $('resourceStatus').textContent=note||'读取完成';
      $('resourceMeta').innerHTML=metaRow('来源',source==='local'?'本机已发布':source==='upstream'?'上游预览':'版本差异')+metaRow(source==='diff'?'本地版本':'版本',source==='diff'?data.local_revision||'—':data.revision||'—')+(data.geo_revision?metaRow('关联 geo 版本',data.geo_revision):'')+(source==='diff'?metaRow('上游版本',data.upstream_revision)+metaRow('原始文件校验',typeof data.same==='boolean'?(data.same?'SHA-256 相同':'SHA-256 不同'):'缺少可比文件'):metaRow(source==='local'?'本地原始文件':'上游文件状态',source==='local'&&data.mirrored===false?'未镜像（仅兼容缓存）':data.cached===false?(source==='local'?'本地缺失':'尚未获取'):'可用'))+metaRow('格式 / 行为',[data.format,data.behavior].filter(Boolean).join(' / ')||'—')+metaRow('文件大小',size(data.bytes))+metaRow('SHA-256',data.sha256||'—')+metaRow(source==='local'?'本地更新时间':'上游预览缓存时间',resourceDate(data.updated_at))+metaRow('最近检查',resourceDate(data.checked_at));
      if(data.content_kind==='associated_geo')$('resourceScope').textContent+=' 下方为关联 geo 可读条目，并非 MRS 二进制文件的反向解析；两者版本分别显示。';
      renderLinks(data);
      const start=matching?(actualPage-1)*limit+1:0,end=matching?Math.min(start+entries.length-1,matching):0;
      $('resourceCount').textContent=source==='diff'?`新增 ${data.comparable===false?'—':data.added_count??'—'} · 删除 ${data.comparable===false?'—':data.removed_count??'—'} · 当前 ${start}–${end} / ${matching} 条`:`匹配 ${matching.toLocaleString()} / 全部 ${total.toLocaleString()} 条 · 当前 ${start}–${end} 条${data.truncated?' · 本页仅展示部分条目，可继续翻页':''}`;
      $('resourceEntries').textContent=entries.join('\n')||(data.readable===false?'当前格式无可读条目。':source==='diff'?(data.comparable===false||data.readable===false||data.added_count===undefined?'没有足够的关联可读内容可比较，请查看元数据与说明。':'当前关联可读条目没有差异。'):'没有匹配条目。');
      $('resourcePage').textContent=`第 ${actualPage} / ${pages} 页 · 每页 ${limit} 条`;
      $('resourcePrev').disabled=actualPage<=1;$('resourceNext').disabled=actualPage>=pages;
      $('resourceSearchRow').classList.toggle('hidden',data.readable===false);$('resourcePagination').classList.toggle('hidden',data.readable===false);
    }catch(error){if(id!==request)return;$('resourceStatus').textContent=`${source==='local'?'本地读取':source==='upstream'?'上游预览':'差异比较'}失败：${error.message}`;$('resourceStatus').classList.add('bad');$('resourcePage').textContent='';}
    finally{if(id===request){busy=false;$('resourceReload').disabled=false}}
  }
  return{open(value){mount();config=value;snapshots={};source=value.source||'local';page=1;clearTimeout(timer);$('resourceSearch').value='';$('resourceTitle').textContent=value.name;$('resourceLibrary').textContent=value.library;opener=document.activeElement;if(!dialog.open)dialog.showModal();load()}};
})();
let ruleCatalog666=null;
function showRuleDetails(path,name,source='local'){
  window.CoralBayResourceDrawer.open({name,library:'666OS / YYDS',source,serverDiff:true,load:params=>json('/api/resources/666os/details?'+new URLSearchParams({path,...params}))});
}
function render666Status(data){
  const status=data.source_status||{},upstream=status.upstream||{};
  $('ruleSourceStatus').innerHTML=`<div><strong>本地已发布</strong><code>${escapeHTML(status.local_revision||data.local_revision||'尚未同步')}</code><small>${Number(status.local_count??data.rules?.filter(rule=>rule.cached).length??0)} / ${Number(status.total??data.count??0)} 项 · ${escapeHTML(resourceDate(status.local_updated_at))}</small></div><div><strong>上游检查</strong><code>${escapeHTML(upstream.revision||data.upstream_revision||'尚未检查')}</code><small>${escapeHTML(resourceDate(upstream.checked_at))}</small></div><div><strong>关联 geo 版本</strong><code>${escapeHTML(status.local_geo_commit||'—')}</code><small>可读条目与 release 文件分别追溯</small></div>`;
  const errors=[data.local_error&&'本地发布失败：'+data.local_error,upstream.last_error&&'最近上游检查失败：'+upstream.last_error].filter(Boolean);$('ruleSourceFeedback').textContent=errors.join('；');$('ruleSourceFeedback').classList.toggle('bad',errors.length>0);
}
async function loadRules(){
  if(!$('ruleRows'))return;
  try{
    const data=await json('/api/public/rules');ruleCatalog666=data;
    $('ruleCount').textContent=`${data.count||0} 项资源`;render666Status(data);
    $('ruleRows').innerHTML=(data.rules||[]).map(rule=>`<tr><td><div class="rule-name"><img src="${escapeHTML(resourceURL(rule.icon_url))}" alt=""><div><strong>${escapeHTML(rule.name)}</strong><br><small>${escapeHTML(rule.path)}</small></div></div></td><td>${escapeHTML(rule.behavior)}<br><small>${escapeHTML(rule.format)}</small></td><td class="${rule.cached?'ok':'warning'}">${rule.cached?'● 本地可用':'○ 待同步'}</td><td>${size(rule.bytes)}</td><td>${resourceLink(rule.local_url||rule.mirror_url,'本机文件')} · ${resourceLink(rule.original_url,'上游文件')}<br><button class="link-button" type="button" data-detail="${escapeHTML(rule.path)}" data-name="${escapeHTML(rule.name)}">本地 / 上游详情</button></td></tr>`).join('');
    $('ruleRows').querySelectorAll('[data-detail]').forEach(button=>button.onclick=()=>showRuleDetails(button.dataset.detail,button.dataset.name));
  }catch(error){$('ruleRows').innerHTML=`<tr><td colspan="5" class="bad">规则目录加载失败：${escapeHTML(error.message)}</td></tr>`;throw error;}
}
async function operate666(kind){
  const button=$(kind==='check'?'ruleCheckUpstream':'ruleSyncLocal');button.disabled=true;
  $('ruleSourceFeedback').classList.remove('bad');$('ruleSourceFeedback').textContent=kind==='check'?'正在检查上游版本；不会发布本地文件…':'正在启动 666OS 同步…';
  try{
    const result=await json(kind==='check'?'/api/resources/666os/check':'/api/admin/sync',{method:'POST'});
    if(kind==='check'){await loadRules();const errors=[ruleCatalog666?.local_error&&'本地发布失败：'+ruleCatalog666.local_error,result.last_error&&'上游检查失败：'+result.last_error].filter(Boolean);$('ruleSourceFeedback').textContent=errors.join('；')||'上游检查完成；本地发布版本保持不变。';$('ruleSourceFeedback').classList.toggle('bad',errors.length>0)}
    else{$('ruleSourceFeedback').textContent='666OS 同步已启动。完成后刷新状态查看已发布版本；MetaCubeX 不受此操作影响。'}
  }catch(error){$('ruleSourceFeedback').textContent=error.message;$('ruleSourceFeedback').classList.add('bad')}
  finally{button.disabled=false}
}

let templateItems = [];
let templatePreviewRequest = 0;
function selectTemplate() {
  const selected = templateItems.find(item => item.id === $('clientTemplate').value); if (!selected) return;
  const original = $('templateVariant').value === 'original';
  const sourceSelect=$('templateRuleSource'),available=original?[]:(selected.rule_source_options||[]),previous=sourceSelect.value;
  sourceSelect.innerHTML='<option value="default">现有模板地址（保持兼容）</option>'+available.map(item=>`<option value="${escapeHTML(item.id)}">${item.id==='local'?'本地规则镜像':'上游固定版本'}</option>`).join('');sourceSelect.value=available.some(item=>item.id===previous)?previous:'default';sourceSelect.disabled=!available.length;
  const ruleSource=available.find(item=>item.id===sourceSelect.value);
  const templateURL = ruleSource?.url || (original ? selected.original_url : selected.online_url);
  $('templateRuleSourceHint').textContent=ruleSource?`${ruleSource.description||''}${ruleSource.revision?' · 版本 '+ruleSource.revision:''}`:original?'Perfect Panel 原始版保持原样。':available.length?'可以明确选择模板内原始规则的下载来源；现有地址继续保持兼容。':['clash','mihomo','openclash','stash'].includes(selected.id)?'当前尚未发布此模板的来源变体，请先同步 666OS 规则。':selected.capability==='nodes-only'?'此输出仅包含节点，不需要规则下载来源。':'此模板没有等价的上游规则变体；使用的派生或转换资源仅由本机提供。';
  $('downloadClientTemplate').href = ruleSource?.url || (original ? selected.original_download_url : selected.download_url);
  $('downloadClientTemplate').textContent = original ? '下载原始版' : '下载改造版';
  $('openClientTemplate').href = templateURL;
  $('openClientTemplate').textContent = original ? '打开原始版' : '打开改造版';
  $('copyClientTemplate').dataset.url = templateURL;
  const statusMap = {adapted:['adapted','● 已改造'],converted:['converted','◉ 已转换规则源'],convertible:['convertible','◐ 可改造'], 'nodes-only':['limited','— 仅节点 / 不适用']};
  const state = statusMap[selected.capability] || ['base','○ 官方基础'];
  const status = `<span class="template-status ${state[0]}">${state[1]}</span>`;
  const variantStatus = original ? '<span class="template-status base">○ Perfect Panel 原始版</span>' : status;
  const sourceLabel=ruleSource?.id==='upstream'?'上游固定版本':ruleSource?.id==='local'?'本地规则镜像':selected.rule_sources;
  const variantDescription = original ? '未经 CoralBay 分流改造的 Perfect Panel 原始模板，用于对照、排错或恢复。' : ruleSource?`${selected.name} 模板：保留现有节点渲染、策略组和规则顺序；规则从${sourceLabel}下载。`:selected.description;
  $('clientTemplateHint').innerHTML = `${variantStatus}<span>${escapeHTML(variantDescription)}<br><small>${original?'节点：官方原始渲染 · 策略组与规则：保持原样':`节点：${selected.node_rendering?'可渲染':'不适用'} · 策略组：${escapeHTML(selected.policy_groups)} · 规则源：${escapeHTML(sourceLabel)} · ${escapeHTML(selected.validation)}`}</small></span>`;
  $('ppanelName').textContent = selected.ppanel_name || '';
  $('ppanelUA').textContent = selected.user_agent || '';
  $('ppanelFormat').textContent = selected.output_format || '';
  $('ppanelScheme').textContent = selected.url_scheme || '（留空）';
  $('ppanelTemplateURL').textContent = templateURL || '';
  const requestID = ++templatePreviewRequest;
  $('clientTemplatePreview').textContent = '正在读取模板…';
  const templatePath=new URL(templateURL,location.href).pathname;
  const previewURL=/^\/(?:_templates\/clients\/|_rule-templates\/666os\/)/.test(templatePath)?templatePath:templateURL;
  fetch(previewURL,{cache:'no-store'}).then(response=>{if(!response.ok)throw new Error(`HTTP ${response.status}`);return response.text()}).then(content=>{if(requestID===templatePreviewRequest)$('clientTemplatePreview').textContent=content}).catch(error=>{if(requestID===templatePreviewRequest)$('clientTemplatePreview').textContent=`模板预览失败：${error.message}`});
}
async function loadTemplates() {
  const previous=$('clientTemplate').value; const data = await json('/api/public/templates'); templateItems = data.templates || [];
  $('clientTemplate').innerHTML = templateItems.map(item => `<option value="${escapeHTML(item.id)}">${escapeHTML(item.name)}</option>`).join('');
  if(templateItems.some(item=>item.id===previous))$('clientTemplate').value=previous;selectTemplate();
}

let conversionItems = [];
function renderConversions() {
  if (!$('conversionRows')) return;
  const query = ($('conversionSearch').value || '').trim().toLowerCase(), kind = $('conversionKind').value;
  const items = conversionItems.filter(item => (!kind || item.kind === kind) && (!query || item.id.toLowerCase().includes(query) || item.source.toLowerCase().includes(query)));
  $('conversionRows').innerHTML = items.map(item => `<tr class="${item.entries===0?'empty-rule':''}"><td><strong>${escapeHTML(item.id.replace(/^(site|ip)-/,''))}</strong>${item.entries===0?'<br><small class="warning">无公开可读源 · 当前零覆盖</small>':''}</td><td>${item.kind==='site'?'域名':'IP/CIDR'}</td><td>${Number(item.entries).toLocaleString()}</td><td><code>${escapeHTML(item.source)}</code></td><td><a href="${escapeHTML(item.list_url)}" target="_blank" rel="noreferrer">RULE-SET</a> · <a href="${escapeHTML(item.singbox_url)}" target="_blank" rel="noreferrer">sing-box JSON</a> · <button class="link-button" data-copy-url="${escapeHTML(item.list_url)}">复制链接</button></td></tr>`).join('') || '<tr><td colspan="5" class="muted">没有匹配的转换产物</td></tr>';
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
function presetDependencies(preset){const dep=preset.rule_dependencies;if(!dep)return '<span class="template-dependency muted">尚无嵌套规则依赖统计；配置文件有本地镜像不代表其中所有规则均已本地化。</span>';const labels={local:'本机依赖',mixed:'混合来源',external:'外部依赖',unknown:'待确认',none:'无远程规则依赖'};return `<span class="template-dependency"><strong>${escapeHTML(labels[dep.status]||dep.status)}</strong> · 本机 ${Number(dep.local||0)} · 外部 ${Number(dep.external||0)} · 缺失或零覆盖 ${Number(dep.missing||0)} · 内嵌 ${Number(dep.inline||0)} · 未识别 ${Number(dep.unknown||0)}<br>${escapeHTML(dep.note||'仅分析已缓存配置，不下载嵌套规则。')}</span>`}
function applySubscriptionPreset(){const preset=subscriptionPresets.find(item=>item.id===$('subPreset').value);if(!preset)return;if(preset.built_in){$('subPresetSource').value='local';$('subPresetSource').disabled=true;$('subConfig').value=preset.local_url;$('subPresetDetail').innerHTML='<span class="template-status adapted">● 内置 MihomoPro</span><span>使用本机 666OS/YYDS Pro_cn 风格分组与 CoralBay 规则镜像，可转换其他来源的节点订阅。</span>'+presetDependencies(preset);return}$('subPresetSource').disabled=false;let local=$('subPresetSource').value==='local';let fallback='';if(local&&!preset.cached){$('subPresetSource').value='original';local=false;fallback='<span class="warning">本机尚无该配置，已自动回退原链接。</span><br>'}$('subConfig').value=preset.id==='none'?'':(local?preset.local_url:preset.original_url);const state=preset.id==='none'?'不使用远程配置':preset.cached?`<span class="ok">● 已缓存 · ${size(preset.bytes)}</span>`:'<span class="bad">○ 未缓存，仅可使用原链接</span>';$('subPresetDetail').innerHTML=`${fallback}${state} · 配置文件来源：${local?'CoralBay 本机镜像':'上游原链接'}${preset.updated_at?` · 更新于 ${new Date(preset.updated_at).toLocaleString('zh-CN',{hour12:false})}`:''}${preset.error?`<br><span class="bad">最近同步错误：${escapeHTML(preset.error)}</span>`:''}<br><span class="muted">此选项仅控制 INI 配置文件的读取位置；其中嵌套的规则 URL 可能仍指向外部来源。</span>${presetDependencies(preset)}`}
async function loadSubscriptionPresets(){const data=await json('/api/admin/subscription-presets');subscriptionPresets=data.presets||[];const groups=[];for(const item of subscriptionPresets){let group=groups.find(value=>value.name===item.group);if(!group){group={name:item.group,items:[]};groups.push(group)}group.items.push(item)}$('subPreset').innerHTML=groups.map(group=>`<optgroup label="${escapeHTML(group.name)}">${group.items.map(item=>`<option value="${escapeHTML(item.id)}">${item.built_in?'◆':item.cached?'●':'○'} ${escapeHTML(item.name)}</option>`).join('')}</optgroup>`).join('');$('subPresetCache').textContent=`${data.cached||0} / ${data.total||0} 已缓存 · MihomoPro 内置`;$('subPreset').onchange=applySubscriptionPreset;$('subPresetSource').onchange=applySubscriptionPreset;applySubscriptionPreset()}
async function loadSubscriptionCapabilities(){const data=await json('/api/admin/subscription-capabilities');const modern=(data.targets||[]).filter(item=>item.modern).length;$('subCapabilities').textContent=`${(data.targets||[]).length} 种输出 · ${modern} 种现代协议`;}
function historySettings(item){const s=item.settings;if(!s)return '<small class="muted">旧记录 · 可通过复用解析原链接</small>';const enabled=[['emoji','Emoji'],['sort','排序'],['dedup','去重'],['udp','UDP'],['xudp','XUDP'],['tfo','TFO'],['scv','跳过证书'],['tls13','TLS 1.3'],['append_type','附加协议'],['list','仅节点'],['insert','插入节点'],['expand','展开规则'],['new_name','新命名'],['fdn','过滤节点'],['clash_doh','Clash DoH'],['surge_doh','Surge DoH'],['singbox_ipv6','IPv6']].filter(([key])=>s[key]).map(([,label])=>label);const filters=[s.include&&`包含：${s.include}`,s.exclude&&`排除：${s.exclude}`,s.rename&&`重命名：${s.rename}`].filter(Boolean);const detail=[`源订阅 ${item.source_count||1} 条`,`更新 ${s.interval||24} 小时`,s.config?'远程配置':'无远程配置',...filters,...enabled].map(escapeHTML).join(' · ');return `<details class="history-settings"><summary>查看 ${3+filters.length+enabled.length} 项设置</summary><small>${detail}</small></details>`}
async function reuseSubscriptionHistory(url){const data=await json('/api/admin/subscription-parse',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({url})});applyParsedSubscription(data.params||{});$('subPreset').value='none';$('subPresetSource').disabled=false;$('subPresetDetail').textContent='已从历史记录恢复设置；远程配置 URL 保持记录中的值。';activateTab('subscription');$('subURLs').scrollIntoView({behavior:'smooth',block:'center'});$('message').textContent=`已复用历史设置：${data.source_count} 个源订阅`}
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

async function loadSubscriptionKeysStatus(){return json('/api/admin/subscription-keys').then(k=>{$('signingInfo').textContent='独立签名 v2 · 旧链接兼容至 '+new Date(k.legacy_until).toLocaleDateString('zh-CN')}).catch(()=>{$('signingInfo').textContent='签名状态读取失败'})}
async function refreshAll(){const results=await Promise.allSettled([loadAdmin(),loadRules(),loadTemplates(),loadConversions(),loadNative(),loadSubconverterStatus(),loadSubscriptionPresets(),loadSubscriptionCapabilities(),loadSubscriptionUsage(),loadSubscriptionKeysStatus()]);const failed=results.find(item=>item.status==='rejected');if(failed)connected(false,`部分数据加载失败：${failed.reason.message}`)}

if ($('sync')) {
	prepareTabs();
	document.querySelectorAll('[data-tab]').forEach(button => button.onclick=()=>activateTab(button.dataset.tab));
	window.addEventListener('hashchange',()=>activateTab(currentConsolePage(),{updateHash:false}));
  window.addEventListener('popstate',()=>activateTab(currentConsolePage(),{updateHash:false}));
	activateTab(currentConsolePage(),{updateHash:false});
  $('navToggle').onclick=()=>setNavigationOpen(!$('consoleNavigation').classList.contains('open'));$('navClose').onclick=$('navBackdrop').onclick=()=>setNavigationOpen(false);
  document.addEventListener('keydown',event=>{if(event.key==='Escape')setNavigationOpen(false);if(event.key==='Tab'&&$('consoleNavigation').classList.contains('open')){const buttons=[...$('consoleNavigation').querySelectorAll('button')].filter(button=>button.offsetParent!==null);const first=buttons[0],last=buttons[buttons.length-1];if(event.shiftKey&&document.activeElement===first){event.preventDefault();last.focus()}else if(!event.shiftKey&&document.activeElement===last){event.preventDefault();first.focus()}}});
  window.addEventListener('resize',()=>{if(window.innerWidth>1000)setNavigationOpen(false)});
  const chooseManagement=custom=>{$('manageLegacy').classList.toggle('active',!custom);$('manageRouting').classList.toggle('active',custom);$('manageLegacy').setAttribute('aria-pressed',String(!custom));$('manageRouting').setAttribute('aria-pressed',String(custom));$('legacyManagement').classList.toggle('hidden',custom);$('routingManagement').classList.toggle('hidden',!custom);if(custom)loadRoutingModule('management');else ensureLegacyData()};
  $('manageLegacy').onclick=()=>chooseManagement(false);$('manageRouting').onclick=()=>chooseManagement(true);
  new MutationObserver(()=>{if($('message').textContent&&!document.querySelector('[data-tab="overview"]').classList.contains('active'))consoleNotice($('message').textContent)}).observe($('message'),{childList:true,subtree:true,characterData:true});
	$('logout').onclick=async()=>{await fetch('/api/logout',{method:'POST'});location.replace('/')};
  $('sync').onclick=async()=>{try{await json('/api/admin/sync',{method:'POST',headers:actionHeaders()});$('message').textContent='同步已启动';setTimeout(()=>{loadAdmin();loadRules();loadTemplates()},1500)}catch(error){if(error.message.includes('令牌'))sessionStorage.removeItem('coralbayActionToken');$('message').textContent=error.message}};
  $('updateApp').onclick=async()=>{if(!confirm('确认拉取最新镜像并重启 CoralBay Rules？页面可能短暂断开。'))return;await json('/api/admin/update',{method:'POST',headers:actionHeaders()});$('message').textContent='更新器已启动，请约一分钟后刷新页面'};
  $('saveInterval').onclick=async()=>{await json('/api/admin/settings',{method:'PUT',headers:actionHeaders({'Content-Type':'application/json'}),body:JSON.stringify({interval_seconds:Number($('interval').value)})});$('message').textContent='同步频率已保存';loadAdmin()};
  $('refresh').onclick=()=>refreshAll(); $('ruleCard').onclick=()=>{activateTab('rules');requestAnimationFrame(()=>$('ruleSection').scrollIntoView({behavior:'smooth'}))};
  $('clientTemplate').onchange=selectTemplate; $('templateVariant').onchange=selectTemplate; $('templateRuleSource').onchange=selectTemplate; $('copyClientTemplate').onclick=async()=>{await navigator.clipboard.writeText($('copyClientTemplate').dataset.url);$('message').textContent='在线模板链接已复制'};
  document.querySelectorAll('[data-copy-field]').forEach(button=>button.onclick=async()=>{const value=$(button.dataset.copyField).textContent;if(value==='（留空）')return;$('message').textContent='字段已复制';await navigator.clipboard.writeText(value)});
  $('copyPPanelConfig').onclick=async()=>{const selected=templateItems.find(item=>item.id===$('clientTemplate').value);if(!selected)return;const templateURL=$('ppanelTemplateURL').textContent;const content=[`名称: ${selected.ppanel_name}`,`User-Agent: ${selected.user_agent}`,`输出格式: ${selected.output_format}`,`URL Scheme: ${selected.url_scheme||'留空'}`,`模板: ${templateURL}`].join('\n');await navigator.clipboard.writeText(content);$('message').textContent='PPanel 客户端设置已复制'};
  $('conversionSearch').oninput=renderConversions; $('conversionKind').onchange=renderConversions;
	$('nativeSearch').oninput=renderNative; $('nativePlatform').onchange=renderNative;
	$('subTarget').onchange=updateSubCompatibility; updateSubCompatibility();
	$('generateSub').onclick=async()=>{const feedback=document.querySelector('.converter-actions .field-hint');try{$('generateSub').disabled=true;$('generateSub').textContent='正在拉取并验证…';feedback.textContent='正在由本机后端获取并解析订阅…';feedback.classList.remove('bad','ok');$('subResult').classList.add('hidden');const data=await json('/api/admin/subscription-link',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(subscriptionPayload())});$('subResultURL').textContent=data.url;$('subValidation').textContent=`✓ 已验证 ${data.node_count} 个可解析节点`;$('openSubResult').href=data.url;$('downloadSubResult').href=data.url;$('subQR').src=`/api/admin/subscription-qr?url=${encodeURIComponent(data.url)}`;$('subResult').classList.remove('hidden');feedback.textContent=`转换验证通过：${data.node_count} 个可解析节点`;feedback.classList.add('ok');usagePage=1;await loadSubscriptionUsage()}catch(error){feedback.textContent=`转换失败：${error.message}`;feedback.classList.add('bad')}finally{$('generateSub').disabled=false;$('generateSub').textContent='测试并生成'}};
	$('copySubResult').onclick=async()=>{await navigator.clipboard.writeText($('subResultURL').textContent);$('message').textContent='订阅链接已复制'};
  $('parseSub').onclick=async()=>{try{const data=await json('/api/admin/subscription-parse',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({url:$('parseSubURL').value.trim()})});applyParsedSubscription(data.params||{});$('parseSubStatus').textContent=`已解析并回填 ${data.source_count} 个源订阅`;$('parseSubStatus').className='field-hint ok'}catch(error){$('parseSubStatus').textContent=`解析失败：${error.message}`;$('parseSubStatus').className='field-hint bad'}};
  $('syncSubPresets').onclick=async()=>{const button=$('syncSubPresets');try{button.disabled=true;button.textContent='正在缓存 88 条配置…';const data=await json('/api/admin/subscription-presets/sync',{method:'POST'});$('message').textContent=`远程配置同步完成：${data.cached}/${data.total} 可用，${data.failed} 条上游失败`;await loadSubscriptionPresets()}catch(error){$('message').textContent=`远程配置同步失败：${error.message}`}finally{button.disabled=false;button.textContent='立即更新本机镜像'}};
  $('ruleCheckUpstream').onclick=()=>operate666('check');$('ruleSyncLocal').onclick=()=>operate666('sync');$('ruleRefreshStatus').onclick=async event=>{event.target.disabled=true;try{await loadRules()}catch(error){consoleNotice(error.message)}finally{event.target.disabled=false}};
  installSubscriptionUsage();
  legacyUIReady=true;
  if(!['routing','sources'].includes(currentConsolePage()))ensureLegacyData();
}
publicStatus();

function installSubscriptionUsage(){
 const panel=document.createElement('section');panel.id='subscriptionManager';panel.className='inner-card section-space';
 panel.innerHTML='<div class="config-head"><div><h3>订阅管理</h3><p class="field-hint">生成设置、链接状态与拉取统计集中管理；相同转换参数合并为一条订阅。</p></div><button id="refreshLinkUsage" class="secondary">刷新</button></div><div class="subscription-manager-controls"><input id="usageSearch" aria-label="搜索订阅" placeholder="名称、编号、协议或客户端"><select id="usageState" aria-label="状态筛选"><option value="">全部订阅</option><option value="with_history">有生成记录</option><option value="without_history">无生成记录</option><option value="disabled">已停用</option><option value="archived">已删除（回收站）</option><option value="enabled">已启用</option><option value="never">尚未拉取</option><option value="recent">最近 48 小时有访问</option><option value="inactive">超过 48 小时未访问</option></select><select id="usageSort" aria-label="排序"><option value="activity">最近生成或拉取优先</option><option value="access">最近拉取优先</option></select><button id="usageFilter" class="secondary">筛选</button></div><div class="subscription-manager-tools"><span id="signingInfo" class="field-hint"></span><details class="manager-more"><summary>批量维护</summary><button id="clearSubHistory" class="link-button danger-link">清空生成记录</button><button id="usagePrune" class="link-button">清理过期统计</button></details></div><p class="field-hint">删除生成记录不会影响订阅；删除订阅会将其移入回收站并停止后续更新。次数包含浏览器下载，不代表在线人数。仅保留最近 100 条生成记录。</p><div id="linkUsageFeedback" class="field-hint" role="status" aria-live="polite"></div><div class="table-wrap"><table class="subscription-manager-table"><thead><tr><th>订阅</th><th>状态 / 拉取时间</th><th>成功 / 失败 / 拦截</th><th>生成设置与记录</th><th>操作</th></tr></thead><tbody id="linkUsageRows"><tr><td colspan="5">加载中</td></tr></tbody></table></div><div class="subscription-manager-pagination"><button id="usagePrev" class="secondary">上一页</button><button id="usageNext" class="secondary">下一页</button></div>';
 $('legacyManagement').appendChild(panel);
 $('usageFilter').onclick=()=>{usagePage=1;loadSubscriptionUsage()};
 $('usageSearch').onkeydown=e=>{if(e.key==='Enter'){$('usageFilter').click()}};
 $('usagePrev').onclick=()=>{usagePage--;loadSubscriptionUsage()};$('usageNext').onclick=()=>{usagePage++;loadSubscriptionUsage()};
 $('refreshLinkUsage').onclick=loadSubscriptionUsage;
 $('clearSubHistory').onclick=async()=>{if(!confirm('清空全部生成记录？订阅仍保留在此列表，链接状态、拉取统计均不改变。'))return;try{await json('/api/admin/subscription-history',{method:'DELETE'});await loadSubscriptionUsage();$('linkUsageFeedback').textContent='生成记录已清空，订阅链接和统计仍保留。'}catch(e){$('linkUsageFeedback').textContent=e.message}};
 $('usagePrune').onclick=async()=>{if(!confirm('重置 180 天未访问的已启用链接的累计次数和客户端信息？保留链接、最后访问时间与全部停用记录。'))return;try{const d=await json('/api/admin/subscription-usage/prune',{method:'POST'});await loadSubscriptionUsage();$('linkUsageFeedback').textContent='已重置 '+d.reset+' 条过期统计'}catch(e){$('linkUsageFeedback').textContent=e.message}};
}
async function loadSubscriptionUsage(){
 const feedback=$('linkUsageFeedback');
 try{
 const query=new URLSearchParams({page:String(usagePage),page_size:'20',q:$('usageSearch').value,state:$('usageState').value,sort:$('usageSort').value});
 const data=await json('/api/admin/subscription-usage?'+query);usagePage=data.page;$('usagePrev').disabled=data.page<=1;$('usageNext').disabled=data.page>=data.pages;
 const stamp=v=>v?new Date(v).toLocaleString('zh-CN',{hour12:false}):'—';
 $('linkUsageRows').innerHTML=(data.links||[]).map(item=>{
 const state=item.archived?'已删除':item.disabled?'已停用':!item.last?'尚未拉取':Date.now()-new Date(item.last).getTime()<48*3600000?'最近 48 小时有访问':'超过 48 小时未访问';
 const records=item.history||[],latest=records[0];
 const history=records.length?historySettings(latest)+'<details class="manager-records"><summary>'+records.length+' 条生成记录</summary>'+records.map(h=>`<article><small>${stamp(h.created_at)} · ${h.node_count} 个可解析节点</small>${historySettings(h)}<button class="link-button danger-link" data-history-delete="${escapeHTML(h.id)}">删除这次生成记录</button></article>`).join('')+'</details>':'<small class="muted">无生成记录或已清除；可从当前链接复用设置，统计与停用状态不受影响。</small>';
 const actions=item.archived?'<button class="link-button" data-link-restore="'+escapeHTML(item.id)+'">恢复订阅</button>':'<div class="history-actions"><button class="link-button" data-link-copy="'+escapeHTML(item.url)+'">复制</button><button class="link-button" data-history-reuse="'+escapeHTML(item.url)+'">复用设置</button><a href="'+escapeHTML(item.url)+'" target="_blank" rel="noreferrer">打开</a><button class="link-button '+(item.disabled?'':'danger-link')+'" data-link-state="'+escapeHTML(item.id)+'" data-disabled="'+(!item.disabled)+'">'+(item.disabled?'恢复':'停用')+'</button><button class="link-button danger-link" data-link-archive="'+escapeHTML(item.id)+'">删除订阅</button></div><details class="manager-more"><summary>更多操作</summary><button class="link-button" data-link-renew="'+escapeHTML(item.id)+'">更新签名</button><button class="link-button" data-link-probe="'+escapeHTML(item.url)+'" '+(!['stash','clash'].includes(item.target)||item.disabled?'disabled':'')+' title="仅支持已启用的 Stash / Clash 订阅">连通性抽测</button></details>';
 return `<tr><td><strong>${escapeHTML(latest?.filename||item.target)}</strong><br><small>${escapeHTML(item.target)} · ${escapeHTML(item.id.slice(0,10))}</small><br><small>最近生成：${latest?stamp(latest.created_at):'无保留记录'}</small></td><td><span class="soft-badge ${item.disabled?'bad':''}">${state}</span><br><small>首次：${stamp(item.first)}<br>最近：${stamp(item.last)}</small></td><td><strong>${item.success} / ${item.failure} / ${item.blocked}</strong><br><small>最近客户端：${escapeHTML(item.client||'—')}<br>请求标识，非设备身份</small></td><td>${history}</td><td>${actions}</td></tr>`;
 }).join('')||'<tr><td colspan="5">没有符合条件的订阅。</td></tr>';
 document.querySelectorAll('[data-link-copy]').forEach(b=>b.onclick=async()=>{try{await navigator.clipboard.writeText(b.dataset.linkCopy);feedback.textContent='链接已复制'}catch(e){feedback.textContent='复制失败：'+e.message}});
 document.querySelectorAll('[data-history-reuse]').forEach(b=>b.onclick=async()=>{try{await reuseSubscriptionHistory(b.dataset.historyReuse)}catch(e){feedback.textContent='复用失败：'+e.message+'；若旧签名过期，请先更新签名。'}});
 document.querySelectorAll('[data-history-delete]').forEach(b=>b.onclick=async()=>{if(!confirm('仅删除这次生成记录？订阅链接、拉取统计和停用状态均保留。'))return;try{b.disabled=true;await json('/api/admin/subscription-history/'+encodeURIComponent(b.dataset.historyDelete),{method:'DELETE'});await loadSubscriptionUsage();feedback.textContent='该次生成记录已删除，链接与统计仍保留。'}catch(e){feedback.textContent=e.message;b.disabled=false}});
 document.querySelectorAll('[data-link-archive]').forEach(b=>b.onclick=async()=>{if(!confirm('删除此订阅？它会从默认列表隐藏并停止后续更新，可在“已删除（回收站）”中恢复。'))return;try{b.disabled=true;await json('/api/admin/subscription-usage/'+encodeURIComponent(b.dataset.linkArchive),{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({archived:true})});await loadSubscriptionUsage();feedback.textContent='订阅已删除并停止更新，可从回收站恢复。'}catch(e){feedback.textContent=e.message;b.disabled=false}});
 document.querySelectorAll('[data-link-restore]').forEach(b=>b.onclick=async()=>{if(!confirm('恢复此订阅并允许继续更新？'))return;try{b.disabled=true;await json('/api/admin/subscription-usage/'+encodeURIComponent(b.dataset.linkRestore),{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({archived:false})});$('usageState').value='';await loadSubscriptionUsage();feedback.textContent='订阅已恢复。'}catch(e){feedback.textContent=e.message;b.disabled=false}});
 document.querySelectorAll('[data-link-renew]').forEach(b=>b.onclick=async()=>{try{b.disabled=true;await json('/api/admin/subscription-usage/'+encodeURIComponent(b.dataset.linkRenew)+'/renew',{method:'POST'});await loadSubscriptionUsage();feedback.textContent='签名已更新，请复制新链接替换客户端订阅；统计和停用状态不变。'}catch(e){feedback.textContent=e.message;b.disabled=false}});
 document.querySelectorAll('[data-link-probe]').forEach(b=>b.onclick=async()=>{if(!confirm('由服务器经 Mihomo 抽测前 3 个节点至 Google HTTPS 204，会产生少量节点流量。结果不是手机端连通性保证。继续？'))return;try{b.disabled=true;feedback.textContent='连通性抽测中，通常需要 10–90 秒…';const d=await json('/api/admin/subscription-probe',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({url:b.dataset.linkProbe})});feedback.textContent=d.scope+'；'+d.results.map(x=>x.name+'：'+(x.ok?x.delay_ms+' ms':x.error)).join('；')}catch(e){feedback.textContent=e.message}finally{b.disabled=false}});
 document.querySelectorAll('[data-link-state]').forEach(b=>b.onclick=async()=>{
 const disabled=b.dataset.disabled==='true';if(!confirm(disabled?'停用此订阅？后续更新将被拒绝，已下载配置仍可使用；相同参数的链接同时生效。':'恢复此订阅的更新？'))return;
 try{b.disabled=true;await json('/api/admin/subscription-usage/'+encodeURIComponent(b.dataset.linkState),{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({disabled})});await loadSubscriptionUsage()}catch(e){feedback.textContent=e.message;b.disabled=false}
 });
 feedback.textContent=`${data.count} 条订阅 · 第 ${data.page}/${data.pages} 页 · ${new Date().toLocaleTimeString('zh-CN')} 更新（非在线监测）`;
 }catch(e){feedback.textContent='读取订阅管理失败：'+e.message}
}
