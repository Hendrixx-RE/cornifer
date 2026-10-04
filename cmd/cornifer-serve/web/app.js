(() => {
  'use strict';
  const $ = selector => document.querySelector(selector);
  const state = {repo:null, repos:[], job:null, pack:null, sessionID:'', health:null, epoch:0, sourceRequest:0, pending:false, scale:1, x:0, y:0};
  const motion = matchMedia('(prefers-reduced-motion: reduce)');
  const svgNS = 'http://www.w3.org/2000/svg';
  const esc = value => String(value ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
  const name = repo => (repo?.canonical_url || '').replace(/^https:\/\/github.com\//, '').replace(/\.git$/, '');
  const sessionKey = repo => `cornifer-session:${repo.id}`;
  const api = async (path, options={}) => {
    const response = await fetch(path,{headers:{'content-type':'application/json'},...options});
    const result = await response.json().catch(()=>({}));
    if (!response.ok) { const error = new Error(result.error || `Request failed (${response.status})`); error.status=response.status; throw error; }
    return result;
  };
  const post = (path, body) => api(path,{method:'POST',body:JSON.stringify(body)});
  const status = (selector, text, error=false) => { const target=$(selector);target.textContent=text;target.classList.toggle('error',error); };
  function reveal(element) { if (!motion.matches && element.animate) element.animate([{opacity:.5,transform:'translateY(5px)'},{opacity:1,transform:'translateY(0)'}],{duration:220,easing:'ease-out'}); }
  function view(mode) { $('#main').dataset.view=mode;for(const key of ['entry','indexing','query']) $(`#${key}`).hidden=key!==mode;reveal($(`#${mode}`)); }
  function saveSession() { if(!state.repo)return;try { if(state.sessionID)localStorage.setItem(sessionKey(state.repo),state.sessionID);else localStorage.removeItem(sessionKey(state.repo)); } catch {} }
  function readSession() { try{return localStorage.getItem(sessionKey(state.repo))||'';}catch{return '';} }
  function clearPresentation() { state.sourceRequest++;state.pack=null;$('#results').hidden=true;$('#query').classList.remove('has-results');$('#source').hidden=true;$('#question').value=''; }
  function changeRepo() { state.epoch++;state.repo=null;state.job=null;state.sessionID='';state.pending=false;$('#start').disabled=false;$('#ask').disabled=false;clearPresentation();view('entry');status('#entry-status','A public repository. A pinned snapshot. Your questions.');$('#repo-url').focus(); }
  $('#change-repo').addEventListener('click',changeRepo);$('#index-back').addEventListener('click',changeRepo);
  function validURL(raw) {
    try { const url=new URL(raw);const path=url.pathname.replace(/^\/+|\/+$/g,'').replace(/\.git$/,'');return url.protocol==='https:'&&['github.com','www.github.com'].includes(url.hostname)&&!url.port&&!url.username&&!url.password&&!url.search&&!url.hash&&/^[^/]+\/[^/]+$/.test(path)&&!path.split('/').some(part=>part==='.'||part==='..'); }catch{return false;}
  }
  async function ingest(url,ref) {
    if(!validURL(url)) { status('#entry-status','Use a public https://github.com/owner/repository URL, without credentials or a file path.',true);$('#repo-url').focus();return; }
    const epoch=++state.epoch;state.pending=true;$('#start').disabled=true;state.job=null;view('indexing');status('#phase','Queuing repository');$('#index-name').textContent=url;$('#index-detail').textContent='';$('#loading-mark').hidden=false;$('#cancel-job').hidden=true;$('#retry-job').hidden=true;
    try {
      const result=await post('/api/repos',{url,ref});if(epoch!==state.epoch)return;
      state.repo=result.repository;state.job=result.job;state.sessionID='';await refreshRepos();
      if(result.repository.status==='ready')await ready(result.repository,epoch);else if(result.job?.id)await poll(result.job.id,epoch);else showJob(null,result.repository);
    }catch(error){if(epoch===state.epoch){$('#loading-mark').hidden=true;status('#phase','Could not start indexing',true);status('#index-detail',error.message,true);}}
    finally{if(epoch===state.epoch){state.pending=false;$('#start').disabled=false;}}
  }
  $('#repo-form').addEventListener('submit',event=>{event.preventDefault();if(!state.pending)ingest($('#repo-url').value.trim(),$('#repo-ref').value.trim());});
  const phaseText = {queued:'Waiting for index worker',cloning:'Cloning repository',resolving:'Pinning the requested commit',indexing:'Preparing the index',parse:'Parsing source files',graph:'Resolving Python relationships',embed:'Embedding Python source',store:'Saving source and lexical index',ready:'Snapshot ready',awaiting_credentials:'Embedding credentials required',failed:'Indexing failed',cancelled:'Indexing cancelled',stale:'Snapshot unavailable'};
  function showJob(job,repo) {
    const phase=job?.phase||repo.status;const active=!!job?.cancellable||['queued','cloning','resolving','indexing','parse','graph','embed','store'].includes(phase);
    $('#loading-mark').hidden=!active;status('#phase',phaseText[phase]||phase,['failed','awaiting_credentials'].includes(phase));$('#index-name').textContent=`${name(repo)}${repo.resolved_commit_sha?' · '+repo.resolved_commit_sha.slice(0,10):''}`;
    $('#cancel-job').hidden=!job?.cancellable;$('#cancel-job').disabled=!!job?.cancel_requested;$('#cancel-job').textContent=job?.cancel_requested?'Cancellation requested':'Cancel indexing';
    $('#retry-job').hidden=!['failed','cancelled','awaiting_credentials'].includes(phase);
    const explanations={awaiting_credentials:'Configure Voyage embeddings in the server environment, restart, then choose Retry indexing. The resolved commit stays pinned. No automatic resume.',failed:repo.safe_message||job?.safe_message||'Check the public URL, ref, database and provider configuration, then retry.',cancelled:'This job stopped. Retry starts a new indexing attempt.',stale:'Choose another ready snapshot or submit a new repository.'};
    $('#index-detail').textContent=explanations[phase]||(job?.files_seen?`${job.files_seen} files discovered${job.chunks?' · '+job.chunks+' chunks':''}${job.edges?' · '+job.edges+' resolved relations':''}`:'Progress reflects the running job.');
  }
  async function poll(jobID,epoch) {
    try {
      const [job,repo]=await Promise.all([api(`/api/jobs/${encodeURIComponent(jobID)}`),api(`/api/repos/${encodeURIComponent(state.repo.id)}`)]);if(epoch!==state.epoch)return;
      state.job=job;state.repo=repo;showJob(job,repo);
      if(job.phase==='ready'&&repo.status==='ready'){await refreshRepos();await ready(repo,epoch);return;}
      if(job.cancellable||['queued','cloning','resolving','indexing','parse','graph','embed','store'].includes(job.phase))setTimeout(()=>poll(jobID,epoch),1100);
    }catch(error){if(epoch===state.epoch){$('#loading-mark').hidden=true;status('#phase','Status unavailable',true);status('#index-detail',`${error.message}. Reopen this snapshot in Settings to check again.`,true);}}
  }
  $('#cancel-job').addEventListener('click',async()=>{if(!state.job)return;try{await post(`/api/jobs/${state.job.id}/cancel`,{});$('#cancel-job').disabled=true;$('#cancel-job').textContent='Cancellation requested';}catch(error){status('#index-detail',error.message,true);}});
  $('#retry-job').addEventListener('click',()=>{if(state.repo)ingest(state.repo.canonical_url,state.repo.requested_ref||'');});
  async function ready(repo,epoch) {
    if(epoch!==state.epoch)return;state.pending=false;$('#ask').disabled=false;$('#ask').removeAttribute('aria-busy');state.repo=repo;state.job=null;state.sessionID=readSession();clearPresentation();$('#repo-name').textContent=name(repo);$('#commit').textContent=repo.resolved_commit_sha.slice(0,10);$('#commit').title=repo.resolved_commit_sha;$('#fixture-label').hidden=!repo.safe_message?.startsWith('Verification fixture:');view('query');
    status('#question-status',state.health?.chat_configured?'Ask a question. Answers use cited source context.':'Ask a question. Cited evidence is available; hosted answer generation is not configured.');
    await loadSession();$('#question').focus();
  }
  async function refreshHealth() {
    try{state.health=await api('/api/health');status('#provider-state',`Embeddings: ${state.health.embedding_configured?'configured ('+state.health.embedding_provider+')':'credentials required'} · Chat: ${state.health.chat_configured?'configured':'not configured'}.`);}
    catch(error){state.health=null;status('#provider-state','Companion unavailable: '+error.message,true);status('#entry-status','Local companion unavailable. Check the server, then refresh.',true);}
  }
  async function refreshRepos() {
    try{state.repos=(await api('/api/repos'))||[];const select=$('#saved-repo'),chosen=select.value;select.replaceChildren(new Option('Choose a saved snapshot…',''));for(const repo of state.repos)select.add(new Option(`${name(repo)} · ${repo.status} · ${repo.resolved_commit_sha?.slice(0,10)||repo.requested_ref||'HEAD'}${repo.safe_message?.startsWith('Verification fixture:')?' · verification fixture':''}`,repo.id));select.value=state.repos.some(repo=>repo.id===chosen)?chosen:'';$('#existing-open').hidden=!state.repos.some(repo=>repo.status==='ready');}
    catch(error){status('#provider-state',error.message,true);}
  }
  async function openSnapshot() {
    const repo=state.repos.find(repo=>repo.id===$('#saved-repo').value);if(!repo){status('#provider-state','Choose a snapshot first.',true);return;}
    $('#settings').close();const epoch=++state.epoch;state.repo=repo;state.sessionID='';if(repo.status==='ready'){await ready(repo,epoch);return;}
    view('indexing');showJob(null,repo);try{state.job=await api(`/api/repos/${repo.id}/job`);if(epoch===state.epoch)await poll(state.job.id,epoch);}catch(error){if(epoch===state.epoch){$('#loading-mark').hidden=true;status('#index-detail',error.message,true);}}
  }
  $('#open-snapshot').addEventListener('click',openSnapshot);
  function openSettings() { $('#settings').showModal();refreshHealth();refreshRepos();loadSession(); }
  $('#settings-open').addEventListener('click',openSettings);$('#existing-open').addEventListener('click',()=>{openSettings();$('#saved-repo').focus();});$('#settings-close').addEventListener('click',()=>$('#settings').close());$('#refresh-repos').addEventListener('click',()=>{refreshHealth();refreshRepos();});
  function inlineCitations(text) { return esc(text).replace(/\[(e\d+)\]/g,'<a href="#source" data-citation="$1">[$1]</a>'); }
  $('#question').addEventListener('input',()=>{const input=$('#question');input.style.height='auto';input.style.height=Math.min(170,input.scrollHeight)+'px';});
  $('#question').addEventListener('keydown',event=>{if(event.key==='Enter'&&!event.shiftKey&&!event.isComposing){event.preventDefault();$('#question-form').requestSubmit();}});
  $('#question-form').addEventListener('submit',async event=>{
    event.preventDefault();if(state.pending||!state.repo||!$('#question').value.trim())return;
    const question=$('#question').value.trim(),epoch=state.epoch,repoID=state.repo.id;state.pending=true;$('#ask').disabled=true;$('#ask').setAttribute('aria-busy','true');state.sourceRequest++;$('#source').hidden=true;status('#question-status',state.health?.chat_configured?'Retrieving source and generating a grounded answer…':'Retrieving cited source evidence…');
    try{
      const generate=!!state.health?.chat_configured;const output=await post(generate?'/api/answer':'/api/context',{repository_id:repoID,question,session_id:state.sessionID});if(epoch!==state.epoch)return;
      state.sessionID=output.session.id;saveSession();state.pack=generate?output.answer.context:output.context;renderResults(question,generate&&output.answer.status==='ok'?output.answer:null);await loadSession();status('#question-status',generate&&output.answer.status==='ok'?'Grounded answer with source citations.':(state.pack.evidence||[]).length?'Source evidence only · configure hosted chat separately for a generated answer.':'No matching source evidence. Try terms used in the repository.');
    }catch(error){if(epoch===state.epoch){status('#question-status',error.message+(state.health?.chat_configured?' Hosted generation failed; no answer was fabricated.':''),true);if(/session.*(expired|stale|different)|not found/i.test(error.message)){state.sessionID='';saveSession();}}}
    finally{if(epoch===state.epoch){state.pending=false;$('#ask').disabled=false;$('#ask').removeAttribute('aria-busy');}}
  });
  function sourceButton(symbol,label) { return `<button class="source-link" data-symbol="${esc(symbol.id)}">${esc(label||symbol.qualified_name)}</button>`; }
  function renderResults(question,answer) {
    const pack=state.pack,evidence=pack.evidence||[],symbols=pack.symbols||[],relations=pack.relationships||[],byID=new Map(symbols.map(symbol=>[symbol.id,symbol]));
    $('#results').hidden=false;$('#query').classList.add('has-results');$('#asked-question').textContent=question;$('#result-mode').textContent=answer?'Grounded answer':'Cited source evidence';$('#retrieval-mode').textContent=(pack.retrieval.systems_used||[]).join(' · ');
    $('#answer').innerHTML=answer?inlineCitations(answer.text):evidence.length?'Relevant source is below. Hosted chat is not configured, so this is retrieved evidence, not a generated answer.':'No source evidence matched this question in the selected snapshot. Try a class, function, filename, or terms used in the code.';
    const direct=symbols.filter(symbol=>symbol.evidence);
    $('#symbols').innerHTML=direct.map(symbol=>`<details class="symbol-card"><summary><span class="symbol-kind">${esc(symbol.kind)}</span>${esc(symbol.qualified_name)}</summary><pre class="symbol-signature">${esc(symbol.signature||'No signature recorded.')}</pre>${sourceButton(symbol,`${symbol.path}:${symbol.start_line}–${symbol.end_line} ↗`)}<p class="hint">${esc(symbolRoles(symbol,relations))}</p></details>`).join('');
    $('#dependencies').hidden=!relations.length;$('#dependencies').open=false;$('#relation-count').textContent=`(${relations.length})`;$('#relation-list').innerHTML=relations.map(relation=>{const from=byID.get(relation.from_symbol_id),to=byID.get(relation.to_symbol_id);return `<div class="relation">${from?sourceButton(from,from.name):esc(relation.from_symbol)}<span class="relation-type">${esc(relation.kind)} →</span>${to?sourceButton(to,to.name):esc(relation.to_symbol)}<span class="confidence">confidence ${Number(relation.confidence).toFixed(2)}</span></div>`;}).join('');
    $('#evidence-count').textContent=`(${evidence.length})`;$('#evidence-details').hidden=!evidence.length;$('#evidence-details').open=!answer&&evidence.length>0;$('#evidence').innerHTML=evidence.map(citation=>`<article class="evidence"><div class="evidence-head"><button class="source-link" data-citation="${esc(citation.id)}">[${esc(citation.id)}] ${esc(citation.path)}:${citation.start_line}–${citation.end_line} ↗</button>${citation.symbol?`<span class="hint">${esc(citation.symbol)}</span>`:''}</div><pre>${esc(citation.snippet)}</pre><p class="hint">${esc((citation.retrieval_sources||[]).join(' · '))} · excerpt SHA-256 ${esc(citation.excerpt_sha256)}${citation.truncated?' · excerpt truncated':''}</p></article>`).join('');
    $('#snapshot').textContent=`Snapshot ${pack.repository.commit_sha}${pack.omitted.evidence_count?' · '+pack.omitted.evidence_count+' evidence items omitted ('+pack.omitted.reason+')':''}`;
    $('#coverage').textContent='Structural dependencies are resolved Python relationships only. Other supported source and documentation have lexical evidence, without a structural graph.';
    renderGraph(symbols,relations);reveal($('#results'));
  }
  function symbolRoles(symbol,relationships) {
    const callers=relationships.filter(edge=>edge.to_symbol_id===symbol.id&&edge.kind==='calls').length,callees=relationships.filter(edge=>edge.from_symbol_id===symbol.id&&edge.kind==='calls').length,dependencies=relationships.filter(edge=>edge.from_symbol_id===symbol.id&&edge.kind!=='calls').length;
    return `${callers} callers · ${callees} callees · ${dependencies} other outgoing dependencies in this bounded context. ${relationships.some(edge=>edge.from_symbol_id===symbol.id||edge.to_symbol_id===symbol.id)?'Expand dependencies for names and confidence.':'No resolved relationships in this context.'}`;
  }
  $('#results').addEventListener('click',event=>{const target=event.target.closest('[data-citation],[data-symbol]');if(!target||!state.pack)return;event.preventDefault();const item=target.dataset.citation?state.pack.evidence.find(citation=>citation.id===target.dataset.citation):state.pack.symbols.find(symbol=>symbol.id===target.dataset.symbol);if(item)openSource(item);});
  async function openSource(item) {
    const epoch=state.epoch,request=++state.sourceRequest,repoID=state.repo.id;$('#source').hidden=false;$('#source-title').textContent=`${item.path}:${item.start_line}–${item.end_line}`;$('#source-code').textContent='Loading pinned source…';$('#source-snapshot').textContent=`Snapshot ${state.pack.repository.commit_sha}`;
    try{const params=new URLSearchParams({path:item.path,start:item.start_line,end:item.end_line});const source=await api(`/api/repos/${repoID}/source?${params}`);if(epoch!==state.epoch||request!==state.sourceRequest)return;$('#source-code').textContent=source.content.split('\n').map((line,index)=>String(source.start_line+index).padStart(4,' ')+'  '+line).join('\n');$('#source-title').textContent=`${source.path}:${source.start_line}–${source.end_line}`;reveal($('#source'));$('#source').scrollIntoView({behavior:motion.matches?'auto':'smooth',block:'nearest'});}
    catch(error){if(epoch===state.epoch&&request===state.sourceRequest)$('#source-code').textContent='Source unavailable: '+error.message;}
  }
  $('#source-close').addEventListener('click',()=>$('#source').hidden=true);$('#follow-up').addEventListener('click',()=>{$('#question').value='';$('#question').style.height='auto';$('#question').focus();$('#question').scrollIntoView({behavior:motion.matches?'auto':'smooth',block:'center'});});
  function renderGraph(symbols,relations) {
    const world=$('#graph-world');world.replaceChildren();state.scale=1;state.x=0;state.y=0;transformGraph();if(!relations.length)return;
    const connected=new Set(relations.flatMap(edge=>[edge.from_symbol_id,edge.to_symbol_id]));const nodes=symbols.filter(symbol=>connected.has(symbol.id));const positions=new Map();const direct=nodes.filter(symbol=>symbol.evidence),neighbors=nodes.filter(symbol=>!symbol.evidence);
    const arrange=(list,x)=>list.forEach((symbol,index)=>positions.set(symbol.id,{x,y:28+(index+1)*(280/(list.length+1))}));
    if(neighbors.length){arrange(direct,230);arrange(neighbors,560);}else nodes.forEach((symbol,index)=>positions.set(symbol.id,{x:380+Math.cos(index/nodes.length*Math.PI*2)*225,y:170+Math.sin(index/nodes.length*Math.PI*2)*120}));
    for(const edge of relations){const a=positions.get(edge.from_symbol_id),b=positions.get(edge.to_symbol_id);if(!a||!b)continue;const line=document.createElementNS(svgNS,'line');Object.entries({x1:a.x,y1:a.y,x2:b.x,y2:b.y,class:'edge'}).forEach(([key,value])=>line.setAttribute(key,value));const title=document.createElementNS(svgNS,'title');title.textContent=`${edge.kind} · confidence ${edge.confidence}`;line.append(title);world.append(line);}
    for(const symbol of nodes){const point=positions.get(symbol.id),group=document.createElementNS(svgNS,'g');Object.entries({transform:`translate(${point.x} ${point.y})`,class:`node${symbol.evidence?' direct':''}`,tabindex:'0',role:'button','aria-label':`Open ${symbol.qualified_name} source`}).forEach(([key,value])=>group.setAttribute(key,value));const circle=document.createElementNS(svgNS,'circle');circle.setAttribute('r',8);const label=document.createElementNS(svgNS,'text');label.setAttribute('x',13);label.setAttribute('y',4);label.textContent=symbol.name.slice(0,24);const title=document.createElementNS(svgNS,'title');title.textContent=symbol.qualified_name;group.append(circle,label,title);group.addEventListener('click',()=>openSource(symbol));group.addEventListener('keydown',event=>{if(event.key==='Enter'||event.key===' '){event.preventDefault();openSource(symbol);}});world.append(group);}
  }
  function transformGraph(){ $('#graph-world').setAttribute('transform',`translate(${state.x} ${state.y}) scale(${state.scale})`);$('#dependency-graph').setAttribute('aria-label',`Answer-relevant dependency graph. Zoom ${Math.round(state.scale*100)}%. Position ${Math.round(state.x)}, ${Math.round(state.y)}.`); }
  function zoom(delta){state.scale=Math.max(.5,Math.min(2.5,state.scale+delta));transformGraph();}
  $('#zoom-in').addEventListener('click',()=>zoom(.15));$('#zoom-out').addEventListener('click',()=>zoom(-.15));$('#graph-fit').addEventListener('click',()=>{state.scale=1;state.x=state.y=0;transformGraph();});
  const graph=$('#dependency-graph');let drag=null;
  graph.addEventListener('pointerdown',event=>{if(event.target.closest('.node'))return;const bounds=graph.getBoundingClientRect();drag={x:event.clientX,y:event.clientY,tx:state.x,ty:state.y,ratio:760/bounds.width};graph.setPointerCapture(event.pointerId);graph.classList.add('panning');});
  graph.addEventListener('pointermove',event=>{if(drag){state.x=drag.tx+(event.clientX-drag.x)*drag.ratio;state.y=drag.ty+(event.clientY-drag.y)*drag.ratio;transformGraph();}});
  const endDrag=()=>{drag=null;graph.classList.remove('panning');};graph.addEventListener('pointerup',endDrag);graph.addEventListener('pointercancel',endDrag);graph.addEventListener('wheel',event=>{event.preventDefault();zoom(event.deltaY<0?.1:-.1);},{passive:false});
  graph.addEventListener('keydown',event=>{if(event.target!==graph)return;const moves={ArrowLeft:[20,0],ArrowRight:[-20,0],ArrowUp:[0,20],ArrowDown:[0,-20]};if(moves[event.key]){event.preventDefault();state.x+=moves[event.key][0];state.y+=moves[event.key][1];transformGraph();}});
  async function loadSession() {
    $('#note-form').hidden=!state.sessionID;$('#session-id').textContent=state.sessionID;$('#memory-events').replaceChildren();
    if(!state.sessionID){$('#session-status').textContent='A successful question starts a session for this snapshot.';return;}
    const id=state.sessionID,epoch=state.epoch;
    try{const out=await api(`/api/sessions/${id}`);if(epoch!==state.epoch||id!==state.sessionID)return;if(out.session.repository_id!==state.repo.id||out.session.commit_sha!==state.repo.resolved_commit_sha||out.session.stale||Date.parse(out.session.expires_at)<=Date.now()){state.sessionID='';saveSession();return loadSession();}
      $('#session-status').textContent=`This snapshot only · expires ${new Date(out.session.expires_at).toLocaleDateString()}`;$('#session-id').textContent=id;$('#memory-events').innerHTML=(out.events||[]).slice(0,12).map(event=>`<div class="memory-event">${esc(event.kind==='note'?event.payload.note:'Asked: '+event.payload.question)}</div>`).join('');
    }catch(error){if(epoch===state.epoch){state.sessionID='';saveSession();$('#note-form').hidden=true;$('#session-id').textContent='';status('#session-status',error.message,true);}}
  }
  $('#note-form').addEventListener('submit',async event=>{event.preventDefault();if(!state.sessionID)return;try{await post(`/api/sessions/${state.sessionID}/remember`,{note:$('#note').value});$('#note').value='';await loadSession();}catch(error){status('#session-status',error.message,true);}});
  $('#clear-session').addEventListener('click',async()=>{if(!state.sessionID)return;try{await api(`/api/sessions/${state.sessionID}`,{method:'DELETE'});state.sessionID='';saveSession();await loadSession();status('#session-status','Session and its events cleared. The next question starts a new one.');}catch(error){status('#session-status',error.message,true);}});
  const mcpURL = `${location.origin}/mcp`;$('#mcp-url').textContent=mcpURL;
  async function copy(text,label){let copied=false;try{if(navigator.clipboard){await navigator.clipboard.writeText(text);copied=true;}}catch{}if(!copied){const input=document.createElement('textarea');input.value=text;$('#settings').append(input);input.select();try{copied=document.execCommand('copy');}catch{}input.remove();}status('#copy-status',copied?`${label} copied.`:`Clipboard unavailable. ${text}`,!copied);}
  $('#copy-mcp').addEventListener('click',()=>copy(mcpURL,'Endpoint'));$('#copy-config').addEventListener('click',()=>copy(JSON.stringify({mcpServers:{'cornifer-companion':{url:mcpURL}}},null,2),'Client config'));
  motion.addEventListener('change',()=>{if(motion.matches)document.getAnimations().forEach(animation=>animation.cancel());});
  async function init(){await refreshHealth();await refreshRepos();$('#repo-url').focus();}
  init();
})();
