import json, os, socket, subprocess, threading, time, urllib.request, urllib.error, signal, sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlparse
class Mock(BaseHTTPRequestHandler):
 def do_GET(self):
  path=urlparse(self.path).path
  if self.headers.get("Authorization") not in ("OAuth synthetic-not-a-real-token","Bearer synthetic-not-a-real-token"):
   self.send_error(403);return
  if path=="/management/v1/counters": v={"counters":[{"id":42,"name":"Synthetic test","site":"example.invalid"}]}
  elif path=="/stat/v1/data": v={"data":[{"dimensions":[],"metrics":[100]}],"totals":[100],"sampled":False,"sample_share":1,"total_rows":1}
  elif path=="/stat/v1/data/comparison": v={"data":[{"dimensions":[],"metrics":{"a":[100],"b":[80]}}],"totals":{"a":[100],"b":[80]},"sampled":False,"sample_share":1,"total_rows":1}
  else:self.send_error(404);return
  b=json.dumps(v).encode();self.send_response(200);self.send_header("Content-Type","application/json");self.send_header("Content-Length",str(len(b)));self.end_headers();self.wfile.write(b)
 def log_message(self,*a):pass
mock=ThreadingHTTPServer(("127.0.0.1",0),Mock)
threading.Thread(target=mock.serve_forever,daemon=True).start()
with socket.socket() as sock:
 sock.bind(("127.0.0.1",0));port=sock.getsockname()[1]
prefix=sys.argv[1] if len(sys.argv)>1 else "node_modules/.bin"
prefix=os.path.abspath(prefix)
env={**os.environ,"PATH":prefix+":"+os.environ["PATH"],"YANDEX_METRIKA_TOKEN":"synthetic-not-a-real-token","SUPERGATEWAY_API_KEY":"synthetic-gateway-key","YANDEX_METRIKA_ENABLE_WRITE":"false","YANDEX_METRIKA_BASE_URL":"http://127.0.0.1:"+str(mock.server_port)}
tools=["get_metadata","describe_counter","run_report","run_comparison","run_timeseries","run_drilldown"]
args=[prefix+"/supergateway","--stdio","yandex-metrica-mcp","--outputTransport","streamableHttp","--stateful","--host","127.0.0.1","--port",str(port)]
for tool in tools:args.extend(["--tools",tool])
log=open("/tmp/aifence-boxlab-smoke.log","w")
p=subprocess.Popen(args,env=env,stdin=subprocess.PIPE,stdout=log,stderr=log,start_new_session=True)
session=None;protocol=None
url="http://127.0.0.1:"+str(port)+"/mcp"
def rpc(method,params,reqid=None,auth=True):
 global session,protocol
 d={"jsonrpc":"2.0","method":method,"params":params}
 if reqid is not None:d["id"]=reqid
 h={"Content-Type":"application/json","Accept":"application/json, text/event-stream"}
 if auth:h["Authorization"]="Bearer synthetic-gateway-key"
 if session:h["Mcp-Session-Id"]=session
 if protocol:h["MCP-Protocol-Version"]=protocol
 with urllib.request.urlopen(urllib.request.Request(url,data=json.dumps(d).encode(),headers=h),timeout=8) as res:
  if res.headers.get("Mcp-Session-Id"):session=res.headers["Mcp-Session-Id"]
  if res.status==202:return {}
  if "text/event-stream" in res.headers.get("Content-Type",""):
   for line in res:
    if line.startswith(b"data:"):
     x=json.loads(line[5:].strip())
     if x.get("id")==reqid:return x
   raise RuntimeError("no SSE response")
  return json.load(res)
try:
 for _ in range(50):
  try:
   with socket.create_connection(("127.0.0.1",port),timeout=.2):break
  except OSError:time.sleep(.1)
 init={"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"aifence-smoke","version":"1.0"}}
 try:
  rpc("initialize",init,1,False);raise AssertionError("missing gateway auth accepted")
 except urllib.error.HTTPError as e:assert e.code in (401,403),e.code
 print("Unauthenticated request rejected")
 r=rpc("initialize",init,2);assert "result" in r,r
 protocol=r["result"]["protocolVersion"]
 rpc("notifications/initialized",{})
 r=rpc("tools/list",{},3)
 names=sorted(t["name"] for t in r["result"]["tools"])
 assert names==sorted(tools),names
 print("MCP initialize and six-tool allowlist passed")
 def call(n,a,i):
  r=rpc("tools/call",{"name":n,"arguments":a},i)
  res=r["result"];assert not res.get("isError"),str(res)[:500]
  return res.get("structuredContent") or json.loads(res["content"][0]["text"])
 meta=call("get_metadata",{},4);assert meta["counters"][0]["id"]==42
 report=call("run_report",{"counterId":42,"metrics":["ym:s:visits"],"date1":"2026-01-01","date2":"2026-01-07","accuracy":"full"},5)
 assert report["totals"]["ym:s:visits"]==100,report
 compare=call("run_comparison",{"counterId":42,"metrics":["ym:s:visits"],"date1A":"2026-01-01","date2A":"2026-01-07","date1B":"2026-01-08","date2B":"2026-01-14","accuracy":"full"},6)
 assert compare["totals"]["ym:s:visits"]["delta_pct"]==-20,compare
 print("Actual BoxLab package against synthetic local API: metadata, report and comparison passed")
finally:
 try:os.killpg(p.pid,signal.SIGTERM)
 except ProcessLookupError:pass
 try:p.wait(timeout=3)
 except subprocess.TimeoutExpired:os.killpg(p.pid,signal.SIGKILL)
 mock.shutdown();log.close()
