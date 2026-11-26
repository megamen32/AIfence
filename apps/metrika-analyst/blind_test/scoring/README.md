# Blind-test scoring and Sales export

This directory is deliberately separate from the agent-readable dataset.

## Score a card

Hidden answers must be stored outside the input catalog consumed by the agent:

```sh
python3 score_agent.py \
  --answers /private/aifence_answers \
  --cards /private/aifence_agent_cards \
  --input-root /Users/user/AIfence/apps/metrika-analyst/blind_test/input \
  --out /private/aifence_score.json
```

The scorer emits both JSON and CSV. Each scenario receives:

- `found` / `missed` — the family and failure week were identified;
- `false_alarm` — the card points at a different family or week;
- `cause_type_correct` — the cause class matches the hidden answer;
- `status` — `correct`, `found_wrong_cause`, `missed`, `correct_silence`, or `false_alarm`.

Accepted cause classes include the original tracking_gap, mobile_regression,
price_competition, release_regression, and clean, plus the selected
blind-test classes traffic_quality, taxonomy_migration,
late_event_delivery, masked_multi_cause_regression, and
experiment_allocation_shift.

For an intentional honest-refusal scenario such as S12, the hidden answer can
declare both accepted outcomes:

{"scenario":"S12","cause_type":"honest_refusal",
 "accepted_outcomes":["no_alert","insufficient_data"]}

A card with either explicit outcome is scored as correct: no_alert or
insufficient_data. A guessed family/week incident is scored as false_alarm.

## Export the customer-shaped Sales sheet

The exporter reads a truth JSON with `weeks` rows containing
`week_start`, `product_family`, `orders`, and `revenue_rub`:

```sh
python3 export_sales.py \
  --truth /private/truth/weekly_truth.json \
  --out /private/truth/Продажи.xlsx
```

The workbook contains a `Продажи` sheet with the customer's columns
`week_start`, `product_family`, `orders_index`, `orders_index_4w`,
`base_period`, `channel`, `comment`, plus full `orders` and `revenue_rub`.
