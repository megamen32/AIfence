# /// script
# requires-python = ">=3.11"
# dependencies = ["jsonschema[format]>=4.18,<5"]
# ///
import copy
import json
import unittest
from pathlib import Path

from jsonschema import Draft202012Validator, FormatChecker
from referencing import Registry, Resource


ROOT = Path(__file__).resolve().parents[1]
FIXTURES = ROOT / "fixtures/synthetic/competitors"


def read(path):
    return json.loads(path.read_text(encoding="utf-8"))


class CompetitorContracts(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        schemas = [read(ROOT / "contracts" / name) for name in
                   ("evidence-v1.schema.json", "signal-v1.schema.json")]
        for schema in schemas:
            Draft202012Validator.check_schema(schema)
        registry = Registry().with_resources(
            (schema["$id"], Resource.from_contents(schema)) for schema in schemas
        )
        cls.evidence, cls.signal = [
            Draft202012Validator(s, registry=registry, format_checker=FormatChecker())
            for s in schemas
        ]

    def test_examples_and_confirmation_references(self):
        batch = read(FIXTURES / "allsoft-kes-observations.json")
        change = read(FIXTURES / "allsoft-kes-change.json")
        self.evidence.validate(batch)
        self.evidence.validate(change)
        self.signal.validate(change)
        self.assertFalse(self.signal.is_valid(batch))
        versions = {row["source_ref"]["revision"]: row["value"] for row in batch["observations"]
                    if row["metric_key"] == "purchase.steps_to_cart"}
        details = change["observations"][0]["details"]
        self.assertEqual([versions[v] for v in details["snapshot_ids"]],
                         [details["before"], details["after"], details["after"]])

    def test_typed_readings_and_required_proof(self):
        batch = read(FIXTURES / "allsoft-kes-observations.json")
        for index, wrong in ((0, True), (3, "no"), (4, ["term", "term"])):
            with self.subTest(index=index):
                bad = copy.deepcopy(batch)
                bad["observations"][index]["value"] = wrong
                self.assertFalse(self.evidence.is_valid(bad))
        row = batch["observations"][0]
        del row["source_ref"]["url"]
        self.assertFalse(self.evidence.is_valid(batch))
        row["details"].update(screenshot_ref="frames/KES-checkout.png", origin="customer_research")
        self.evidence.validate(batch)
        row["details"]["origin"] = "unknown"
        self.assertFalse(self.evidence.is_valid(batch))

    def test_confirmed_change_requires_independent_reading(self):
        change = read(FIXTURES / "allsoft-kes-change.json")
        row = change["observations"][0]
        ids = row["details"]["snapshot_ids"]
        last = ids.pop()
        self.assertFalse(self.signal.is_valid(change))
        ids.append(ids[-1])
        self.assertFalse(self.signal.is_valid(change))
        ids[-1] = last
        row["details"]["confirmed_by_second_snapshot"] = False
        self.assertFalse(self.signal.is_valid(change))
        row["details"]["confirmed_by_second_snapshot"] = True
        row["details"]["after"] = "3"
        self.assertFalse(self.signal.is_valid(change))

    def test_legacy_sources_keep_their_contract(self):
        batch = read(ROOT / "fixtures/synthetic/metrika-evidence.json")
        self.evidence.validate(batch)
        batch["observations"][0]["value"] = ["edition"]
        self.assertFalse(self.evidence.is_valid(batch))
        change = read(FIXTURES / "allsoft-kes-change.json")
        change["source"] = "sales"
        self.assertFalse(self.signal.is_valid(change))
        row = change["observations"][0]
        row.update(kind="metric", metric_key="watchdog.alert", unit="boolean", value=True)
        row["details"] = {"control_product_key": "astra_linux_special_edition", "metric_key": "orders",
                          "evidence_ids": [row["id"]], "reason": "observed_decline"}
        self.signal.validate(change)
        row["value"] = 2
        self.assertFalse(self.signal.is_valid(change))


if __name__ == "__main__":
    unittest.main()
