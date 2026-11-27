import json
import unittest

import blind30_trace


class TraceTests(unittest.TestCase):
    def test_pairs_results_and_ignores_error_and_catalog(self):
        def msg(part):return {'parts':json.dumps([part])}
        calls=[]
        for callid in ('used','empty','error','missing'):
            calls.append(msg({'type':'tool-call','toolCallId':callid,'args':{'code':'return await apps.market_watch.tools.list_offers({});'}}))
        row={'value':42,'unit':'RUB','source_ref':{'source_id':'market','locator':'offer/1'}}
        calls.extend([
            msg({'type':'tool-result','toolCallId':'used','output':{'value':json.dumps({'observations':[row]})}}),
            msg({'type':'tool-result','toolCallId':'empty','output':{'value':json.dumps({'available_products':['sku']})}}),
            msg({'type':'tool-result','toolCallId':'error','output':{'value':json.dumps({'isError':True,'observations':[row]})}}),
            {'role':'assistant','content':'answer','parts':''},
        ])
        result=blind30_trace.trace({'chat':{'messages':calls}})
        by_id={x['tool_call_id']:x for x in result['calls']}
        self.assertEqual(len(by_id['used']['observations']),1)
        self.assertEqual(by_id['empty']['observations'],[])
        self.assertEqual(by_id['error']['observations'],[])
        self.assertTrue(blind30_trace.failed({'type':'error-text','value':'execution error'}))
        self.assertFalse(by_id['missing']['result_received'])
        self.assertEqual(result['final'],'answer')


if __name__=='__main__':unittest.main()
