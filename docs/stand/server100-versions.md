# Server-100 pin record

Read-only capture from the personal Mac stand (docker context colima-aifence, Caddy host port 42080) on 2026-11-28. No secret values are included.

## Release pins

The running Airlock/frontend release is personal/2026.11.27.6.

| Component | SHA | Evidence |
| --- | --- | --- |
| Airlock core | 0c5951ebb5d32f666d94c7ef909dd8ebb13097ad | .runtime/personal-releases/2026.11.27.6/release.json, rollout-m1/rollout.json, image revision label |
| Agent SDK | 0c8f01752e1bf7917bcaced92eca96aa12482362 | release.json |
| AIfence app source bundle | d47e5103b383783f9590b9fbc2887be0a87e8db8 | apps/apps.json source_commit |
| Sol release component | 297ec9a165e3b03375d7b0030e7ca310814a8707 | release.json |

AIfence source SHA d47e5103b383783f9590b9fbc2887be0a87e8db8 exists on origin/main, origin/sol-merge-20261127, origin/fix/live-app-freeze-guard-20261127, and origin/personal-88/personal/integration. The Airlock, Agent SDK, and Sol SHAs belong to their respective upstream repositories and are not commits in this AIfence checkout.

## 42080 infrastructure containers

| Container | Image tag | Image ID |
| --- | --- | --- |
| aifence-airlock-1 | aifence-airlock:personal-0c5951ebb5d3 | sha256:74b2f8fd3f031006999362582cd98bd62da93c20de5c81ee3a0a148b152aabe8 |
| aifence-frontend-1 | aifence-frontend:personal-0c5951ebb5d3 | sha256:f8f31fa881bed29faf0ded7cedd762e7716939f7130d7baee1d3a6a401e57f7d |
| aifence-caddy-local-1 | caddy:2-alpine | sha256:003a97808e781aa9f7e75cc2f43d34d461099031faf5e20faefb9f61126836f0 |
| aifence-postgres-1 / bootstrap | pgvector/pgvector:pg17 | sha256:b513c48566e4777cd3370451400000c78eda6a08131d68f4c31d4a76f27bd8fd |
| aifence-rustfs-1 | rustfs/rustfs:latest | sha256:fea0d9b89cd2f70a2f02d66d34b00bb2b3e72c48aafe892125ec9500dcaf50f9 |
| agent-base | aifence-agent-base:be0f9ee03be3 | sha256:a03f5dca625b90051af27670bd8a29c08854cc0e8a84a5b89e3cf397df74bce6 |
| agent-builder | aifence-agent-builder:be0f9ee03be3 | sha256:4ca7514c7d0e53a74f7be5e29d28b52ef6a2ccd267bc5ed345530290eb54b30c |
| js-executor | aifence-js-executor:be0f9ee03be3 | sha256:d6d1f14be6f837313dc53815e07e3dc76a4e213d41a85ef3c0fe9a2241fc3268 |

Airlock/frontend labels report Airlock-internal image revision 0c5951ebb5d32f666d94c7ef909dd8ebb13097ad, runtime protocol airlock.app-runtime.v4, schema 35.

## Sidecars

| Service | Image tag | Image ID |
| --- | --- | --- |
| Baserow | baserow/baserow:2.4.3 | sha256:304bebe22bf4c66d96d11bb746727492fa91637bd6deb807b2ef464f071c215c |
| Metrika MCP | tagless local image | sha256:af6e345a1fe1d5b7b87c193ad24b9bd95d1f9ce77f9aac70e27a41484f74d2dd |
| Redmine | redmine:6.1.6-alpine3.23 | sha256:cfdfa8a50f426d439b62a2baaaf8121246f3359a4f85a49b7d2cc23d0ad3b22e |
| Redmine DB | postgres:16-alpine | sha256:f2f98726820d0342055351c6e9d36e705f5bfffb3dd53ff556325114b08afb6d |
| Redmine MCP | aifence-redmine-mcp:2.23.0 | sha256:16b7b703f4940b67df528b17a0a323ceb37304658d96896138d7963e985b15ef |
| UserIO Dion | userio-airlock:56c66260-mcp-oauth-arm64 | sha256:184ddd32c7f05b506fb882216a85200f38844f91ad966e9f131eb41871fffa52 |

UserIO image revision label: 56c66260f2c1bf75c4b615e369fd0e032184e0ee. Caddy sidecars use the caddy ID above. Redmine runtime is /Users/user/Airlock-apps/redmine-runtime; Metrika is .runtime/m1-native-sourcewatch-20261127/gateway; UserIO is /Users/user/apps/userio-airlock-standalone.

## Airlock app records

All production manifests have runtime version 0.11.0. token_version is the runtime generation; config_revision is the deployment config revision. Manifest SHA-256 is computed from the exact JSON text in agent_runtime_manifests.manifest.

| App | App ID | Source ref | Image ref | token | config | Manifest SHA-256 |
| --- | --- | --- | --- | ---: | ---: | --- |
| Baserow | 37aa7358-942e-44c4-acd0-b971e83e0ce9 | 42e14e662657c6cbf333030b401d2d1bdb794a6a | 37aa7358-942e-44c4-acd0-b971e83e0ce9:42e14e662657 | 9 | 9 | 706158923dec95932b1dfd05751dec2e3e3bcb752971232070e22d6b60c5e81c |
| Competitor Research | 03dc1bb2-869e-4783-897f-7c7fbc4b395d | 38084395dc6add0c2634e560bb9258e965ce26fa | 03dc1bb2-869e-4783-897f-7c7fbc4b395d:38084395dc6a | 4 | 4 | f2d2435a9143b57a721682c145a8343cfd0ef402b63495bb43f48e22f4096695 |
| Market Watch | 8702e3ad-c528-45af-8172-00e3be59ae7f | 194805802313b95f4e80c33b7a2e3a49e0344a86 | 8702e3ad-c528-45af-8172-00e3be59ae7f:194805802313 | 19 | 18 | 0e9e874d83ac8fad041dd21d4f1bde7ae73026d72bd1a409418143164d5f8bd1 |
| Metrika Analyst | 6e985f49-ae84-4f6d-b345-edb4ea0d95cf | bfbcff813a00e4c8f8654662b3d78a8421eec751 | 6e985f49-ae84-4f6d-b345-edb4ea0d95cf:bfbcff813a00 | 25 | 26 | d244814c86abb23e54568294bb8f2e494b34bb63c9389722a5b259adce9d02e2 |
| OpenAI BYOK | 624d3617-da89-479f-954f-1c2f54f23fbe | dbeb5cfe66a88f571faaae5166b34d329b814c7c | 624d3617-da89-479f-954f-1c2f54f23fbe:dbeb5cfe66a8 | 3 | 3 | c12c1071a065b9c567d5c38cb4b5478e639584280a46e1c107ac041694338fda |
| Product Manager | 01a5630d-261e-4c41-88a7-15fe5bd4fa2b | 159d7daf4ddc5b9f91958d058569e1b711e18820 | 01a5630d-261e-4c41-88a7-15fe5bd4fa2b:159d7daf4ddc | 26 | 31 | 104c017a4be5f2a23bf548ac6df9f1f056d0c17f564ab91e1d0303f946ea2fd8 |
| Redmine | 358285c9-3347-436e-812d-55c6d3a61e73 | d38f9f8cc429cc0d55a62feefa10fa8b3df83fe6 | 358285c9-3347-436e-812d-55c6d3a61e73:d38f9f8cc429 | 5 | 4 | c779486787c3b672aae74ff7e1c2f06b94f7116ce3fe9db2775641647ba41add |
| UserIO Dion | 870d47ee-6234-4109-a6b3-3e06f960a094 | 96b19a2f9e6ea96e5f412c1dd65656da8a59e5e5 | 870d47ee-6234-4109-a6b3-3e06f960a094:96b19a2f9e6e | 9 | 11 | ffbfe2ac8e1b34d0f3aa5df9cb7b121b2778b11efeedbb9bc5f9d7d5a80a8bdf |
| Web Scraper | cdd2224a-e2e8-4be8-a325-49fe7fa37be7 | 5e4eab47e5eca868e60517228c4d6637bb8318d8 | cdd2224a-e2e8-4be8-a325-49fe7fa37be7:5e4eab47e5ec | 6 | 6 | 4fd8597a24c506c891475ddaf889bee0d6e865f4714666c16dbdec857e64ae17 |

The release app package source_commit is d47e5103b383783f9590b9fbc2887be0a87e8db8. Individual source_ref values are Airlock source revisions, not commits in this AIfence repository.

## Database majors

Airlock bundled database: pgvector/pgvector:pg17 (Postgres 17), migration schema 35. Redmine bundled database: postgres:16-alpine (Postgres 16). Pin Baserow's bundled Postgres major from apps/baserow/compose.yml.

## Running 42080 app containers

The live 42080 stack also had five Airlock-managed app containers. Their image labels report the pinned Airlock source SHA `be0f9ee03be31ee15dacd5f6ebcc0f28acea6468`; the per-app source refs and manifest hashes above are the app-generation pins. The `run.airlock.source-digest` label is a content digest, not a Git SHA.

| Container | Image ref | Image ID |
| --- | --- | --- |
| aifence-agent-01a5630d-261e-4c41-88a7-15fe5bd4fa2b | 01a5630d-261e-4c41-88a7-15fe5bd4fa2b:d1be948e647d | sha256:d0f54bb29158bd28dbd6d269f16fe608fb7e4f283107229b8ec796ed646c003f |
| aifence-agent-37aa7358-942e-44c4-acd0-b971e83e0ce9 | 37aa7358-942e-44c4-acd0-b971e83e0ce9:42e14e662657 | sha256:971e3b183441bc4eb372662c8de14911e1f0651c7449457afd8d9087da9e7e6b |
| aifence-agent-6e985f49-ae84-4f6d-b345-edb4ea0d95cf | 6e985f49-ae84-4f6d-b345-edb4ea0d95cf:299e53bb21da | sha256:7bd71d4bcf1f047a3892ddf7c90ed9a95230d3c5ec6aa235c65a2a915494d631 |
| aifence-agent-8702e3ad-c528-45af-8172-00e3be59ae7f | 8702e3ad-c528-45af-8172-00e3be59ae7f:14a80479963f | sha256:2ee33a45c971afb1eb6b8d948911ac6e81b206be02044a856c74ea1414ee6add |
| aifence-agent-cdd2224a-e2e8-4be8-a325-49fe7fa37be7 | cdd2224a-e2e8-4be8-a325-49fe7fa37be7:51cb9a874b1c | sha256:e2012085f3dbcd62cb2be8f58c574ced6fe2944320572b3917493a183384a027 |

The exact full container inventory, including QA-only containers excluded from the personal move, is captured at pin time in `/home/roomhacker/aifence-move/pin/containers.tsv`.
