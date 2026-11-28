# Economics measurement handoff

Measured Terra fast blind30 values:

- 20 cases, 18 correct causes;
- 10 negative cases, 0 false alarms;
- median 112 seconds;
- 0 errors;
- $0.028 per case;
- previous detector result: 4/20 correct causes and 7 false alarms;
- result after PR #18 and #19: 14/20 correct causes and 1 false alarm.

The calculator uses:

```json
{
  "price_usd_per_case": 0.028,
  "cases_per_period": 20,
  "model_call_count": 1
}
```

Run:

```sh
python3 -B docs/submission/economics.py data/economics-input.json
```

Formula: `cost_usd_per_period = price_usd_per_case × cases_per_period × model_call_count`.
