---
name: skill-creator
description: Create or improve portable Agent Skills with focused instructions, reusable scripts, references, assets, metadata, validation, and forward tests. Use when asked to create, build, update, review, or repair a skill.
license: Apache-2.0
compatibility: Portable SKILL.md format; works with Koder and other Agent Skills-compatible hosts.
metadata:
  display-name: Skill Creator
  short-description: Build and improve portable agent skills
  brand-color: "#6D7CFF"
---

# Skill Creator

Create a small, portable folder that teaches an agent a repeatable capability. Treat the
description as the routing rule and the Markdown body as instructions loaded only after
activation.

## Choose the location

- Put a project skill in `<project>/.agents/skills/<skill-name>/`.
- Put a portable shared skill in `~/.agents/skills/<skill-name>/`.
- Use `~/.koder/skills/` only for Koder-managed built-ins.
- A symlinked skill directory is acceptable. Keep the target available to every host that
  should use it.

Prefer `~/.agents/skills` when a user wants to share one library between compatible agents.

## Use the portable structure

```text
skill-name/
├── SKILL.md                 # required
├── scripts/                 # optional executable helpers
├── references/              # optional material loaded on demand
└── assets/                  # optional templates and UI artwork
    └── logo.png             # optional PNG/JPEG/WebP/GIF logo
```

Do not add README, changelog, installation guide, or quick-reference files unless the
workflow truly needs them. Put agent-facing material in `SKILL.md` or `references/`.

## Write portable frontmatter

Use real YAML. The directory and `name` must match exactly. Keep the name to lowercase
letters, digits, and single hyphens, with at most 64 characters.

```yaml
---
name: my-skill
description: Do a specific job. Use when the user asks for these concrete tasks or supplies these artifacts.
license: Apache-2.0
compatibility: Optional runtime or tool requirements in at most 500 characters.
metadata:
  display-name: My Skill
  short-description: Optional compact catalog label
  logo: assets/logo.png
  brand-color: "#3366FF"
---
```

`name` and `description` are required portable fields. `license`, `compatibility`, and the
string-valued `metadata` map are optional. Koder's presentation keys are deliberately
host-neutral hints; hosts that do not recognize them ignore them.

For a logo:

- Prefer a compact square PNG or WebP with a transparent background.
- Keep it inside the skill directory and reference it with a relative path.
- Use PNG, JPEG, WebP, or GIF. Do not use SVG because hosts may render untrusted active
  content differently.
- If metadata omits `logo`, Koder automatically looks for `assets/logo.*` and
  `assets/icon.*` in the supported formats.

## Design the skill

1. Gather two or three concrete user requests that should activate it.
2. Identify stable steps, fragile steps, reusable code, required references, and outputs.
3. Put deterministic or error-prone work in tested scripts.
4. Put large schemas, policies, and variants in `references/` and say exactly when to read
   each one.
5. Put templates and output resources in `assets/`.
6. Write the shortest instructions that reliably cover the examples.

Match freedom to risk:

- Use plain instructions when several approaches are valid.
- Use pseudocode or parameterized helpers when one pattern is preferred.
- Use exact, tested scripts for fragile or repetitive transformations.

Keep `SKILL.md` under roughly 500 lines. Use imperative language. Do not repeat generic
knowledge the model already has. Put all activation cues in `description`; the body is not
visible until the skill has been selected.

## Make resource use explicit

Tell the agent which relative files to read or run and under what condition. Resolve paths
relative to the skill directory. Avoid absolute machine-specific paths and undocumented
environment dependencies.

Koder grants a loaded skill's directory read-only access to the chat sandbox. If a helper
must write output, direct it to the project or a temporary working directory, never back
into the installed skill.

## Validate and verify

Run both commands from the intended environment:

```bash
koder skill validate /path/to/skill-name
koder skill verify skill-name --workdir /path/to/project
```

Validation checks YAML, portable field limits, directory/name agreement, presentation
metadata, and logo safety. `koder skill list --workdir /path/to/project` also shows invalid,
disabled, and shadowed entries plus every searched root.

Test every bundled script directly. Then forward-test the skill in a fresh conversation
using a realistic request. Test implicit activation from the description and explicit
activation with `$skill-name`. Pass raw artifacts to the test, not conclusions or hints.

## Improve an existing skill

1. Read the complete `SKILL.md` and only the resources it routes to.
2. Reproduce the failure or ambiguity with a concrete request.
3. Fix the smallest underlying instruction, script, reference, or metadata issue.
4. Remove stale duplication and machine-specific assumptions.
5. Re-run validation, helper tests, discovery verification, and a fresh forward test.

Preserve portable fields and behavior unless the user explicitly wants a host-specific
extension. Never silently move, rename, enable, or disable other skills.
