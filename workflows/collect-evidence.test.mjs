import assert from 'node:assert/strict';
import fs from 'node:fs';
import {test} from 'node:test';
const body=fs.readFileSync(new URL('./collect-evidence.js',import.meta.url),'utf8');
const invoke=new (Object.getPrototypeOf(async function(){}).constructor)('args','platform','apps',body);
const key='KL4141RCEFR';
function batch(source) {return {schema_version:'aifence.v1',source,as_of:'2026-11-01T00:00:00Z',status:'ok',warnings:[],observations:[{id:source+'-1',kind:'metric',product_key:key,metric_key:'example',value:10,unit:'count',period:{from:'2026-10-01',to:'2026-10-31'},source_ref:{source_id:'synthetic',locator:'row 1'},status:'observed'}]};}
function setup({marketFails=false,metricsFail=false,lostAck=false,denyWrite=false,corrupt=false}={}) {
 const store=new Map();let seq=0,calls=0,writes=0;
 function seed(path,text){const f={id:'f'+(++seq),contentId:'c'+seq,path,name:path.split('/').at(-1),size:text.length};store.set(f.id,{file:f,text});return f;}
 const platform={inspectApp:async({app})=>{if(marketFails&&app==='market-watch')throw Error('not available');},files:async q=>{
  if(q.operation==='list')return{entries:[...store.values()].map(x=>({isDirectory:false,path:x.file.path,file:x.file}))};
  if(q.operation==='read'||q.operation==='stat'){const r=store.get(q.fileId);if(!r)throw Error('missing');return q.operation==='stat'?r.file:r;}
  if(q.operation==='write'){writes++;if(denyWrite)throw Error('denied');if([...store.values()].some(r=>r.file.path===q.path))throw Error('conflict');const f=seed(q.path,q.data);if(corrupt)store.get(f.id).text+=' ';if(lostAck)throw Error('lost ack');return f;}
  throw Error('unexpected operation');
 }};
 const apps={market_watch:{tools:{list_offers:async()=>{calls++;return batch('market');}}},metrika_analyst:{tools:{compare_periods:async()=>{calls++;if(metricsFail)throw Error('unavailable');return batch('metrika');}}}};
 const a={run_id:'one',product_key:key,market_query:{product_key:key,limit:8},metrics_query:{product_key:key},evidence_file_ids:[seed('/home/Documents/research.json',JSON.stringify(batch('research'))).id,seed('/home/Documents/economics.json',JSON.stringify(batch('economics'))).id]};
 return{platform,apps,a,seed,store,calls:()=>calls,writes:()=>writes};
}
test('immutable shared snapshot and identical replay',async()=>{const s=setup();const first=await invoke(s.a,s.platform,s.apps);assert.equal(first.status,'complete');assert.equal(first.reused,false);assert.equal(first.sources.length,4);const second=await invoke({...s.a,market_query:{limit:8,product_key:key}},s.platform,s.apps);assert.equal(second.reused,true);assert.deepEqual(second.snapshot,first.snapshot);assert.equal(s.calls(),2);assert.equal(s.writes(),1);});
test('run ID conflict cannot overwrite',async()=>{const s=setup();await invoke(s.a,s.platform,s.apps);await assert.rejects(()=>invoke({...s.a,market_query:{product_key:key,limit:9}},s.platform,s.apps),/different request/);assert.equal(s.writes(),1);});
test('missing sources are explicit, no invented values',async()=>{const s=setup({marketFails:true,metricsFail:true});const r=await invoke(s.a,s.platform,s.apps);assert.equal(r.status,'partial');assert.equal(r.sources[0].status,'unavailable');assert.equal(r.sources[1].count,0);assert.equal(s.calls(),1);});
test('next run links previous evidence and document references',async()=>{const s=setup();const a=await invoke(s.a,s.platform,s.apps);const d=s.seed('/home/Documents/interview.md','synthetic interview');const b=await invoke({...s.a,run_id:'two',previous_snapshot_id:a.snapshot.id,document_file_ids:[d.id]},s.platform,s.apps);assert.equal(b.previous_snapshot.id,a.snapshot.id);assert.equal(b.document_refs[0].id,d.id);assert.notEqual(b.snapshot.id,a.snapshot.id);});
test('lost acknowledgment resolves to persisted snapshot',async()=>{const s=setup({lostAck:true});const r=await invoke(s.a,s.platform,s.apps);assert.equal(r.reused,true);assert.equal(s.writes(),1);});
test('write denial is not success',async()=>{const s=setup({denyWrite:true});await assert.rejects(()=>invoke(s.a,s.platform,s.apps),/not published/);});
test('read-back mismatch is not success',async()=>{const s=setup({corrupt:true});await assert.rejects(()=>invoke(s.a,s.platform,s.apps),/read-back mismatch/);});
test('wrong product query rejected before tool calls',async()=>{const s=setup();await assert.rejects(()=>invoke({...s.a,market_query:{product_key:'other'}},s.platform,s.apps),/identity mismatch/);assert.equal(s.calls(),0);});

function caseDoc(policy, signal) {
 const obs=[{id:'watchdog/'+signal,kind:'metric',product_key:'kes',metric_key:'watchdog.alert',value:true,unit:'boolean',period:{from:'2026-05-11',to:'2026-06-07'},segment:'mobile',source_ref:{source_id:'aifence-watchdog',locator:'watch-series.json#2026-06-01'},status:'observed',details:{control_product_key:'astra_linux_special_edition'}}];
 return {schema_version:'aifence.case.v1',...(policy?{source_policy:policy}:{}),as_of:'2026-07-19T23:59:59Z',batches:[{schema_version:'aifence.v1',source:'sales',as_of:'2026-07-19T23:59:59Z',status:'ok',observations:obs,warnings:[]}]};
}

test('every scanned controlled-decline case is saved with its own policy and locators', async () => {
 const s=setup();
 const seen=[];
 s.apps.metrika_analyst.tools.scan_watchdog_series=async q=>{seen.push(q);return {status:'ok',origin:'synthetic_fixture',dataset_id:'ds-1',as_of:'2026-07-19T23:59:59Z',compared_windows:10,uncertain_windows:0,candidate_count:3,
  candidates:[
   {candidate_id:'c-1',signal_id:'watchdog/one',kind:'controlled_decline',period:{from:'2026-06-01',to:'2026-06-07'},reason:'decline_with_stable_control_ranked_on_baseline',control_product_key:'astra_linux_special_edition',relative_change_pct:-21.875,control_change_pct:10.4},
   {candidate_id:'c-2',signal_id:'watchdog/two',kind:'controlled_decline',period:{from:'2026-06-01',to:'2026-06-07'},reason:'decline_with_stable_control_ranked_on_baseline',control_product_key:'astra_linux_special_edition',relative_change_pct:-8,control_change_pct:1},
   {candidate_id:'c-3',signal_id:'watchdog/three',kind:'spike',period:{from:'2026-06-01',to:'2026-06-07'},reason:'spike',relative_change_pct:40}
  ],warnings:[]};};
 const asked=[];
 s.apps.metrika_analyst.tools.collect_selected_watchdog_case=async q=>{asked.push(q);return {status:'alert',first_alert_signal_id:'watchdog/'+(q.candidate_id==='c-1'?'one':'two'),document:caseDoc(q.candidate_id==='c-2'?'local_project_only':'',q.candidate_id)};};
 const r=await invoke(s.a,s.platform,s.apps);
 // Both controlled candidates are collected; the spike is not a controlled case.
 assert.deepEqual(asked.map(q=>q.candidate_id),['c-1','c-2']);
 assert.equal(seen.length,1);
 assert.equal(r.watchdog_cases.length,2);
 assert.equal(r.watchdog_cases[0].candidate_id,'c-1');
 assert.equal(r.watchdog_cases[0].control_product_key,'astra_linux_special_edition');
 assert.equal(r.watchdog_cases[0].observations,1);
 const stored=JSON.parse(s.store.get(r.snapshot.id).text);
 assert.equal(stored.watchdog_cases.length,2);
 // The private case keeps its own policy and the snapshot becomes private.
 assert.equal(stored.watchdog_cases[1].document.source_policy,'local_project_only');
 assert.equal(stored.source_policy,'local_project_only');
 // Every alert keeps the producer's locator; nothing is rewritten.
 const alerts=stored.watchdog_cases.flatMap(c=>c.document.batches.flatMap(b=>b.observations));
 assert.ok(alerts.every(o=>o.source_ref.locator==='watch-series.json#2026-06-01'));
 assert.ok(alerts.every(o=>o.details.control_product_key==='astra_linux_special_edition'));
});

test('private evidence retains its policy through catalog read-back and PM import', async () => {
  const s = setup();
  const privateFile = s.seed('/home/Documents/sales.json', JSON.stringify({
    ...batch('sales'), source_policy:'local_project_only'
  }));
  const receipt = await invoke({...s.a, evidence_file_ids:[privateFile.id]}, s.platform, s.apps);
  const document = JSON.parse(s.store.get(receipt.snapshot.id).text);
  assert.equal(document.source_policy, 'local_project_only');
  assert.equal(document.sources.find(source => source.source === 'sales').batch.observations.length, 1);
});

test('request policy is part of immutable run identity', async () => {
  const s = setup();
  const request = {...s.a, source_policy:'local_project_only'};
  const first = await invoke(request, s.platform, s.apps);
  assert.equal(JSON.parse(s.store.get(first.snapshot.id).text).source_policy, 'local_project_only');
  assert.equal((await invoke(request, s.platform, s.apps)).reused, true);
  await assert.rejects(() => invoke(s.a, s.platform, s.apps), /different request/);
  await assert.rejects(() => invoke({...s.a, source_policy:'public'}, s.platform, s.apps), /Invalid evidence source policy/);
});
