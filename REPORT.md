# pm-deeplink

## Code

- `origin/main` did not contain the Product Manager deep-link slice.
- Commit `771c014` adds `source_ref.deep_link` to the evidence contract.
- Wordstat, Google Trends, Market Watch and Metrika source references receive owner-facing URLs on `localhost:42080`.
- Evidence rows expose the link and `submit_answer` renders an HTTPS/HTTP reference as Markdown.

## Build and deploy

- `scripts/personal-build-apps.py --commit HEAD --app product-manager --output .runtime/pm-build` stopped because the pinned `agentsdk` source is absent from the local personal cache.
- The Product Manager currently running on 42080 is source `206b48645546f7839d61058f396b4c21a2d51ec4`, runtime generation `44`.
- No deployment was performed for `771c014`.
- Live owner query and screenshot acceptance remain pending because the new commit is not running.

## Design review

No post-deploy screenshots were captured. The mandatory 1440px and 420px visual acceptance is pending a successful Product Manager deployment.
