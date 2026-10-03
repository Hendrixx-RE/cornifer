const $ = (selector, root = document) => {
  const scope = typeof root === 'string' ? document.querySelector(root) : root;
  return scope?.querySelector?.(selector) || null;
};
const API = async (url, options = {}) => {
  const response = await fetch(url, {headers: {'content-type': 'application/json'}, ...options});
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(payload.error || `Request failed (${response.status})`);
  return payload;
};
const state = {repos: [], selected: null, graph: null, nodes: new Map(), edges: [], selectedNode: null, activeFile: '', sessionID: '', chatConfigured: false, embeddingConfigured: false, scale: 1, tx: 0, ty: 0, graphMode: true, jobs: new Map()};
const MCP_URL = `http://127.0.0.1:${location.port || '7791'}/mcp`;
const svgNS = 'http://www.w3.org/2000/svg';
const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)');
const panelAnimations = new WeakMap();
function reveal(element) {
  panelAnimations.get(element)?.cancel();
  if (reducedMotion.matches || !element?.animate || element.hidden) return;
  panelAnimations.set(element, element.animate([
    {opacity: .35, transform: 'translateY(5px)'},
    {opacity: 1, transform: 'translateY(0)'}
  ], {duration: 220, easing: 'cubic-bezier(.2,.8,.2,1)'}));
}
let toastTimer;
function toast(message) { const el = $('#toast'); el.textContent = message; el.classList.add('visible'); clearTimeout(toastTimer); toastTimer = setTimeout(() => el.classList.remove('visible'), 2200); }
function setStatus(selector, message, cls = '') { const el = $(selector); el.textContent = message; el.className = `inline-status ${cls}`.trim(); }
function safeText(value) { return String(value ?? ''); }
function esc(value) { return safeText(value).replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c])); }
function nameFor(repo) { return (repo.canonical_url || '').replace('https://github.com/', '').replace(/\.git$/, ''); }
function shortSHA(repo) { return repo.resolved_commit_sha ? repo.resolved_commit_sha.slice(0, 10) : (repo.requested_ref || 'ref pending'); }
function apiPath(repoID, endpoint) { return `/api/repos/${encodeURIComponent(repoID)}/${endpoint}`; }

async function init() {
  $('#mcp-endpoint code').textContent = MCP_URL;
  try {
    const health = await API('/api/health');
    state.embeddingConfigured = !!health.embedding_configured;
    state.chatConfigured = !!health.chat_configured;
    $('#connection').textContent = 'local companion connected';
    $('#chat-badge').textContent = state.chatConfigured ? 'hosted chat ready' : 'retrieval only';
    $('#chat-badge').classList.toggle('configured', state.chatConfigured);
    $('#chat-badge').classList.toggle('missing', !state.chatConfigured);
    setStatus('#provider', state.embeddingConfigured ? `Hosted embeddings configured · ${health.embedding_provider}.` : 'Hosted embedding credentials required before indexing. No local inference is used.');
  } catch (error) {
    $('#connection').textContent = 'companion unavailable'; $('#topbar')?.classList.add('offline');
    $('#connection').parentElement.classList.add('offline'); setStatus('#provider', error.message, 'error');
  }
  await refreshRepos();
}

async function refreshRepos() {
  try {
    state.repos = (await API('/api/repos')) || [];
    renderRepos();
    if (state.selected) {
      const latest = state.repos.find(repo => repo.id === state.selected.id);
      if (latest) { state.selected = latest; renderRepositoryStatus(latest); }
    }
  } catch (error) { setStatus('#provider', error.message, 'error'); }
}

function renderRepos() {
  const root = $('#repos'); root.replaceChildren();
  if (!state.repos.length) { const p = document.createElement('p'); p.className = 'empty-state'; p.textContent = 'No indexed snapshots yet.'; root.append(p); return; }
  for (const repo of state.repos) {
    const wrapper = document.createElement('div'); wrapper.className = 'repo-wrap';
    const button = document.createElement('button'); button.type = 'button'; button.className = `repo-item${state.selected?.id === repo.id ? ' selected' : ''}`;
    button.setAttribute('aria-current', state.selected?.id === repo.id ? 'true' : 'false');
    const title = document.createElement('strong'); title.textContent = nameFor(repo) || 'Repository'; button.append(title);
    const small = document.createElement('small'); small.className = 'repo-status-line';
    const dot = document.createElement('i'); dot.className = `state-dot ${repo.status}`; dot.setAttribute('aria-hidden','true');
    const status = document.createElement('span'); status.textContent = `${repo.status} · ${shortSHA(repo)}`; small.append(dot,status); button.append(small);
    button.addEventListener('click', () => selectRepository(repo)); wrapper.append(button);
    const job = state.jobs.get(repo.id);
    if (job && job.cancellable) {
      const progress = document.createElement('div'); progress.className = 'repo-progress'; progress.textContent = `${job.phase} · ${job.files_indexed} files · ${job.chunks} chunks`;
      const cancel = document.createElement('button'); cancel.className = 'cancel-job'; cancel.type = 'button'; cancel.textContent = 'Cancel indexing'; cancel.addEventListener('click', async event => { event.stopPropagation(); try { await API(`/api/jobs/${job.id}/cancel`, {method:'POST',body:'{}'}); toast('Index cancellation requested'); pollJob(repo.id, job.id); } catch (e) { toast(e.message); } }); wrapper.append(progress,cancel);
    }
    root.append(wrapper);
  }
}

async function selectRepository(repo) {
  state.selected = repo; state.selectedNode = null; state.activeFile = ''; state.sessionID = '';
  if (repo.status === 'ready') state.sessionID = readStoredSession(repo.id);
  state.nodes.clear(); state.edges = []; state.graph = null;
  $('#welcome').hidden = true; $('#workspace').hidden = false; $('#files-section').hidden = true;
  reveal($('#workspace'));
  $('#answer').replaceChildren(); $('#memory-events').replaceChildren(); $('#remember-form').hidden = true;
  $('#session').textContent = 'Search or ask a question to start a repository and commit scoped session.';
  $('#clear-session').disabled = !state.sessionID; $('#question').disabled = repo.status !== 'ready'; $('#ask').disabled = repo.status !== 'ready';
  $('#repo-name').textContent = nameFor(repo); $('#commit').textContent = shortSHA(repo); $('#snapshot-counts').textContent = '';
  renderRepositoryStatus(repo); renderRepos();
  $('#node-kind').textContent = 'Select a node'; $('#inspector-content').innerHTML = '<div class="inspector-blank"><span>↖</span><p>Select a symbol in the graph or a file in the sidebar to inspect its source and relationships.</p></div>';
  $('#graph').hidden = true; $('#graph-empty').hidden = true;
  if (repo.status !== 'ready') { showRepoState(repo); return; }
  try {
    const graph = await API(apiPath(repo.id, 'graph'));
    if (state.selected?.id !== repo.id) return;
    state.graph = graph; state.nodes = new Map(graph.nodes.map(node => [node.id,node])); state.edges = graph.edges;
    $('#files-section').hidden = false; $('#coverage-note').hidden = false;
    $('#catalog-count').textContent = `${graph.files.length} files · ${graph.nodes.length} symbols`;
    renderFileList(); drawGraph();
    $('#snapshot-counts').textContent = `${graph.nodes.length} symbols · ${graph.edges.length} relations`;
    $('#graph').hidden = graph.nodes.length === 0; $('#graph-empty').hidden = graph.nodes.length > 0;
  } catch (error) { showRepoState({...repo, safe_message:error.message, status:'failed'}); }
  $('#question').disabled = false; $('#ask').disabled = false;
  if (state.sessionID) await loadSession();
  setStatus('#question-status', state.chatConfigured ? 'Hosted chat can generate an answer with linked source evidence.' : 'Cited retrieval is ready. Hosted chat is separate and not configured.');
}

function renderRepositoryStatus(repo) {
  const fixture = repo.safe_message?.startsWith('Verification fixture:');
  $('#repo-status').textContent = fixture ? `${repo.status} · fixture` : repo.status;
  $('#repo-status').classList.toggle('status-error', ['failed','awaiting_credentials'].includes(repo.status));
}
function showRepoState(repo) {
  $('#graph').hidden = true; $('#graph-empty').hidden = false; $('#graph-empty').innerHTML = `<span class="empty-icon">${repo.status === 'failed' ? '!' : '…'}</span><strong>${esc(repo.status.replaceAll('_',' '))}</strong><p>${esc(repo.safe_message || statusMessage(repo.status))}</p>`;
  $('#snapshot-counts').textContent = '';
  if (repo.status === 'awaiting_credentials') setStatus('#provider','Hosted embedding credentials are required; indexing paused without local inference.','error');
  $('#question').disabled = true; $('#ask').disabled = true;
}
function statusMessage(status) { return ({queued:'Waiting for the index worker.',cloning:'Cloning the selected GitHub ref.',resolving:'Resolving source relationships.',indexing:'Building the repository snapshot.',awaiting_credentials:'Configure a hosted embedding provider, then submit this repository again.',failed:'The snapshot could not be indexed. Check configuration and retry.',cancelled:'Indexing was cancelled. Submit again to retry.'})[status] || 'Snapshot is not ready yet.'; }

$('#ingest-form').addEventListener('submit', async event => {
  event.preventDefault(); const button = $('#index'); button.disabled = true; button.setAttribute('aria-busy','true');
  try {
    const result = await API('/api/repos',{method:'POST',body:JSON.stringify({url:$('#repo-url').value.trim(),ref:$('#repo-ref').value.trim()})});
    state.selected = result.repository; if (result.job?.id) state.jobs.set(result.repository.id,result.job);
    await refreshRepos(); await selectRepository(state.repos.find(r=>r.id===result.repository.id)||result.repository);
    if (result.reused) toast('Existing repository job selected'); else toast('Index job queued');
    if (result.job?.id) pollJob(result.repository.id,result.job.id);
  } catch(error) { setStatus('#provider',error.message,'error'); }
  finally { button.disabled = false; button.removeAttribute('aria-busy'); }
});
$('#refresh').addEventListener('click', refreshRepos);

async function pollJob(repoID, jobID) {
  try {
    const job = await API(`/api/jobs/${encodeURIComponent(jobID)}`); state.jobs.set(repoID,job); renderRepos();
    if (state.selected?.id === repoID) {
      $('#repo-status').textContent = job.phase; $('#snapshot-counts').textContent = `${job.files_indexed} / ${job.files_seen} files · ${job.chunks} chunks · ${job.edges} edges`;
      $('#graph').hidden = true; $('#graph-empty').hidden = false; $('#graph-empty').innerHTML = `<span class="empty-icon">${job.phase === 'failed' ? '!' : '◌'}</span><strong>${esc(job.phase.replaceAll('_',' '))}</strong><p>${esc(job.safe_message || `${job.files_indexed} files indexed · ${job.chunks} chunks · ${job.edges} relations`)}</p>`;
      $('#question').disabled = true; $('#ask').disabled = true;
    }
    await refreshRepos();
    if (job.cancellable || ['queued','cloning','resolving','indexing'].includes(job.phase)) setTimeout(()=>pollJob(repoID,jobID),1100);
    else if (['ready','awaiting_credentials','failed','cancelled'].includes(job.phase)) {
      const repo=state.repos.find(r=>r.id===repoID); if(repo&&state.selected?.id===repoID) selectRepository(repo);
    }
  } catch(error) { if(state.selected?.id===repoID) setStatus('#question-status',error.message,'error'); }
}

function renderFileList(filterText = $('#catalog-search').value.toLowerCase()) {
  const root = $('#file-list'); root.replaceChildren(); if(!state.graph)return;
  const files=state.graph.files.filter(path=>path.toLowerCase().includes(filterText));
  const fragment=document.createDocumentFragment();
  for(const path of files){
    const item=document.createElement('button');item.type='button';item.className=`file-row${state.activeFile===path?' active':''}`;item.setAttribute('role','treeitem');item.title=path;
    item.innerHTML='<span class="file-icon" aria-hidden="true">▤</span>';item.append(document.createTextNode(path));item.addEventListener('click',()=>openFile(path));fragment.append(item);
    const symbols=[...state.nodes.values()].filter(node=>node.path===path&&(`${node.name} ${node.qualified_name}`.toLowerCase().includes(filterText)));
    for(const node of symbols){const row=document.createElement('button');row.type='button';row.className=`symbol-row${state.selectedNode?.id===node.id?' active':''}`;row.setAttribute('role','treeitem');row.title=`${node.qualified_name} · ${node.kind}`;row.innerHTML=`<span class="symbol-kind">${esc(kindGlyph(node.kind))}</span>${esc(node.name)}`;row.addEventListener('click',()=>selectNode(node.id));fragment.append(row);}
  }
  if(!files.length){const empty=document.createElement('p');empty.className='empty-state';empty.textContent='No matching files or symbols.';fragment.append(empty);}
  root.append(fragment);
}
function kindGlyph(kind){return({module:'M',class:'C',function:'ƒ',method:'ƒ',variable:'v'})[kind]||'·';}
$('#catalog-search').addEventListener('input',event=>renderFileList(event.target.value.toLowerCase()));
$('#catalog-search').addEventListener('keydown',event=>{if(event.key==='Enter'){const first=$('#file-list button');first?.click();}});

function filteredEdges(){const kind=$('#edge-filter').value;return state.edges.filter(edge=>kind==='all'||edge.kind===kind);}
function graphSlice(nodes,edges){
  const limit=100, degree=new Map(nodes.map(node=>[node.id,0])), neighbors=new Map(nodes.map(node=>[node.id,[]]));
  for(const edge of edges){if(!degree.has(edge.source)||!degree.has(edge.target))continue;degree.set(edge.source,degree.get(edge.source)+1);degree.set(edge.target,degree.get(edge.target)+1);neighbors.get(edge.source).push(edge.target);neighbors.get(edge.target).push(edge.source);}
  const rank=(a,b)=>(degree.get(b)-degree.get(a))||a.qualified_name.localeCompare(b.qualified_name);
  const byID=new Map(nodes.map(node=>[node.id,node])),visible=new Set(),seeds=[];
  if(state.selectedNode?.id&&byID.has(state.selectedNode.id))seeds.push(byID.get(state.selectedNode.id));
  else seeds.push(...[...nodes].sort(rank).slice(0,18));
  const add=node=>{if(node&&visible.size<limit&&!visible.has(node.id)){visible.add(node.id);return true;}return false;};
  seeds.forEach(add);
  let frontier=seeds;
  while(frontier.length&&visible.size<limit){const next=[];for(const node of frontier){const candidates=(neighbors.get(node.id)||[]).map(id=>byID.get(id)).filter(Boolean).sort(rank);for(const candidate of candidates)if(add(candidate))next.push(candidate);}frontier=next;}
  if(visible.size<limit)for(const node of [...nodes].sort(rank))if(!visible.has(node.id))add(node);
  const visibleNodes=[...visible].map(id=>byID.get(id)).filter(Boolean),labels=new Set();
  if(state.selectedNode){labels.add(state.selectedNode.id);const focused=neighbors.get(state.selectedNode.id)||[];focused.map(id=>byID.get(id)).filter(Boolean).sort(rank).slice(0,12).forEach(node=>labels.add(node.id));}
  else [...nodes].sort(rank).slice(0,12).forEach(node=>{if(visible.has(node.id))labels.add(node.id);});
  return {nodes:visibleNodes,edges:edges.filter(edge=>visible.has(edge.source)&&visible.has(edge.target)),labels,degree};
}
function layoutGraph(nodes,edges,selectedID){
  const points=new Map(),count=nodes.length,cx=500,cy=350,byID=new Map(nodes.map(node=>[node.id,node]));
  nodes.forEach((node,index)=>{const hash=[...node.id].reduce((sum,char)=>(sum*31+char.charCodeAt(0))>>>0,7),angle=(index/count)*Math.PI*2+(hash%1000)/1000,radius=selectedID?(node.id===selectedID?0:130+(index%4)*24):185+(index%3)*35;points.set(node.id,{x:cx+Math.cos(angle)*radius,y:cy+Math.sin(angle)*radius});});
  for(let iteration=0;iteration<70;iteration++){
    const forces=new Map(nodes.map(node=>[node.id,{x:0,y:0}])),temperature=Math.max(1,7*(1-iteration/70));
    for(let i=0;i<nodes.length;i++)for(let j=i+1;j<nodes.length;j++){
      const a=nodes[i],b=nodes[j],pa=points.get(a.id),pb=points.get(b.id),dx=pa.x-pb.x,dy=pa.y-pb.y,d2=Math.max(64,dx*dx+dy*dy),force=1400/d2,fa=forces.get(a.id),fb=forces.get(b.id);fa.x+=dx*force;fa.y+=dy*force;fb.x-=dx*force;fb.y-=dy*force;
    }
    for(const edge of edges){const a=points.get(edge.source),b=points.get(edge.target);if(!a||!b)continue;const dx=b.x-a.x,dy=b.y-a.y,d=Math.max(1,Math.hypot(dx,dy)),pull=(d-100)*.012,fa=forces.get(edge.source),fb=forces.get(edge.target);fa.x+=dx/d*pull;fa.y+=dy/d*pull;fb.x-=dx/d*pull;fb.y-=dy/d*pull;}
    for(const node of nodes){if(node.id===selectedID)continue;const p=points.get(node.id),f=forces.get(node.id);f.x+=(cx-p.x)*.003;f.y+=(cy-p.y)*.003;const scale=Math.min(temperature,7/Math.max(1,Math.hypot(f.x,f.y)));p.x=Math.max(25,Math.min(975,p.x+f.x*scale));p.y=Math.max(25,Math.min(675,p.y+f.y*scale));}
  }
  return points;
}
function drawGraph(){
  const svg=$('#graph'),world=$('#graph-world');world.replaceChildren(); if(!state.graph||!state.graph.nodes.length)return;
  let defs=svg.querySelector('defs');if(!defs){defs=document.createElementNS(svgNS,'defs');const marker=document.createElementNS(svgNS,'marker');marker.setAttribute('id','edge-arrow');marker.setAttribute('viewBox','0 0 10 10');marker.setAttribute('refX','9');marker.setAttribute('refY','5');marker.setAttribute('markerWidth','4');marker.setAttribute('markerHeight','4');marker.setAttribute('orient','auto-start-reverse');const arrow=document.createElementNS(svgNS,'path');arrow.setAttribute('d','M 0 0 L 10 5 L 0 10 z');arrow.setAttribute('fill','#665c54');marker.append(arrow);defs.append(marker);svg.insertBefore(defs,world);}
  const allNodes=[...state.nodes.values()],slice=graphSlice(allNodes,filteredEdges()),nodes=slice.nodes,positions=layoutGraph(nodes,slice.edges,state.selectedNode?.id);
  $('#graph-slice-status').textContent=state.selectedNode?`Focused · ${nodes.length} nearby symbols`:`Connected slice · ${nodes.length} of ${allNodes.length}`;
  for(const edge of slice.edges){
    const a=positions.get(edge.source),b=positions.get(edge.target);if(!a||!b)continue;
    const line=document.createElementNS(svgNS,'line');line.setAttribute('x1',a.x);line.setAttribute('y1',a.y);line.setAttribute('x2',b.x);line.setAttribute('y2',b.y);line.setAttribute('class',`edge ${edge.kind}`);line.setAttribute('marker-end','url(#edge-arrow)');line.dataset.source=edge.source;line.dataset.target=edge.target;world.append(line);
  }
  nodes.forEach(node=>{
    const point=positions.get(node.id),group=document.createElementNS(svgNS,'g');group.setAttribute('class',`node ${node.kind}${state.selectedNode?.id===node.id?' selected':''}`);group.setAttribute('transform',`translate(${point.x} ${point.y})`);group.setAttribute('tabindex','0');group.setAttribute('role','button');group.setAttribute('aria-label',`${node.kind} ${node.qualified_name} at ${node.path}:${node.start_line}`);group.dataset.id=node.id;
    const title=document.createElementNS(svgNS,'title');title.textContent=`${node.qualified_name} · ${node.path}:${node.start_line}`;group.append(title);
    const circle=document.createElementNS(svgNS,'circle');circle.setAttribute('r',node.kind==='module'?8:6);group.append(circle);
    if(slice.labels.has(node.id)){const label=document.createElementNS(svgNS,'text');label.setAttribute('x','10');label.setAttribute('y','3');label.textContent=node.name.length>23?`${node.name.slice(0,21)}…`:node.name;group.append(label);}
    group.addEventListener('click',()=>selectNode(node.id));group.addEventListener('keydown',event=>{if(event.key==='Enter'||event.key===' '){event.preventDefault();selectNode(node.id);}});world.append(group);
  });
  applyTransform();
}
let transformFrame = 0;
const renderedTransform = {tx: 0, ty: 0, scale: 1};
function applyTransform(smooth=false){
  cancelAnimationFrame(transformFrame);
  const from={...renderedTransform},to={tx:state.tx,ty:state.ty,scale:state.scale};
  const paint=progress=>{
    for(const key of ['tx','ty','scale'])renderedTransform[key]=from[key]+(to[key]-from[key])*progress;
    $('#graph-world').setAttribute('transform',`translate(${renderedTransform.tx} ${renderedTransform.ty}) scale(${renderedTransform.scale})`);
  };
  if(!smooth||reducedMotion.matches){paint(1);return;}
  const start=performance.now();
  const step=time=>{const progress=Math.min(1,(time-start)/180);paint(1-Math.pow(1-progress,3));if(progress<1)transformFrame=requestAnimationFrame(step);};
  transformFrame=requestAnimationFrame(step);
}
function zoom(delta, x=500,y=350){const old=state.scale;state.scale=Math.max(.45,Math.min(3.2,state.scale+delta));const k=state.scale/old;state.tx=x-(x-state.tx)*k;state.ty=y-(y-state.ty)*k;applyTransform(true);}
$('#zoom-in').addEventListener('click',()=>zoom(.2));$('#zoom-out').addEventListener('click',()=>zoom(-.2));$('#fit-graph').addEventListener('click',()=>{state.scale=1;state.tx=0;state.ty=0;applyTransform(true);});$('#edge-filter').addEventListener('change',drawGraph);
const graphSVG=$('#graph');let drag=null;
graphSVG.addEventListener('pointerdown',event=>{if(event.target.closest('.node'))return;cancelAnimationFrame(transformFrame);Object.assign(state,renderedTransform);drag={x:event.clientX,y:event.clientY,tx:state.tx,ty:state.ty};graphSVG.classList.add('panning');graphSVG.setPointerCapture(event.pointerId);});
graphSVG.addEventListener('pointermove',event=>{if(!drag)return;state.tx=drag.tx+(event.clientX-drag.x);state.ty=drag.ty+(event.clientY-drag.y);applyTransform();});
graphSVG.addEventListener('pointerup',()=>{drag=null;graphSVG.classList.remove('panning');});
graphSVG.addEventListener('pointercancel',()=>{drag=null;graphSVG.classList.remove('panning');});
reducedMotion.addEventListener('change',()=>{if(reducedMotion.matches){document.getAnimations().forEach(animation=>animation.cancel());applyTransform();}});
graphSVG.addEventListener('wheel',event=>{event.preventDefault();zoom(event.deltaY<0?.08:-.08,event.offsetX,event.offsetY);},{passive:false});

async function selectNode(id){const node=state.nodes.get(id);if(!node)return;state.selectedNode=node;state.activeFile=node.path;renderFileList();drawGraph();$('#node-kind').textContent=node.kind;
  const incident=state.edges.filter(edge=>edge.source===id||edge.target===id);const related=incident.map(edge=>({edge,node:state.nodes.get(edge.source===id?edge.target:edge.source),direction:edge.source===id?'→':'←'})).filter(item=>item.node);
  $('#inspector-content').innerHTML=`<h3 class="symbol-title">${esc(node.name)}</h3><p class="symbol-qualified">${esc(node.qualified_name)}</p><a class="symbol-location" href="#source-panel" data-source-path="${esc(node.path)}" data-line="${node.start_line}">${esc(node.path)}:${node.start_line}–${node.end_line} ↗</a>${node.signature?`<pre class="signature">${esc(node.signature)}</pre>`:''}<button class="text-button open-source" type="button">Open source</button><h4 class="inspector-subhead">Relationships · ${related.length}</h4>${related.slice(0,60).map(item=>`<button class="relation-link" data-node="${esc(item.node.id)}"><span>${item.direction} ${esc(item.node.name)}</span><small>${esc(item.edge.kind)}</small></button>`).join('')||'<p class="panel-copy">No resolved relationships for this symbol.</p>'}`;
  $('#inspector-content .open-source').addEventListener('click',()=>openFile(node.path,node.start_line,node.end_line));
  $$('.relation-link','#inspector-content').forEach(button=>button.addEventListener('click',()=>selectNode(button.dataset.node)));
  $('.symbol-location','#inspector-content').addEventListener('click',event=>{event.preventDefault();openFile(node.path,node.start_line,node.end_line);});
  reveal($('#inspector-content'));
  await openFile(node.path,node.start_line,node.end_line,false);
}
function $$(selector,root=document){root=typeof root==='string'?document.querySelector(root):root;return [...root.querySelectorAll(selector)];}
async function openFile(path,start=1,end=80,showSourceTab=true){if(!state.selected)return;state.activeFile=path;renderFileList();
  try{const query=new URLSearchParams({path,start:String(Math.max(1,start)),end:String(Math.max(start,end))});const source=await API(`${apiPath(state.selected.id,'source')}?${query}`);$('#source-panel').hidden=false;$('#source-path').textContent=source.path;$('#source-range').textContent=`lines ${source.start_line}–${source.end_line} · ${source.language}`;const code=$('#source-code code');code.replaceChildren();code.textContent=source.content.split('\n').map((line,index)=>`${source.start_line+index}`.padStart(4,' ')+'  '+line).join('\n');if(showSourceTab)setView('source');reveal($('#source-code'));}
  catch(error){toast(`Could not open source: ${error.message}`);}
}
$('#source-close').addEventListener('click',()=>$('#source-panel').hidden=true);
function setView(mode){state.graphMode=mode==='graph';$('#graph-tab').classList.toggle('active',state.graphMode);$('#graph-tab').setAttribute('aria-selected',String(state.graphMode));$('#source-tab').classList.toggle('active',!state.graphMode);$('#source-tab').setAttribute('aria-selected',String(!state.graphMode));$('#graph-controls').hidden=!state.graphMode;$('.explorer-grid').hidden=!state.graphMode;$('#source-panel').hidden=state.graphMode||!state.activeFile;$('#source-panel').classList.toggle('standalone',!state.graphMode);}
$('#graph-tab').addEventListener('click',()=>{setView('graph');reveal($('.explorer-grid'));});$('#source-tab').addEventListener('click',()=>{setView('source');if(state.activeFile)openFile(state.activeFile,1,100,false);});

$('#ask-form').addEventListener('submit',async event=>{event.preventDefault();if(!state.selected||!$('#question').value.trim())return;const button=$('#ask');button.disabled=true;button.setAttribute('aria-busy','true');$('#answer').innerHTML='<p class="answer-state">Retrieving indexed evidence…</p>';setStatus('#question-status',state.chatConfigured?'Preparing a cited response using hosted chat…':'Searching indexed text with citations…');
  try{const endpoint=state.chatConfigured?'/api/answer':'/api/context';const output=await API(endpoint,{method:'POST',body:JSON.stringify({repository_id:state.selected.id,question:$('#question').value.trim(),session_id:state.sessionID})});const pack=state.chatConfigured?output.answer.context:output.context;state.sessionID=output.session.id;storeSession(state.selected.id,state.sessionID);renderSession(output.session,[]);
    const generated=state.chatConfigured?`<p class="answer-intro">${linkCitations(output.answer.text)}</p>`:'';const degraded=pack.retrieval.degraded?'<p class="answer-state">Lexical retrieval only · no hosted embedding query credentials are available.</p>':'';
    const evidences=pack.evidence.map(e=>`<article class="evidence"><div class="evidence-head"><a class="citation-link" href="#source-panel" data-path="${esc(e.path)}" data-start="${e.start_line}" data-end="${e.end_line}">[${esc(e.id)}] ${esc(e.path)}:${e.start_line}–${e.end_line}</a><span class="evidence-symbol">${esc(e.symbol||'module')}</span></div><pre>${esc(e.snippet)}</pre><small class="evidence-meta">${esc((e.retrieval_sources||[]).join(' · '))} · SHA ${esc((e.excerpt_sha256||'').slice(0,12))}</small></article>`).join('');
    $('#answer').innerHTML=`${generated}${degraded}${evidences||'<p class="answer-state">No evidence matched this question in the selected snapshot.</p>'}`;
    reveal($('#answer'));
    $$('.citation-link','#answer').forEach(link=>link.addEventListener('click',event=>{event.preventDefault();openFile(link.dataset.path,Number(link.dataset.start),Number(link.dataset.end));}));
    $$('.inline-citation','#answer').forEach(link=>link.addEventListener('click',event=>{event.preventDefault();const citation=pack.evidence.find(item=>item.id===link.dataset.citation);if(citation)openFile(citation.path,citation.start_line,citation.end_line);}));
    setStatus('#question-status',state.chatConfigured?'Answer linked to retrieved source evidence.':'Context pack ready · configure hosted chat separately to generate an answer.');
    await loadSession();
  }catch(error){$('#answer').innerHTML=`<p class="answer-state error">${esc(error.message)}</p>`;setStatus('#question-status',error.message,'error');}
  finally{button.disabled=false;button.removeAttribute('aria-busy');}
});
$('#question').addEventListener('keydown',event=>{if(event.key==='Enter'&&!event.shiftKey&&!event.isComposing){event.preventDefault();$('#ask-form').requestSubmit();}});
function linkCitations(text){const escaped=esc(text);return escaped.replace(/\[(e\d+)\]/g,'<a class="citation-link inline-citation" href="#source-panel" data-citation="$1">[$1]</a>');}

function sessionStorageKey(repoID){return `cornifer-session:${repoID}`;}
function readStoredSession(repoID){try{return localStorage.getItem(sessionStorageKey(repoID))||'';}catch{return '';}}
function storeSession(repoID,sessionID){try{localStorage.setItem(sessionStorageKey(repoID),sessionID);}catch{}}
function forgetStoredSession(repoID){try{localStorage.removeItem(sessionStorageKey(repoID));}catch{}}
async function loadSession(){if(!state.sessionID)return;try{const out=await API(`/api/sessions/${encodeURIComponent(state.sessionID)}`);if(out.session.repository_id!==state.selected?.id||out.session.commit_sha!==state.selected?.resolved_commit_sha||out.session.stale||Date.parse(out.session.expires_at)<=Date.now()){forgetStoredSession(state.selected?.id);state.sessionID='';$('#clear-session').disabled=true;return;}renderSession(out.session,out.events||[]);}catch(error){forgetStoredSession(state.selected?.id);state.sessionID='';$('#clear-session').disabled=true;setStatus('#question-status',error.message,'error');}}
function renderSession(session,events){$('#remember-form').hidden=false;$('#remember').disabled=false;$('#clear-session').disabled=false;$('#session').textContent=`${session.commit_sha.slice(0,10)} · this snapshot only · expires ${new Date(session.expires_at).toLocaleDateString()}`;const root=$('#memory-events');root.replaceChildren();for(const event of [...events].reverse()){const row=document.createElement('div');row.className='memory-event';const date=document.createElement('small');date.textContent=new Date(event.created_at).toLocaleString();const payload=event.payload;const body=event.kind==='note'?(payload?.note||''):`Searched: ${payload?.question||payload?.context?.query||'repository context'}`;row.append(date,document.createTextNode(body));root.append(row);}}
$('#remember-form').addEventListener('submit',async event=>{event.preventDefault();if(!state.sessionID)return;try{await API(`/api/sessions/${encodeURIComponent(state.sessionID)}/remember`,{method:'POST',body:JSON.stringify({note:$('#note').value})});$('#note').value='';toast('Note saved to this session');await loadSession();}catch(error){setStatus('#question-status',error.message,'error');}});
$('#clear-session').addEventListener('click',async()=>{if(!state.sessionID)return;try{await API(`/api/sessions/${encodeURIComponent(state.sessionID)}`,{method:'DELETE'});forgetStoredSession(state.selected?.id);state.sessionID='';$('#memory-events').replaceChildren();$('#remember-form').hidden=true;$('#clear-session').disabled=true;$('#session').textContent='Session memory cleared.';toast('Session memory cleared');}catch(error){toast(error.message);}});

function copyMCP(){
  const input=document.createElement('textarea');input.value=MCP_URL;input.setAttribute('readonly','');input.style.position='fixed';input.style.opacity='0';document.body.append(input);input.select();
  let copied=false;try{copied=document.execCommand('copy');}catch{}input.remove();
  const status=$('#copy-status');
  if(copied){status.textContent='MCP endpoint copied to clipboard';toast('MCP endpoint copied');return;}
  status.textContent='Clipboard unavailable; copy the endpoint shown above.';toast('Clipboard unavailable');
  try{navigator.clipboard?.writeText(MCP_URL).then(()=>{status.textContent='MCP endpoint copied to clipboard';toast('MCP endpoint copied');}).catch(()=>{});}catch{}
}
$('#copy-mcp').addEventListener('click',copyMCP);$('#mcp-endpoint').addEventListener('click',copyMCP);
$('#coverage-info').addEventListener('click',()=>toast('Python has structural symbols and edges. Other indexed languages are text retrieval only.'));
document.addEventListener('keydown',event=>{if((event.metaKey||event.ctrlKey)&&event.key.toLowerCase()==='k'){event.preventDefault();$('#catalog-search').focus();$('#files-section').hidden=false;}if(event.key==='Escape'&&document.activeElement===$('#catalog-search')){$('#catalog-search').blur();}});
init();
