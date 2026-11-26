import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

HERE=Path(__file__).resolve().parent
ROOT=HERE.parents[2]
spec=importlib.util.spec_from_file_location('input_lint',HERE/'lint_inputs.py')
lint=importlib.util.module_from_spec(spec);spec.loader.exec_module(lint)
spec=importlib.util.spec_from_file_location('test_generator',ROOT/'apps/metrika-analyst/demo_sources/blind_test/blind_test_generator.py')
g=importlib.util.module_from_spec(spec);spec.loader.exec_module(g)

class InputLint(unittest.TestCase):
    def test_all_xlsx_sheets_and_json_are_checked(self):
        products={'kaspersky_small_office_security': {'label':'Kaspersky Small Office Security','sku':'KL4141RCEFR','catalog_seller':'Softline'},
                  'dr_web_desktop_security_suite': {'label':'Dr.Web Desktop Security Suite','sku':'LBW-BC-12M-100-A3','catalog_seller':'SoftMagazin'}}
        with tempfile.TemporaryDirectory() as d:
            p=Path(d)
            (p/'manifest.json').write_text(json.dumps({'families':list(products)}))
            (p/'metrika.json').write_text(json.dumps({'observations':[]}))
            (p/'releases.json').write_text('[]');(p/'backlog.json').write_text('[]')
            sheets={'Продажи':[{'product_family':products['kaspersky_small_office_security']['label'], 'product_key':'kaspersky_small_office_security','sku':'KL4141RCEFR'}],
                    'Релизы':[{'family':'kaspersky_small_office_security','product_label':'Kaspersky Small Office Security','sku':'KL4141RCEFR','catalog_seller':'Softline','description':'Обновление карточки продукта'}],
                    'Бэклог_задачи':[{'family':'dr_web_desktop_security_suite','product_label':'Dr.Web Desktop Security Suite','sku':'LBW-BC-12M-100-A3','catalog_seller':'SoftMagazin','title':'Проверить карточку'}]}
            g.write_xlsx(p/'Продажи.xlsx',sheets)
            issues,checked=lint.lint(p,products)
            self.assertFalse(any(issues.values()),issues)
            self.assertEqual((checked['xlsx_sheets'],checked['xlsx_product_rows'],checked['xlsx_context_rows']),(3,1,2))
            sheets['Бэклог_задачи'][0]['title']='demo_target'
            sheets['Релизы'][0]['family']='microsoft_365'
            g.write_xlsx(p/'Продажи.xlsx',sheets)
            issues,_=lint.lint(p,products)
            self.assertGreater(issues['unverified_placeholder_strings'],0)
            self.assertGreater(issues['unverified_xlsx_context_product'],0)

if __name__=='__main__':unittest.main()
