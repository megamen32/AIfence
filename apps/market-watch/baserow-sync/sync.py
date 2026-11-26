#!/usr/bin/env python3
"""Publish Market Watch's seven-day matrix to the current Airlock user's Baserow database."""
import argparse
import csv
from datetime import datetime, timezone
from decimal import Decimal
import fcntl
import io
import json
import re
from pathlib import Path
import subprocess
from urllib.parse import urlencode, urlsplit

ROOT = Path(__file__).resolve().parents[3]
RUNTIME = ROOT / ".runtime"
SDK = RUNTIME / "bin/market-air"
TABLE = "Цены по лицензиям"
DATABASE = "Market Watch"
# CSV column, Baserow field, type, native grid width. Hidden metadata stays available in row details.
COLUMNS = (
    ("product", "Лицензия", "text", 175),
    ("softline", "Softline", "number", 88),
    ("softmap", "SoftMap", "number", 88),
    ("softmagazin", "SoftMagazin", "number", 98),
    ("spread", "Разница", "number", 80),
    ("sku", "Артикул", "text", 130),
    ("currency","Валюта","text",76),
    ("conditions","Условия","text",230),
    ("compare_url","Сравнение и история","url",170),
    ("biggest_drop","Снижение за 7 дней","number",138),
    ("leader","Смена лидера за 7 дней","text",0),
    ("as_of","Последний замер","date",155),
    ("softline_change","Softline за 7 дней","number",0),
    ("softmap_change","SoftMap за 7 дней","number",0),
    ("softmagazin_change","SoftMagazin за 7 дней","number",0),
    ("status","Сопоставление","text",140),
    ("softline_url", "Источник Softline", "url", 0),
    ("softmap_url", "Источник SoftMap", "url", 0),
    ("softmagazin_url", "Источник SoftMagazin", "url", 0),
    ("softline_observed", "Дата Softline", "date", 0),
    ("softmap_observed", "Дата SoftMap", "date", 0),
    ("softmagazin_observed", "Дата SoftMagazin", "date", 0),
    ("active","В текущем каталоге","boolean",0),
    ("key","Ключ Market Watch","text",0),
)
VIEWS = (
    ("Цены по магазинам",None,("spread","DESC")),
    ("Сопоставимые цены",("status","equal","Сопоставимо"),("spread","DESC")),
    ("Подешевело за 7 дней",("biggest_drop","higher_than","0"),("biggest_drop","DESC")),
    ("Смена лидера за 7 дней",("leader","equal","Да"),("biggest_drop","DESC")),
    ("Условия требуют проверки",("status","equal","Вне сравнения"),None),
)
NAMES = {key:name for key,name,_,_ in COLUMNS}
DIRECT_URL = "http://localhost:42080"


def request(app, method, path, payload=None):
    args = [str(SDK), "request", method, path, "--agent", app,
            "--remote", "default", "--target", "production", "--url", DIRECT_URL, "--timeout", "2m"]
    if payload is not None:
        args.extend(["--header", "Content-Type: application/json", "--data", json.dumps(payload,ensure_ascii=False,separators=(",",":"))])
    proc = subprocess.run(args, cwd=ROOT/"apps"/app, capture_output=True, timeout=135)
    if proc.returncode:
        raise RuntimeError(f"{app} {method} {path.split('?')[0]} failed ({proc.returncode}): {proc.stderr.decode(errors='replace')[-450:]}")
    return proc.stdout


def api(method, path, body=None):
    result = json.loads(request("baserow", method, "/api/"+path, body))
    if isinstance(result, dict) and "error" in result:
        raise RuntimeError(f"{method} {path.split('?')[0]}: {result['error']}")
    return result


def find_one(rows, name):
    matches = [r for r in rows if r["name"] == name]
    if len(matches) > 1:
        raise ValueError(f"Ambiguous name in Baserow: {name}")
    return matches[0] if matches else None


def configure(configure_views=False):
    db = find_one([r for r in api("GET","applications/") if r["type"] == "database"], DATABASE)
    if not db:
        raise ValueError("The current user has no configured Market Watch database")
    table_path = f"database/tables/database/{db['id']}/"
    table = find_one(api("GET",table_path), TABLE)
    if not table:
        table = api("POST", table_path, {"name":TABLE,"data":[[NAMES["product"]]],"first_row_header":True})
    tid = table["id"]
    fields_path = f"database/fields/table/{tid}/"
    fields = {r["name"]:r for r in api("GET", fields_path)}
    if NAMES["product"] not in fields or not fields[NAMES["product"]]["primary"]:
        raise ValueError("Existing matrix has incompatible primary field")
    ids = {}
    for key, name, kind, _ in COLUMNS:
        row = fields.get(name)
        if not row:
            payload = {"name":name,"type":kind}
            if kind == "number":
                payload.update(number_decimal_places=2, number_negative=True)
            if kind == "date":
                payload.update(date_include_time=True,date_format="EU",date_time_format="24")
            row = api("POST",fields_path,payload)
        if row["type"] != kind:
            raise ValueError(f"Field type changed: {name}")
        ids[key] = row["id"]
    path = f"database/views/table/{tid}/"
    views = api("GET",path)
    view_ids = {}
    for idx,(name,rule,sort) in enumerate(VIEWS):
        view = find_one(views,name)
        new_view = view is None
        if not view and idx == 0 and len(views)==1 and views[0]["name"]=="Grid":
            view = api("PATCH",f"database/views/{views[0]['id']}/",{"name":name})
        if not view:
            view = api("POST",path,{"name":name,"type":"grid"})
        vid = view["id"]
        view_ids[name] = vid
        if not new_view and not configure_views:
            continue
        opts_path=f"database/views/{vid}/field-options/"
        desired = {str(ids[key]):{"width":width or 100,"hidden":not width,"order":order} for order,(key,_,_,width) in enumerate(COLUMNS)}
        # Keep the column layout unchanged when the user reorders other personal views.
        current=api("GET",opts_path)["field_options"]
        if any(not isinstance(current.get(k),dict) or any(current[k].get(prop)!=v for prop,v in opt.items()) for k,opt in desired.items()):
            api("PATCH",opts_path,{"field_options":desired})
        fp = f"database/views/{vid}/filters/"
        existing=api("GET",fp)
        rules=[("active","boolean","1")]
        if rule:
            rules.append(rule)
        for key,kind,value in rules:
            if not any(x["field"]==ids[key] and x["type"]==kind and x["value"]==value for x in existing):
                api("POST",fp,{"field":ids[key],"type":kind,"value":value})
        if sort:
            sp=f"database/views/{vid}/sortings/"
            if not any(x["field"]==ids[sort[0]] for x in api("GET",sp)):
                api("POST",sp,{"field":ids[sort[0]],"order":sort[1]})
    return db["id"], tid, view_ids, ids


def normal(kind, value):
    if value in (None, ""):
        return None if kind in ("number","date") else ""
    if kind == "number":
        return format(Decimal(str(value)).normalize(),"f")
    if kind == "date":
        # Python 3.9 on macOS accepts only three or six fractional digits.
        value = re.sub(r"(?<=:\d{2})\.(\d+)", lambda m: "." + m[1][:6].ljust(6, "0"), value)
        dt = datetime.fromisoformat(value.replace("Z", "+00:00"))
        if dt.tzinfo is None:
            raise ValueError("Observation timestamp has no timezone")
        return dt.astimezone(timezone.utc).isoformat(timespec="milliseconds").replace("+00:00","Z")
    return value


def mapped(row):
    data = dict(row)
    data["active"]=True
    data["status"]="Сопоставимо" if row["comparable"]=="true" else "Вне сравнения"
    data["leader"]={"true":"Да","false":"Нет","":""}[row["leader_changed"]]
    u=urlsplit(data["compare_url"])
    if u.scheme!="http" or u.netloc!="market-watch.localhost:42080":
        raise ValueError("Matrix returned a nonlocal comparison URL")
    return {name:normal(kind,data[key]) for key,name,kind,_ in COLUMNS}


def source():
    cursor=""
    seen=set()
    while True:
        query=urlencode({"days":7,"limit":400,"after":cursor})
        raw=request("market-watch","GET","/matrix.csv?"+query).decode("utf-8-sig")
        reader=csv.DictReader(io.StringIO(raw),delimiter=";")
        required={k for k in NAMES if k not in ("active","leader","status")}|{"leader_changed","cursor","comparable"}
        if not required.issubset(reader.fieldnames or ()):
            raise ValueError("Missing columns in Market Watch matrix: "+str(required-set(reader.fieldnames or ())))
        page=list(reader)
        if not page:
            return
        for row in page:
            key=row["key"]
            if not key or key in seen:
                raise ValueError("Duplicate or empty matrix key")
            seen.add(key)
            yield key,mapped(row)
        nxt=page[-1]["cursor"]
        if not nxt or nxt==cursor:
            raise ValueError("Invalid matrix continuation")
        cursor=nxt


def batches(items, size=100):
    batch=[]
    length=0
    for item in items:
        length_item=len(json.dumps(item,ensure_ascii=False).encode())
        if batch and (len(batch)>=size or length+length_item>90000):
            yield batch
            batch=[]
            length=0
        batch.append(item)
        length+=length_item
    if batch:
        yield batch


def sync(tid, ids):
    # Refresh from native rows on every run. A crash after a server commit can always resume.
    base=f"database/rows/table/{tid}/"
    old={}
    page=1
    while True:
        resp=api("GET",base+f"?user_field_names=true&size=200&page={page}")
        for row in resp["results"]:
            key=row.get(NAMES["key"])
            if not key:
                continue  # user-created rows are outside the projection
            if key in old:
                raise ValueError("Duplicate managed key in Baserow")
            old[key]=row
        if not resp["next"]:
            break
        page+=1
    rows=dict(source()) # fetch a complete export before modifying native state
    if not rows and old:
        raise ValueError("Empty matrix unexpectedly replacing an existing snapshot")
    new=[]
    changed=[]
    inactive=[]
    for key,values in rows.items():
        prev=old.get(key)
        if not prev:
            new.append(values)
        elif any(normal(kind,prev.get(name)) != values[name] for _,name,kind,_ in COLUMNS):
            changed.append({"id":prev["id"],**values})
    for key,row in old.items():
        if key not in rows and row.get(NAMES["active"]):
            inactive.append({"id":row["id"],NAMES["active"]:False})
    for method,entries in [("POST",new),("PATCH",changed),("PATCH",inactive)]:
        for batch in batches(entries):
            answer=api(method,base+"batch/?user_field_names=true",{"items":batch})
            if len(answer["items"])!=len(batch):
                raise ValueError("Baserow did not acknowledge every row")
    count=api("GET",base+f"?filter__field_{ids['active']}__boolean=1&size=1")["count"]
    if count!=len(rows):
        raise ValueError(f"Visible matrix has {count} rows; expected {len(rows)}")
    return {"count":len(rows),"created":len(new),"updated":len(changed),"retired":len(inactive)}


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--schema-only",action="store_true")
    args=parser.parse_args()
    state_dir=RUNTIME/"baserow"
    state_dir.mkdir(parents=True,exist_ok=True)
    with (state_dir/"matrix.lock").open("a") as lock:
        try:
            fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
        except BlockingIOError:
            print("Baserow matrix sync already running")
            return
        db,table,views,ids=configure(args.schema_only or not (state_dir/"matrix-progress.json").exists())
        summary={"database":db,"table":table,"views":views}
        if not args.schema_only:
            summary.update(sync(table,ids),updated_at=datetime.now(timezone.utc).isoformat())
            receipt=state_dir/"matrix-progress.json"
            tmp=receipt.with_suffix(".tmp")
            tmp.write_text(json.dumps(summary,ensure_ascii=False))
            tmp.replace(receipt)
        print(json.dumps(summary,ensure_ascii=False))


if __name__=="__main__":
    main()
