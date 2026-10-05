package webui

import "html/template"

// template.go — HTML halaman tunggal (3 tab). html/template escape otomatis;
// data diambil via fetch() ke /api/* (token dari cookie HttpOnly).

const pageHTML = `<!doctype html>
<html lang="id">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Aleph — Panel</title>
<style>
:root{--bg:#0f1420;--card:#1a2233;--fg:#e7ecf5;--dim:#8b96ab;--acc:#4f8cff;--ok:#38c172;--err:#e3342f}
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:system-ui,Segoe UI,sans-serif;background:var(--bg);color:var(--fg);padding:16px;max-width:880px;margin:auto}
h1{font-size:1.2rem;margin-bottom:12px}
.tabs{display:flex;gap:8px;margin-bottom:16px}
.tabs button{flex:1;padding:10px;border:0;border-radius:8px;background:var(--card);color:var(--dim);font-size:1rem;cursor:pointer}
.tabs button.on{background:var(--acc);color:#fff}
.card{background:var(--card);border-radius:10px;padding:14px;margin-bottom:12px}
.row{display:flex;gap:8px;align-items:center;flex-wrap:wrap}
input,textarea,select{background:#0d1220;border:1px solid #2a3550;border-radius:6px;color:var(--fg);padding:8px;font-size:.95rem}
input:focus,textarea:focus{outline:1px solid var(--acc)}
textarea{width:100%;min-height:140px;font-family:ui-monospace,monospace}
button.act{background:var(--acc);color:#fff;border:0;border-radius:6px;padding:8px 14px;cursor:pointer;font-size:.95rem}
button.del{background:var(--err)}
.note{border-left:3px solid var(--acc);padding:8px 10px;margin-bottom:8px;background:#141b2c;border-radius:0 8px 8px 0}
.note b{cursor:pointer}
.note small{color:var(--dim)}
.dim{color:var(--dim);font-size:.85rem}
.ok{color:var(--ok)}.err{color:var(--err)}
table{width:100%;border-collapse:collapse}
td,th{padding:6px 8px;text-align:left;border-bottom:1px solid #26314d;font-size:.9rem}
.st-siap{color:var(--ok)}.st-proses,.st-menunggu{color:#f6ad3b}.st-gagal{color:var(--err)}
.drop{border:2px dashed #2a3550;border-radius:10px;padding:26px;text-align:center;color:var(--dim);margin-bottom:12px}
.drop.over{border-color:var(--acc);color:var(--acc)}
label.ck{display:inline-flex;gap:6px;align-items:center;margin:4px 10px 4px 0}
#toast{position:fixed;bottom:16px;left:50%;transform:translateX(-50%);background:var(--acc);color:#fff;padding:10px 18px;border-radius:8px;display:none}
</style>
</head>
<body>
<h1>🐘 Aleph — Panel Lokal</h1>
<div class="tabs">
<button id="t-files" class="on" onclick="show('files')">📁 Files</button>
<button id="t-notes" onclick="show('notes')">🗂 Notes</button>
<button id="t-config" onclick="show('config')">⚙️ Config AI</button>
</div>

<div id="p-files">
  <div class="drop" id="drop">Tarik-lepas file ke sini, atau <label style="color:var(--acc);text-decoration:underline"><input type="file" id="fpick" multiple hidden>pilih file</label><br><span class="dim">PDF/DOCX/TXT — masuk folder inbox, otomatis diekstrak</span></div>
  <div class="card"><table id="ftab"><tr><th>Nama</th><th>Ukuran</th><th>Status</th></tr></table></div>
</div>

<div id="p-notes" style="display:none">
  <div class="card row">
    <input id="n-title" placeholder="Judul catatan" style="flex:1;min-width:140px">
    <input id="n-cat" placeholder="Kategori" style="width:130px">
    <button class="act" onclick="saveNote()">💾 Simpan</button>
  </div>
  <div class="card"><textarea id="n-content" placeholder="Isi catatan… pakai [[Judul lain]] untuk wiki-link"></textarea></div>
  <div class="card row">
    <input id="n-q" placeholder="Cari…" style="flex:1" oninput="loadNotes()">
    <select id="n-catf" onchange="loadNotes()"><option value="">Semua kategori</option></select>
  </div>
  <div id="nlist"></div>
</div>

<div id="p-config" style="display:none">
  <div class="card">
    <b>Max Context (token verbatim sebelum compacting)</b>
    <div class="row" style="margin:8px 0">
      <label class="ck"><input type="radio" name="cb" value="4096">4k</label>
      <label class="ck"><input type="radio" name="cb" value="8192">8k</label>
      <label class="ck"><input type="radio" name="cb" value="16384">16k</label>
      <label class="ck"><input type="radio" name="cb" value="32768">32k</label>
      <label class="ck"><input type="radio" name="cb" value="65536">64k</label>
    </div>
    <div class="dim" id="cbwarn" style="display:none">⚠️ Konteks besar membuat tiap ronde LLM lambat di Atom N2600 — gunakan hanya jika perlu.</div>
  </div>
  <div class="card row">
    <b>Max Tool Calls</b> <input id="c-mr" type="number" min="1" max="100" style="width:90px">
    <b>Timeout LLM (dtk)</b> <input id="c-to" type="number" min="30" max="600" style="width:100px">
    <b>Model</b> <input id="c-model" style="flex:1;min-width:160px">
  </div>
  <button class="act" onclick="saveCfg()">💾 Simpan Config</button>
  <span id="cfgmsg" class="dim"></span>
</div>

<div id="toast"></div>
<script>
let TAB='files';
function show(t){TAB=t;for(const x of['files','notes','config']){document.getElementById('p-'+x).style.display=x===t?'':'none';document.getElementById('t-'+x).className=x===t?'on':''}
if(t==='files')loadFiles();if(t==='notes')loadNotes();if(t==='config')loadCfg();}
function toast(m){const t=document.getElementById('toast');t.textContent=m;t.style.display='block';setTimeout(()=>t.style.display='none',2500)}
async function api(u,o){const r=await fetch(u,o);if(!r.ok){throw new Error(await r.text())}return r.json()}

/* FILES */
async function loadFiles(){try{const d=await api('/api/files');const tb=document.getElementById('ftab');tb.innerHTML='<tr><th>Nama</th><th>Ukuran</th><th>Status</th></tr>';
for(const f of d){const tr=document.createElement('tr');tr.innerHTML='<td>'+esc(f.name)+'</td><td>'+esc(f.size)+'</td><td class="st-'+esc(f.status)+'">'+esc(f.status)+(f.pages?' '+esc(f.pages):'')+'</td>';tb.appendChild(tr)}}catch(e){toast('gagal: '+e.message)}}
const drop=document.getElementById('drop');
['dragover','dragenter'].forEach(ev=>drop.addEventListener(ev,e=>{e.preventDefault();drop.classList.add('over')}));
['dragleave','drop'].forEach(ev=>drop.addEventListener(ev,e=>{e.preventDefault();drop.classList.remove('over')}));
drop.addEventListener('drop',e=>uploadFiles(e.dataTransfer.files));
document.getElementById('fpick').addEventListener('change',e=>uploadFiles(e.target.files));
async function uploadFiles(fl){for(const f of fl){const fd=new FormData();fd.append('file',f);
try{const r=await api('/api/upload',{method:'POST',body:fd});toast(r.name+' ('+r.size+') ✓ '+r.info)}catch(e){toast('gagal: '+e.message)}}
loadFiles()}

/* NOTES */
async function loadNotes(){try{const q=document.getElementById('n-q').value;const c=document.getElementById('n-catf').value;
let u='/api/notes?q='+encodeURIComponent(q);if(c)u='/api/notes?category='+encodeURIComponent(c);
const d=await api(u);const el=document.getElementById('nlist');el.innerHTML='';
for(const n of d){const dv=document.createElement('div');dv.className='note';
dv.innerHTML='<b onclick="openNote(this)">'+esc(n.title)+'</b> <small>['+esc(n.category)+']</small><br><span class="dim">'+esc(n.preview)+'</span> <small>· '+esc(n.updated)+'</small> <button class="del act" style="float:right;padding:2px 8px" onclick="delNote(this.dataset.s)">🗑</button>';
dv.querySelector('.del').dataset.s=n.slug;el.appendChild(dv)}
// isi filter kategori
const cats=[...new Set(d.map(x=>x.category))].sort();const sel=document.getElementById('n-catf');
const cur=sel.value;sel.innerHTML='<option value="">Semua kategori</option>'+cats.map(c=>'<option'+(c===cur?' selected':'')+'>'+esc(c)+'</option>').join('');
}catch(e){toast('gagal: '+e.message)}}
function openNote(b){const dv=b.parentElement;const t=b.textContent;const catEl=b.parentElement.querySelector('small');
document.getElementById('n-title').value=t;
document.getElementById('n-cat').value=catEl?catEl.textContent.replace(/[\\[\\]]/g,''):'';
document.getElementById('n-content').value='';toast('Ketik isi baru lalu Simpan untuk memperbarui catatan ini')}
async function saveNote(){try{const r=await api('/api/notes',{method:'POST',headers:{'Content-Type':'application/json'},
body:JSON.stringify({title:document.getElementById('n-title').value,category:document.getElementById('n-cat').value||'Umum',content:document.getElementById('n-content').value})});
toast('tersimpan: '+r.slug);loadNotes()}catch(e){toast('gagal: '+e.message)}}
async function delNote(s){if(!confirm('Hapus catatan '+s+'?'))return;try{await api('/api/notes?slug='+encodeURIComponent(s),{method:'DELETE'});loadNotes()}catch(e){toast('gagal: '+e.message)}}

/* CONFIG */
document.querySelectorAll('input[name=cb]').forEach(r=>r.addEventListener('change',warnCb));
function warnCb(){const v=+document.querySelector('input[name=cb]:checked').value;
document.getElementById('cbwarn').style.display=v>=32768?'':'none'}
async function loadCfg(){try{const c=await api('/api/config');
const cb=c.context_budget||8192;const el=document.querySelector('input[name=cb][value="'+cb+'"]')||document.querySelector('input[name=cb][value="8192"]');el.checked=true;warnCb();
document.getElementById('c-mr').value=c.max_rounds||20;document.getElementById('c-to').value=c.timeout_sec||120;document.getElementById('c-model').value=c.model||''}catch(e){toast('gagal: '+e.message)}}
async function saveCfg(){try{await api('/api/config',{method:'POST',headers:{'Content-Type':'application/json'},
body:JSON.stringify({context_budget:+document.querySelector('input[name=cb]:checked').value,max_rounds:+document.getElementById('c-mr').value,timeout_sec:+document.getElementById('c-to').value,model:document.getElementById('c-model').value.trim()})});
document.getElementById('cfgmsg').textContent='tersimpan ✓ (langsung aktif, tanpa restart)';setTimeout(()=>document.getElementById('cfgmsg').textContent='',4000)}catch(e){toast('gagal: '+e.message)}}

function esc(s){const d=document.createElement('div');d.textContent=s==null?'':String(s);return d.innerHTML}
show('files');
</script>
</body>
</html>`

var tmpl = template.Must(template.New("page").Parse(pageHTML))
