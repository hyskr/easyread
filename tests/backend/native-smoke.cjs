// Exercise the compiled backend with no Python/Go/Node executable on its PATH.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const {spawn, spawnSync} = require('node:child_process');
const binary = path.resolve(process.argv[2] || 'build/backend/easyread-backend');
const temporary = fs.mkdtempSync(path.join(os.tmpdir(), 'easyread-native-smoke-'));
function pdf() {
 const stream='BT /F1 12 Tf 20 260 Td (Native Go backend smoke test) Tj ET';
 const objects=['<< /Type /Catalog /Pages 2 0 R >>','<< /Type /Pages /Kids [3 0 R] /Count 1 >>','<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 300] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>','<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>',`<< /Length ${stream.length} >>\nstream\n${stream}\nendstream`];
 let text='%PDF-1.4\n'; const offsets=[0];
 objects.forEach((o,i)=>{offsets.push(Buffer.byteLength(text));text+=`${i+1} 0 obj\n${o}\nendobj\n`;});
 const xref=Buffer.byteLength(text);text+=`xref\n0 ${offsets.length}\n0000000000 65535 f \n`;
 offsets.slice(1).forEach(n=>text+=`${String(n).padStart(10,'0')} 00000 n \n`);
 return Buffer.from(text+`trailer\n<< /Size ${offsets.length} /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF\n`);
}
const sleep = ms => new Promise(r=>setTimeout(r,ms));
async function main(){
 fs.writeFileSync(path.join(temporary,'config.json'),JSON.stringify({engine:'none',check_updates:false}));
 const env={PATH:path.join(temporary,'no-programs'),HOME:temporary,USERPROFILE:temporary,EASYREAD_HOME:temporary,EASYREAD_LIBRARY:path.join(temporary,'library'),EASYREAD_PDF_BIN:process.env.EASYREAD_PDF_BIN || '/usr/bin',LANG:'en_US.UTF-8'};
 const child=spawn(binary,['serve','--port','0'],{cwd:temporary,env,stdio:['ignore','pipe','pipe']});
 let stdout='',stderr='';child.stdout.on('data',b=>stdout+=b);child.stderr.on('data',b=>stderr+=b);
 let exited=false;child.once('exit',()=>exited=true);
 try {
  let address;for(let i=0;i<150;i++){address=stdout.match(/http:\/\/127\.0\.0\.1:\d+/)?.[0];if(address)break;if(exited)throw Error(stderr||stdout);await sleep(100);}assert.ok(address,'backend failed to announce address');
  const get=async p=>{const r=await fetch(address+p);assert.equal(r.status,200,p);return r.json();};
  const health=await get('/api/health');assert.equal(health.backend,'go');assert.equal(health.pdf.python_required,false);assert.equal(health.pdf.available,true);
  const library=await get('/api/library');const headers={'X-Token':library.token,'Content-Type':'application/json'};
  for(const p of ['/','/web/tabs.html','/web/js/common/confirm.js','/web/css/base.css']){const r=await fetch(address+p);assert.equal(r.status,200,p);assert.ok((await r.text()).length>50);}
  const imported=await fetch(address+'/api/import?name=Smoke.pdf&translate=0',{method:'POST',headers,body:pdf()});assert.equal(imported.status,200);const item=await imported.json();
  let state;for(let i=0;i<150;i++){state=await get(`/api/p/${item.id}/state`);if(state.job.state==='done')break;if(state.job.state==='error')throw Error(JSON.stringify(state.job));await sleep(100);}assert.equal(state.job.state,'done');assert.equal(state.paper.meta.pages.length,1);
  const image=await fetch(address+`/p/${item.id}/`+state.paper.meta.pages[0].img);assert.equal(image.status,200);assert.ok((await image.arrayBuffer()).byteLength>100);
  const save=await fetch(address+`/api/p/${item.id}/ops`,{method:'POST',headers,body:JSON.stringify({client:'smoke',ops:[{op:'paper_note',body:'Native note survives restart',at:'2026-11-03T06:00:00Z'}]})});assert.equal(save.status,200);
  const notes=await get(`/api/p/${item.id}/part/reader`);assert.equal(notes.data.paper_note.body,'Native note survives restart');
  const offline=await fetch(address+`/api/p/${item.id}/export`);assert.equal(offline.status,200);assert.ok((await offline.text()).includes('Native note survives restart'));
  if(process.env.EASYREAD_SMOKE_CHROMIUM){const shot=path.resolve(process.env.EASYREAD_SMOKE_SCREENSHOT || path.join(temporary,'library.png'));const result=spawnSync(process.env.EASYREAD_SMOKE_CHROMIUM,['--headless','--no-sandbox','--disable-gpu',`--user-data-dir=${path.join(temporary,'chromium')}`,'--hide-scrollbars','--window-size=1440,1000','--virtual-time-budget=3000',`--screenshot=${shot}`,address],{timeout:20000,encoding:'utf8'});assert.equal(result.status,0,result.stderr);assert.ok(fs.statSync(shot).size>1000);console.log('Browser screenshot:',shot);}
  console.log(JSON.stringify({backend:health.backend,python_on_path:false,assets:'embedded',import:item.id,pages:state.paper.meta.pages.length,notes:'saved',offline_export:'passed'}));
 } finally {child.kill('SIGTERM');for(let i=0;i<50&&!exited;i++)await sleep(100);if(!exited)child.kill('SIGKILL');fs.rmSync(temporary,{recursive:true,force:true});}
}
main().catch(e=>{console.error(e);process.exitCode=1});
