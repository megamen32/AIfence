#!/usr/bin/env python3
"""Publish Market Watch's seven-day matrix to the current Airlock user's Baserow database."""
import argparse
import csv
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timezone
from decimal import Decimal
import fcntl
import io
from itertools import groupby
import json
import re
from pathlib import Path
import subprocess
import tempfile
from urllib.parse import urlencode, urlsplit

ROOT = Path(__file__).resolve().parents[3]
RUNTIME = ROOT / ".runtime"
SDK = RUNTIME / "bin/market-air"
TABLE = "Цены по лицензиям"
TITLE = "Открыть лицензию"
ORIGINAL_TITLE_FORMULA = "link(field('Сравнение и история'), field('Лицензия'))"
TITLE_FORMULA = ("link(field('Сравнение и история'), if(isblank(field('Артикул')), "
                 "field('Лицензия'), concat(field('Артикул'), ' · ', field('Лицензия'))))")
DATABASE = "Market Watch"
# CSV column, Baserow field, type, native grid width. Hidden metadata stays available in row details.
COLUMNS = (
    ("product", "Лицензия", "text", 155),
    ("softline", "Softline", "number", 88),
    ("softmap", "SoftMap", "number", 88),
    ("softmagazin", "SoftMagazin", "number", 98),
    ("spread", "Разница", "number", 75),
    ("compare_url", "Сравнение и история", "url", 140),
    ("sku", "Артикул", "text", 130),
    ("store_count", "Магазинов по артикулу", "number", 0),
    ("currency","Валюта","text",76),
    ("conditions","Условия","text",230),
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
    ("Без сопоставимой пары",("status","equal","Вне сравнения"),None),
    ("Артикул в нескольких магазинах", ("store_count", "higher_than_or_equal", "2"), ("sku", "ASC")),
)
NAMES = {key:name for key,name,_,_ in COLUMNS}
DIRECT_URL = "http://localhost:42080"


def request(app, method, path, payload=None, sink=None):
    args = [str(SDK), "request", method, path, "--agent", app,
            "--remote", "default", "--target", "production", "--url", DIRECT_URL, "--timeout", "2m"]
    if payload is not None:
        args.extend(["--header", "Content-Type: application/json", "--data", json.dumps(payload,ensure_ascii=False,separators=(",",":"))])
    proc = subprocess.run(args, cwd=ROOT/"apps"/app, stdout=sink if sink is not None else subprocess.PIPE,
                          stderr=subprocess.PIPE, timeout=135)
    if proc.returncode:
        raise RuntimeError(f"{app} {method} {path.split('?')[0]} failed ({proc.returncode}): {proc.stderr.decode(errors='replace')[-450:]}")
    return proc.stdout


def api(method, path, body=None):
    result = json.loads(request("baserow", method, "/api/"+path, body))
    if isinstance(result, dict) and result.get("error"):
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
    primary = next((field for field in fields.values() if field["primary"]), None)
    if not primary or primary["name"] not in (NAMES["product"], TITLE):
        raise ValueError("Existing matrix has an incompatible primary field")
    ids = {}
    for key, name, kind, _ in COLUMNS:
        row = fields.get(name)
        if not row:
            payload = {"name":name,"type":kind}
            if kind == "number":
                payload.update(number_decimal_places=0 if key == "store_count" else 2,
                               number_negative=key != "store_count")
            if kind == "date":
                payload.update(date_include_time=True,date_format="EU",date_time_format="24")
            row = api("POST",fields_path,payload)
        if row["type"] != kind:
            raise ValueError(f"Field type changed: {name}")
        ids[key] = row["id"]
    # Baserow can display the original product name as a native clickable primary
    # value. Both original inputs remain intact and stay under the user's ACL.
    title_field = fields.get(TITLE)
    if not title_field:
        params = {"name": TITLE, "formula": TITLE_FORMULA}
        checked = api("POST", f"database/formula/{tid}/type/", params)
        if checked.get("formula_type") != "link":
            raise ValueError("Native Baserow did not validate the comparison link")
        title_field = api("POST", fields_path, {"type": "formula", **params})
    elif title_field.get("formula") == ORIGINAL_TITLE_FORMULA and title_field.get("type") == "formula":
        title_field = api("PATCH", f"database/fields/{title_field['id']}/", {"formula": TITLE_FORMULA})
    if title_field.get("formula") != TITLE_FORMULA or title_field.get("formula_type") != "link":
        raise ValueError("Existing Baserow title is not the managed comparison link")
    if primary["id"] != title_field["id"]:
        api("POST", fields_path + "change-primary-field/", {"new_primary_field_id": title_field["id"]})
        configure_views = True
    path = f"database/views/table/{tid}/"
    views = api("GET",path)
    view_ids = {}
    for idx,(name,rule,sort) in enumerate(VIEWS):
        view = find_one(views,name)
        if not view and name == "Без сопоставимой пары":
            previous = find_one(views, "Условия требуют проверки")
            if previous:
                view = api("PATCH", f"database/views/{previous['id']}/", {"name": name})
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
        desired = {str(ids[key]): {"width": width or 100, "hidden": not width or key == "product", "order": order}
                   for order, (key, _, _, width) in enumerate(COLUMNS, start=1)}
        desired[str(title_field["id"])] = {"width": 155, "hidden": False, "order": 0}
        if rule and rule[0] == "store_count":
            desired[str(ids["store_count"])].update(width=120, hidden=False)
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
            sp = f"database/views/{vid}/sortings/"
            sort_rules = [sort]
            if idx == 0:
                sort_rules.insert(0, ("status", "DESC"))
            current_sorts = api("GET", sp)
            ordered_ids = []
            for key, direction in sort_rules:
                field_id = ids[key]
                setting = next((x for x in current_sorts if x["field"] == field_id), None)
                if setting is None:
                    setting = api("POST", sp, {"field": field_id, "order": direction})
                    current_sorts.append(setting)
                ordered_ids.append(setting["id"])
            ordered_ids.extend(x["id"] for x in current_sorts if x["id"] not in ordered_ids)
            if [x["id"] for x in current_sorts] != ordered_ids:
                # Native reorder replies with HTTP 204 and no JSON entity.
                request("baserow", "POST", "/api/" + sp + "prioritize/", {"view_sort_ids": ordered_ids})
                actual = api("GET", sp)
                if [x["id"] for x in actual] != ordered_ids:
                    raise ValueError("Baserow did not apply the requested sort priority")
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


def mapped(row, store_count=None):
    data = dict(row)
    data["active"]=True
    data["store_count"] = store_count
    data["status"]="Сопоставимо" if row["comparable"]=="true" else "Вне сравнения"
    data["leader"]={"true":"Да","false":"Нет","":""}[row["leader_changed"]]
    u=urlsplit(data["compare_url"])
    if u.scheme!="http" or u.netloc!="market-watch.localhost:42080":
        raise ValueError("Matrix returned a nonlocal comparison URL")
    return {name:normal(kind,data[key]) for key,name,kind,_ in COLUMNS}


def market_stamp():
    args = [str(SDK), "tools", "invoke", "catalog_status", "--json", "--agent", "market-watch",
            "--remote", "default", "--target", "production", "--url", DIRECT_URL, "--timeout", "60s"]
    proc = subprocess.run(args, cwd=ROOT/"apps/market-watch", capture_output=True, timeout=70, check=True)
    status = json.loads(json.loads(proc.stdout)["data"]["result"]["output"])
    return {row["source"]: (row["observed_offers"], row.get("last_observation")) for row in status["sources"]}


def matrix_groups(stream):
    reader = csv.DictReader(stream, delimiter=";", strict=True)
    derived = {"active", "leader", "status", "store_count"}
    required = (set(NAMES) - derived) | {"leader_changed", "cursor", "comparable"}
    missing = required - set(reader.fieldnames or ())
    if missing:
        raise ValueError(f"Market Watch matrix is missing columns: {sorted(missing)}")
    for cursor, group in groupby(reader, key=lambda row:row["cursor"]):
        items = list(group)
        if not cursor or any(None in row or None in row.values() for row in items):
            raise ValueError("Truncated Market Watch matrix")
        sellers = {seller for row in items for seller in ("softline", "softmap", "softmagazin") if row[seller]}
        count = len(sellers) if cursor.startswith("sku:") and len(sellers) >= 2 else None
        yield cursor, [(row["key"], mapped(row, count)) for row in items]


def matrix_page(cursor, limit):
    query = urlencode({"days":7, "limit":limit, "after":cursor})
    raw = request("market-watch","GET","/matrix.csv?"+query).decode("utf-8-sig")
    return list(matrix_groups(io.StringIO(raw)))


def add_group(rows, items):
    for key, value in items:
        if not key or key in rows:
            raise ValueError("Duplicate or empty Market Watch key")
        rows[key] = value


def bulk_matrix():
    rows = {}
    previous = ""
    last = ""
    last_keys = set()
    with tempfile.TemporaryFile(mode="w+b") as raw:
        request("market-watch", "GET", "/matrix.csv?days=7", sink=raw)
        raw.seek(0)
        with io.TextIOWrapper(raw, encoding="utf-8-sig", newline="") as stream:
            for cursor, group in matrix_groups(stream):
                add_group(rows, group)
                previous, last = last, cursor
                last_keys = {key for key, _ in group}
    # A streaming response can terminate after its response headers were sent.
    # Verify both the last complete SKU and exhaustion of the canonical cursor.
    tail = matrix_page(previous, 1)
    if last and (not tail or tail[0][0] != last or {key for key, _ in tail[0][1]} != last_keys):
        raise ValueError("Incomplete final Market Watch SKU")
    if matrix_page(last, 1):
        raise ValueError("Incomplete Market Watch matrix")
    return rows


def paged_matrix():
    rows = {}
    cursor = ""
    while True:
        page = matrix_page(cursor, 500)
        if not page:
            return rows
        for key, items in page:
            add_group(rows, items)
        next_cursor = page[-1][0]
        if next_cursor == cursor:
            raise ValueError("Repeated Market Watch cursor")
        cursor = next_cursor


def source():
    for _ in range(3):
        start = market_stamp()
        try:
            rows = bulk_matrix()
        except (csv.Error, ValueError, RuntimeError, subprocess.TimeoutExpired):
            # Some deployments cap a single route response. Keep cursor paging available.
            rows = paged_matrix()
        if market_stamp() == start:
            return rows.items()
    raise RuntimeError("Market Watch changed during export; keeping the previous published matrix")


def batches(items, size=200):
    batch=[]
    length=0
    for item in items:
        length_item=len(json.dumps(item,ensure_ascii=False).encode())
        if batch and (len(batch)>=size or length+length_item>180000):
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
    old = {}
    # Fetch only fields maintained by this projection. In particular no personal
    # annotations or computed link text are needed to decide which rows changed.
    field_ids = {"field_"+str(ids[key]):name for key,name,_,_ in COLUMNS}
    params = urlencode({"include":",".join(field_ids), "size":200})
    def fetch_page(page):
        return api("GET", base + "?" + params + "&page=" + str(page))
    first = fetch_page(1)
    total = first["count"]
    def index_page(page):
        if page["count"] != total:
            raise ValueError("Native table changed while scanning; retry the projection")
        for item in page["results"]:
            row = {name:item.get(fid) for fid,name in field_ids.items()}
            key = row[NAMES["key"]]
            if key:
                if key in old:
                    raise ValueError("Duplicate managed key in Baserow")
                old[key] = {"id":item["id"], **row}
    index_page(first)
    # Writes remain serialized. Three local reads bound the cost of large native catalogs.
    with ThreadPoolExecutor(max_workers=3) as pool:
        for page in pool.map(fetch_page, range(2, (total + 199)//200 + 1)):
            index_page(page)
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
            acknowledged = answer["items"]
            if len(acknowledged) != len(batch):
                raise ValueError("Baserow did not acknowledge every row")
            identifier = NAMES["key"] if method == "POST" else "id"
            if {item[identifier] for item in acknowledged} != {item[identifier] for item in batch}:
                raise ValueError("Baserow acknowledged different comparison rows")
    managed_filters = urlencode({f"filter__field_{ids['active']}__boolean": "1",
                                 f"filter__field_{ids['key']}__not_empty": "", "size": 1})
    count = api("GET", base + "?" + managed_filters)["count"]
    if count!=len(rows):
        raise ValueError(f"Visible matrix has {count} rows; expected {len(rows)}")
    multi = [row for row in rows.values() if row[NAMES["store_count"]] is not None]
    return {"count":len(rows),"created":len(new),"updated":len(changed),"retired":len(inactive),
            "multi_store_skus":len({r[NAMES["sku"]].upper() for r in multi}),"multi_store_rows":len(multi)}


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
