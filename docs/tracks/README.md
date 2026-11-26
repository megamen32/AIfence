# Parallel tracks

Branch names (local Mac worktrees under /Users/user/AIfence-worktrees/):
- track/market-watch: apps/market-watch
- track/metrika-analyst: apps/metrika-analyst
- track/product-manager: apps/product-manager
- track/research-data: data-tools + fixtures/synthetic (schema edits only by agreement)
- track/integration-qa: tests + docs/qa

Read exact assignment in matching file. Each owner commits only assigned files; integration maintainer merges after tests. Do not use main for parallel edits. Private input path /Users/user/AIfence/материалы is shared read-only; normalized local results in /Users/user/AIfence/data, ignored by Git, not copied to worktrees. On remote host use authorized access to inputs, not public transfer. Track branches do not deploy automatically. 
