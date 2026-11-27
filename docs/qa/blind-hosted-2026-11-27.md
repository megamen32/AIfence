# Hosted blind package, 27 November 2026

One fixed director question was sent once per isolated user on PM project revision `b09ef84e34ff451d653f830652d8c37f5906bfa8`. The deployment receipt records sourceRef `c0aca0f0ba03db11d3ff7632e518ee406cb0cbb7` and runtime generation `30`; it does not itself map the sourceRef to the Git SHA. All 11 raw records were frozen before oracle access. Source datasets are synthetic. This table reports the hosted chat, not offline sourcewatch replay.

| Case / oracle | Expected | Observed result | Chat ID |
|---|---|---|---|
| 01 / S7 | Recognize taxonomy migration | No diagnosis; asks for price and search data | `1812a99e-8b85-4ab2-ad50-396fc08c5128` |
| 02 / S14 | Find masked checkout regression and secondary cause | Does not find it; says external sources unavailable | `391b0ab0-0ec7-40f6-9a34-7a7d49b01829` |
| 03 / S13 | Recognize late events | No diagnosis; says it will continue collecting | `fa9b1bb3-7fb7-4e14-b7bc-66e8803b83f2` |
| 04 / S6 | Distinguish bot traffic from sales regression | No finding; asks for product and external sources | `852ff40f-214d-454a-81c0-8edd77aba298` |
| 05 / S0 | No unsupported alert | First freeze was unknown after a transport read error; later completed, reporting sources unavailable | `546279fa-2095-4463-9526-f9e052e05f65` |
| 06 / S12 | Insufficient data; request variant split | Does not establish allocation shift or ask for the split | `10735acf-b037-4dde-bce3-0fa233aa8394` |
| 07 / Wordstat-only | No sales decline; distinguish search indicators | Promises to collect sources; no verified decision | `2b3ed0fa-8eb2-458a-869e-3bb5457e3518` |
| 08 / Google-only | No basis for changing price | Suggests a price cut might work and asserts unsupported sales and source findings | `1f8c7e3f-9d10-4993-b1d1-a6e59b2fa5a9` |
| 09 / price pressure | Sales decline against control; investigate price without asserting causation | Requests caution but asserts competitor prices and search decline without supporting values | `ad07bad0-157b-48b0-bf19-5c493bc59d45` |
| 10 / price and demand | Keep both explanations; no proven primary cause | Withholds causal conclusion; does not establish the required comparisons | `3bd69039-2cdb-4e2e-96f7-661e1a2d6706` |
| 11 / demand, no price gap | Demand signals agree; discount not established | Reports unavailable sources and requests further data | `9e57dc9a-1b1b-4917-96f6-fbe7dfb07ec3` |

**Source attribution.** A mentioned source or JavaScript request is not evidence of reading an observation. Calls were joined with their actual results by `toolCallId`. Across the 11 frozen chats, the current conservative parser identified zero returned observation rows with values and source locators. Cases 09 and 10 did receive a Google Trends product list and two competitor product identifiers; those are availability metadata, not trend values or prices. In case 02 the result explicitly returned unavailable sources; case 08 returned empty/zero results and `Done` strings, not supporting observations. Results sometimes include file-data references that were not read back in this audit, so zero identified rows does not prove the attached files were empty. No direct `submit_answer` call/result was found in the frozen chat parts; final text was inspected separately.

No case demonstrates all expected findings with source evidence. The five multisource cases were not run through a validated automatic scorer; do not represent that as an automatic 0/5 score. Case 08 contains an unsupported price-sensitive sales claim. Case 05's original timeout/unknown freeze and later terminal readback are both retained; the late answer must not be substituted silently.

For an honest diagnostic video, cases 02 and 10 are comparatively safer partial examples: both decline to assert a cause on the available outputs. Neither is a successful demonstration of the hidden scenario, and case 02 missed its checkout regression. Case 10 did not obtain comparable prices or a complete trend.

The first import attempt for case 01 created seven locally restricted snapshots that were absent from the model-facing index. No question had been sent. A transport correction removed the restriction that the test runner itself added to synthetic derived documents; the model-facing index then showed only the seven replacement snapshots. Original input files and first import receipts remain unchanged. The original six inputs required seven PM snapshots each; the five multisource inputs used one each. This asymmetry may affect discovery and limits comparisons.
