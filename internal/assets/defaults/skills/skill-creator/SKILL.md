---
name: skill-creator
description: >-
  Create or update a reusable Agent Skill when asked to build, refine, review,
  or repair a SKILL.md workflow, improve automatic triggering, or add supporting
  scripts, references, assets, metadata, and validation. Use for new and existing
  skills. Do not use for ordinary project instructions that belong in AGENTS.md
  or for executing a one-off task that does not need a reusable workflow.
license: Apache-2.0
metadata:
  display-name: Skill Creator
  short-description: Build and improve portable agent skills
  brand-color: "#6D7CFF"
---

# Skill Creator

Create skills that give an agent useful, non-obvious guidance without constraining
unrelated work. Treat the frontmatter description as the discovery rule and the body
as instructions loaded only after activation.

## Core principles

**Assume the agent is capable.** Include only guidance that changes decisions or makes
fragile work more reliable. Remove generic advice, repeated rules, speculative edge
cases, and examples that do not clarify behavior.

**Preserve intent and scope.** A skill supports the user's task; it does not replace
their chosen product, expand permissions, authorize external mutations, or silently
change invocation policy. Do not turn one observed failure into a universal rule.

**Match specificity to risk.** Allow judgment when several approaches are valid. Use
exact sequences and deterministic scripts only where deviation creates a concrete
correctness, safety, or reliability problem.

**Keep discovery cheap and precise.** Describe the actual capability and when it
applies. Add a boundary only when it prevents likely misrouting. Avoid keyword dumps,
generic catchalls, and trigger terms broad enough to attract unrelated requests.

**Disclose detail progressively.** Keep shared purpose, routing, and essential
constraints in `SKILL.md`. Put substantial schemas, modes, examples, and procedures in
focused references that are loaded only when relevant.

## Choose the location

- Project skill: `<project>/.agents/skills/<skill-name>/`
- Shared skill: `~/.agents/skills/<skill-name>/`
- Koder-managed built-in: `~/.koder/skills/<skill-name>/`

Prefer `~/.agents/skills` when the user wants one library shared by compatible hosts.
Respect a user-specified location. Do not move or rename an existing skill unless asked.

## Use the smallest useful structure

```text
skill-name/
|-- SKILL.md                 required
|-- scripts/                 optional deterministic helpers
|-- references/              optional instructions loaded on demand
`-- assets/                  optional files used in generated output
```

Create optional directories only when they provide a concrete benefit. Do not add a
README, changelog, installation guide, duplicated quick reference, or placeholders
unless the workflow genuinely needs them.

Use scripts for repeated transformations, reliable API operations, and other work that
would otherwise be reimplemented. Test every bundled script. Use references for large
schemas, policies, provider-specific procedures, and detailed examples. State exactly
when each reference should be read. Use assets for templates and output resources, not
for hidden instructions.

Resolve every relative resource path from the skill directory. Avoid absolute
machine-specific paths and undocumented environment dependencies. Write generated
output to the project or a temporary directory, never back into an installed skill.

## Write portable frontmatter

For a skill intended to work in both Koder and Codex, default to the common fields:

```yaml
---
name: my-skill
description: Do a specific job when the user asks for these concrete tasks or supplies these artifacts.
license: Apache-2.0
metadata:
  display-name: My Skill
  short-description: Optional compact catalog label
  logo: assets/logo.png
  brand-color: "#3366FF"
---
```

Only `name` and `description` are required. The directory and `name` must match. Use
lowercase letters, digits, and single hyphens, with at most 64 characters.

`license` and string-valued `metadata` are optional and accepted by Koder and Codex.
Koder uses `display-name`, `short-description`, `logo`, and `brand-color` only for UI
presentation; they do not affect model routing. Omit unused metadata. Do not add
`compatibility`: Codex rejects that top-level field.

Add host-specific fields or files only when the user targets that host and the feature
is needed. Do not claim portability until every requested target host validates the
skill. Preserve existing host-specific invocation policy unless the user asks to change
it.

For Koder logos, use a compact square PNG, JPEG, WebP, or GIF inside the skill
directory; SVG is not supported. If `metadata.logo` is absent, Koder looks for
`assets/logo.*` and `assets/icon.*`.

## Design discovery before instructions

Before writing a new or substantially revised skill, identify:

- two or three realistic requests that should activate it;
- one explicit `$skill-name` request;
- two near-miss requests that should not activate it;
- the stable outcome, fragile steps, required inputs, and meaningful boundaries.

Write a concise, discriminating description from those cases. Put all activation cues
there because the body is unavailable until selection. Then write the shortest body
that reliably produces the intended outcome. A large line count is not a target.

For multiple substantial modes, put only their selection criteria in `SKILL.md` and
route to separate references. Do not build a router for a simple, self-contained skill.

## Create or update

For a new skill:

1. Establish its intended requests, exclusions, target hosts, and location.
2. Create only the resources justified by the workflow.
3. Write discovery metadata first, then the shared instructions.
4. Test scripts and validate resources before behavioral testing.

For an existing skill:

1. Read its complete `SKILL.md` and only the resources it routes to.
2. Reproduce the reported ambiguity or failure with a concrete request when practical.
3. Preserve working behavior, user choices, unrelated metadata, and invocation policy.
4. Fix the smallest underlying instruction, script, reference, or metadata issue.
5. Remove stale duplication and machine-specific assumptions exposed by the change.

Ask a clarifying question only when a missing choice materially changes the result and
cannot be discovered safely. Otherwise make a bounded, explicit assumption and proceed.

## Validate and forward-test

For Koder, run:

```bash
koder skill validate /path/to/skill-name
koder skill verify skill-name --workdir /path/to/project
koder skill list --workdir /path/to/project
```

`validate` checks Koder's schema and resource metadata. `verify` checks discovery and
then performs the same schema validation; it is not a behavioral test. `list` exposes
invalid, disabled, and shadowed skills and every searched root.

Also run each requested target host's validator when available. Validation does not
prove that instructions make good decisions, so inspect the description for false
positives and confirm that every routed resource exists. Avoid tests that merely match
chosen wording rather than observable behavior.

Forward-test a meaningful change in a fresh conversation when practical:

- test implicit activation with a realistic positive request;
- test explicit activation with `$skill-name`;
- test at least one near miss that should not activate;
- provide raw inputs or artifacts, not the intended conclusion;
- verify the resulting decisions and artifacts, then make only evidence-backed fixes.

Do not claim cross-host compatibility, successful activation, or behavioral correctness
from schema validation alone.
