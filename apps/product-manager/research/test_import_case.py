import tempfile
import unittest
from datetime import date, timedelta
from pathlib import Path

from import_case import choose_target, observed_until, read_weekly, write_json


class EvidenceTests(unittest.TestCase):
    def test_partial_week_ends_at_source_date(self):
        cutoff=date(2026,7,26)
        row=(7,date(2026,7,20),52,True,'Неделя неполная: данные по 23.07.2026')
        self.assertEqual(observed_until(row,cutoff),date(2026,7,23))

    def test_missing_and_duplicate_weeks_do_not_become_zero(self):
        start=date(2026,6,1)
        row={'A':str(start),'B':'P','C':None}
        with self.assertRaisesRegex(ValueError,'Missing numeric'):
            read_weekly({2:row},'P',start,start,start)
        row['C']=5
        with self.assertRaisesRegex(ValueError,'Duplicate'):
            read_weekly({2:row,3:row},'P',start,start,start)

    def test_discovery_follows_numbers_and_eligible_release(self):
        release=date(2026,6,3)
        first=date(2026,3,2)
        rows={}
        for item,late_value in [('Alpha Tools',60),('Beta Suite',110)]:
            for i in range(23):
                day=first+timedelta(weeks=i)
                rows[len(rows)+2]={'A':str(day),'B':item,'C':100 if day<release else late_value}
        releases={2:{'A':'rel-A','B':str(release),'D':'Alpha catalogue','F':'family_orders_idx'},
                  3:{'A':'rel-B','B':str(release),'D':'Beta UI','F':'family_orders_idx'},
                  4:{'A':'unrelated','B':str(release),'D':'Alpha email','F':'traffic'}}
        cutoff=date(2026,8,9)
        target,control,rel,_=choose_target(rows,releases,cutoff)
        self.assertEqual((target,control,rel),('Alpha Tools','Beta Suite','rel-A'))
        for row in rows.values():
            if row['B']=='Beta Suite' and row['A']>str(release):
                row['C']=10
        self.assertEqual(choose_target(rows,releases,cutoff)[0],'Beta Suite')

    def test_declines_without_linked_release_are_not_selected(self):
        with self.assertRaisesRegex(ValueError,'No eligible release'):
            choose_target({}, {2:{'A':'M','B':'2026-06-03','D':'Marketing','F':'traffic'}}, date(2026,7,10))

    def test_private_output_stays_outside_app(self):
        with self.assertRaisesRegex(ValueError,'outside'):
            write_json(Path(__file__).parent / 'unwanted.json',{})
        with tempfile.TemporaryDirectory() as tmp:
            target=Path(tmp)/'data.json'
            write_json(target,{'version':1})
            write_json(target,{'version':2})
            self.assertEqual(target.read_text(),'{"version":2}\n')


if __name__=='__main__':
    unittest.main()
