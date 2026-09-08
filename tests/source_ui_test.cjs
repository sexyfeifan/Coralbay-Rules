// Node-only UI state regressions; no browser, network or production writes.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');
const root = path.resolve(__dirname, '..');
const elements = new Map();
class Element {
  constructor() { this.value='';this.checked=false;this.dataset={};this.textContent='';this.attributes={};this.classList={add(){},remove(){},toggle(){},contains(){return false}}; }
  set innerHTML(value) { this.markup=value;const options=[...value.matchAll(/<option value="([^"]*)"/g)].map(match=>match[1]);if(options.length&&!options.includes(this.value))this.value=options[0]; }
  get innerHTML() { return this.markup||''; }
  querySelectorAll() { return []; }
  setAttribute(key,value) { this.attributes[key]=value; }
  removeAttribute(key) { delete this.attributes[key];if(key==='href')delete this.href; }
  replaceChildren() { this.innerHTML=''; }
  scrollIntoView() {}
  append() {}
}
const document={getElementById:id=>elements.get(id),querySelectorAll:()=>[],currentScript:{src:'https://rules.example.com/assets/app.js?v=test'}};
const requests=[];
let responseData={};
const sandbox={URL,URLSearchParams,window:{},location:{origin:'https://rules.example.com',href:'https://rules.example.com/',pathname:'/',hash:''},document,setTimeout,clearTimeout,console,navigator:{clipboard:{writeText:async()=>{}}},fetch:async(url)=>{requests.push(url);return {ok:true,json:async()=>responseData,text:async()=>`file: ${url}`}}};
vm.createContext(sandbox);
vm.runInContext(fs.readFileSync(path.join(root,'web/app.js'),'utf8')+`\nglobalThis.testUI={size,verifiedRuleComparison,renderRuleSourceControl,ruleSourceSummary,subscriptionPayload,applyParsedSubscription,loadSubscriptionPresets,renderSubscriptionRuleSource,selectTemplate,renderMihomoPro,selectedMihomoSource,previewMihomoPro,loadMihomoPro,seedPresets(items){subscriptionPresets=items},source(){return subscriptionSourceChoice},setSource(value){subscriptionSourceChoice=value},seedTemplates(items){templateItems=items},setTemplateSource(value){templateSourceSelection=value},seedMihomo(value){mihomoCatalog=value}};`,sandbox);
// app startup had no admin DOM: no eager legacy or MihomoPro API fetch.
assert.equal(requests.length,0);
for(const match of fs.readFileSync(path.join(root,'web/admin.html'),'utf8').matchAll(/\bid="([^"]+)"/g))elements.set(match[1],new Element());
const el=id=>{if(!elements.has(id))elements.set(id,new Element());return elements.get(id)};
const t=sandbox.testUI;
for(const [bytes,expected] of [[0,'0 B'],[1023,'1023 B'],[1024,'1.0 KB'],[1024**2,'1.0 MB'],[1024**3,'1.0 GB'],[2.5*1024**4,'2.5 TB'],[1024**5,'1.0 PB'],[null,'—'],[-1,'—'],[NaN,'—']])assert.equal(t.size(bytes),expected,'byte sizes use bounded readable units');
const local={verified:true,mirrored:true,health:'verified',revision:'a',sha256:'same',can_compare:false};
const upstream={verified:true,health:'verified',revision:'a',sha256:'same'};
assert.equal(t.verifiedRuleComparison(local,upstream).equal,true,'independently verified reads are comparable even when first read had no upstream cache');
assert.equal(t.verifiedRuleComparison(local,{...upstream,sha256:'different'}).equal,false);
for(const broken of [{verified:false},{mirrored:false},{health:'legacy_only'},{health:'corrupt'},{local_error:'missing file'},{sha256:''}])assert.equal(t.verifiedRuleComparison({...local,...broken},upstream).comparable,false);
assert.equal(t.verifiedRuleComparison({sha256:'same'},upstream).comparable,false,'old manifest digest is not proof of a local raw file');
assert.equal(t.verifiedRuleComparison(local,{...upstream,last_error:'upstream timed out'}).comparable,false);
assert.equal(t.verifiedRuleComparison(local,null).comparable,false);
t.renderRuleSourceControl('subRuleSource',{value:'legacy',options:[{id:'local',available:true},{id:'upstream',available:false,reason:'<script>bad</script>'}]});
assert.match(el('subRuleSource').innerHTML,/分流规则来源/);
assert.match(el('subRuleSource').innerHTML,/value="legacy" checked/);
assert.match(el('subRuleSource').innerHTML,/value="upstream" disabled/);
assert.doesNotMatch(el('subRuleSource').innerHTML,/<script>/);
const presets=[{id:'builtin',built_in:true,local_url:'https://rules.example.com/_configs/builtin.ini',rule_source_options:[{id:'local',available:true,targets:['clash','stash']},{id:'upstream',available:true,targets:['clash','stash']}]},{id:'thirdparty',original_url:'https://third.example/custom.ini',rule_source_options:[]},{id:'none'}];
t.seedPresets(presets);
el('subTarget').value='clash';el('subConfig').value=presets[0].local_url;el('subURLs').value='https://nodes.example/sub';el('subSurgeVersion').value='4';el('subInterval').value='24';
t.setSource('local');assert.equal(t.subscriptionPayload().rule_source,'local');
t.setSource('upstream');assert.equal(t.subscriptionPayload().rule_source,'upstream');
el('subTarget').value='surge';assert.throws(()=>t.subscriptionPayload(),/目前仅支持/);
el('subTarget').value='ss';assert.equal(Object.hasOwn(t.subscriptionPayload(),'rule_source'),false,'node-only targets omit rule source');
el('subTarget').value='clash';el('subListOnly').checked=true;assert.equal(Object.hasOwn(t.subscriptionPayload(),'rule_source'),false);el('subListOnly').checked=false;
el('subConfig').value=presets[1].original_url;assert.throws(()=>t.subscriptionPayload(),/完整/);
const params={target:['clash'],config:[presets[0].local_url],url:['https://nodes.example/sub'],interval:['86400']};
t.applyParsedSubscription(params);assert.equal(t.source(),'legacy');assert.equal(Object.hasOwn(t.subscriptionPayload(),'rule_source'),false,'old signed URL keeps the field omitted');
t.applyParsedSubscription({...params,rule_source:['upstream']});assert.equal(t.subscriptionPayload().rule_source,'upstream');
assert.match(t.ruleSourceSummary({actual_source:'upstream',revision:'abc',count:33,uncovered:2}),/上游源.*abc.*33.*2/);
(async()=>{
  responseData={presets,cached:1,total:1};
  el('subConfig').value='https://custom.example/do-not-overwrite.ini';await t.loadSubscriptionPresets();assert.equal(el('subConfig').value,'https://custom.example/do-not-overwrite.ini');assert.equal(t.source(),'upstream','catalog refresh must preserve the source choice');
  const localConfig='https://rules.example.com/_rule-templates/666os/abc/local/mihomopro-config';
  const upstreamConfig=localConfig.replace('/local/','/upstream/');
  t.seedMihomo({legacy:{config_url:'https://rules.example.com/_templates/MihomoPro.yaml',overwrite_url:'https://rules.example.com/_templates/MihomoPro_overwrite.conf'},source_options:[{id:'local',available:true,config_url:localConfig,overwrite_url:localConfig.replace('mihomopro-config','mihomopro-overwrite'),revision:'abc',provider_count:33},{id:'upstream',available:true,config_url:upstreamConfig,overwrite_url:upstreamConfig.replace('mihomopro-config','mihomopro-overwrite'),revision:'abc',provider_count:33}]});
  for(const source of ['local','upstream']){el('mihomoRuleSource').dataset.value=source;t.renderMihomoPro();assert.match(el('mihomoArtifacts').innerHTML,new RegExp('/abc/'+source+'/mihomopro-config'));assert.match(el('mihomoArtifacts').innerHTML,new RegExp('/abc/'+source+'/mihomopro-overwrite'));assert.doesNotMatch(el('mihomoArtifacts').innerHTML,new RegExp('/abc/'+(source==='local'?'upstream':'local')+'/'));el('mihomoPreviewKind').value='config';await t.previewMihomoPro();assert.equal(requests.at(-1),`/_rule-templates/666os/abc/${source}/mihomopro-config`)}
  t.seedMihomo({source_options:[{id:'local',available:false,reason:'raw missing',config_url:localConfig}]});el('mihomoRuleSource').dataset.value='local';t.renderMihomoPro();assert.doesNotMatch(el('mihomoArtifacts').innerHTML,/href=/,'unavailable source must not leak a fallback artifact link');
  const template={id:'mihomo',name:'Mihomo',online_url:'https://rules.example.com/_templates/clients/mihomo',original_url:'https://rules.example.com/_templates/clients/mihomo-original',download_url:'https://rules.example.com/adapted-download',original_download_url:'https://rules.example.com/original-download',rule_source_options:[{id:'local',url:'https://rules.example.com/_rule-templates/666os/abc/local/mihomo'},{id:'upstream',url:'https://rules.example.com/_rule-templates/666os/abc/upstream/mihomo'}]};
  t.seedTemplates([template]);el('clientTemplate').value='mihomo';el('templateVariant').value='adapted';t.setTemplateSource('upstream');t.selectTemplate();assert.equal(el('ppanelTemplateURL').textContent,template.rule_source_options[1].url);assert.equal(el('copyClientTemplate').dataset.url,template.rule_source_options[1].url);
  el('templateVariant').value='original';t.selectTemplate();assert.equal(el('ppanelTemplateURL').textContent,template.original_url);assert.equal(el('templateRuleSource').dataset.value,'default');el('templateVariant').value='adapted';t.selectTemplate();assert.equal(el('ppanelTemplateURL').textContent,template.rule_source_options[1].url,'template-version switch preserves the adapted source choice');
  t.seedTemplates([{...template,rule_source_options:[]}]);t.selectTemplate();assert.equal(el('ppanelTemplateURL').textContent,'');assert.equal(el('copyClientTemplate').disabled,true,'missing source must not silently use old template URL');
  const routeElements=new Map(),routeElement=id=>{if(!routeElements.has(id))routeElements.set(id,new Element());return routeElements.get(id)},routeCalls=[];let routeReply={};
  const routeSandbox={URL,URLSearchParams,window:{},location:{origin:'https://rules.example.com',pathname:'/routing'},document:{getElementById:routeElement},confirm:()=>true,ruleSourceName:sandbox.ruleSourceName,size:t.size,fetch:async(url,opt)=>{routeCalls.push({url,opt});return{ok:true,json:async()=>routeReply}}};
  const routingSource=fs.readFileSync(path.join(root,'web/routing.js'),'utf8').replace('window.CoralBayRouting={activate};','metadata=async()=>{};renderEditor=()=>{};window.test={state,defaultSpec,newProfile,openProfile,validate,sourceHistory,renderStorage,loadSourceHistory,repairRules};');
  vm.runInNewContext(routingSource,routeSandbox);const r=routeSandbox.window.test,plain=value=>JSON.parse(JSON.stringify(value));r.state.catalog={rules:[{id:'openai',name:'OpenAI',recommended:true,default_action:'proxy'}]};
  assert.deepEqual(plain(r.defaultSpec().rule_delivery),{mode:'provider',source:'local'});
  const legacy={id:'old',version:2,spec:{name:'Old',sources:['https://example.test/sub'],clients:['stash'],rules:[{id:'openai',action:'proxy'}],global:{},interval_hours:24}};
  await r.openProfile(legacy);assert.equal(Object.hasOwn(r.validate(),'rule_delivery'),false);await r.openProfile(legacy,true);assert.equal(Object.hasOwn(r.validate(),'rule_delivery'),false);assert.equal(r.state.profile,null);
  await r.openProfile({...legacy,spec:{...legacy.spec,rule_delivery:{mode:'provider',source:'upstream'}}});assert.deepEqual(plain(r.validate().rule_delivery),{mode:'provider',source:'upstream'});
  await r.newProfile({id:'openai',name:'OpenAI',source:'upstream'});assert.deepEqual(plain(r.state.spec.rule_delivery),{mode:'provider',source:'upstream'});
  r.state.spec=null;r.state.catalog={rules:[],revision:'current',disk_bytes:999,disk_capacity_bytes:5*1024**4,disk_free_bytes:2*1024**3,disk_warnings:['low disk <test>']};
  routeElement('rtSourceMaintenance').open=false;r.renderStorage();assert.equal(routeCalls.length,0,'folded maintenance must not fetch extra data');
  routeElement('rtSourceMaintenance').open=true;r.renderStorage();assert.match(routeElement('rtStorageSummary').innerHTML,/5\.0 TB/);assert.match(routeElement('rtStorageSummary').innerHTML,/2\.0 GB/);assert.match(routeElement('rtStorageWarnings').textContent,/low disk <test>/,'warnings are text content');
  routeReply={versions:[{revision:'historical',current:false,manifest_valid:true,resource_count:78,total:78,verified:false,health:'unchecked'}],count:21,page:1,page_size:20};await r.loadSourceHistory(1);
  assert.equal(routeCalls.at(-1).url,'/api/routing/rules/versions?page=1&page_size=20');assert.equal(routeElement('rtHistoryNext').disabled,false);assert.match(routeElement('rtHistoryTable').innerHTML,/原始文件未检查/);assert.match(routeElement('rtHistoryTable').innerHTML,/不切换当前版本/);
  routeReply={versions:[],count:21,page:2,page_size:20};await r.loadSourceHistory(2);assert.equal(routeCalls.at(-1).url,'/api/routing/rules/versions?page=2&page_size=20');assert.equal(routeElement('rtHistoryNext').disabled,true);
  routeElement('rtSourceMaintenance').open=false;routeReply={rules:[],revision:'current',repaired_revision:'historical'};await r.repairRules('historical');assert.deepEqual(JSON.parse(routeCalls.at(-1).opt.body),{revision:'historical'});assert.equal(r.state.catalog.revision,'current','historical repair must not activate the target');
  console.log('PASS source UI: verified raw comparison, legacy preservation, target constraints, input retention, MihomoPro paired URLs, independent PPanel version, routing defaults, lazy history pagination and repair');
})().catch(error=>{console.error(error);process.exitCode=1});
