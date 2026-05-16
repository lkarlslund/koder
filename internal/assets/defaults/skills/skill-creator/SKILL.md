---
name: skill-creator
description: Guide for creating effective koder skills. Use when the user wants to create a new skill (or update an existing skill) that extends koder's capabilities with specialized knowledge, workflows, or tool integrations. Triggers on requests like "create a skill", "make a skill", "build a skill", "new skill for", "skill to".
---

# Skill Creator

This skill provides guidance for creating effective koder skills.

## About Skills

Skills are modular, self-contained folders that extend koder's capabilities by providing
specialized knowledge, workflows, and tools. Think of them as "onboarding guides" for specific
domains or tasks.

### What Skills Provide

1. Specialized workflows - Multi-step procedures for specific domains
2. Tool integrations - Instructions for working with specific file formats or APIs
3. Domain expertise - Company-specific knowledge, schemas, business logic
4. Bundled resources - Scripts, references, and assets for complex and repetitive tasks

## Skill File System

Koder discovers skills from `.agents/skills` directories:

- **Project skills**: `<project-root>/.agents/skills/` (and parent directories up to project root)
- **User skills**: `~/.agents/skills/`

Each skill is a directory containing a `SKILL.md` file with YAML frontmatter.

### SKILL.md Format

```yaml
---
name: my-skill
description: What the skill does and when to use it. Include specific triggers and scenarios.
---

# My Skill

## Instructions
...
```

Only `name` and `description` are read from frontmatter.

- **name**: lowercase, hyphen-separated, alphanumeric (e.g. `pdf-editor`, `brand-guidelines`)
- **description**: primary triggering mechanism - describe what the skill does AND when to use it

## Core Principles

### Concise Is Key

Only add context the model doesn't already have. Challenge each piece: "Does the model really need this?"
Prefer concise examples over verbose explanations.

### Set Appropriate Degrees of Freedom

Match specificity to the task's fragility:
- **High freedom** (text instructions): multiple approaches valid, context-dependent
- **Medium freedom** (pseudocode/parameterized scripts): preferred pattern exists, some variation OK
- **Low freedom** (specific scripts): fragile operations, consistency critical

### Anatomy of a Skill

```
skill-name/
├── SKILL.md (required)
│   ├── YAML frontmatter: name, description
│   └── Markdown instructions (body)
├── scripts/ (optional) - Executable code (Python/Bash/etc.)
├── references/ (optional) - Docs loaded into context as needed
└── assets/ (optional) - Files used in output (templates, icons, etc.)
```

### Progressive Disclosure

Keep SKILL.md under 500 lines. Split content when approaching this limit:
- Keep core workflow in SKILL.md
- Move variant-specific details to `references/` files
- Reference them from SKILL.md with clear guidance on when to read

### What Not to Include

Do NOT create: README.md, INSTALLATION_GUIDE.md, QUICK_REFERENCE.md, CHANGELOG.md, or similar auxiliary files. The skill should only contain what an AI agent needs to do the job.

## Skill Creation Process

Follow these steps in order:

### Step 1: Understand the Skill with Concrete Examples

Skip only when the skill's usage is already clear.

Ask focused questions (don't overwhelm):
- What functionality should this skill support?
- Can you give examples of how it would be used?
- What would a user say that should trigger this skill?

### Step 2: Plan Reusable Skill Contents

For each concrete example, identify:
- **Scripts**: code that would be rewritten repeatedly → `scripts/`
- **References**: schemas, docs, specs the model needs → `references/`
- **Assets**: templates, boilerplate, icons for output → `assets/`

### Step 3: Create the Skill Directory

Ask where to create the skill. Default to `~/.agents/skills/`.

Create the directory and SKILL.md:

```bash
mkdir -p ~/.agents/skills/my-skill
```

Write a SKILL.md with frontmatter:

```yaml
---
name: my-skill
description: Clear description of what this skill does and when to use it.
---

# My Skill

## Instructions
...
```

### Step 4: Edit the Skill

Remember: the skill is being created for another koder instance to use.

#### Start with Reusable Skill Contents

Implement `scripts/`, `references/`, and `assets/` first. Test scripts by actually running them.

#### Write SKILL.md

**Writing Guidelines:** Always use imperative/infinitive form.

**Frontmatter:**
- `name`: hyphen-case, short, describes the action
- `description`: include what the skill does AND specific triggers/scenarios
  - All "when to use" info goes here - not in the body

**Body:**
- Write instructions for using the skill and its bundled resources
- Structure based on skill type (workflow, task-based, reference, or capabilities)

### Step 5: Validate the Skill

```bash
koder skill validate <path/to/skill-folder>
```

Fix any reported issues and re-run.

### Step 6: Verify the Skill is Discoverable

```bash
koder skill verify my-skill
```

This confirms koder can discover the skill by name and that its SKILL.md is valid.

### Step 7: Iterate and Forward-Test

1. Use the skill on real tasks
2. Notice struggles or inefficiencies
3. Identify how SKILL.md or bundled resources should be updated
4. Implement changes and test again

#### Forward-Testing

To forward-test, launch subagents with minimal context:
- Prompt should look like: `Use $skill-name at /path/to/skill to solve problem`
- NOT: `Review the skill at /path; pretend a user asks you to...`
- Use fresh threads for independent passes
- Pass raw artifacts, not conclusions
- Clean up subagent artifacts between iterations
