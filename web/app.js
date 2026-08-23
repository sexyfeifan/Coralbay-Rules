const $ = id => document.getElementById(id);
async function json(url, opt = {}) { const response = await fetch(url, {...opt, cache:'no-store'}); const data = await response.json().catch(() => ({})); if (!response.ok) throw new Error(data.error || `HTTP ${response.status}`); return data; }
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

async function publicStatus() {
  if (!$('state')) return;
  try { const data=await json('/api/public/status'), status=data.status||{}; setState('state',data.syncing?'同步中':'正常',true); $('commit').textContent=short(status.commit); $('files').textContent=status.validated_files??'—'; $('synced').textContent=status.synced_at||'—'; }
  catch { setState('state','异常',false); }
}

let detailItems = [];
async function showRuleDetails(path, name) {
  try {
    const data = await json(`/api/public/rule-details?path=${encodeURIComponent(path)}`);
    detailItems = data.entries || []; $('detailTitle').textContent = `${name} 规则详情`;
    $('detailMeta').textContent = `${data.count} 条 · ${data.source_path}`; $('detailSearch').value = '';
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
  const status = selected.enhanced ? '<span class="template-status adapted">● 已改造</span>' : '<span class="template-status base">○ 官方基础</span>';
  $('clientTemplateHint').innerHTML = `${status}<span>${escapeHTML(selected.description)}</span>`;
}
async function loadTemplates() {
  const data = await json('/api/public/templates'); templateItems = data.templates || [];
  $('clientTemplate').innerHTML = templateItems.map(item => `<option value="${escapeHTML(item.id)}">${item.enhanced?'🟢 已改造 · ':'⚪ 官方基础 · '}${escapeHTML(item.name)}</option>`).join('');
  selectTemplate();
}

async function loadAdmin() {
  try {
    const data=await json('/api/admin/status'), status=data.status||{}, certificate=data.certificate||{}, icons=data.icons||{};
    setState('adminState',data.syncing?'同步中':'正常',!data.last_error); $('adminCommit').textContent=`提交 ${short(status.commit)}`;
    $('runningVersion').textContent=`v${data.version}`; $('latestVersion').textContent=data.latest_version?`最新 ${data.latest_version}${data.update_available?' · 可升级':' · 已是最新'}`:'暂时无法检查最新版';
    setState('certificate',certificate.ok?`${certificate.days_remaining} 天`:'异常',certificate.ok&&certificate.days_remaining>14); $('certificateIssuer').textContent=certificate.ok?`${certificate.issuer} · ${new Date(certificate.not_after).toLocaleDateString()}`:(certificate.error||'无法读取');
    setState('iconCache',`${icons.cached||0} / ${icons.expected||27}`,icons.ok); $('iconBytes').textContent=size(icons.bytes); $('adminFiles').textContent=`${status.validated_files||0} / 33`;
    $('interval').value=String(data.interval_seconds); $('intervalHint').textContent=`当前每 ${Math.round(data.interval_seconds/3600)} 小时`;
    $('updateApp').disabled=!data.update_available; $('updateApp').textContent=data.update_available?`升级到 ${data.latest_version}`:'已是最新版本';
    const logs=await json('/api/admin/logs'); $('logs').textContent=(logs.logs||[]).join('\n')||'暂无本次运行日志';
    const releases=await json('/api/admin/releases'); $('releases').innerHTML=(releases.releases||[]).map(item=>`<div class="release"><code>${short(item.commit)} ${item.active?'（当前）':''}</code>${item.active?'':`<button data-rollback="${item.commit}" class="secondary">回滚</button>`}</div>`).join('')||'暂无历史版本';
    document.querySelectorAll('[data-rollback]').forEach(button=>button.onclick=()=>rollback(button.dataset.rollback));
    connected(true);
  } catch(error) { setState('adminState','连接异常',false); $('message').textContent=`状态读取失败：${error.message}`; connected(false, `API 请求失败：${error.message}`); }
}
async function rollback(commit) { if(!confirm(`确认回滚到 ${short(commit)}？`)) return; await json('/api/admin/rollback',{method:'POST',headers:{'Content-Type':'application/json','X-CoralBay-Action':'console'},body:JSON.stringify({commit})}); $('message').textContent='回滚完成'; loadAdmin(); }

if ($('sync')) {
  $('sync').onclick=async()=>{try{await json('/api/admin/sync',{method:'POST',headers:{'X-CoralBay-Action':'console'}});$('message').textContent='同步已启动';setTimeout(()=>{loadAdmin();loadRules();loadTemplates()},1500)}catch(error){$('message').textContent=error.message}};
  $('updateApp').onclick=async()=>{if(!confirm('确认拉取最新镜像并重启 CoralBay Rules？页面可能短暂断开。'))return;await json('/api/admin/update',{method:'POST',headers:{'X-CoralBay-Action':'console'}});$('message').textContent='更新器已启动，请约一分钟后刷新页面'};
  $('saveInterval').onclick=async()=>{await json('/api/admin/settings',{method:'PUT',headers:{'Content-Type':'application/json','X-CoralBay-Action':'console'},body:JSON.stringify({interval_seconds:Number($('interval').value)})});$('message').textContent='同步频率已保存';loadAdmin()};
  $('refresh').onclick=()=>refreshAll(); $('ruleCard').onclick=()=>$('ruleSection').scrollIntoView({behavior:'smooth'});
  $('clientTemplate').onchange=selectTemplate; $('copyClientTemplate').onclick=async()=>{await navigator.clipboard.writeText($('copyClientTemplate').dataset.url);$('message').textContent='在线模板链接已复制'};
  $('closeDetails').onclick=()=>$('detailPanel').classList.add('hidden'); $('detailSearch').oninput=()=>{const query=$('detailSearch').value.toLowerCase();$('detailEntries').textContent=detailItems.filter(item=>item.toLowerCase().includes(query)).join('\n')};
  async function refreshAll(){const results=await Promise.allSettled([loadAdmin(),loadRules(),loadTemplates()]);const failed=results.find(item=>item.status==='rejected');if(failed)connected(false,`部分数据加载失败：${failed.reason.message}`)}
  refreshAll();
}
publicStatus();
