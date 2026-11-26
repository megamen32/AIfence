// Airlock run_js_file async function body. Uses only native app and file capabilities.
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
const path = home + "/AIfence-evidence-" + run_id + ".json";
const request = {run_id,product_key,market_query:args.market_query,metrics_query:args.metrics_query,
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
      return {file:ref(r.file),doc};
    }
    if (!list.nextCursor) return null;
    if (list.nextCursor===cursor) throw Error("Repeated file catalog cursor");
    cursor=list.nextCursor;
  }
  throw Error("File catalog pagination limit; cannot establish run identity");
}
function result(file,doc,reused) {
  return {run_id,product_key,reused,snapshot:file,status:doc.status,
    sources:doc.sources.map(s=>({source:s.source,status:s.status,count:s.batch?.observations?.length||0,
      observed_count:s.batch?.observations?.filter(o=>o.status==="observed").length||0,
      reason:s.reason||null,file:s.file||null})),document_refs:doc.document_refs,
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
for(const source of ["research","economics"]) {
  if(!sources.some(s=>s.source===source&&s.batch?.observations?.some(o=>o.status==="observed")))
    warnings.push(source+": no observed evidence; do not infer facts or measured profit");
}
const status=sources.every(s=>s.status==="ok"&&s.batch?.observations?.some(o=>o.status==="observed"))&&!warnings.length?"complete":"partial";
const doc={schema_version:version,run_id,product_key,collected_at:new Date().toISOString(),request,status,sources,document_refs,previous_snapshot,warnings};
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
