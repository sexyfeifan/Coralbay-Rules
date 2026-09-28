// User-flow regressions without dependencies: lazy loading, saving, source races,
// invalid delivery URLs and keeping a generated artifact separate from its draft.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const decode = value => String(value).replace(/&(amp|lt|gt|quot|#39);/g,(_,key)=>({'amp':'&',lt:'<',gt:'>',quot:'"','#39':"'"}[key]));
class Element {
  constructor(tag = 'div') { this.tagName=tag;this.children=[];this.dataset={};this.attributes={};this.value='';this.type=tag==='input'?'text':'';this.checked=false;this.disabled=false;this.hidden=false;this.classes=new Set();this.classList={toggle:(name,value)=>value?this.classes.add(name):this.classes.delete(name)};this._text=''; }
  setAttribute(key,value) { this.attributes[key]=value;if(key.startsWith('data-'))this.dataset[key.slice(5).replace(/-([a-z])/g,(_,letter)=>letter.toUpperCase())]=value;if(key==='type')this.type=value;if(key==='value')this.value=decode(value);if(key==='checked')this.checked=true;if(key==='disabled')this.disabled=true;if(key==='hidden')this.hidden=true; }
  getAttribute(key) { return this.attributes[key] ?? null; }
  set textContent(value) { this._text=String(value);this.children=[]; }
  get textContent() { return this._text+this.children.map(child=>child.textContent).join(''); }
  set innerHTML(html) {
    this.markup=html;this.children=[];this._text='';const stack=[this];
    for(const match of html.matchAll(/<(?:[^>"']|"[^"]*"|'[^']*')*>|[^<]+/g)) {
      const token=match[0];
      if(token.startsWith('</')) { if(stack.length>1)stack.pop();continue; }
      if(token.startsWith('<')) {
        const open=token.match(/^<([\w-]+)\b([\s\S]*)>$/);if(!open)continue;
        const element=new Element(open[1]);
        for(const attr of open[2].matchAll(/([\w-]+)(?:="([^"]*)")?/g))element.setAttribute(attr[1],decode(attr[2] ?? ''));
        stack.at(-1).children.push(element);
        if(!['input','br','hr','img'].includes(open[1]))stack.push(element);
      } else { stack.at(-1)._text+=decode(token); }
    }
    this.querySelectorAll('textarea').forEach(element=>element.value=element.textContent);
    this.querySelectorAll('select').forEach(element=>{const options=element.querySelectorAll('option');element.value=(options.find(option=>Object.hasOwn(option.attributes,'selected')) || options[0])?.value || '';});
  }
  get innerHTML() { return this.markup || ''; }
  querySelectorAll(selector) {
    const match=(element,raw)=>{
      const selected=raw.trim(), tag=selected.match(/^[a-z]+/)?.[0];
      if(tag&&tag!==element.tagName)return false;
      if(selected.includes(':checked')&&!element.checked)return false;
      for(const attr of selected.matchAll(/\[([\w-]+)(?:="([^"]*)")?\]/g))if(!Object.hasOwn(element.attributes,attr[1])||attr[2]!=null&&element.attributes[attr[1]]!==attr[2])return false;
      return !!tag || selected.includes('[');
    };
    const out=[];const visit=parent=>parent.children.forEach(element=>{if(selector.split(',').some(choice=>match(element,choice)))out.push(element);visit(element);});visit(this);return out;
  }
  querySelector(selector) { return this.querySelectorAll(selector)[0] || null; }
}
const requests=[],copies=[],hosts=new Map([['mm',new Element()],['pp',new Element()],['ow',new Element()]]);
const sandbox={URL,URLSearchParams,Object,setTimeout:callback=>setImmediate(callback),window:{confirm:()=>true},location:{origin:'https://rules.example.com'},document:{getElementById:id=>hosts.get(id)},navigator:{clipboard:{writeText:async text=>copies.push(text)}},fetch:(url,options)=>new Promise(resolve=>requests.push({url,options,resolve}))};
vm.createContext(sandbox);
const source=fs.readFileSync(path.join(__dirname,'../web/template-grouping.js'),'utf8');
vm.runInContext(source.replace('window.CoralBayTemplateGrouping = {activate};','window.CoralBayTemplateGrouping = {activate}; globalThis.safeForTest=safeArtifactURL;globalThis.diffForTest=contentDifference;'),sandbox);
const profile={name:'测试方案',regions:['hk','jp','us'],default:'全球自动',show_nodes:true,categories:[{name:'人工智能',enabled:true,default:'auto'},{name:'国际媒体',enabled:true,default:'auto'}],media:[],test_url:'https://example.com/204',interval:300,tolerance:50,strategy:'consistent-hashing',icons:true,hide_auto:false,dns_mode:'inherit',ipv6:'inherit',sniffer:'inherit',overrides:[]};
const catalog={regions:[{code:'hk',name:'香港'},{code:'jp',name:'日本'},{code:'us',name:'美国'},{code:'de',name:'德国'}],media:['YouTube','Netflix','Disney','Spotify'],macros:[{code:'europe',name:'欧洲'}],default_options:['全球自动','全球手动','DIRECT','德国自动']};
const profileResponse=(value=profile)=>({scope:'miaomiaowu',inherit:true,profile:value,defaults:profile,catalog,revision:'rev1'});
const response=(value,{status=200,type='application/json'}={})=>({ok:status>=200&&status<300,status,headers:{get:()=>type},json:async()=>value,text:async()=>value});
const tick=()=>new Promise(resolve=>setImmediate(resolve));
const latest=()=>requests.at(-1);
const find=(host,key)=>hosts.get(host).querySelector(`[data-grouping="${key}"]`);
const field=(host,key)=>hosts.get(host).querySelector(`[data-grouping-field="${key}"]`);
const artifactPath='/_grouped-templates/'+'a'.repeat(64)+'/miaomiaowu.yaml';
const generated={scope:'miaomiaowu',client:'clash',source:'local',id:'a'.repeat(64),group_count:12,provider_count:33,rule_count:29,profile_hash:'rev1',input_revision:'template-rev',rule_revision:'rules-rev',artifacts:[{name:'miaomiaowu.yaml',url:'https://rules.example.com'+artifactPath,download_url:'https://rules.example.com'+artifactPath+'?download=1',sha256:'checksum',content:'proxy-groups:\n  - name: 新分组\n    type: select\nrules:\n  - MATCH,新分组\n'}]};
const context={host:'mm',client:'clash',source:'local',available:true,baseline:()=> 'proxy-groups:\n  - name: 旧分组\n    type: select\nrules:\n  - MATCH,旧分组\n'};

(async()=>{
  assert.equal(requests.length,0,'loading script must not fetch settings for every page');
  const active=sandbox.window.CoralBayTemplateGrouping.activate('miaomiaowu',context);
  assert.equal(latest().url,'/api/template-grouping/profiles/miaomiaowu');latest().resolve(response(profileResponse()));
  const state=await active;assert.equal(state.loaded,true);assert.equal(find('mm','generate').disabled,false);
  assert.equal(requests.length,1,'opening one page only reads its own settings');
  assert.doesNotMatch(field('mm','default').textContent,/德国自动/,'defaults exclude unselected countries');
  assert.doesNotMatch(field('mm','default').textContent,/默认出口/,'global default cannot reference itself');
  assert.match(hosts.get('mm').querySelector('[data-grouping-category="0"][data-property="default"]').textContent,/默认出口/,'category may follow global default');

  field('mm','show_nodes').checked=false;field('mm','show_nodes').onchange();
  assert.equal(find('mm','generate').disabled,true,'unsaved edits cannot be mistaken for saved output');
  assert.equal(state.profile.show_nodes,false);
  const saved=find('mm','save-current').onclick();
  assert.equal(latest().url,'/api/template-grouping/profiles/miaomiaowu');
  assert.ok(latest().options.body,find('mm','feedback').textContent);
  assert.equal(JSON.parse(latest().options.body).inherit,false);
  latest().resolve(response({...profileResponse({...profile,show_nodes:false}),inherit:false}));await saved;
  assert.equal(state.inherit,false);assert.equal(state.dirty,false);assert.equal(find('mm','generate').disabled,false);

  const generating=find('mm','generate').onclick();
  assert.deepEqual(JSON.parse(latest().options.body),{scope:'miaomiaowu',client:'clash',source:'local',profile:{...profile,show_nodes:false}},'generation is bound to the exact visible saved profile');
  latest().resolve(response(generated));await generating;
  assert.equal(state.artifacts.length,1);
  assert.match(find('mm','artifacts').innerHTML,/新增分组：新分组/);
  assert.match(find('mm','artifacts').innerHTML,/移除分组：旧分组/);
  await find('mm','copy-content').onclick();assert.equal(copies.at(-1),generated.artifacts[0].content);
  await find('mm','copy-url').onclick();assert.equal(copies.at(-1),generated.artifacts[0].url);

  field('mm','name').value='已编辑';field('mm','name').onchange();
  assert.equal(state.artifacts.length,0,'editing invalidates previous generated artifact');
  assert.equal(find('mm','copy-content'),null,'old content cannot be copied as new settings');
  find('mm','defaults').onclick();assert.equal(state.dirty,true);assert.equal(state.profile.name,profile.name);
  const common=find('mm','save-common').onclick();
  assert.equal(latest().url,'/api/template-grouping/profiles/common');
  assert.equal(JSON.parse(latest().options.body).apply_scope,'miaomiaowu','shared save also changes inheritance atomically');
  latest().resolve(response({error:'操作频繁'},{status:429}));await tick();await tick();
  assert.equal(latest().url,'/api/template-grouping/profiles/common','a throttled write receives one bounded retry');
  latest().resolve(response(profileResponse()));await common;
  assert.equal(state.inherit,true);assert.equal(state.dirty,false);

  const stale=find('mm','generate').onclick(),slowRequest=latest();
  await sandbox.window.CoralBayTemplateGrouping.activate('miaomiaowu',{...context,source:'upstream'});
  slowRequest.resolve(response(generated));await stale;
  assert.equal(state.artifacts.length,0,'a late local artifact must not replace an upstream selection');
  const unsupportedCount=requests.length;
  await sandbox.window.CoralBayTemplateGrouping.activate('miaomiaowu',{...context,client:'surge'});
  assert.equal(find('mm','generate').disabled,true);
  assert.match(find('mm','boundary').textContent,/暂不支持/);
  await find('mm','generate').onclick();assert.equal(requests.length,unsupportedCount,'unsupported client cannot call generation endpoint');
  await sandbox.window.CoralBayTemplateGrouping.activate('miaomiaowu',{...context,source:'legacy'});
  assert.equal(find('mm','generate').disabled,true,'legacy source cannot silently choose local');

  await sandbox.window.CoralBayTemplateGrouping.activate('miaomiaowu',context);
  const invalid=find('mm','generate').onclick();latest().resolve(response({...generated,artifacts:[{...generated.artifacts[0],url:'https://evil.example'+artifactPath}]}));await invalid;
  assert.equal(state.artifacts.length,0);assert.match(find('mm','feedback').textContent,/地址无效/);
  for(const url of ['javascript:alert(1)','https://user@rules.example.com'+artifactPath,'https://rules.example.com/api/admin/status','/_grouped-templates/../../api/admin/status'])assert.equal(sandbox.safeForTest(url),'');

  const moved=hosts.get('mm').querySelector('[data-category-move="1"][data-direction="-1"]');moved.onclick();
  assert.equal(state.profile.categories[0].name,'国际媒体','category order is part of saved profile');
  const moveRegion=hosts.get('mm').querySelector('[data-region-move="1"][data-direction="-1"]');moveRegion.onclick();
  assert.equal(state.profile.regions[0],'jp','selected-region priority can be changed');
  field('mm','icons').checked=false;field('mm','icons').onchange();assert.equal(state.profile.regions[0],'jp','other field edits preserve selected-region ordering');

  find('mm','names').value='德国 01\n越南 01';
  const preview=find('mm','preview').onclick();assert.deepEqual(JSON.parse(latest().options.body).names,['德国 01','越南 01']);
  latest().resolve(response({total:2,covered:2,unknown:0,ambiguous:0,regions:[{code:'europe',name:'欧洲',count:1,nodes:['德国 01']}],groups:[],warnings:[]}));await preview;
  assert.match(find('mm','diagnostics').innerHTML,/德国 01/);
  assert.equal(state.dirty,true,'preview does not save settings as a side effect');

  const before=sandbox.diffForTest('proxy-groups:\n  - { name: A, type: select }\nrules:\n - MATCH,A','proxy-groups:\n  - name: B\n    type: select\nrules:\n - MATCH,B');
  assert.equal(before.added.join(','),'B');assert.equal(before.removed.join(','),'A');

  const media=hosts.get('mm').querySelector('[data-grouping-media="YouTube"]');media.checked=true;media.onchange();
  assert.ok(state.profile.categories.some(item=>item.name==='YouTube'),'enabled media gets configurable category defaults');

  const ppContext={host:'pp',client:'mihomo',source:'local',available:true};
  const restore=sandbox.window.CoralBayTemplateGrouping.activate('ppanel',ppContext);
  const previous={...generated,scope:'ppanel',client:'mihomo',profile_hash:'rev1',artifacts:generated.artifacts.map(item=>({...item,content:undefined}))};
  latest().resolve(response({...profileResponse(),scope:'ppanel',last_generation:previous}));await tick();
  assert.equal(latest().url,artifactPath,'restored artifacts load their verified same-origin content');latest().resolve(response(generated.artifacts[0].content,{type:'application/yaml'}));
  const ppState=await restore;assert.equal(ppState.artifacts.length,1);
  const saveAgain=find('mm','save-common').onclick();latest().resolve(response(profileResponse(state.profile)));await saveAgain;
  const refresh=sandbox.window.CoralBayTemplateGrouping.activate('ppanel',ppContext);
  latest().resolve(response({...profileResponse({...profile,name:'新的共用方案'}),revision:'rev2',last_generation:previous}));await refresh;
  assert.equal(ppState.artifacts.length,0,'old output is removed when the inherited profile changes');
  assert.equal(find('pp','copy-content'),null,'old manifest cannot be represented as current settings');
  const remove=hosts.get('mm').querySelector('[data-region-remove="jp"]');remove.onclick();
  assert.ok(!state.profile.regions.includes('jp'),'remove chip cancels independent region');
  const macro=hosts.get('mm').querySelector('[data-grouping-macro="europe"]');macro.checked=false;macro.onchange();
  assert.equal(state.profile.macros.length,0,'macro switches are saved explicitly, including empty selection');
  assert.doesNotMatch(field('mm','default').textContent,/欧洲自动/,'disabled macro removed from default choices');
  const batch=hosts.get('mm').querySelector('[data-mode-batch="auto"][data-on="false"]');batch.onclick();
  assert.equal(state.profile.modes.hk.auto,false);assert.equal(state.profile.modes.other.auto,false);
  assert.match(field('mm','default').textContent,/全球自动/,'batch does not disable global auto');
  assert.doesNotMatch(field('mm','default').textContent,/香港自动/,'disabled auto removed from choices');

  hosts.get('mm').querySelector('[data-grouping-tab="advanced"]').onclick();
  assert.match(latest().url,/advanced\?scope=miaomiaowu&client=clash/);
  latest().resolve(response({revision:'actual-base',dns:{enable:false,'enhanced-mode':'fake-ip',ipv6:false},ipv6:false,sniffer:{enable:true}}));await tick();await tick();
  assert.match(find('mm','advanced-help').textContent,/actual-base/);
  field('mm','dns_mode').value='redir-host';field('mm','dns_mode').onchange();
  assert.match(find('mm','advanced-help').textContent,/不会自动开启 DNS/,'DNS mode does not silently enable disabled DNS');
  assert.match(find('mm','advanced-help').textContent,/redir-host/);

  const historyLoad=find('mm','history-search').onclick();
  assert.match(latest().url,/history\?scope=miaomiaowu/);
  const entry={...generated,profile,created_at:'2026-09-28T12:00:00Z',last_generated_at:'2026-09-28T13:00:00Z',generation_count:2};
  latest().resolve(response({items:[entry],total:1,page:1,page_size:20}));await historyLoad;
  assert.match(find('mm','history').textContent,/生成 2 次/);
  const detail=hosts.get('mm').querySelector('[data-history-detail]').onclick();latest().resolve(response(entry));await detail;
  assert.match(find('mm','history-detail').textContent,/规则版本 rules-rev/);
  find('mm','history-reuse').onclick();assert.equal(state.dirty,true);assert.equal(state.profile.name,profile.name);
  assert.equal(state.context.source,'local','reuse does not silently change source');
  const deletion=hosts.get('mm').querySelector('[data-history-change]').onclick();
  assert.equal(latest().options.method,'DELETE');latest().resolve(response({ok:true}));await tick();
  latest().resolve(response({items:[],total:0,page:1,page_size:20}));await deletion;
  assert.match(find('mm','feedback').textContent,/可在回收站恢复/);
  const archive=find('mm','history-archive').onclick();assert.match(latest().url,/archived=true/);
  latest().resolve(response({items:[{...entry,deleted_at:'2026-09-28T14:00:00Z'}],total:1,page:1,page_size:20}));await archive;
  const restoration=hosts.get('mm').querySelector('[data-history-change]').onclick();
  assert.match(latest().url,/\/restore$/);assert.equal(latest().options.method,'POST');latest().resolve(response({ok:true}));await tick();
  latest().resolve(response({items:[],total:0,page:1,page_size:20}));await restoration;
  console.log('PASS grouping UI: lazy loading, scopes, races, regions/macros, mode switches, actual advanced baseline, history inspection/reuse/recycle/restore');
})().catch(error=>{console.error(error);process.exitCode=1;});
