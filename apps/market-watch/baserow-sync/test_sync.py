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
        with patch.object(sync, "request", side_effect=[page([source, another, unique, only_url]), page([])]):
            rows = dict(sync.source())
        self.assertEqual([rows[k]["Магазинов по артикулу"] for k in ("first","other","single","missing")],
                         ["2","2",None,None])
        self.assertTrue(all(row["Разница"] is None for row in rows.values()))

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
            return {"results":[previous,mine],"next":None}
        with patch.object(sync,"source",lambda:iter([("sample",active)])), patch.object(sync,"api",api):
            result = sync.sync(42, {"active":13})
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
            return {"results":[{"id":10,**active}],"next":None}
        with patch.object(sync,"source",broken), patch.object(sync,"api",api):
            with self.assertRaises(OSError):
                sync.sync(42,{"active":13})
        self.assertEqual(calls,["GET"])


if __name__ == "__main__":
    unittest.main()
