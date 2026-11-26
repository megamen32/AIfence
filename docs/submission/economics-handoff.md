# Economics measurement handoff

The local QA measurement package is stored on the M1 in the Git-ignored
`data/submission/economics-audit-20261126/` directory. Read `report.md`
first. It contains measured Airlock run usage, a native detector
microbenchmark, provenance, calculator checks and the remaining acceptance
steps. The historical QA data are synthetic, not a final KES/Gemma run.

For final acceptance, measure one full run on the deployed open model:

1. Record its actual model ID, build, date and scope.
2. Read the completed conversation using `scripts/measure-chat` on the
   correct Docker context. Use only aggregate usage from the intended run.
   Measure the native scheduled detector separately.
3. Fill the private `final-input.json` and `sources.json` in the local
   package with the confirmed usage, tariff, computing allocation, order
   counts and contribution from Q&A. Keep unverified values null. An order
   index is not an order count; a zero platform cost estimate does not
   establish free execution.
4. Run:

   ```sh
   python3 -B docs/submission/economics.py data/submission/economics-audit-20261126/final-input.json
   ```

5. Prepare the authorized submission summary. Keep raw run identifiers,
   private customer values and unapproved measurements outside Git.

The calculator's exact arithmetic, missing-input handling and invalid-input
rejection were checked on local synthetic inputs. This handoff does not
claim that the final deployment or the final open-model measurement passed.
