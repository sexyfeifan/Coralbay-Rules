// Focused state regressions for the independent MiaoMiaoWuX page.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const root = path.resolve(__dirname, '..');
const elements = new Map();
class Element {
  constructor() { this.dataset={};this.attributes={};this.textContent='';this.disabled=false;this.classes=new Set();this.classList={toggle:(key,value)=>value?this.classes.add(key):this.classes.delete(key)}; }
  set innerHTML(value) { this.markup=value;for(const match of value.matchAll(/\bid="([^"]+)"/g)) if(!elements.has(match[1]))elements.set(match[1],new Element()); }
  get innerHTML() { return this.markup || ''; }
  setAttribute(key,value) { this.attributes[key]=value; }
  removeAttribute(key) { delete this.attributes[key];if(key==='href')delete this.href;if(key==='download')delete this.download; }
}
elements.set('miaomiaowuPage',new Element());
const requests=[], copies=[];
let sourceControl;
const sandbox={URL,AbortController,window:{CoralBayMiaomiaowuRules:{activate:async()=>{}}},location:{origin:'https://rules.example.com'},document:{getElementById:id=>elements.get(id)},renderRuleSourceControl:(id,options)=>{sourceControl=options},activateTab:()=>{},navigator:{clipboard:{writeText:async text=>copies.push(text)}},fetch:(url,options)=>new Promise(resolve=>requests.push({url,options,resolve}))};
vm.createContext(sandbox);
vm.runInContext(fs.readFileSync(path.join(root,'web/miaomiaowu.js'),'utf8').replace('window.CoralBayMiaomiaowu = {activate};','window.CoralBayMiaomiaowu = {activate}; globalThis.previewURLForTest = safeTemplateURL;'),sandbox);
const el=id=>elements.get(id);
const tick=()=>new Promise(resolve=>setImmediate(resolve));
const response=(data,{status=200,type='application/yaml'}={})=>({ok:status>=200&&status<300,status,headers:{get:()=>type},json:async()=>data,text:async()=>data});
const catalog={format:'miaomiaowu-v3',source_name:'666OS / YYDS Pro_cn',source_url:'https://github.com/666OS/YYDS/blob/main/mihomo/config/cn/Pro_cn.yaml',docs_url:'https://miaomiaowux.com/docs/templates/',source_options:['local','upstream'].map(id=>({id,available:true,template_url:`https://rules.example.com/_miaomiaowu/rev/${id}/template.yaml`,revision:'rev',rule_revision:'rules',provider_count:33,group_count:20,rule_count:29}))};
const latest=()=>requests.at(-1);
async function supplyCatalog(data=catalog) {
  const active=sandbox.window.CoralBayMiaomiaowu.activate();
  assert.equal(latest().url,'/api/templates/miaomiaowu');
  latest().resolve(response(data,{type:'application/json'}));
  await tick();
  return {active};
}
(async()=>{
  assert.equal(requests.length,0,'lazy module does not fetch on script load');
  const artifactPath='/_miaomiaowu/rev/local/template.yaml';
  for (const host of ['localhost','127.0.0.1','[::1]']) {
    sandbox.location.origin=`http://${host}:44000`;
    assert.equal(sandbox.previewURLForTest(`https://${host}:44000${artifactPath}`),sandbox.location.origin+artifactPath,'only matching loopback host and port can use HTTP preview');
    assert.equal(sandbox.previewURLForTest(`https://${host}:44001${artifactPath}`),'','different preview port stays blocked');
    assert.equal(sandbox.previewURLForTest(`https://another-host:44000${artifactPath}`),'','different preview host stays blocked');
    assert.equal(sandbox.previewURLForTest(`https://${host}:44000/api/admin/status`),'','preview normalization retains artifact path restrictions');
  }
  sandbox.location.origin='http://localhost';
  assert.equal(sandbox.previewURLForTest('https://localhost'+artifactPath),'','implicit HTTPS 443 is not the current HTTP 80');
  assert.equal(sandbox.previewURLForTest('https://localhost:80'+artifactPath),'http://localhost'+artifactPath);
  sandbox.location.origin='http://localhost:443';
  assert.equal(sandbox.previewURLForTest('https://localhost'+artifactPath),'http://localhost:443'+artifactPath,'normalization preserves the actual target port');
  sandbox.location.origin='http://rules.example.com:44000';
  assert.equal(sandbox.previewURLForTest('https://rules.example.com:44000'+artifactPath),'','public hosts do not receive the preview exception');
  sandbox.location.origin='https://rules.example.com';
  assert.equal(sandbox.previewURLForTest('http://rules.example.com'+artifactPath),'','production HTTPS never downgrades');
  const {active}=await supplyCatalog();
  assert.equal(sourceControl.value,'local','new page defaults to its own local source');
  assert.equal(latest().url,'/_miaomiaowu/rev/local/template.yaml');
  assert.equal(latest().options.redirect,'error','artifact preview cannot follow a cross-origin redirect');
  assert.equal(el('mmCopyURL').disabled,true,'unverified preview cannot expose a download URL');
  latest().resolve(response('rules:\n  - MATCH,全球手动\n'));
  await active;
  assert.equal(el('mmDownload').download,'CoralBay_MiaoMiaoWuX_YYDS_local.yaml');
  assert.equal(el('mmProviderCount').textContent,'33');
  await el('mmCopyText').onclick();
  assert.equal(copies.at(-1),'rules:\n  - MATCH,全球手动\n');
  await el('mmCopyURL').onclick();
  assert.equal(copies.at(-1),catalog.source_options[0].template_url);

  sourceControl.onchange('upstream');
  const slowUpstream=latest();
  assert.equal(el('mmCopyText').disabled,true);
  assert.equal(el('mmDownload').href,undefined,'source change immediately removes the old link');
  sourceControl.onchange('local');
  const fastLocal=latest();
  fastLocal.resolve(response('current-local'));
  await tick();
  slowUpstream.resolve(response('stale-upstream'));
  await tick();
  assert.equal(el('mmPreview').textContent,'current-local','an old source response must not replace the chosen source');
  assert.equal(el('mmDownload').href,catalog.source_options[0].template_url);

  sourceControl.onchange('upstream');
  latest().resolve(response('missing',{status:404}));
  await tick();
  assert.equal(el('mmDownload').href,undefined);
  assert.equal(el('mmCopyURL').disabled,true);
  assert.match(el('mmStatus').textContent,/404/);
  assert.doesNotMatch(el('mmPreview').textContent,/current-local/);

  const unavailable={...catalog,local_error:'raw missing',source_options:catalog.source_options.map(item=>({...item,available:false,reason:'请先同步'}))};
  const count=requests.length;
  const unavailableLoad=await supplyCatalog(unavailable);
  await unavailableLoad.active;
  assert.equal(requests.length,count+1,'unavailable sources must not fetch a provided stale URL');
  assert.equal(sourceControl.value,'upstream','refresh preserves this page selection');
  assert.equal(el('mmDownload').href,undefined);
  assert.equal(el('mmSyncHint').classes.has('hidden'),false);
  assert.match(el('mmLocalError').textContent,/raw missing/);

  for(const unsafe of ['https://evil.example/_miaomiaowu/a.yaml','https://rules.example.com/sub?token=private','javascript:alert(1)','https://user@rules.example.com/_miaomiaowu/a.yaml','/_miaomiaowu/../../api/admin/status']) {
    const previous=requests.length;
    const load=await supplyCatalog({...catalog,source_options:catalog.source_options.map(item=>({...item,template_url:unsafe}))});
    await load.active;
    assert.equal(requests.length,previous+1,'unsafe preview URL is never fetched: '+unsafe);
    assert.equal(el('mmDownload').href,undefined);
    assert.equal(el('mmCopyText').disabled,true);
  }

  const htmlLoad=await supplyCatalog();
  latest().resolve(response('<html>login</html>',{type:'text/html'}));
  await htmlLoad.active;
  assert.equal(el('mmDownload').href,undefined,'a successful login page is not a YAML template');
  assert.equal(el('mmCopyText').disabled,true);

  const reloaded=sandbox.window.CoralBayMiaomiaowu.activate();
  latest().resolve(response({error:'offline'},{status:503,type:'application/json'}));
  await reloaded;
  assert.equal(el('mmDownload').href,undefined);
  assert.equal(el('mmRefresh').disabled,false);
  assert.match(el('mmStatus').textContent,/offline/);
  assert.equal(copies.length,2,'background operations do not copy data');

  // Routing integration keeps the page separate from the legacy refresh fan-out.
  const appSource=fs.readFileSync(path.join(root,'web/app.js'),'utf8');
  assert.match(appSource,/selected==='miaomiaowu'\?'\/miaomiaowu'/);
  assert.match(appSource,/else if\(!\['routing','sources','miaomiaowu'\]\.includes\(selected\)\)ensureLegacyData\(\)/);
  assert.match(appSource,/if\(!\['routing','sources','miaomiaowu'\]\.includes\(currentConsolePage\(\)\)\)ensureLegacyData\(\)/);
  assert.match(appSource,/currentConsolePage\(\)==='miaomiaowu'\?loadMiaomiaowuModule\(\):refreshAll\(\)/);
  assert.match(fs.readFileSync(path.join(root,'web/admin.html'),'utf8'),/data-tab="miaomiaowu"/);
  console.log('MiaoMiaoWuX UI state checks passed');
})().catch(error=>{console.error(error);process.exitCode=1});
