## 1. English language use in docs

Use US English with plain tone for docs.
Apply rules to `docs/`, `README.md`, comments, and messages.

### Style rules

Prefer active voice with `[Subject] + [Verb] + [Object]`.
Keep each sentence under 20 words.
Convey single idea in each sentence.
Define acronyms at first use.
Prohibit emojis in docs, comments, commits, and logs.
Prohibit vague modifiers, example `very`, `simply`, `just`.
Use sentence case for titles.
Use lowercase for commit subjects, not sentence case.
Use US spellings, example `color`, not `colour`.
Spell out numbers under ten in prose, use digits for ten and above.
Apply length and number rules to prose only, not to code or versions.

### Examples

Good:

```text
The API (application programming interface) returns user data.
The CLI (command-line interface) validates config files.
```

Avoid:

```text
Very simply, the data gets returned!!! :)
Just fix stuff properly.
```

### Agent rules

Review docs for tone before commit.
Correct spelling errors before merge.
Flag unclear sentences in review comments.

## 2. Workspace layout

Use `.ai/` for agent state only.
Keep `.ai/` narrow in new projects.
Large folders confuse agents.
Large folders slow reviews.
This section does not define product code layout.
Use default layout below.

```text
.ai/
├── handoff.md
├── plans/
│   ├── active.md
│   └── history/
├── artifacts/
└── tmp/
```

Store current goals in `plans/active.md`.
Archive done plans in `plans/history/`.
Store final outputs in `artifacts/`.
Store volatile logs in `tmp/`.
Exclude logs, caches, and secrets from `.ai/` outside `tmp/`.
Add other folders only on demand.
Consider `context/` for durable project facts.
Consider `decisions/` for architecture history.
Skip memory folders without proven demand.
Adjust paths in project `AGENTS.md` as needed.

## 3. Commit message conventions

Use Conventional Commits 1.0.0 for messages.

### Format

Use format `type(scope): subject` for titles.
Keep scope optional and lowercase.
Keep subject under 72 characters.
Start subject with imperative verb in lowercase.
Write `add feature`, not `adds feature` or `added feature`.
Omit final period in subject line.

### Allowed types

| Type | Use |
| ---- | --- |
| `feat` | Add user feature |
| `fix` | Repair defect |
| `docs` | Update documentation only |
| `style` | Adjust format without logic change |
| `refactor` | Restructure code without behavior change |
| `perf` | Improve speed or memory use |
| `test` | Add test coverage |
| `build` | Update build scripts or dependencies |
| `ci` | Update pipeline configuration |
| `chore` | Update tooling without product change |
| `revert` | Revert prior commit |

### Body

Add body only for complex changes.
Explain reason for change in body.
Wrap body lines at 72 characters.

### Footer

Add footer for API splits and issue links.
Mark API splits with `!` after type or scope.
Include `BREAKING CHANGE:` token in footer for splits.
Link issues with `Refs: #123` or `Fixes: #123`.
Add `Assisted-by` trailer for AI-assisted kernel work, see section 6.

### Examples

```text
feat(auth): add OAuth login
fix(api): handle null response
docs(readme): update install steps
feat(api)!: drop v1 endpoint

feat(search): add filters

Explain user need for filters here.

Refs: #42
```

```text
BREAKING CHANGE: drop v1 endpoint, use v2 endpoint
```

### Agent rules

Create atomic commits with single logical change.
Do not commit or push unless user requests it.
Never commit secrets, tokens, or env files.
Verify commits with `git log --oneline -5` before push.
Reject vague subjects, example `fix stuff` or `update code`.

## 4. Changelog conventions

Use Keep a Changelog 1.1.0 for releases.
Maintain changelog in `CHANGELOG.md` at root.

### Format rules

Place `Unreleased` section at top for new changes.
Order versions newest first with SemVer (semantic versioning) numbers.
Write dates in ISO (International Organization for Standardization) 8601 format `YYYY-MM-DD`.
Group changes under categories below.
Add entries in past tense with plain verbs.
Omit internal refactors without user impact.
Never copy raw git logs into changelog.

### Categories

* `Added`
* `Changed`
* `Deprecated`
* `Removed`
* `Fixed`
* `Security`

### Example

```markdown
# Changelog

## [Unreleased]

### Added
- Add OAuth login for web app.

## [1.2.0] - 2026-10-21

### Fixed
- Handle null response in user API.

### Security
- Patch token leak in logs.

[Unreleased]: <repo-url>/compare/v1.2.0...HEAD
[1.2.0]: <repo-url>/releases/tag/v1.2.0
```

Replace `<repo-url>` with repository URL.

### Agent rules

Update changelog for user-facing changes only.
Check version link format before merge.
Generate release notes from the release branch history on GitHub.

## 5. Session handoff

Update `handoff.md` at session end.
Update `handoff.md` before context handoff.
Keep three fields: current status, blocking issues, next actions.
Limit current status to one sentence.
List zero or more open problems under blocking issues.
List one or two concrete tasks under next actions.
Use `flock` only for shared runs with concurrent agents.
Archive handoff text into `plans/history/` for milestones.

```markdown
# Handoff: [Task name]
* **Current status:** [one sentence]
* **Blocking issues:** [None] or [list]
* **Next actions:** [one or two tasks]
```

## 6. Attribution

Use `Assisted-by: <model-id>` trailer for AI-assisted kernel work.
Replace `<model-id>` with exact provider and model ID.
Copy session model ID verbatim without abbreviation.
Omit generic `LLM` label.
List specialized analysis tools after model ID.
Omit basic tools such as `git`, `gcc`, `make`, and editors.
Do not invent alternative attribution formats.

```text
Assisted-by: opencode/muse-spark-1.3-contributor coccinelle sparse
```

## 7. Issue tracking with Beads

Use `bd` for task tracking in repos with `.beads/`.
Run `bd prime` for full workflow and commands.
Claim work before you start it.
Refresh leases with `heartbeat` for long tasks.
Close issues when work lands.
Sync issue data with `bd dolt pull` and `bd dolt push`.
Use one worktree per parallel task.
Create worktrees with `bd worktree create <name> --branch <branch>`.
Worktrees share the same issue database automatically.
Check state with `bd worktree list` and `bd worktree info`.
Remove worktrees with `bd worktree remove <name>`.
Do not hand-edit inside Beads managed comment blocks.

### Release branches

Name release branches with bare versions like `0.2.1`.
Do not use `-dev` suffixes on branch names.
Review the backlog and file epics and issues first.
Create one work branch per task off the release branch.
Use `bd worktree create <work-branch> --branch <work-branch>` for isolation.
Make as many commits as needed on work branches.
Include gate commits on work branches.
Squash-merge each work branch into the release branch.
Keep one squash commit per task on the release branch.
Merge the release branch into `main` when it ships.
Generate the GitHub changelog from that clean history.
Tag `<version>-dev` for pre-release builds; re-tag for rebuilds.
Tag `<version>` once for the final release build.

```text
git branch <version> main
bd worktree create <work-branch> --branch <work-branch>  # from <version>
# many commits on <work-branch>, gates included
git merge --squash <work-branch>  # into <version>, one commit
git merge <version>               # into main, no squash
```

### Agent rules

Do not use markdown TODO lists for tracked work.
Do not work the same issue in two worktrees.
Explicit user instructions override Beads profiles.
Section 3 commit rules still apply unless user opts in.
Never branch work directly off `main` during a release.
Never merge work branches into `main`; target the release branch.
Never squash-merge the release branch into `main`.

## 8. Token-efficient commands with RTK

Prefix shell commands with `rtk` when available.
Run `rtk --help` for full command reference.
Run `rtk gain` to inspect savings.
Do not paste full RTK tables into this file.

### Agent rules

Keep `rtk` prefix even in `&&` chains.
Fall back to raw command if `rtk` lacks a filter.

## 9. Persistent memory with ICM

Skip this section when `NO_ICM=1` exists in the environment.
Recall context before you start work.
Store decisions and fixes after work lands.
Use `icm recall "query"` for lookup.
Use `icm store` for durable facts.
Do not create ad hoc memory files.

### Agent rules

Check `NO_ICM` before any ICM command.
Skip `icm recall` and `icm store` when `NO_ICM=1`.
Do not read `.icm/` files when `NO_ICM=1`.
Keep section 5 `handoff.md` for session status.
Keep ICM for durable cross-session memory.
