---
name: github-actions-pipeline
description: "STUB — scope agreed, implementation pending. Will cover initializing pipelines from the blessed reusable workflow, adding/modifying custom composite-action steps, SHA pinning, and the CI gate that enforces the standard. Use only to check agreed scope, not as a working procedure."
license: Proprietary
metadata:
  owner: platform
  version: "0.1.0"
  category: cicd
  status: stub
  impl-session: separate OpenAgents thread session (implementation planned soon)
---

# GitHub Actions Pipeline Standard (STUB)

> **Status: stub.** This skill records the agreed scope only. Pipeline
> standardization implementation is planned soon and will be handled in a
> dedicated OpenAgents session. Do NOT treat this as a usable procedure yet.

## Agreed scope (to be implemented)

1. **Pipeline initialization** — new workflows MUST extend the blessed reusable
   workflow (to be authored in this repo or a companion `.github` repo) instead
   of hand-rolling job definitions.
2. **Custom steps** — company-specific steps are composite actions with pinned
   dependencies, versioned in-repo; no inline `run:` blobs for shared logic.
3. **Supply-chain rules** — all third-party actions SHA-pinned (no floating
   tags); provenance noted for internal actions.
4. **Enforcement gate** — a checker workflow in product repos that fails PRs
   touching `.github/workflows/**` when they bypass the standard (not extending
   the reusable workflow, floating tags, etc.).
5. **Conventions** — naming, required checks per repo type (Go/Python/TS/IaC),
   WIF-based auth (already proven in gcp-provision), environment protections.

## References

- Research summary: workspace file `agent-skills-standardization-plan-draft-v1.md`
