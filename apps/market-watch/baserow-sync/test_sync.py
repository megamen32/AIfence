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
        self.assertEqual(result, {"count":1,"created":0,"updated":0,"retired":0})
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
