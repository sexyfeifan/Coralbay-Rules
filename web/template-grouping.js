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
    const state = {scope,host,context:{},loaded:false,loading:false,busy:false,dirty:false,inherit:true,profile:null,catalog:{},defaults:null,activeTab:'categories',ticket:0,artifacts:[],artifactIndex:0,preview:null,names:'',feedback:'',history:null,historyPage:1,historyQuery:'',historyArchived:false,historyBusy:false,historyDetail:null,baseline:null,baselineClient:'',baselineBusy:false};
    const find = part => host.querySelector(`[data-grouping="${part}"]`);
    function notice(text, bad = false) { state.feedback = text; const box = find('feedback'); if (box) { box.textContent = text; box.classList.toggle('bad', bad); } }
    function invalidate() { state.ticket++; state.artifacts = []; state.generated = null; state.preview = null; state.dirty = true; renderArtifacts(); renderStatus(); notice('设置有修改，请保存后重新生成。'); }
    function defaultsOptions() {
      const regions = state.catalog.regions || fallbackRegions, macros = [...(state.catalog.macros || []).filter(item=>item.code!=='other'),{code:'other',name:'其他未识别'}];
      const buckets = [...(state.profile.regions || []).map(code => regions.find(item => item.code === code)).filter(Boolean),...macros.filter(item => item.code==='other' || state.profile.macros == null || state.profile.macros.includes(item.code))];
      return ['全球自动','全球手动','故障转移','DIRECT','REJECT','REJECT-DROP',...buckets.flatMap(item => ['自动','均衡','手动'].filter(suffix => suffix==='手动' || modes(item.code)[suffix==='自动'?'auto':'balance']).map(suffix => item.name+suffix))];
    }
    function modes(code) { return state.profile.modes?.[code] || {auto:true,balance:true}; }
    function policyChoices(selected, category = false) {
      const options = [...(category ? [{value:'auto',label:'沿用分类推荐'},'默认出口'] : []),...defaultsOptions()];
      if (selected && !options.some(item => (typeof item === 'string' ? item : item.value) === selected)) options.push({value:selected,label:selected+'（分组已关闭，请重选）'});
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
      state.profile.macros = Array.from(host.querySelectorAll('[data-grouping-macro]:checked')).map(element => element.dataset.groupingMacro);
      state.profile.modes = state.profile.modes || {};
      host.querySelectorAll('[data-grouping-mode]').forEach(element => { const code=element.dataset.groupingMode; state.profile.modes[code]={...modes(code),[element.dataset.mode]:element.checked}; });
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
      const options=state.profile?defaultsOptions():[], invalid=state.profile && (!options.includes(state.profile.default) || state.profile.categories.some(item=>item.default && item.default!=='auto' && item.default!=='默认出口' && !options.includes(item.default)));
      const reason = contextReason() || (invalid ? '有默认出口指向已关闭分组，请在“分类与默认”重新选择后保存。' : ''), tag = find('state');
      if (tag) tag.textContent = state.dirty ? '有未保存的修改' : state.inherit ? '使用共用方案' : '当前页独立方案';
      if (find('boundary')) { find('boundary').textContent = reason || '此分组方案面向现代 Mihomo 内核。编辑并保存后生成新文件；下方基础模板不会自动变化。'; find('boundary').classList.toggle('bad', !!reason); }
      if (find('generate')) find('generate').disabled = state.busy || !state.loaded || !!reason || state.dirty;
      host.querySelectorAll('[data-grouping-write]').forEach(button => button.disabled = state.busy || !state.loaded);
      host.querySelectorAll('input,select,textarea').forEach(element => element.disabled = state.busy);
      host.querySelectorAll('[data-grouping-mode]').forEach(element => { const code=element.dataset.groupingMode; element.disabled=state.busy || code!=='other' && !state.profile.regions.includes(code) && !(state.profile.macros==null || state.profile.macros.includes(code)); });
      host.querySelectorAll('[data-category-move], [data-region-move]').forEach(button => { const index = Number(button.dataset.categoryMove ?? button.dataset.regionMove), count = button.dataset.categoryMove != null ? state.profile.categories.length : state.profile.regions.length; button.disabled = state.busy || index + Number(button.dataset.direction) < 0 || index + Number(button.dataset.direction) >= count; });
    }
    function renderCategories() {
      const p = state.profile;
      return `<div class="grouping-grid"><div>${field('方案名称', input('name', p.name || '', 'maxlength="80"'))}${field('全局默认出口', `<select data-grouping-field="default">${policyChoices(p.default)}</select>`, '分类可分别覆盖默认出口；客户端已保存的手选结果可能继续保留。')}</div><div class="grouping-note"><strong>每个分类都能自由选节点</strong><p>默认出口、全球自动、全球手动，以及各地区的自动、均衡、手动共同组成候选。</p>${check('show_nodes','分类中直接展开全部节点',p.show_nodes)}<small>关闭后仍可进入“全球手动”或“地区手动”选择单个节点。</small></div></div><div class="grouping-category-list">${(p.categories || []).map((category,index) => `<div class="grouping-category"><label><input type="checkbox" data-grouping-category="${index}" data-property="enabled"${category.enabled ? ' checked' : ''}><span>${esc(category.name)}</span></label><select aria-label="${esc(category.name)}默认出口" data-grouping-category="${index}" data-property="default">${policyChoices(category.default || 'auto',true)}</select><div class="grouping-order"><button type="button" class="ghost" data-category-move="${index}" data-direction="-1" aria-label="上移${esc(category.name)}"${index === 0 ? ' disabled' : ''}>↑</button><button type="button" class="ghost" data-category-move="${index}" data-direction="1" aria-label="下移${esc(category.name)}"${index === p.categories.length - 1 ? ' disabled' : ''}>↓</button></div></div>`).join('')}</div><p class="field-hint">箭头调整分类展示顺序。关闭分类后保留规则，流量跟随默认出口；广告、国内与苹果服务保留各自的阻断或直连默认。</p>`;
    }
    function renderRegions() {
      const regions = state.catalog.regions || fallbackRegions, macros = state.catalog.macros || [];
      const row = (item, enabled = true, macro = false) => `<div class="grouping-bucket${enabled?'':' inactive'}"><div><strong>${macro && item.code!=='other' ? `<label><input type="checkbox" data-grouping-macro="${esc(item.code)}"${enabled?' checked':''}> ${esc(item.name)}</label>`:esc(item.name)}</strong><small>${item.code==='other'?'固定兜底 · 不可删除':macro?'只接收未分到独立地区的节点':'独立地区优先'} · ${state.preview?.regions?.find(value=>value.code===item.code)?.count ?? '未检测'}${state.preview?.regions?.some(value=>value.code===item.code)?' 个节点':''}</small></div><div class="grouping-bucket-modes">${['auto','balance'].map(key=>`<label><input type="checkbox" data-grouping-mode="${esc(item.code)}" data-mode="${key}"${modes(item.code)[key]?' checked':''}${!enabled?' disabled':''}> ${key==='auto'?'自动':'均衡'}</label>`).join('')}<span>✓ 手动</span></div></div>`;
      return `<p class="grouping-copy">独立地区 → 启用大区 → 其他未识别。取消分组不删除节点；关闭自动或均衡是不生成该模式，而不是隐藏。</p><h4>已选择的独立地区</h4><div class="grouping-region-order">${(state.profile.regions || []).map((code,index) => `<div><span>${esc(regions.find(item => item.code === code)?.name || code)}</span><button type="button" class="ghost" data-region-move="${index}" data-direction="-1" aria-label="前移${esc(code)}"${index === 0 ? ' disabled' : ''}>←</button><button type="button" class="ghost" data-region-move="${index}" data-direction="1" aria-label="后移${esc(code)}"${index === state.profile.regions.length - 1 ? ' disabled' : ''}>→</button><button type="button" class="ghost" data-region-remove="${esc(code)}" aria-label="移除${esc(regions.find(item=>item.code===code)?.name || code)}">×</button></div>`).join('') || '<p class="field-hint">未单列地区，节点将进入启用大区或兜底。</p>'}</div><div class="grouping-bucket-list">${(state.profile.regions || []).map(code => row(regions.find(item=>item.code===code) || {code,name:code})).join('')}</div><p class="field-hint">取消独立地区后，其节点回到所属大区；所属大区也关闭时进入兜底。顺序决定同层多重匹配优先级。</p><details class="grouping-note"><summary>添加或搜索独立地区</summary><label class="grouping-field"><span>搜索独立地区</span><input data-grouping="region-search" placeholder="国家、地区或代码"></label><div class="grouping-regions">${regions.map(region => `<label class="grouping-check" data-region-label="${esc((region.name+' '+region.code).toLowerCase())}"><input type="checkbox" data-grouping-region="${esc(region.code)}"${state.profile.regions?.includes(region.code) ? ' checked' : ''}><span>${esc(region.flag || '')} ${esc(region.name)}<small>${esc(region.code)}</small></span></label>`).join('')}</div></details><h4>剩余节点的大区</h4><div class="grouping-bucket-list">${macros.filter(item=>item.code!=='other').map(item=>row(item,state.profile.macros==null || state.profile.macros.includes(item.code),true)).join('')}</div><h4>最后兜底</h4><div class="grouping-bucket-list">${row({code:'other',name:'其他未识别'})}</div><p class="field-hint">兜底还会收纳“所属大区未启用”的节点，预览分别标注原因。手动始终保留；全球手动和业务组完整节点列表不受地区开关影响。</p>${field('归属修正（每行一条）', `<textarea data-grouping="overrides" rows="4" spellcheck="false" placeholder="德国|Germany => europe">${esc((state.profile.overrides || []).map(item => item.pattern+' => '+item.region).join('\n'))}</textarea>`, '目标必须是已启用的独立地区或大区，不支持 other。人工修正在同层提升优先级，大区不会抢走独立地区节点。取消目标后请移除或修改相关修正规则。')}`;
    }
    function renderAutomatic() {
      const p = state.profile;
      return `<div class="grouping-mode-batch">${[['auto',true,'全部开启地区自动'],['auto',false,'全部关闭地区自动'],['balance',true,'全部开启地区均衡'],['balance',false,'全部关闭地区均衡']].map(([mode,on,label])=>`<button type="button" class="secondary" data-mode-batch="${mode}" data-on="${on}">${label}</button>`).join('')}</div><p class="field-hint">批量操作针对当前启用的独立地区、大区和兜底；单个地区开关在“地区”页。全球自动、全球手动和故障转移不受影响。</p><div class="grouping-grid">${field('测速地址',input('test_url',p.test_url,'type="url" placeholder="https://www.gstatic.com/generate_204"'))}${field('测速间隔（秒）',input('interval',p.interval,'type="number" min="60" step="1"'))}${field('切换容差（毫秒）',input('tolerance',p.tolerance,'type="number" min="0" step="1"'))}${field('均衡方式',`<select data-grouping-field="strategy">${choices([{value:'consistent-hashing',label:'相同网站优先使用同一节点（推荐）'},{value:'round-robin',label:'轮流分配新连接'},{value:'sticky-sessions',label:'同一来源与网站保持会话'}],p.strategy)}</select>`)}</div><div class="grouping-note"><strong>均衡是多节点分配连接</strong><p>不会把多个节点叠加成一条更快的连接。对登录、支付等业务，优先使用默认的一致性哈希或手动固定节点。</p></div><div class="grouping-check-row">${check('hide_auto','折叠辅助自动组',p.hide_auto)}${check('icons','使用本机缓存图标',p.icons)}</div>`;
    }
    function renderAdvanced() {
      const p = state.profile, inherit = [{value:'inherit',label:'沿用当前模板'},{value:'on',label:'开启'},{value:'off',label:'关闭'}];
      return `<h4>媒体独立分流</h4><p class="grouping-copy">启用后增加独立分类，优先于国际媒体、谷歌服务等综合规则匹配。</p><div class="grouping-check-row">${(state.catalog.media || ['YouTube','Netflix','Disney','Spotify']).map(name => `<label class="grouping-check"><input type="checkbox" data-grouping-media="${esc(name)}"${p.media?.includes(name) ? ' checked' : ''}><span>${esc(name)}</span></label>`).join('')}</div><div class="grouping-grid">${field('DNS 模式',`<select data-grouping-field="dns_mode">${choices([{value:'inherit',label:'沿用当前模板'},{value:'fake-ip',label:'Fake-IP'},{value:'redir-host',label:'Redir-Host'}],p.dns_mode || 'inherit')}</select>`)}${field('IPv6',`<select data-grouping-field="ipv6">${choices(inherit,p.ipv6 || 'inherit')}</select>`)}${field('流量嗅探',`<select data-grouping-field="sniffer">${choices(inherit,p.sniffer || 'inherit')}</select>`)}</div><div data-grouping="advanced-help"></div><p class="field-hint">以上仅影响生成的客户端配置，不改变 Rules 服务器或手机系统网络设置，也不改节点的认证、TLS 或传输参数。</p>`;
    }
    function renderAdvancedHelp() {
      const target=find('advanced-help'); if(!target || !state.profile)return;
      const p=state.profile, base=state.baseline, dns=base?.dns || {}, sniffer=base?.sniffer || {};
      const value=(field,fallback='未声明（遵循内核默认）')=>!base?'尚未确认（读取基础模板后显示）':field==null?fallback:typeof field==='boolean'?(field?'开启':'关闭'):String(field);
      const dnsText={inherit:'不覆盖基础模板的 DNS 模式和服务器列表；沿用并不等于自动选择最佳模式。', 'fake-ip':'给域名分配虚拟 IP，连接时依据映射识别域名。适用于域名分流；依赖真实 DNS 地址的应用可能需要 fake-ip-filter 例外。虚拟 IP 不是节点出口地址。', 'redir-host':'返回实际解析地址，不使用虚拟 IP。兼容依赖真实解析地址的应用，但域名分流还依赖 DNS 映射和可用的域名信息，不保证解决解析污染。'};
      const ipv6Text={inherit:'不覆盖模板的顶层 ipv6 和 dns.ipv6；二者实际值分别显示在下方。',on:'允许内核处理 IPv6，并允许 DNS 返回 IPv6（AAAA）结果。仍需本地网络、节点和客户端接管方式支持；开启不保证 IPv6 连通。',off:'关闭模板中的 ipv6 和 dns.ipv6，DNS 对 AAAA 查询返回空结果。不是关闭路由器或手机系统的 IPv6；客户端之外的流量不由此设置控制。'};
      const sniffText={inherit:'保留基础模板的开关、协议、端口及排除域名；不会自动扩展嗅探范围。',on:'开启 HTTP/TLS/QUIC 域名嗅探，尝试从支持的协议元信息中取得域名，辅助域名规则匹配；不是 HTTPS 解密。不能保证每条流量都能识别，特殊应用可通过跳过域名处理。',off:'停止通过流量嗅探补充域名；仍保留正常规则分流，但只有目标 IP 且没有 DNS 映射的流量可能改为匹配 IP 或兜底规则。'};
      const effective={}; if(p.dns_mode!=='inherit')effective['dns.enhanced-mode']=p.dns_mode;
      if(p.ipv6!=='inherit'){effective.ipv6=p.ipv6==='on';effective['dns.ipv6']=p.ipv6==='on';}
      if(p.sniffer!=='inherit')effective['sniffer.enable']=p.sniffer==='on';
      target.innerHTML=`<div class="grouping-help-grid"><article class="grouping-note"><strong>DNS · ${esc(p.dns_mode)}</strong><p>${esc(dnsText[p.dns_mode])}</p><small>基础模式：${esc(value(dns['enhanced-mode']))}；DNS 服务：${esc(value(dns.enable))}</small>${base && dns.enable===false?'<p class="warning">基础 DNS 服务关闭；只改模式不会自动开启 DNS，需在客户端完整配置中启用后才生效。</p>':''}<details><summary>基础 DNS 字段</summary><pre>${esc(JSON.stringify(dns,null,2))}</pre></details><a href="https://wiki.metacubex.one/config/dns/" target="_blank" rel="noopener noreferrer">Mihomo DNS 说明 ↗</a></article><article class="grouping-note"><strong>IPv6 · ${esc(p.ipv6)}</strong><p>${esc(ipv6Text[p.ipv6])}</p><small>基础 ipv6：${esc(value(base?.ipv6))}；dns.ipv6：${esc(value(dns.ipv6))}</small></article><article class="grouping-note"><strong>流量嗅探 · ${esc(p.sniffer)}</strong><p>${esc(sniffText[p.sniffer])}</p><small>基础开关：${esc(value(sniffer.enable))}；仅支持 HTTP/TLS/QUIC。端口和跳过域名按下方字段保留。</small><details><summary>协议、端口与目标覆盖字段</summary><pre>${esc(JSON.stringify(sniffer,null,2))}</pre></details><a href="https://wiki.metacubex.one/config/sniff/" target="_blank" rel="noopener noreferrer">Mihomo 嗅探说明 ↗</a></article></div><div class="grouping-note"><strong>本次明确覆盖的字段</strong><pre>${esc(Object.keys(effective).length?JSON.stringify(effective,null,2):'没有覆盖，沿用基础模板。')}</pre><small>${base?'基础版本：'+esc(base.revision):state.baselineError?esc(state.baselineError):'打开本标签后读取基础模板实际值…'} · 显式开启嗅探且模板没有协议定义时，会补入 HTTP 80/8080–8880、TLS/QUIC 443/8443。</small></div>`;
    }
    async function loadBaseline() {
      const key=scope+':'+state.context.client;
      if(state.baselineBusy || state.baselineClient===key || !supported(scope,state.context.client))return;
      state.baselineBusy=true;
      try { const data=await request('advanced?'+new URLSearchParams({scope,client:state.context.client})); if(key!==scope+':'+state.context.client)return; state.baseline=data;state.baselineClient=key;state.baselineError=''; }
      catch(error){if(key===scope+':'+state.context.client)state.baselineError='基础字段读取失败：'+error.message;}
      finally{state.baselineBusy=false;renderAdvancedHelp();if(key!==scope+':'+state.context.client && state.activeTab==='advanced')loadBaseline();}
    }
    function renderPreview() {
      return `<p class="grouping-copy">粘贴节点名称即可检查分区。这里只需要名称，不需要订阅链接或节点密码。</p><label class="grouping-field"><span>节点名称（每行一个，可选）</span><textarea data-grouping="names" rows="5" placeholder="香港 01&#10;德国 01&#10;越南 01">${esc(state.names)}</textarea></label><button data-grouping="preview" data-grouping-write type="button" class="secondary">检查分组</button><div data-grouping="diagnostics"></div>`;
    }
    function renderDiagnostics() {
      const result = state.preview, target = find('diagnostics');
      if (!target) return;
      if (!result) { target.innerHTML = '<p class="field-hint">预览用于核对名称归属与分组；不代表节点已连通。</p>'; return; }
      target.innerHTML = `<div class="grouping-summary"><span><b>${Number(result.total || 0)}</b>个名称</span><span><b>${Number(result.covered || 0)}</b>已归组</span><span><b>${Number(result.unknown || 0)}</b>进入兜底</span><span><b>${Number(result.ambiguous || 0)}</b>多重匹配</span></div>${(result.warnings || []).map(item => `<p class="field-hint warning">${esc(item)}</p>`).join('')}<div class="grouping-region-results">${(result.regions || []).map(region => `<details><summary>${esc(region.name)}<span>${Number(region.count || 0)} 个节点</span></summary><pre>${esc((region.nodes || []).join('\n') || '当前没有匹配节点')}</pre></details>`).join('')}</div>${(result.nodes || []).some(node=>node.region==='other')?`<details class="grouping-note"><summary>兜底节点与归属原因</summary>${result.nodes.filter(node=>node.region==='other').map(node=>`<p>${esc(node.name)}<small>${esc(node.reason || '名称未识别')}</small></p>`).join('')}</details>`:''}${(result.nodes || []).some(node => node.ambiguous) ? `<details class="grouping-note"><summary>需要留意的多重匹配</summary>${result.nodes.filter(node => node.ambiguous).map(node => `<p>${esc(node.name)} → ${esc(node.region_name || node.region)}<small>匹配：${esc((node.matches || []).join(' / '))}</small></p>`).join('')}</details>` : ''}<details class="grouping-note"><summary>分组与候选顺序</summary>${(result.groups || []).map(group => `<p><strong>${esc(group.name)}</strong><small>${esc((group.proxies || []).join(' → '))}</small></p>`).join('')}</details>`;
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
    const dateLabel = raw => { const value=new Date(raw); return raw && !Number.isNaN(value.getTime()) ? value.toLocaleString('zh-CN',{hour12:false}) : '—'; };
    function renderHistory() {
      const target=find('history'); if(!target)return;
      const data=state.history, item=state.historyDetail;
      target.innerHTML=`<details data-grouping="history-panel"${state.historyOpen?' open':''}><summary>历史生成文件 <span>${data?Number(data.total)+' 个版本':'查看全部版本'}</span></summary><p class="field-hint">相同设置与来源版本重复生成保留一个版本，记录首次与最近生成日期；覆写与配套配置作为一个版本管理。删除会移入回收站，源站不再提供该版本的新下载，但不会删除规则或客户端本地文件，已有缓存可能继续可用。</p><div class="grouping-history-tools"><label class="grouping-field"><span>搜索方案、客户端或版本</span><input data-grouping="history-query" value="${esc(state.historyQuery)}" maxlength="256"></label><button type="button" class="secondary" data-grouping="history-search">搜索 / 刷新</button><button type="button" class="secondary" data-grouping="history-archive">${state.historyArchived?'返回历史文件':'回收站'}</button></div><p class="field-hint" data-grouping="history-feedback" role="status">${esc(state.historyError || (state.historyBusy?'正在读取…':state.historyArchived?'正在查看回收站':'历史文件仅管理员可管理；文件链接可供客户端读取。'))}</p><div class="grouping-history-list">${(data?.items || []).map(entry=>`<article><div><strong>${esc(entry.profile?.name || '分组方案')}</strong><span class="soft-badge">${esc(entry.client)} · ${entry.source==='local'?'本机镜像':'上游源'}</span><small>首次：${esc(dateLabel(entry.created_at))}<br>最近：${esc(dateLabel(entry.last_generated_at || entry.created_at))} · 生成 ${Number(entry.generation_count || 1)} 次${entry.deleted_at?'<br>删除：'+esc(dateLabel(entry.deleted_at)):''}</small><code>${esc(entry.id.slice(0,12))} · ${Number(entry.group_count)} 组 / ${Number(entry.provider_count)} 规则集</code></div><div class="panel-actions"><button type="button" class="secondary" data-history-detail="${esc(entry.id)}">查看文件与设置</button><button type="button" class="ghost" data-history-change="${esc(entry.id)}">${state.historyArchived?'恢复':'删除'}</button></div></article>`).join('') || `<p class="field-hint">${state.historyBusy?'读取中…':data?'没有符合条件的版本。':'展开后读取历史。'}</p>`}</div><div class="panel-actions"><button type="button" class="ghost" data-grouping="history-prev"${state.historyPage<=1?' disabled':''}>上一页</button><span class="field-hint">第 ${state.historyPage} 页${data?' · 共 '+Number(data.total)+' 个版本':''}</span><button type="button" class="ghost" data-grouping="history-next"${!data || state.historyPage*20>=data.total?' disabled':''}>下一页</button></div><div data-grouping="history-detail">${item?`<section class="grouping-history-detail"><h4>${esc(item.profile?.name || '历史版本')} · ${esc(item.id.slice(0,12))}</h4><p class="field-hint">原入口 ${esc(scopes[item.scope])} · 客户端 ${esc(item.client)} · 来源 ${esc(item.source)}<br>配置版本 ${esc(item.input_revision)} · 规则版本 ${esc(item.rule_revision)}</p><button type="button" class="secondary" data-grouping="history-reuse">复用设置为草稿</button><details class="grouping-provenance"><summary>生成时的完整设置</summary><pre>${esc(JSON.stringify(item.profile,null,2))}</pre></details>${(item.artifacts || []).map((artifact,index)=>`<details class="template-preview"><summary>${esc(artifact.name)}</summary>${item.deleted_at?'<p class="field-hint">此版本在回收站，恢复后可打开和下载。</p>':`<div class="panel-actions"><a class="button secondary" href="${esc(safeArtifactURL(artifact.url))}" target="_blank" rel="noopener noreferrer">打开</a><a class="button secondary" href="${esc(safeArtifactURL(artifact.download_url || artifact.url))}" download="${esc(artifact.name)}">下载</a><button type="button" class="secondary" data-history-copy="${index}">复制链接</button></div>`}<pre>${esc(artifact.content)}</pre></details>`).join('')}</section>`:''}</div></details>`;
      find('history-panel').ontoggle=()=>{state.historyOpen=find('history-panel').open;if(state.historyOpen && !state.history && !state.historyBusy)loadHistory();};
      find('history-search').onclick=()=>{state.historyQuery=find('history-query').value.trim();state.historyPage=1;return loadHistory();};
      find('history-archive').onclick=()=>{state.historyArchived=!state.historyArchived;state.historyPage=1;state.historyDetail=null;return loadHistory();};
      find('history-prev').onclick=()=>{if(state.historyPage>1){state.historyPage--;return loadHistory();}};
      find('history-next').onclick=()=>{if(data && state.historyPage*20<data.total){state.historyPage++;return loadHistory();}};
      target.querySelectorAll('[data-history-detail]').forEach(button=>button.onclick=()=>historyDetail(button.dataset.historyDetail));
      target.querySelectorAll('[data-history-change]').forEach(button=>button.onclick=()=>historyChange(button.dataset.historyChange));
      target.querySelectorAll('[data-history-copy]').forEach(button=>button.onclick=async()=>{try{const url=safeArtifactURL(item.artifacts[Number(button.dataset.historyCopy)].url);if(!url)throw new Error('文件地址无效');await navigator.clipboard.writeText(url);notice('历史文件链接已复制。');}catch(error){notice(error.message,true);}});
      if(find('history-reuse'))find('history-reuse').onclick=()=>{if(state.busy)return;if(state.dirty && !window.confirm('当前未保存的草稿将被替换，继续复用历史设置吗？'))return;state.profile=clone(item.profile);invalidate();state.activeTab='categories';render();notice(`已载入历史设置草稿。客户端仍为当前选择 ${state.context.client}、来源仍为 ${state.context.source}；保存后生成，不会覆盖原文件。`);};
      target.querySelectorAll('button').forEach(button=>button.disabled=button.disabled || state.historyBusy);
    }
    async function loadHistory() {
      if(state.historyBusy)return;state.historyBusy=true;state.historyOpen=true;state.historyError='';renderHistory();
      try{state.history=await request('history?'+new URLSearchParams({scope,page:String(state.historyPage),archived:String(state.historyArchived),q:state.historyQuery}));}
      catch(error){state.historyError=error.message;}
      finally{state.historyBusy=false;renderHistory();}
    }
    async function historyDetail(id) {
      if(state.historyBusy)return;state.historyBusy=true;state.historyError='';renderHistory();
      try{const data=await request('history/'+id);if(data.scope!==scope)throw new Error('历史文件入口不匹配');if((data.artifacts || []).some(item=>!safeArtifactURL(item.url)))throw new Error('历史文件地址无效');state.historyDetail=data;}
      catch(error){state.historyError=error.message;}
      finally{state.historyBusy=false;renderHistory();}
    }
    async function historyChange(id) {
      if(state.historyBusy || state.busy)return;
      const restore=state.historyArchived;
      if(!restore && !window.confirm('将此版本移入回收站？源站会停止新的文件下载；客户端已下载的文件和缓存不会被删除。'))return;
      state.historyBusy=true;renderHistory();
      try{await request('history/'+id+(restore?'/restore':''),{method:restore?'POST':'DELETE'});state.historyDetail=null;if(state.generated?.id===id && !restore){state.ticket++;state.generated=null;state.artifacts=[];renderArtifacts();}state.historyBusy=false;await loadHistory();notice(restore?'历史版本已恢复，原链接可再次读取。':'已移入回收站，可在回收站恢复。');}
      catch(error){state.historyError=error.message;}
      finally{state.historyBusy=false;renderHistory();}
    }
    function render() {
      if (!state.loaded) { host.innerHTML = `<p class="routing-feedback${state.error ? ' bad' : ''}">${esc(state.error || '正在读取分组设置…')}</p>${state.error ? '<button type="button" data-grouping="retry" class="secondary">重试</button>' : ''}`; if (find('retry')) find('retry').onclick = load; return; }
      host.className = 'template-grouping';
      host.innerHTML = `<div class="grouping-heading"><div><span class="eyebrow">GROUPING PROFILE</span><h3>分组与分流设置</h3><p>同一套设置可用于妙妙屋X、PPanel 与 MihomoPro 覆写。</p></div><span data-grouping="state" class="soft-badge"></span></div><div class="grouping-profile-bar"><label><span>方案来源</span><select data-grouping="inherit"><option value="common"${state.inherit ? ' selected' : ''}>使用共用方案</option><option value="independent"${!state.inherit ? ' selected' : ''}>${scopes[scope]}独立方案</option></select></label><button type="button" class="secondary" data-grouping="save-common" data-grouping-write>保存为共用</button><button type="button" class="secondary" data-grouping="save-current" data-grouping-write>另存当前页</button><button type="button" class="ghost" data-grouping="copy-profile" data-grouping-write>复制方案</button><button type="button" class="ghost" data-grouping="defaults" data-grouping-write>恢复默认</button></div><p data-grouping="boundary" class="field-hint"></p><div class="grouping-tabs" role="tablist" aria-label="分组设置">${tabs.map(([key,label]) => `<button type="button" role="tab" id="grouping-${scope}-tab-${key}" aria-controls="grouping-${scope}-panel-${key}" aria-selected="${state.activeTab === key}" data-grouping-tab="${key}" class="${state.activeTab === key ? 'active' : ''}">${label}</button>`).join('')}</div><div class="grouping-tab-body">${tabs.map(([key]) => `<section id="grouping-${scope}-panel-${key}" role="tabpanel" aria-labelledby="grouping-${scope}-tab-${key}" data-grouping-panel="${key}"${state.activeTab === key ? '' : ' hidden'}>${({categories:renderCategories,regions:renderRegions,automatic:renderAutomatic,advanced:renderAdvanced,preview:renderPreview})[key]()}</section>`).join('')}</div><div class="grouping-publish"><div><strong>保存设置 → 检查分组 → 生成文件</strong><p data-grouping="feedback" class="field-hint" role="status" aria-live="polite">${esc(state.feedback || '已有订阅不会自动替换。请导入生成的新文件。')}</p></div><button type="button" data-grouping="generate">生成新文件</button></div><div data-grouping="artifacts"></div>`;
      host.innerHTML += '<section class="grouping-history" data-grouping="history"></section>';
      bind(); renderStatus(); renderDiagnostics(); renderArtifacts(); renderAdvancedHelp(); renderHistory();
    }
    function bind() {
      host.querySelectorAll('[data-grouping-tab]').forEach(button => button.onclick = () => { state.activeTab = button.dataset.groupingTab; host.querySelectorAll('[data-grouping-tab]').forEach(tab => { const active = tab === button; tab.setAttribute('aria-selected', String(active)); tab.classList.toggle('active',active); }); host.querySelectorAll('[data-grouping-panel]').forEach(panel => panel.hidden = panel.dataset.groupingPanel !== state.activeTab); if(state.activeTab==='advanced')loadBaseline(); });
      host.querySelectorAll('[data-grouping-field], [data-grouping-region], [data-grouping-category], [data-grouping-media]').forEach(element => element.onchange = () => { try { saveFields(); invalidate(); renderAdvancedHelp(); } catch (error) { invalidate(); notice(error.message,true); } });
      host.querySelectorAll('[data-category-move], [data-region-move]').forEach(button => button.onclick = () => {
        try { saveFields(); const items = button.dataset.categoryMove != null ? state.profile.categories : state.profile.regions, index = Number(button.dataset.categoryMove ?? button.dataset.regionMove), next = index + Number(button.dataset.direction); if (next < 0 || next >= items.length) return; [items[index],items[next]] = [items[next],items[index]]; invalidate(); render(); } catch (error) { notice(error.message,true); }
      });
      host.querySelectorAll('[data-grouping-region]').forEach(element => element.onchange = () => { try { saveFields(); invalidate(); render(); } catch (error) { invalidate(); notice(error.message,true); } });
      host.querySelectorAll('[data-grouping-macro], [data-grouping-mode]').forEach(element=>element.onchange=()=>{try{saveFields();invalidate();render();}catch(error){notice(error.message,true);}});
      host.querySelectorAll('[data-region-remove]').forEach(button=>button.onclick=()=>{try{saveFields();state.profile.regions=state.profile.regions.filter(code=>code!==button.dataset.regionRemove);invalidate();render();}catch(error){notice(error.message,true);}});
      host.querySelectorAll('[data-mode-batch]').forEach(button=>button.onclick=()=>{try{saveFields();const codes=[...state.profile.regions,...(state.profile.macros || []),'other'];codes.forEach(code=>state.profile.modes[code]={...modes(code),[button.dataset.modeBatch]:button.dataset.on==='true'});invalidate();render();}catch(error){notice(error.message,true);}});
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
      try { saveFields(); state.busy = true; renderStatus(); const ticket = ++state.ticket, profile = clone(state.profile); const data = await request('preview',{method:'POST',...body({profile,names:state.names.split('\n').map(name => name.trim()).filter(Boolean)})}); if (ticket !== state.ticket) return; state.preview = data; render(); notice('分组检查完成。兜底与多重匹配可在“地区”中修正。'); }
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
        state.history=null;state.historyDetail=null;if(state.historyOpen)await loadHistory();
      } catch (error) { if (ticket === state.ticket) notice(error.message,true); } finally { state.busy = false; renderStatus(); }
    }
    async function activate(context) {
      const changed = state.context.client !== context.client || state.context.source !== context.source || state.context.original !== context.original || state.context.available !== context.available;
      state.context = {...context};
      if (changed) { state.ticket++; state.artifacts = []; state.generated = null; renderArtifacts(); state.baseline=null;state.baselineClient='';state.baselineError='';if(state.activeTab==='advanced')loadBaseline(); }
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
