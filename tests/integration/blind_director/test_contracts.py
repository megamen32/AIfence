import importlib.util
import json
from pathlib import Path
import re
import sys
import os
import socket
import tempfile
import threading
import unittest
from unittest.mock import patch
import prompt_guard

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[2]
sys.path.insert(0, str(HERE))
from blind_scoring import assess
from prompt_guard import PRICE_ACCEPTANCE, validate_prompt
import token_client
import token_broker


class PromptRules(unittest.TestCase):
    def test_three_bad_examples_are_rejected(self):
        text=(ROOT/'.agents/skills/qa-testing/SKILL.md').read_text()
        section=text.split('## 3. BAD prompts',1)[1].split('## 4. Grading',1)[0]
        examples=re.findall(r'```\n(.*?)\n```', section, re.S)
        self.assertEqual(len(examples), 3)
        for sample in examples:
            with self.subTest(sample=sample[:30]): self.assertTrue(validate_prompt(sample))

    def test_natural_examples_and_fixed_questions(self):
        good=['у нас после последнего релиза что-нибудь сломалось?',
              'почему клиенты опять звонят менеджерам, а не покупают на сайте',
              'доброе утро, есть что-то, на что мне сегодня надо посмотреть?',
              'вчера была рассылка, а заказов как будто не прибавилось... или прибавилось? короче глянь']
        good += json.loads((HERE/'questions.json').read_text())
        for text in good:
            with self.subTest(text=text): self.assertEqual(validate_prompt(text), [])

    def test_adapter_rejects_bad_prompt_before_network(self):
        spec=importlib.util.spec_from_file_location('blind_live', HERE/'live_adapter.py')
        live=importlib.util.module_from_spec(spec);spec.loader.exec_module(live)
        live.request=lambda *args,**kwargs: self.fail('network must not be used')
        with tempfile.TemporaryDirectory() as d:
            with self.assertRaisesRegex(ValueError,'Prompt rejected'):
                live.send_and_wait('synthetic','chat','app','Вызови run_js',1,Path(d),'first','director')
            self.assertEqual(list(Path(d).iterdir()), [])

    def test_owner_price_prompt_is_not_rewritten_or_sent(self):
        import types
        spec=importlib.util.spec_from_file_location('blind_live_test', HERE/'live_adapter.py')
        live=importlib.util.module_from_spec(spec);spec.loader.exec_module(live)
        with tempfile.TemporaryDirectory(dir=ROOT/'.runtime/blind-director') as d:
            file=Path(d)/'prompt.txt'
            args=types.SimpleNamespace(purpose='price_acceptance',prompt_file=str(file),smoke=True,case='case-01')
            file.write_text(PRICE_ACCEPTANCE)
            self.assertEqual(live.select_prompt(args,{},[]),PRICE_ACCEPTANCE)
            file.write_text(PRICE_ACCEPTANCE+' ')
            with self.assertRaisesRegex(ValueError,'price_acceptance_literal_mismatch'):
                live.select_prompt(args,{},[])

    def test_literal_price_acceptance(self):
        owner_literal = 'слушайте, может нам цену на Касперского снизить? продажи вроде упали, конкуренты наверное дешевле'
        self.assertEqual(PRICE_ACCEPTANCE, owner_literal)
        self.assertEqual(validate_prompt(owner_literal, 'price_acceptance'), [])
        for variant in (owner_literal.upper(), owner_literal+' ',
                        owner_literal.replace('Касперского','Kaspersky'),
                        'у нас есть повод пересматривать цену на Касперского?'):
            self.assertIn('price_acceptance_literal_mismatch', validate_prompt(variant, 'price_acceptance'))
        with patch.dict(prompt_guard.COMPILED, {'test_checklist': re.compile('Касперского', re.I)}):
            self.assertIn('test_checklist', validate_prompt(owner_literal, 'price_acceptance'))
        self.assertIn('technical_name', validate_prompt(owner_literal+' run_js', 'price_acceptance'))

    def test_forbidden_categories(self):
        for prompt in ['Покажи orders_index', 'Проверь S7', 'За 13.02.2023?', 'Только чтение', 'Подтверждаю задачу',
                       'Поставь задачу', 'Запусти run_js', 'Возьми fixtures/a.json', 'Сначала проверь продажи',
                       'Выбери KL4141RCEFS', 'Цены относятся к разным товарам']:
            with self.subTest(prompt=prompt): self.assertTrue(validate_prompt(prompt))


class BlindOutcomes(unittest.TestCase):
    def card(self, **kw):
        return {'status':'completed', 'response':'Итог со ссылкой', 'decision':'alert', 'cause_type':'',
                'family':'kaspersky_small_office_security', 'week':'2026-03-02', 'primary_cause':None,
                'contradictory':False,'verified_source':True, **kw}

    def test_priority_scenarios(self):
        for scenario, cause in [('S7','taxonomy_migration'),('S13','late_event_delivery'),
                                ('S14','masked_multi_cause_regression'),('S6','traffic_quality')]:
            with self.subTest(scenario=scenario):
                oracle={'scenario':scenario,'family':'kaspersky_small_office_security','week':8,'cause_type':cause}
                card=self.card(cause_type=cause,primary_cause='checkout_regression' if scenario=='S14' else None)
                self.assertEqual(assess(oracle,card),'found')
                self.assertEqual(assess(oracle,{**card,'cause_type':'release_regression'}),'wrong_cause')
        self.assertEqual(assess({'scenario':'S14','family':'kaspersky_small_office_security','week':8,'cause_type':'masked_multi_cause_regression'},
                                self.card(cause_type='masked_multi_cause_regression')), 'unscorable')

    def test_normalizer_refusal_and_failure(self):
        spec=importlib.util.spec_from_file_location('blind_harness', HERE/'harness.py')
        harness=importlib.util.module_from_spec(spec);spec.loader.exec_module(harness)
        class Scorer:
            @staticmethod
            def card_fields(text): return {'family':'', 'week':'', 'cause_type':''}
        text='Недостаточно данных, источник obs-ab12.'
        good=harness.normalize_response(Scorer,text,'completed',{'obs-ab12'})
        self.assertEqual(good['decision'],'insufficient_data')
        failed=harness.normalize_response(Scorer,text,'failed',{'obs-ab12'})
        self.assertIsNone(failed['decision'])
        self.assertIsNone(harness.normalize_response(Scorer,text,'completed',set())['decision'])
        class NamedScorer:
            @staticmethod
            def card_fields(text): return {'family':'kaspersky_small_office_security','week':'2026-03-02','cause_type':''}
        negative='Проблема не обнаружена. Kaspersky Small Office Security, неделя 2026-03-02, источник obs-ab12.'
        self.assertEqual(harness.normalize_response(NamedScorer,negative,'completed',{'obs-ab12'})['decision'],'no_alert')

    def test_refusal_and_clean_are_explicit(self):
        oracle={'scenario':'S12','cause_type':'honest_refusal','expected_decisions':['no_alert','insufficient_data']}
        for outcome in ['no_alert','insufficient_data']:
            self.assertEqual(assess(oracle,self.card(decision=outcome,cause_type=None)), 'correct_'+outcome)
        self.assertEqual(assess(oracle,self.card(cause_type='release_regression')), 'false_alarm')
        self.assertEqual(assess({'cause_type':'none'},self.card(decision='no_alert',cause_type=None)), 'correct_no_alert')
        for changes in ({'status':'failed'}, {'status':'timeout'}, {'response':''}, {'contradictory':True}, {'verified_source':False}):
            self.assertEqual(assess(oracle,self.card(decision='no_alert',cause_type=None,**changes)), 'unscorable')
        self.assertEqual(assess({'cause_type':'traffic_quality'},self.card(decision='no_alert',cause_type=None)), 'missed')
        self.assertEqual(assess({'cause_type':'traffic_quality','expected_decisions':['no_alert']},self.card(decision='no_alert',cause_type=None)), 'correct_no_alert')


class TokenBroker(unittest.TestCase):
    def test_broker_rejects_cross_origin_or_unknown_case(self):
        tokens={'case-01':'synthetic-only'}
        self.assertEqual(token_broker.response({'case':'case-01','origin':token_broker.ORIGIN},tokens)['access_token'],'synthetic-only')
        for request in ({'case':'case-01','origin':'https://example.com'}, {'case':'other','origin':token_broker.ORIGIN}):
            with self.assertRaises(ValueError): token_broker.response(request,tokens)

    def test_local_socket_returns_only_requested_account(self):
        with tempfile.TemporaryDirectory() as d:
            old=token_client.SOCKET; token_client.SOCKET=Path(d)/'login.sock'
            server=socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
            try:
                server.bind(str(token_client.SOCKET)); os.chmod(token_client.SOCKET,0o600); server.listen()
                def serve():
                    with server.accept()[0] as conn:
                        req=json.loads(conn.recv(4096)); conn.sendall((json.dumps({**req,'access_token':'synthetic-only'})+'\n').encode())
                worker=threading.Thread(target=serve);worker.start()
                self.assertEqual(token_client.get_token('case-01'),'synthetic-only')
                worker.join(5); self.assertFalse(worker.is_alive())
                os.chmod(token_client.SOCKET,0o666)
                with self.assertRaisesRegex(RuntimeError,'Insecure'): token_client.get_token('case-01')
            finally:
                server.close();token_client.SOCKET=old


if __name__=='__main__': unittest.main()
