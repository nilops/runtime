---
name: commit-conventions
description: "NilOps git commit and pull-request conventions: commit message format, hygiene gates before every commit and PR, forbidden AI-attribution trailers, and evidence-based PR bodies. Use before creating any commit, branch, or pull request in a NilOps repo."
license: Proprietary
metadata:
  owner: platform
  version: "1.0.0"
  category: vcs
  status: active
---

# NilOps Commit & PR Conventions

## Commit messages

1. Subject: imperative, ≤72 chars, no trailing period.
   `feat(hub): add external-ip reservation audit log`
2. Body (when non-trivial): WHAT + WHY, wrapped at 72. Evidence over adjectives.
3. Conventional prefixes: `feat`, `fix`, `refactor`, `test`, `docs`, `chore`,
   `perf`, `security`.
4. **No AI attribution — never** append `Co-Authored-By: Claude` (or any
   AI-attribution trailer) to commits, PRs, or comments. Strip it if a tool
   auto-appends one. This is a hard NilOps rule (see nilops-spec).
5. One logical change per commit. Mixed concerns → split commits.

## Approval gate (superset of hygiene)

Hygiene gates make commits clean; they do not authorize publishing them. Pushing,
opening/merging PRs, creating repos, and infra changes require the directing
member's explicit approval naming the action, in the conversation or channel
where they direct the agent — see the
`execution-approval-gate` skill.
"Continue" is never authorization.

## Pre-commit hygiene gates

Run these before every commit; all must pass:

1. `git status` — only intended files staged. Unrelated changes stay unstaged.
2. `git diff --cached` — review what you are actually committing.
3. Tests for touched code pass (repo's own runner; if the repo has a hygiene
   script, it gates the commit).
4. No secrets in the diff: `git diff --cached | grep -iE "token|secret|password|api[_-]?key"`
   must be clean or justified inline.

## Concurrency rule

NilOps agents may run concurrently on the same repo. Never bare `git commit -a`;
commit explicit paths so a sibling's in-flight edits are not swept in:

```bash
git add <explicit-paths>
git commit -F <message-file> -- <explicit-paths>
```

## PR voice — write as the human author (hard rule, 2026-10-24; generalized 2026-10-25)

Every PR title, body, and comment is published under a member's GitHub
account — the person the agent works for — and is visible to the whole
organization. The reader is an engineer reviewing the change, not the author
and not another agent. An agent always acts on behalf of one specific member;
the PR must read as that member's own writing, whoever it is. Therefore:

1. **Write in the author's voice — first person or neutral owner voice** —
   "I ran…", "This PR keeps…", or plain declarative. The author is the
   engineer describing their own change.
2. **Never use agent perspective**: no "I (the agent)…", no "as instructed",
   no "the author asked me to", no "awaiting approval", no "Say the word
   and I'll…", no references to channels, agents, sessions, or tooling.
3. **No third-person owner naming**: "David picks" → wrong, and so is any
   member's name in that slot. State the decision and its rationale directly
   ("Opened against #204's branch so CI runs").
4. Options/alternatives are fine — phrased as engineering trade-offs the
   author considered, not as questions to a person.
5. Approval and coordination happen in the workspace channel, never in the
   PR text. After approval, the PR states the outcome as a done decision.

Reference: nilops-hub #205 rewrite (2026-10-24), which prompted this rule.
Generalized 2026-10-25: applies to every member an agent works for, not one
person — this skill is company-wide.

## Branches & PRs

1. Branch naming: `<type>/<issue-or-feature>-<slug>` (e.g. `feat/ENG-1234-egress-menu`).
2. PR body: What / Why / Evidence (test output, screenshots, links), each labeled.
   Claims of "tested" must carry the actual command + output.
3. Link the tracking issue (Linear, when the linear skill is live) in the PR body.
4. PR title follows the commit subject rules.
5. Push each verified item — do not batch unrelated work into one PR.
6. Sibling edits to your files: keep them, rebase on top; never revert a
   collaborator's landed work without an explicit ask in-channel.

## PR readiness checklist

- [ ] Hygiene gates passed (above)
- [ ] No AI trailers anywhere in the history of this PR
- [ ] Body has real evidence, not summaries of intent
- [ ] Scope: one PR = one verifiable objective
- [ ] Linked tracking issue
