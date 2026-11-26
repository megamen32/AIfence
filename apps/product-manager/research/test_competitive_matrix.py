# -*- coding: utf-8 -*-
import json
import tempfile
import unittest
from pathlib import Path
import importlib.util


ROOT = Path(__file__).resolve().parent
SPEC = importlib.util.spec_from_file_location("competitive_matrix", ROOT / "competitive_matrix.py")
MODULE = importlib.util.module_from_spec(SPEC)
assert SPEC.loader
SPEC.loader.exec_module(MODULE)


class CompetitiveMatrixTests(unittest.TestCase):
    def reference(self):
        sheets = [
            ["Итоги", [[4, {"A": "Вес", "B": "10", "C": "25", "D": "25", "E": "20", "F": "10", "G": "10"}]]],
            ["03_Цена", [
                [4, {"A": "Конкурент", "B": "Продукт", "E": "Сопоставимый", "I": "Цена конкурента", "J": "Цена Store", "K": "Индекс"}],
                [5, {"A": "allsoft.ru", "B": "Kaspersky Endpoint Security", "E": "Да", "I": "110", "J": "100", "K": "1.1"}],
            ]],
            ["04_Покупка_Счет", [
                [4, {"A": "Конкурент", "B": "Продукт", "D": "Регистрация", "E": "Счёт", "F": "До счёта", "G": "До заказа", "I": "Срок"}],
                [5, {"A": "allsoft.ru", "B": "KES", "D": "Нет", "E": "Да", "F": "2", "G": "3", "I": "Да"}],
            ]],
            ["08_UX_Сайт", [
                [3, {"A": "Конкурент", "B": "Поиск", "C": "Фильтры", "D": "Навигация", "E": "Карточка", "F": "Лицензия", "G": "Сравнение", "H": "Цена", "I": "Наличие", "J": "Действия", "K": "Шаги", "L": "Мобильный", "M": "Скорость", "O": "Баллы"}],
                [4, {"A": "allsoft.ru", "B": "Да", "C": "Да", "D": "Да", "E": "Да", "F": "Да", "G": "Нет", "H": "Да", "I": "Да", "J": "Да", "K": "Да", "L": "Да", "M": "Да", "O": "4"}],
            ]],
            ["06_Личный_Кабинет", [[4, {"A": "Конкурент", "B": "ЛК"}], [5, {"A": "allsoft.ru", "B": "Да"}]]],
            ["07_Сервис_Преимущества", [[4, {"A": "Конкурент", "B": "Скидки", "I": "Баллы"}], [5, {"A": "allsoft.ru", "B": "Да", "I": "4"}]]],
            ["02_Поиск_Семантика", [[4, {"A": "Конкурент", "N": "Баллы"}], [5, {"A": "allsoft.ru", "N": "5"}]]],
        ]
        return sheets

    def test_price_score_and_missing_are_explicit(self):
        with tempfile.TemporaryDirectory() as tmp:
            tmp = Path(tmp)
            reference = tmp / "reference.json"
            reference.write_text(json.dumps(self.reference(), ensure_ascii=False), encoding="utf-8")
            agent = tmp / "agent.json"
            agent.write_text(json.dumps({"cells": [{
                "competitor": "allsoft.ru", "product": "KES", "block": "Цена",
                "criterion": "price_index", "value": 110, "value_type": "price"
            }]}), encoding="utf-8")
            report = MODULE.build_report(reference, agent, "Kaspersky Endpoint Security", ["allsoft.ru"])
            self.assertEqual(report["summary"]["matches"], 1)
            self.assertGreater(report["summary"]["missing"], 0)
            self.assertEqual(MODULE.price_score(1.1), 3.0)
            self.assertEqual(MODULE.truth_score("нет"), 1.0)

    def test_weighted_score_does_not_turn_missing_blocks_into_zero(self):
        with tempfile.TemporaryDirectory() as tmp:
            tmp = Path(tmp)
            reference = tmp / "reference.json"
            reference.write_text(json.dumps(self.reference(), ensure_ascii=False), encoding="utf-8")
            report = MODULE.build_report(reference, None, "Kaspersky Endpoint Security", ["allsoft.ru"])
            score = report["weighted_scores"][0]
            self.assertIsNotNone(score["score"])
            self.assertGreater(score["score"], 0)
            self.assertGreater(report["summary"]["missing"], 0)


if __name__ == "__main__":
    unittest.main()
