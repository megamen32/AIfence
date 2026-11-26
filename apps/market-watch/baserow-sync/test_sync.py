import unittest
from unittest.mock import patch
import sync


def source_row():
    row = {k: "" for k in sync.NAMES}
    row.update(
        product="Комплект", sku="TEST", currency="RUB", key="sample",
        comparable="false", conditions="Тираж не указан", softmagazin="7488.00",
        as_of="2026-11-26T10:49:06Z", leader_changed="",
        compare_url="http://market-watch.localhost:42080/?q=TEST&tab=compare",
    )
    return row


IDS = {key:i for i,(key,_,_,_) in enumerate(sync.COLUMNS, start=1)}


def native(row):
    return {"id":row["id"],**{"field_"+str(IDS[key]):row.get(name) for key,name,_,_ in sync.COLUMNS}}


class MatrixProjectionTests(unittest.TestCase):
    def test_nullable_and_noncomparable_prices(self):
        row = sync.mapped(source_row())
        self.assertEqual(row["SoftMagazin"], "7488")
        self.assertIsNone(row["Softline"])
        self.assertIsNone(row["Разница"])
        self.assertEqual(row["Смена лидера за 7 дней"], "")
        self.assertEqual(row["Сопоставление"], "Вне сравнения")

    def test_rfc3339_nanoseconds_on_system_python(self):
        self.assertEqual(sync.normal("date", "2026-11-26T10:49:06.123456789Z"),
                         "2026-11-26T10:49:06.123Z")
        self.assertEqual(sync.normal("date", "2026-11-26T10:49:06.1Z"),
                         "2026-11-26T10:49:06.100Z")

    def test_multi_store_count_keeps_incomparable_rows_separate(self):
        import csv
        import io
        source = source_row()
        source.update(cursor="sku:TEST", **{"key":"first"})
        another = dict(source, key="other", softmagazin="", softmap="500")
        only_url = dict(source, key="missing", cursor="url:softmap:example", sku="")
        unique = dict(source, key="single", cursor="sku:TEST2", sku="TEST2")
        def page(rows):
            out = io.StringIO()
            writer = csv.DictWriter(out, fieldnames=source.keys(), delimiter=";")
            writer.writeheader()
            writer.writerows(rows)
            return out.getvalue().encode("utf-8-sig")
        stream = io.StringIO(page([source, another, unique, only_url]).decode("utf-8-sig"))
        rows = {key:row for _, group in sync.matrix_groups(stream) for key, row in group}
        self.assertEqual([rows[k]["Магазинов по артикулу"] for k in ("first","other","single","missing")],
                         ["2","2",None,None])
        self.assertTrue(all(row["Разница"] is None for row in rows.values()))

    def test_discards_changing_snapshot_before_publishing(self):
        with patch.object(sync, "market_stamp", side_effect=[1, 2, 2, 2]), \
             patch.object(sync, "bulk_matrix", side_effect=[{"old":1}, {"stable":2}]):
            self.assertEqual(dict(sync.source()), {"stable":2})

    def test_unstable_source_never_retires_existing_rows(self):
        with patch.object(sync, "market_stamp", side_effect=range(6)), \
             patch.object(sync, "bulk_matrix", return_value={"incomplete":1}):
            with self.assertRaises(RuntimeError):
                sync.source()

    def test_capped_export_uses_verified_cursor_fallback(self):
        with patch.object(sync, "market_stamp", return_value={"store":(10, "date")}), \
             patch.object(sync, "bulk_matrix", side_effect=ValueError("incomplete")), \
             patch.object(sync, "paged_matrix", return_value={"complete":1}):
            self.assertEqual(dict(sync.source()), {"complete":1})

    def test_refuses_untrusted_drilldown(self):
        row = source_row()
        row["compare_url"] = "https://other.invalid/"
        with self.assertRaises(ValueError):
            sync.mapped(row)

    def test_retry_matches_native_row_and_preserves_user_fields(self):
        active = sync.mapped(source_row())
        previous = {**active, "SoftMagazin":"7488.00", "id":10, "Моя заметка":"важно"}
        mine = {"id":20, "Лицензия":"Моя лицензия"}
        calls = []
        def api(method, path, body=None):
            calls.append(method)
            if "filter__" in path:
                return {"count":1}
            return {"count":2,"results":[native(previous),native(mine)],"next":None}
        with patch.object(sync,"source",lambda:iter([("sample",active)])), patch.object(sync,"api",api):
            result = sync.sync(42, IDS)
        self.assertEqual(result, {"count":1,"created":0,"updated":0,"retired":0,"multi_store_skus":0,"multi_store_rows":0})
        self.assertEqual(calls, ["GET","GET"])

    def test_interrupted_source_does_not_retire_or_write(self):
        active = sync.mapped(source_row())
        def broken():
            yield "sample",active
            raise OSError("Interrupted matrix export")
        calls = []
        def api(method, path, body=None):
            calls.append(method)
            return {"count":1,"results":[native({"id":10,**active})],"next":None}
        with patch.object(sync,"source",broken), patch.object(sync,"api",api):
            with self.assertRaises(OSError):
                sync.sync(42,IDS)
        self.assertEqual(calls,["GET"])


if __name__ == "__main__":
    unittest.main()
