# Issue tracker: GitHub Issues and Projects

Issues and specs live in the GitHub repository [lpyaia/llm-clinic-scheduler](https://github.com/lpyaia/llm-clinic-scheduler). Track implementation issues in [GitHub Project 1](https://github.com/users/lpyaia/projects/1), owned by `lpyaia`.

## Access and operations

Prefer a connected GitHub integration when it supports the operation. Otherwise use an authenticated `gh` CLI or browser session. Never treat a missing login or insufficient permission as evidence that a private project does not exist.

- Read the full issue body and comments before working on an existing issue.
- List existing issues before publication to avoid duplicates.
- Create repository issues rather than draft-only Project items. Use a UTF-8 body file for multiline CLI descriptions.
- Add every implementation issue to Project 1. With the CLI: `gh project item-add 1 --owner lpyaia --url <issue-url>`.
- Verify both the repository issue and its membership in the Project after publication.
- Read existing Project fields and options before changing them; do not invent a mapping between Project status and triage labels.
- Apply the configured triage labels from [triage-labels.md](triage-labels.md). Tickets published by `to-tickets` use `ready-for-agent`.
- Creating issues does not authorize changing or closing a parent issue.

## Blocking and parent relationships

Use native GitHub issue dependencies. With the CLI, add a blocker using `gh api --method POST repos/lpyaia/llm-clinic-scheduler/issues/<blocked-number>/dependencies/blocked_by -F issue_id=<blocker-database-id>`. The database ID is different from the issue number and node ID.

Create blockers first and verify every relationship. If native dependencies are unavailable, put links to all blocking issues in a `Blocked by` section of the issue body.

When the source is an existing issue, attach tickets as its sub-issues using the supported native operation. When the source is a document, no parent issue is assumed.

The work frontier consists of open, unclaimed issues whose blocking issues are all closed. Claim an issue before starting work and verify the existing resolution convention before closing it.

## Pull requests as a triage surface

**PRs as a request surface: no.** A bare number must still be resolved as an issue or pull request before acting on it.

## Wayfinding operations

A wayfinding map is one issue labelled `wayfinder:map`; each decision is a child issue with its appropriate `wayfinder:<type>` label. Use native sub-issues and dependencies where supported. Claim by assigning the issue, record the answer before resolving it, and append a decision reference to the map. Wayfinding issues do not receive triage labels.
