// Airlock saved run_js async function body. Uses only native app and file capabilities.
const version = "aifence.workflow.evidence.v1";
const home = "/home/Documents";
const isObject = x => x !== null && typeof x === "object" && !Array.isArray(x);
const requireString = (x, name) => {
  if (typeof x !== "string" || !x.trim() || x.length > 256) throw Error("Invalid " + name);
  return x;
};
const canonical = x => Array.isArray(x) ? x.map(canonical) : isObject(x) ?
  Object.fromEntries(Object.keys(x).sort().map(k => [k, canonical(x[k])])) : x;
const same = (a, b) => JSON.stringify(canonical(a)) === JSON.stringify(canonical(b));
const ids = (xs, max) => {
  if (xs === undefined) return [];
  if (!Array.isArray(xs) || xs.length > max || new Set(xs).size !== xs.length) throw Error("Invalid file IDs");
  return xs.map(x => requireString(x, "file ID"));
};
if (!isObject(args)) throw Error("Expected args object");
const run_id = requireString(args.run_id,"run_id");
if (!/^[a-zA-Z0-9][a-zA-Z0-9_-]{0,79}$/.test(run_id)) throw Error("Invalid run_id");
const product_key = requireString(args.product_key,"product_key");
if (args.source_policy !== undefined && args.source_policy !== "local_project_only")
  throw Error("Invalid evidence source policy");
const path = home + "/AIfence-evidence-" + run_id + ".json";
const request = {run_id,product_key,...(args.source_policy ? {source_policy:args.source_policy} : {}),market_query:args.market_query,metrics_query:args.metrics_query,
  evidence_file_ids:ids(args.evidence_file_ids,12),document_file_ids:ids(args.document_file_ids,20),
  previous_snapshot_id:args.previous_snapshot_id == null ? null : requireString(args.previous_snapshot_id,"previous_snapshot_id")};
for (const q of [request.market_query,request.metrics_query]) {
  if (!isObject(q)) throw Error("Provide both inspected tool query objects");
  if (q.product_key !== undefined && q.product_key !== product_key) throw Error("Product identity mismatch");
}
if (JSON.stringify(request).length>16000) throw Error("Request too large");
function ref(f) {
  if (!isObject(f) || typeof f.id !== "string" || typeof f.contentId !== "string") throw Error("Invalid file reference");
  return {id:f.id,contentId:f.contentId,path:f.path,name:f.name,size:f.size};
}
async function existing() {
  let cursor;
  for (let i=0;i<20;i++) {
    const list=await platform.files({operation:"list",path:home,limit:100,...(cursor?{cursor}:{})});
    if (!Array.isArray(list.entries)) throw Error("Invalid file catalog response");
    const hit=list.entries.find(e=>!e.isDirectory&&(e.file?.path||e.path)===path);
    if (hit) {
      const r=await platform.files({operation:"read",fileId:ref(hit.file).id});
      const doc=JSON.parse(r.text);
      if (doc.schema_version!==version || !same(doc.request,request)) throw Error("Run ID belongs to a different request");
      if (request.source_policy && doc.source_policy !== request.source_policy) throw Error("Snapshot source policy mismatch");
      return {file:ref(r.file),doc};
    }
    if (!list.nextCursor) return null;
    if (list.nextCursor===cursor) throw Error("Repeated file catalog cursor");
    cursor=list.nextCursor;
  }
  throw Error("File catalog pagination limit; cannot establish run identity");
}
function result(file,doc,reused) {
  // A private snapshot keeps its values in the protected owner-scoped file.
  // The hosted return carries only the receipt and counts, never a percentage,
  // a control or a magnitude that came from a local_project_only document.
  const privateSnapshot = doc.source_policy === "local_project_only";
  return {run_id,product_key,reused,snapshot:file,status:doc.status,
    sources:doc.sources.map(s=>({source:s.source,status:s.status,count:s.batch?.observations?.length||0,
      observed_count:s.batch?.observations?.filter(o=>o.status==="observed").length||0,
      reason:s.reason||null,file:s.file||null})),document_refs:doc.document_refs,
    watchdog_cases:(doc.watchdog_cases||[]).map(c=>{
      const observations=c.document?.batches?.reduce((n,b)=>n+(b.observations?.length||0),0)||0;
      if(privateSnapshot) return {dataset_id:c.dataset_id,candidate_id:c.candidate_id,observations};
      return {dataset_id:c.dataset_id,candidate_id:c.candidate_id,signal_id:c.signal_id,
        kind:c.kind,period:c.period,control_product_key:c.control_product_key,relative_change_pct:c.relative_change_pct,
        control_change_pct:c.control_change_pct,observations,first_alert_magnitude:c.document?.first_alert_magnitude||null};
    }),
    previous_snapshot:doc.previous_snapshot,warnings:doc.warnings};
}
const old=await existing();
if(old) return result(old.file,old.doc,true);
const allowed=new Set(["market","metrika","sales","research","backlog","release","economics","browser"]);
function batch(b,expected) {
  if(typeof b==="string") b=JSON.parse(b);
  if(!isObject(b)||b.schema_version!=="aifence.v1"||!allowed.has(b.source)||
    (expected&&b.source!==expected)||!["ok","partial","unavailable"].includes(b.status)||
    typeof b.as_of!=="string"||!Array.isArray(b.observations)||b.observations.length>200||!Array.isArray(b.warnings))
    throw Error("Invalid evidence batch");
  if (b.source_policy && b.source_policy !== "local_project_only") throw Error("Unknown evidence source policy");
  const seen=new Set();
  for(const o of b.observations) {
    if(!isObject(o)||typeof o.id!=="string"||!o.id||seen.has(o.id)||
      typeof o.product_key!=="string"||typeof o.metric_key!=="string"||typeof o.unit!=="string"||
      !Object.hasOwn(o,"value")||!["observed","missing","incomparable","estimated"].includes(o.status)||
      !isObject(o.period)||typeof o.period.from!=="string"||typeof o.period.to!=="string"||
      !isObject(o.source_ref)||typeof o.source_ref.source_id!=="string"||typeof o.source_ref.locator!=="string"||
      (o.status==="missing"&&o.value!==null)) throw Error("Invalid observation");
    seen.add(o.id);
  }
  return b;
}
const sources=[],warnings=[],document_refs=[];
function add(b,file) {
  sources.push({source:b.source,status:b.status,batch:b,...(file?{file}:{})});
  if(!b.observations.some(o=>o.status==="observed")) warnings.push(b.source+": no observed values");
}
async function collect(source,slug,call) {
  try {
    await platform.inspectApp({app:slug,section:"tools"});
    add(batch(await call(),source));
  } catch(e) {
    sources.push({source,status:"unavailable",reason:"App access, query or evidence contract failed"});
    warnings.push(source+": no verified evidence");
  }
}
// The queries must match the live typed tool schemas. No silent fallback to invented data.
await collect("market","market-watch",()=>apps.market_watch.tools.list_offers(request.market_query));
await collect("metrika","metrika-analyst",()=>apps.metrika_analyst.tools.compare_periods(request.metrics_query));
// Computed watchdog cases are collected the same way for every candidate the
// scan returns. Nothing is chosen by an expected answer: each candidate with a
// measured control is turned into its own signed, owner-scoped case.document.
const watchdog_cases=[];
const watchdog_warnings=[];
// scan_watchdog_series reports the kind of its first candidate as the scan
// status (alert, measurement_issue, campaign_effect, temporary_spike), or
// quiet when it compared windows and found nothing. needs_selection means the
// owner has not chosen a dataset. Every other value is an unexpected contract.
const scanWithCases=new Set(["alert","measurement_issue","campaign_effect","temporary_spike"]);
try {
  await platform.inspectApp({app:"metrika-analyst",section:"tools"});
  let dataset=null,offset=0,pages=0;
  const pageLimit=20;
  while(pages<pageLimit) {
    pages++;
    const scan=await apps.metrika_analyst.tools.scan_watchdog_series(offset?{offset}:{});
    if(!isObject(scan)||typeof scan.status!=="string"||(scan.candidates!==undefined&&!Array.isArray(scan.candidates)))
      throw Error("Invalid watchdog scan response");
    if(scan.status==="needs_selection") {
      watchdog_warnings.push("watchdog: no owner-selected dataset; choose one on the Metrika page");
      break;
    }
    // Quiet is an ordinary empty result, not a collection problem, so it does
    // not lower the snapshot status.
    if(scan.status==="quiet") break;
    if(!scanWithCases.has(scan.status)) throw Error("Unexpected watchdog scan status "+scan.status);
    if(!scan.dataset_id) throw Error("Watchdog scan returned candidates without a dataset");
    dataset=scan.dataset_id;
    for(const c of scan.candidates||[]) {
      if(!isObject(c)||typeof c.candidate_id!=="string"||!c.candidate_id) continue;
      if(c.kind!=="controlled_decline"||!c.control_product_key) continue;
      const r=await apps.metrika_analyst.tools.collect_selected_watchdog_case({dataset_id:dataset,candidate_id:c.candidate_id});
      if(isObject(r)&&isObject(r.document)) watchdog_cases.push({dataset_id:dataset,candidate_id:c.candidate_id,
        signal_id:r.first_alert_signal_id||c.signal_id||null,kind:c.kind,period:c.period||null,
        control_product_key:c.control_product_key||null,
        relative_change_pct:c.magnitude?.relative_change_pct??null,
        control_change_pct:c.control_change_pct??null,document:r.document});
      else watchdog_warnings.push("watchdog case "+c.candidate_id+": no document returned");
    }
    if(scan.next_offset===undefined||scan.next_offset===null) break;
    if(typeof scan.next_offset!=="number"||scan.next_offset<=offset) throw Error("Repeated or invalid watchdog scan cursor");
    offset=scan.next_offset;
  }
  // Reaching the page limit is reported, never presented as a complete scan.
  if(pages===pageLimit) watchdog_warnings.push("watchdog: page limit reached; remaining candidates were not collected");
} catch(e) {
  // A provider error can quote a row, a phrase or a token. Report the class of
  // failure only; the message text never reaches the hosted return.
  watchdog_warnings.push("watchdog cases: app access, scan or case contract failed");
}
for(const id of request.evidence_file_ids) {
  try {const r=await platform.files({operation:"read",fileId:id});add(batch(JSON.parse(r.text)),ref(r.file));}
  catch(e) {sources.push({source:"provided_evidence",status:"unavailable",reason:"Invalid or unreadable evidence",file_id:id});warnings.push("Unavailable evidence file: "+id);}
}
for(const id of request.document_file_ids) {
  try {const f=await platform.files({operation:"stat",fileId:id});document_refs.push(ref(f.file||f));}
  catch(e) {warnings.push("Unavailable document: "+id);}
}
let previous_snapshot=null;
if(request.previous_snapshot_id) {
  try {
    const r=await platform.files({operation:"read",fileId:request.previous_snapshot_id});
    const p=JSON.parse(r.text);
    if(p.schema_version!==version||p.product_key!==product_key||p.run_id===run_id) throw Error("Invalid previous snapshot");
    previous_snapshot={...ref(r.file),run_id:p.run_id,status:p.status};
  } catch(e) {warnings.push("Previous snapshot unavailable or wrong product");}
}
// Watchdog warnings belong to the outcome, so they must be present before the
// status is decided; otherwise a failed case collection would read complete.
for(const w of watchdog_warnings) warnings.push(w);
for(const source of ["research","economics"]) {
  if(!sources.some(s=>s.source===source&&s.batch?.observations?.some(o=>o.status==="observed")))
    warnings.push(source+": no observed evidence; do not infer facts or measured profit");
}
const status=sources.every(s=>s.status==="ok"&&s.batch?.observations?.some(o=>o.status==="observed"))&&!warnings.length?"complete":"partial";
const localOnly = request.source_policy === "local_project_only" || sources.some(s=>s.batch?.source_policy === "local_project_only") ||
  watchdog_cases.some(c=>c.document?.source_policy === "local_project_only");
const doc={schema_version:version,...(localOnly ? {source_policy:"local_project_only"} : {}),run_id,product_key,collected_at:new Date().toISOString(),request,status,sources,watchdog_cases,document_refs,previous_snapshot,warnings};
const data=JSON.stringify(doc,null,2);
if(data.length>240000) throw Error("Snapshot too large; narrow the queries or evidence files");
let written;
try {written=ref(await platform.files({operation:"write",path,data,contentType:"application/json"}));}
catch(e) {
  const winner=await existing();
  if(winner) return result(winner.file,winner.doc,true);
  throw Error("Snapshot not published; check catalog write access");
}
const confirmed=await platform.files({operation:"read",fileId:written.id});
if(confirmed.file.contentId!==written.contentId||confirmed.text!==data) throw Error("Snapshot read-back mismatch");
return result(written,doc,false);
