// Release-maintainer tool; no asset downloads or rendering at install/runtime.
// Usage: NODE_PATH=<temp>/node_modules node scripts/render-country-flags.cjs <svg-dir>
// Requires @resvg/resvg-js@2.6.2 in a disposable dependency directory.
const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');
const {Resvg} = require('@resvg/resvg-js');
const root = path.resolve(__dirname, '..');
const source = process.argv[2];
if (!source) throw new Error('Provide the pinned country-flags SVG directory');
const output = path.join(root, 'assets/icons/flags');
fs.mkdirSync(output, {recursive:true});
for (const name of fs.readdirSync(source).sort()) {
  if (!/^[a-z]{2}\.svg$/.test(name)) continue;
  const svg = fs.readFileSync(path.join(source,name));
  const png = new Resvg(svg, {fitTo:{mode:'width',value:160},font:{loadSystemFonts:false}}).render().asPng();
  fs.writeFileSync(path.join(output,name.replace(/\.svg$/,'.png')),png);
}
const files = {};
function inventory(dir, prefix='') {
  for (const entry of fs.readdirSync(dir,{withFileTypes:true}).sort((a,b)=>a.name.localeCompare(b.name,'en'))) {
    const relative=prefix+entry.name, filename=path.join(dir,entry.name);
    if (entry.isDirectory()) {inventory(filename,relative+'/');continue;}
    if (!entry.isFile() || !relative.endsWith('.png')) continue;
    const data=fs.readFileSync(filename);
    if (data.length<24 || data.subarray(0,8).toString('hex')!=='89504e470d0a1a0a') throw new Error('Invalid PNG: '+relative);
    files[relative]={bytes:data.length,sha256:crypto.createHash('sha256').update(data).digest('hex'),width:data.readUInt32BE(16),height:data.readUInt32BE(20)};
  }
}
inventory(path.join(root,'assets/icons'));
fs.writeFileSync(path.join(root,'assets/icon-manifest.json'),JSON.stringify({format:1,country_flags_revision:'c09927e63705529bbf59ca6684cd9b23225dddad',qure_revision:'b16b260625f873266f6a6a9b88710132774997b8',files},null,2)+'\n');
console.log('Bundled PNG inventory:',Object.keys(files).length);
