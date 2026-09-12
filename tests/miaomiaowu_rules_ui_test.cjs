const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const root = path.resolve(__dirname, '..');
const elements = new Map();
class Element {
  constructor() { this.value='';this.dataset={};this.attributes={};this.textContent='';this.disabled=false;this.classes=new Set();this.classList={toggle:(key,value)=>value?this.classes.add(key):this.classes.delete(key)}; }
  set innerHTML(value) { this.markup=value;for(const match of value.matchAll(/\bid="([^"]+)"/g))if(!elements.has(match[1]))elements.set(match[1],new Element()); }
  get innerHTML() { return this.markup || ''; }
  setAttribute(key,value) { this.attributes[key]=value; }
  removeAttribute(key) { delete this.attributes[key];if(key==='href')delete this.href;if(key==='download')delete this.download; }
}
elements.set('mmRulesetsSection',new Element());
const requests=[],copies=[];
let sourceControl;
const sandbox={URL,AbortController,window:{},location:{origin:'https://rules.example.com'},document:{getElementById:id=>elements.get(id)},escapeHTML:value=>String(value ?? '').replace(/[&<>'"]/g,char=>({'&':'&amp;','<':'&lt;','>':'&gt;',"'":'&#39;','"':'&quot;'}[char])),renderRuleSourceControl:(id,options)=>{assert.equal(id,'mmRulesSource','rules source control is scoped to its own module');sourceControl=options},activateTab:()=>{},navigator:{clipboard:{writeText:async text=>copies.push(text)}},fetch:(url,options)=>new Promise(resolve=>requests.push({url,options,resolve}))};
vm.createContext(sandbox);
vm.runInContext(fs.readFileSync(path.join(root,'web/miaomiaowu-rules.js'),'utf8').replace('window.CoralBayMiaomiaowuRules = {activate};','window.CoralBayMiaomiaowuRules = {activate}; globalThis.previewURLForTest = safeYAMLURL;'),sandbox);
const el=id=>elements.get(id),latest=()=>requests.at(-1),tick=()=>new Promise(resolve=>setImmediate(resolve));
const response=(data,{status=200,type='application/yaml'}={})=>({ok:status>=200&&status<300,status,headers:{get:()=>type},json:async()=>data,text:async()=>data});
const catalog={revision:'rev1',rule_revision:'rule1',source_name:'666OS / YYDS',available:true,total:2,items:[{id:'domain-Google',name:'Google',behavior:'domain',filename:'yyds-domain-Google.yaml',local_mrs_url:'https://rules.example.com/raw/Google.mrs',upstream_mrs_url:'https://example.org/Google.mrs'},{id:'ip-China',name:'China',behavior:'ipcidr',filename:'yyds-ip-China.yaml'}]};
const generated=(id='domain-Google',source='local')=>({id,source,filename:'yyds-'+id+'.yaml',revision:'rev1',rule_revision:'rule1',count:2,sha256:'sha',input_sha256:'input-sha',yaml_url:`https://rules.example.com/_miaomiaowu/rulesets/v1/rev1/${source}/${id}.yaml`,provider_yaml:`rule-providers:\n  ${id}:\n    type: http\n    behavior: classical\n    format: yaml\n    url: https://rules.example.com/_miaomiaowu/rulesets/v1/rev1/${source}/${id}.yaml\n`,format:'yaml',behavior:'classical'});
const payload='payload:\n  - DOMAIN-SUFFIX,google.com\n  - DOMAIN-SUFFIX,gstatic.com\n';
async function loadCatalog(data=catalog) {const pending=sandbox.window.CoralBayMiaomiaowuRules.activate();latest().resolve(response(data,{type:'application/json'}));await pending;}
async function postReply(data=generated()) {const pending=el('mmRulesGenerate').onclick();latest().resolve(response(data,{type:'application/json'}));await tick();return {pending};}
(async()=>{
  assert.equal(requests.length,0);
  const artifactPath='/_miaomiaowu/rulesets/v1/rev1/local/domain-Google.yaml';
  for (const host of ['localhost','127.0.0.1','[::1]']) {
    sandbox.location.origin=`http://${host}:44000`;
    assert.equal(sandbox.previewURLForTest(`https://${host}:44000${artifactPath}`),sandbox.location.origin+artifactPath,'same loopback host and port can use the current HTTP preview');
    assert.equal(sandbox.previewURLForTest(`https://${host}:44001${artifactPath}`),'');
    assert.equal(sandbox.previewURLForTest(`https://another-host:44000${artifactPath}`),'');
    assert.equal(sandbox.previewURLForTest(`https://${host}:44000/_miaomiaowu/not-a-ruleset.yaml`),'');
    assert.equal(sandbox.previewURLForTest(`https://${host}:44000/_miaomiaowu/rulesets/v1/rev1/local/domain-Google.mrs`),'');
  }
  sandbox.location.origin='http://localhost';
  assert.equal(sandbox.previewURLForTest('https://localhost'+artifactPath),'','default HTTPS port must not be mistaken for default HTTP port');
  assert.equal(sandbox.previewURLForTest('https://localhost:80'+artifactPath),'http://localhost'+artifactPath);
  sandbox.location.origin='http://localhost:443';
  assert.equal(sandbox.previewURLForTest('https://localhost'+artifactPath),'http://localhost:443'+artifactPath);
  sandbox.location.origin='http://rules.example.com:44000';
  assert.equal(sandbox.previewURLForTest('https://rules.example.com:44000'+artifactPath),'');
  sandbox.location.origin='https://rules.example.com';
  assert.equal(sandbox.previewURLForTest('http://rules.example.com'+artifactPath),'');
  await loadCatalog();
  assert.equal(requests.length,1,'catalog loading does not decode all rules');
  assert.equal(sourceControl.value,'local');
  assert.equal(el('mmRulesSelect').value,'domain-Google');
  assert.equal(el('mmRulesCopyText').disabled,true);
  assert.equal(el('mmRulesDownload').href,undefined);

  const first=el('mmRulesGenerate').onclick();
  assert.equal(latest().options.method,'POST');
  assert.equal(latest().url,'/api/templates/miaomiaowu/rulesets/domain-Google');
  assert.deepEqual(JSON.parse(latest().options.body),{revision:'rev1',source:'local'});
  assert.equal(el('mmRulesGenerate').disabled,true);
  const inFlightCount=requests.length;
  await el('mmRulesGenerate').onclick();
  assert.equal(requests.length,inFlightCount,'double clicks do not duplicate a conversion');
  latest().resolve(response(generated(),{type:'application/json'}));
  await tick();
  assert.equal(latest().url,'/_miaomiaowu/rulesets/v1/rev1/local/domain-Google.yaml');
  assert.equal(latest().options.redirect,'error');
  latest().resolve(response(payload));
  await first;
  assert.equal(el('mmRulesDownload').href,generated().yaml_url+'?download=1');
  assert.equal(el('mmRulesDownload').download,'yyds-domain-Google.yaml');
  assert.equal(el('mmRulesPreview').textContent,payload);
  await el('mmRulesCopyText').onclick();
  await el('mmRulesCopyURL').onclick();
  await el('mmRulesCopyProvider').onclick();
  assert.deepEqual(copies,[payload,generated().yaml_url,generated().provider_yaml]);

  const beforeSource=requests.length;
  sourceControl.onchange('upstream');
  assert.equal(requests.length,beforeSource,'changing source does not auto-convert');
  assert.equal(el('mmRulesDownload').href,undefined,'switching source clears the old link immediately');
  assert.equal(el('mmRulesCopyText').disabled,true);
  const slow=await postReply(generated('domain-Google','upstream'));
  const slowFile=latest();
  sourceControl.onchange('local');
  const fast=await postReply(generated());
  latest().resolve(response(payload));
  await fast.pending;
  slowFile.resolve(response('payload:\n  - stale-upstream\n'));
  await slow.pending;
  assert.equal(el('mmRulesPreview').textContent,payload,'late upstream file cannot replace the current local output');

  el('mmRulesSelect').value='ip-China';
  el('mmRulesSelect').onchange();
  assert.equal(el('mmRulesDownload').href,undefined);
  const slowPost=el('mmRulesGenerate').onclick();
  const delayedPost=latest();
  sourceControl.onchange('upstream');
  const beforeStaleReply=requests.length;
  delayedPost.resolve(response(generated('ip-China'),{type:'application/json'}));
  await slowPost;
  assert.equal(requests.length,beforeStaleReply,'stale POST cannot trigger a file fetch');
  assert.equal(el('mmRulesGenerate').disabled,false);
  await loadCatalog();
  assert.equal(el('mmRulesSelect').value,'ip-China','catalog refresh preserves the selected rule');
  assert.equal(sourceControl.value,'upstream','catalog refresh preserves its independent source');

  for(const data of [{...generated('ip-China','upstream'),yaml_url:'https://evil.example/_miaomiaowu/rulesets/v1/a.yaml'}, {...generated('ip-China','upstream'),yaml_url:'https://rules.example.com/sub?secret=1'}, {...generated('ip-China','upstream'),revision:'stale'}, {...generated('ip-China','upstream'),format:'mrs'}]) {
    const before=requests.length;
    const pending=el('mmRulesGenerate').onclick();
    latest().resolve(response(data,{type:'application/json'}));
    await pending;
    assert.equal(requests.length,before+1,'invalid result metadata must not trigger a download');
    assert.equal(el('mmRulesDownload').href,undefined);
    assert.equal(el('mmRulesCopyProvider').disabled,true);
  }
  const failedFile=await postReply(generated('ip-China','upstream'));
  latest().resolve(response('missing',{status:404}));
  await failedFile.pending;
  assert.equal(el('mmRulesDownload').href,undefined);
  assert.match(el('mmRulesStatus').textContent,/404/);
  const invalidPayload=await postReply(generated('ip-China','upstream'));
  latest().resolve(response('MRS BINARY IS NOT YAML'));
  await invalidPayload.pending;
  assert.match(el('mmRulesStatus').textContent,/payload/);
  assert.equal(el('mmRulesCopyText').disabled,true);
  el('mmRulesSelect').value='domain-Google';
  el('mmRulesSelect').onchange();
  const fullCount=1800;
  const largePayload='payload:\n'+Array.from({length:fullCount},(_,index)=>'  - DOMAIN-SUFFIX,rule-'+index+'.'+('long'.repeat(12))+'.example.com\n').join('');
  assert.ok(largePayload.length>64*1024);
  const largeFile=await postReply({...generated('domain-Google','upstream'),count:fullCount});
  latest().resolve(response(largePayload));
  await largeFile.pending;
  const preview=el('mmRulesPreview').textContent;
  assert.equal([...preview.matchAll(/^\s*-\s/gm)].length,200,'the screen renders only the first 200 rules');
  assert.ok(preview.length<=64*1024,'screen preview is also bounded by text length');
  assert.match(preview,/rule-199\./);
  assert.doesNotMatch(preview,/rule-200\./);
  assert.match(el('mmRulesPreviewHint').textContent,/仅预览前 200 条.*完整 1,800 条/);
  await el('mmRulesCopyText').onclick();
  assert.equal(copies.at(-1),largePayload,'copy retains every rule with no preview notice added to the payload');
  assert.equal(el('mmRulesDownload').href,generated('domain-Google','upstream').yaml_url+'?download=1','download keeps the full artifact URL');
  assert.match(el('mmRulesetsSection').innerHTML,/format: yaml、behavior: classical/);
  await loadCatalog({...catalog,available:false,reason:'本机规则未同步'});
  assert.equal(el('mmRulesGenerate').disabled,true);
  assert.equal(el('mmRulesDownload').href,undefined);
  assert.equal(el('mmRulesSyncHint').classes.has('hidden'),false);
  const beforeBlocked=requests.length;
  await el('mmRulesGenerate').onclick();
  assert.equal(requests.length,beforeBlocked);
  console.log('MiaoMiaoWuX ruleset UI state checks passed');
})().catch(error=>{console.error(error);process.exitCode=1});
