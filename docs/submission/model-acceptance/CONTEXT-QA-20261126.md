# Gemma 4 long-context acceptance — 2026-11-26

Scope: server-44, Gemma 4 8B Q4_K_M, model digest prefix c6eb396dbd59,
RTX 3080 Ti 12 GiB, approximately 125 GiB host RAM.

## Distinguish three different results

1. Current production queue: maximum configured context 8192. A synthetic
   near-full-window recall request returned 7605 prompt tokens, 48 output
   tokens, all three markers correct, 13.105 s wall time (1.670147 s prefill).
   It used the existing queue, without changing the model or proxy settings.
2. Current isolated CPU runtime: allocation was verified through 131072.
   The isolated runtime used one slot, no GPU, a 32 GiB memory ceiling and
   bounded CPU/time limits. These are short-prompt allocation checks, not
   proof that the model recalls all information at the allocated size.
3. Historical GPU evidence from the same model digest: the May 23 clean
   benchmark reached 104448 allocated context before CPU-offload at 105472.
   On May 28 the refined benchmark reported 92439 actual input tokens at
   context 93184, GPU_OK; context 94208 was MIXED with 93471 input tokens.
   The old benchmark freed the GPU from other processes. Its capacity is
   not a guarantee for today's shared GPU and two parallel slots.

## Fresh isolated allocation sweep

| Requested / allocated context | Actual short input tokens | Recall markers | Peak cgroup bytes | Wall s | Result |
|---:|---:|---:|---:|---:|---|
| 32768 | 326 | 3/3 | 11225169920 | 25.495 | allocation pass |
| 49152 | 324 | 3/3 | 11572117504 | 29.446 | allocation pass |
| 65536 | 325 | 3/3 | 11845050368 | 29.343 | allocation pass |
| 98304 | 322 | 3/3 | 12389888000 | 29.543 | allocation pass |
| 131072 | 323 | 3/3 | 12940599296 | 29.549 | allocation pass |

The calibration recall run at 4096 allocated context processed 3605 input
tokens and recovered 3/3 markers. CPU prefill 84.54 tokens/s, total 63.392 s.
The subsequent full 32K CPU recall attempt was stopped after the log showed
7168 tokens processed, because the projected runtime exceeded the bounded
request timeout. Fresh full-window recall at 32K/50K/64K was not established.

Historical log locators on server-44:
- /root/ollama_ctx_smart_search.log (2026-05-23)
- /root/llm_bench_ctx_refine_20260528_051640_results/context_cache.jsonl
  (2026-05-28, Gemma rows). Do not publish the raw historical logs or scripts.

## Operational recommendation

64K (65536 total context) is the proposed next dedicated GPU test target,
not a deployed limit or a guaranteed shared-GPU setting. A 50K actual-input
recall test should reserve output and system-template space. Measure exact
prompt_eval_count, marker recovery across the entire input, GPU residency,
memory, first-byte latency and complete response. Keep the current queue
cap at 8192 until a controlled GPU run and route acceptance pass.

The active queue has a global default OLLAMA_PROXY_MAX_NUM_CTX=8192 and
supports model-specific OLLAMA_PROXY_MODEL_CTX_LIMITS overrides. Any future
increase should be limited to the Gemma route, with matching LiteLLM and
Airlock catalog limits. Verify parallel-slot allocation and avoid restarting
the shared service during active hackathon work. Do not advertise a 128K
effective route merely because the model can allocate that window on CPU.

The reproducible Python harness is context_benchmark.py. Use it against an
isolated loopback Ollama runtime. Allocation-only mode is deliberately
separate from full recall. Example:

```sh
python3 context_benchmark.py --base-url http://127.0.0.1:PORT --cpu-only --allocation-only --contexts 32768 49152 65536 98304 131072 --timeout 60 --budget-seconds 300
```

For full recall omit --allocation-only and choose realistic request and
total-budget limits. The harness refuses shared Ollama ports by default.
All input is synthetic; no customer data is required.

No production context settings or model defaults were changed in this audit.
