# Superpowers Vendoring Design

**Date:** 2026-05-16
**Status:** Approved
**Approach:** Separate embedded tier (Approach 1)

## Overview

Vendor the [Superpowers](https://github.com/obra/superpowers) agentic skills framework into the Crush binary. Skills are embedded at compile time via `//go:embed`, discovered as a distinct tier between Crush-native builtins and user-provided skills, and the `using-superpowers` bootstrap skill is auto-injected into every session's system prompt.

## Goals

- Superpowers skills ship with every Crush build — zero runtime network dependency
- Three-tier priority: user skills > superpowers > Crush-native builtins
- `using-superpowers` bootstrap is auto-loaded into every session (no tool call needed)
- Simple manual update workflow via Taskfile script

## Directory Structure

```
internal/skills/
  builtin/                        # Crush-native skills (unchanged)
    crush-config/SKILL.md
    crush-hooks/SKILL.md
    jq/SKILL.md
  superpowers/                    # Vendored from github.com/obra/superpowers
    brainstorming/SKILL.md
    brainstorming/visual-companion.md
    writing-plans/SKILL.md
    writing-plans/plan-document-reviewer-prompt.md
    test-driven-development/SKILL.md
    systematic-debugging/SKILL.md
    using-superpowers/SKILL.md   # Crush-specific version (managed by us)
    ... (all other superpowers skills)
  embed.go                        # Existing: //go:embed builtin/*
  superpowers_embed.go            # New: //go:embed superpowers/*
  skills.go                       # Existing: discovery logic
```

Only the `skills/` directory from the superpowers repo is vendored. No `package.json`, `node_modules/`, `docs/`, or other non-skill files.

## Embedding and Discovery

### New file: `internal/skills/superpowers_embed.go`

Mirrors the existing `embed.go` pattern for builtins:

```go
const SuperpowersPrefix = "crush://superpowers/"

//go:embed superpowers/*
var superpowersFS embed.FS

func SuperpowersFS() embed.FS
func DiscoverSuperpowers() []*Skill
func DiscoverSuperpowersWithStates() ([]*Skill, []*SkillState)
```

Discovery functions walk `superpowersFS` like `DiscoverBuiltinWithStates()` walks `builtinFS`:
- Paths use `SuperpowersPrefix` (e.g., `crush://superpowers/brainstorming/SKILL.md`)
- `skill.Builtin = false`, `skill.Vendored = true`

### Skill struct change (`internal/skills/skills.go`)

Add a `Vendored` field:

```go
type Skill struct {
    // ... existing fields ...
    Builtin   bool  `yaml:"-" json:"builtin"`    // Crush-native embedded skill
    Vendored  bool  `yaml:"-" json:"vendored"`   // Vendored third-party skill
}
```

### ToPromptXML change

Vendored skills emit `<type>vendored</type>` in the prompt XML (parallel to `<type>builtin</type>`).

## Discovery Pipeline

The pipeline in `prompt.go` `promptData()` and `coordinator.go` `discoverSkills()` changes from two-tier to three-tier:

```
1. Discover builtin skills           -> allSkills
2. Discover superpowers skills       -> allSkills (append)
3. Discover user skills from paths   -> allSkills (append)
4. Deduplicate (last wins)           -> user > superpowers > builtin
5. Filter disabled skills            -> remove any in disabled list
6. ToPromptXML                       -> inject into system prompt
```

The existing `Deduplicate()` function already does "last occurrence wins," so appending in this order gives correct priority.

### Concrete code in `promptData()`

```go
// Start with builtin skills.
allSkills := skills.DiscoverBuiltin()

// Add vendored superpowers skills.
allSkills = append(allSkills, skills.DiscoverSuperpowers()...)

// Discover user skills from configured paths. (existing code unchanged)
// ...

// Deduplicate: user > superpowers > builtin.
allSkills = skills.Deduplicate(allSkills)
```

### `coordinator.go` `discoverSkills()`

Same change: insert `DiscoverSuperpowersWithStates()` between builtin and user discovery.

### `crush_info` tool

Report three categories: `builtin`, `vendored`, `user`.

## View Tool Changes

`internal/agent/tools/view.go` supports the new `crush://superpowers/` prefix:

- `readEmbeddedFile()` (refactored from `readBuiltinFile()`) is a new unexported function that handles both embedded prefixes. The existing `readBuiltinFile()` callers are updated to call `readEmbeddedFile()` instead. It checks the path prefix to determine which embedded FS to read from:
  - `crush://skills/` -> read from `skills.BuiltinFS()`
  - `crush://superpowers/` -> read from `skills.SuperpowersFS()`
- Both get the same special treatment: no permission prompts, no size limits, skill metadata in response
- `skillTracker.MarkLoaded()` called for both
- `isInSkillsPath()` extended to recognize `crush://superpowers/` prefix

## Auto-load `using-superpowers` Bootstrap

The `using-superpowers` skill is the bootstrap that teaches the LLM how to use skills. It must be in the system prompt from session start, not loaded on-demand.

### PromptDat change

```go
type PromptDat struct {
    // ... existing fields ...
    SuperpowersBootstrap string  // Full content of using-superpowers skill
}
```

### promptData() logic

After dedup and filter, find `using-superpowers` by name and extract its content:

```go
for _, s := range allSkills {
    if s.Name == "using-superpowers" {
        data.SuperpowersBootstrap = s.Instructions
        break
    }
}
```

Exclude `using-superpowers` from the `<available_skills>` XML to avoid token waste (it's already fully present in the system prompt). This exclusion happens in `promptData()` before calling `ToPromptXML()` — filter it from the list passed to that function.

### System prompt template

`internal/agent/templates/coder.md.tpl` injects the bootstrap at the top of the skills section:

```
{{ if .SuperpowersBootstrap }}
<EXTREMELY_IMPORTANT>
{{ .SuperpowersBootstrap }}
</EXTREMELY_IMPORTANT>
{{ end }}
```

## Crush-Specific `using-superpowers`

We maintain our own `internal/skills/superpowers/using-superpowers/SKILL.md` with Crush-specific tool mappings. The update script excludes it from being overwritten.

The Crush version includes a tool mapping section:

```markdown
**Tool Mapping for Crush:**
- `Skill` tool -> Use the `view` tool to read skill files at their `<location>` path
- `TodoWrite` -> `todowrite`
- `Task` tool (subagents) -> Use the `task` tool with appropriate `subagent_type`
- `Read`, `Write`, `Edit`, `Bash` -> Native tools with same names
```

When upstream `using-superpowers` changes significantly, we manually update our copy to incorporate changes while preserving the Crush section.

## Taskfile Update Script

New task in `Taskfile.yaml`:

```yaml
update-skills:
  desc: Update vendored superpowers skills from GitHub
  cmds:
    - git clone --depth 1 https://github.com/obra/superpowers.git /tmp/superpowers-update
    - rsync -r --exclude='using-superpowers' /tmp/superpowers-update/skills/ internal/skills/superpowers/
    - rm -rf /tmp/superpowers-update
    - echo "Superpowers skills updated. Review and commit the changes."
```

Clones latest, copies all skills except `using-superpowers`, cleans up. Developer reviews diff and commits.

## Files Changed

### New files

| File | Purpose |
|------|---------|
| `internal/skills/superpowers_embed.go` | Embed directive, prefix constant, discovery functions |
| `internal/skills/superpowers/` | Vendored superpowers skills (~13 skill dirs) |
| `internal/skills/superpowers/using-superpowers/SKILL.md` | Crush-specific version with Crush tool mappings |

### Modified files

| File | Change |
|------|--------|
| `internal/skills/skills.go` | Add `Vendored bool` field; update `ToPromptXML()` for `<type>vendored</type>` |
| `internal/agent/prompt/prompt.go` | Add `SuperpowersBootstrap` to `PromptDat`; insert superpowers discovery; extract bootstrap content |
| `internal/agent/templates/coder.md.tpl` | Inject `SuperpowersBootstrap` into system prompt |
| `internal/agent/coordinator.go` | Add superpowers discovery in `discoverSkills()` |
| `internal/agent/tools/view.go` | Support `crush://superpowers/` prefix |
| `internal/agent/tools/crush_info.go` | Report `vendored` skill source type |
| `Taskfile.yaml` | Add `update-skills` task |

### Test files

| File | Change |
|------|--------|
| `internal/skills/skills_test.go` | Add `TestDeduplicateThreeTier`, `TestToPromptXMLVendored` |
| `internal/skills/embed_test.go` | Add `TestDiscoverSuperpowers` |
