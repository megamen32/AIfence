import assert from 'node:assert/strict';
import fs from 'node:fs';
import {test} from 'node:test';
const body=fs.readFileSync(new URL('./collect-evidence.js',import.meta.url),'utf8');
const invoke=new (Object.getPrototypeOf(async function(){}).constructor)('args','platform','apps',body);
const key='synthetic:keyboard-a';
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
