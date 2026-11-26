# Synthetic model acceptance

This is a model transport and capability gate, not the full product KES
acceptance. It sends only fixed synthetic prompts and a generated red square.
It never executes the returned function call. A passing model still needs a
full Airlock run against the pinned AIfence build. HTTP 200 alone is not a pass.

Run offline unit tests:

```sh
cd docs/submission/model-acceptance
python3 -B -m unittest -v
```

Run the explicit chosen OpenAI-compatible endpoint, e.g. from the authorized
local inference host (use its actual queue-proxy base URL):

```sh
python3 -B model_acceptance.py --base-url http://127.0.0.1:11437/v1 --model gemma4:latest
```

For an authenticated gateway use `--key-env NAME` with an existing authorized
environment variable. Never include a key in a command, report or Git.
Results are JSON lines: per-case outcome, HTTP status, elapsed time, usage if
reported; an absent usage record is unknown. Cases run sequentially with no
automatic retry. The stream case requests usage explicitly; a stream that ends
without the expected text is a failure, even if headers report success.
Model metadata/flags and a text-only canary are not proof of tool/vision support.
Run in an isolated synthetic session; do not send the customer's workbook,
interview text or screenshots to unverified routes. Use the final open model
and final endpoint, not a silently substituted model.

The HTTP timeout is a socket timeout. Use a bounded process executor if an
absolute end-to-end deadline is needed. The tool is intentionally independent
of Airlock internals and does not change provider settings or other sessions.

The evidence case uses synthetic observations for Kaspersky Small Office Security and Dr.Web Desktop Security Suite. It requires the model to preserve missing data, avoid unsupported causal claims and respect human approval. The 96/256/512 output limits are intentionally separate: models that consume a thinking budget before answering may otherwise return an empty response. The original short-budget vision failure must remain recorded; the expanded budget is a separately measured configuration, not a retroactive pass.
