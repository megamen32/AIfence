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

Accepted cause classes are `tracking_gap`, `mobile_regression`,
`price_competition`, `release_regression`, and `clean`.

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
