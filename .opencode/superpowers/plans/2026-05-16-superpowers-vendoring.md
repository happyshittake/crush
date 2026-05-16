# Superpowers Vendoring Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Vendor the Superpowers agentic skills framework into the Crush binary as a distinct embedded tier, with three-tier skill priority and auto-loaded bootstrap.

**Architecture:** Superpowers skills are vendored into `internal/skills/superpowers/`, embedded via `//go:embed`, discovered as a separate tier between builtins and user skills, and the `using-superpowers` bootstrap is auto-injected into every session's system prompt.

**Tech Stack:** Go, `//go:embed`, `io/fs`, existing skills discovery infrastructure

---

## File Structure

### New files

| File | Responsibility |
|------|----------------|
| `internal/skills/superpowers_embed.go` | Embed directive, `SuperpowersPrefix`, `SuperpowersFS()`, `DiscoverSuperpowers()`, `DiscoverSuperpowersWithStates()` |
| `internal/skills/superpowers/` | Vendored superpowers skills directory (~13 skill dirs with SKILL.md files) |
| `internal/skills/superpowers/using-superpowers/SKILL.md` | Crush-specific version with Crush tool mappings |

### Modified files

| File | Change |
|------|--------|
| `internal/skills/skills.go` | Add `Vendored bool` field to `Skill` struct; update `ToPromptXML()` for `<type>vendored</type>` |
| `internal/agent/prompt/prompt.go` | Add `SuperpowersBootstrap string` to `PromptDat`; insert superpowers discovery; extract bootstrap content |
| `internal/agent/templates/coder.md.tpl` | Inject `SuperpowersBootstrap` into system prompt |
| `internal/agent/coordinator.go` | Add `DiscoverSuperpowersWithStates()` in `discoverSkills()` |
| `internal/agent/tools/view.go` | Support `crush://superpowers/` prefix via `readEmbeddedFile()` |
| `internal/agent/tools/crush_info.go` | Report `vendored` skill source type |
| `Taskfile.yaml` | Add `update-skills` task |

### Test files

| File | Change |
|------|--------|
| `internal/skills/skills_test.go` | Add `TestDiscoverSuperpowers`, `TestDeduplicateThreeTier`, `TestToPromptXMLVendored` |

---

### Task 1: Add `Vendored` field to `Skill` struct and update `ToPromptXML`

**Files:**
- Modify: `internal/skills/skills.go:38-48` (Skill struct)
- Modify: `internal/skills/skills.go:296-315` (ToPromptXML)
- Test: `internal/skills/skills_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/skills/skills_test.go` after `TestToPromptXMLBuiltinType`:

```go
func TestToPromptXMLVendored(t *testing.T) {
	t.Parallel()

	input := []*Skill{
		{Name: "builtin-skill", Description: "A builtin.", SkillFilePath: "crush://skills/builtin-skill/SKILL.md", Builtin: true},
		{Name: "vendored-skill", Description: "A vendored skill.", SkillFilePath: "crush://superpowers/vendored-skill/SKILL.md", Vendored: true},
		{Name: "user-skill", Description: "A user skill.", SkillFilePath: "/home/user/.config/crush/skills/user-skill/SKILL.md"},
	}
	xml := ToPromptXML(input)
	require.Contains(t, xml, "<type>builtin</type>")
	require.Contains(t, xml, "<type>vendored</type>")
	require.Equal(t, 1, strings.Count(xml, "<type>builtin</type>"))
	require.Equal(t, 1, strings.Count(xml, "<type>vendored</type>"))
	// User skills should have no <type> element.
	require.NotContains(t, xml, "/home/user/.config/crush/skills/user-skill/SKILL.md</location>\n    <type>")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/skills/ -run TestToPromptXMLVendored -v`
Expected: FAIL — `Vendored` field doesn't exist yet.

- [ ] **Step 3: Add `Vendored` field to `Skill` struct**

In `internal/skills/skills.go`, add `Vendored` field to the `Skill` struct (after `Builtin` on line 47):

```go
type Skill struct {
	Name          string            `yaml:"name" json:"name"`
	Description   string            `yaml:"description" json:"description"`
	License       string            `yaml:"license,omitempty" json:"license,omitempty"`
	Compatibility string            `yaml:"compatibility,omitempty" json:"compatibility,omitempty"`
	Metadata      map[string]string `yaml:"metadata,omitempty" json:"metadata,omitempty"`
	Instructions  string            `yaml:"-" json:"instructions"`
	Path          string            `yaml:"-" json:"path"`
	SkillFilePath string            `yaml:"-" json:"skill_file_path"`
	Builtin       bool              `yaml:"-" json:"builtin"`
	Vendored      bool              `yaml:"-" json:"vendored"`
}
```

- [ ] **Step 4: Update `ToPromptXML` to emit `<type>vendored</type>`**

In `internal/skills/skills.go`, update `ToPromptXML` (lines 307-309). Replace:

```go
		if s.Builtin {
			sb.WriteString("    <type>builtin</type>\n")
		}
```

With:

```go
		if s.Builtin {
			sb.WriteString("    <type>builtin</type>\n")
		} else if s.Vendored {
			sb.WriteString("    <type>vendored</type>\n")
		}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/skills/ -v`
Expected: ALL PASS, including `TestToPromptXMLVendored`.

- [ ] **Step 6: Commit**

```bash
git add internal/skills/skills.go internal/skills/skills_test.go
git commit -m "feat: add Vendored field to Skill struct and ToPromptXML support"
```

---

### Task 2: Create `superpowers_embed.go` with discovery functions

**Files:**
- Create: `internal/skills/superpowers_embed.go`
- Test: `internal/skills/skills_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/skills/skills_test.go` after `TestDiscoverBuiltin`:

```go
func TestDiscoverSuperpowers(t *testing.T) {
	t.Parallel()

	discovered := DiscoverSuperpowers()
	require.NotEmpty(t, discovered)

	// Check that all discovered skills have correct properties.
	for _, s := range discovered {
		require.True(t, strings.HasPrefix(s.SkillFilePath, SuperpowersPrefix),
			"skill %q: expected SkillFilePath to start with %q, got %q", s.Name, SuperpowersPrefix, s.SkillFilePath)
		require.True(t, strings.HasPrefix(s.Path, SuperpowersPrefix),
			"skill %q: expected Path to start with %q, got %q", s.Name, SuperpowersPrefix, s.Path)
		require.False(t, s.Builtin, "skill %q: Vendored skills should not be Builtin", s.Name)
		require.True(t, s.Vendored, "skill %q: Vendored skills should have Vendored=true", s.Name)
		require.NotEmpty(t, s.Description, "skill %q: Description should not be empty", s.Name)
		require.NotEmpty(t, s.Instructions, "skill %q: Instructions should not be empty", s.Name)
	}

	// Verify at least one well-known superpowers skill is present.
	var foundBrainstorming bool
	for _, s := range discovered {
		if s.Name == "brainstorming" {
			foundBrainstorming = true
			require.Equal(t, "crush://superpowers/brainstorming/SKILL.md", s.SkillFilePath)
			require.Equal(t, "crush://superpowers/brainstorming", s.Path)
		}
	}
	require.True(t, foundBrainstorming, "brainstorming superpowers skill not found")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/skills/ -run TestDiscoverSuperpowers -v`
Expected: FAIL — `DiscoverSuperpowers` and `SuperpowersPrefix` don't exist yet.

- [ ] **Step 3: Create `superpowers_embed.go`**

Create `internal/skills/superpowers_embed.go`:

```go
package skills

import (
	"embed"
	"io/fs"
	"log/slog"
	"path/filepath"
)

// SuperpowersPrefix is the path prefix for vendored superpowers skill files.
// It is used by the View tool to distinguish embedded vendored files from
// builtin and disk files.
const SuperpowersPrefix = "crush://superpowers/"

//go:embed superpowers/*
var superpowersFS embed.FS

// SuperpowersFS returns the embedded filesystem containing vendored
// superpowers skills.
func SuperpowersFS() embed.FS {
	return superpowersFS
}

// DiscoverSuperpowers finds all valid superpowers skills embedded in the
// binary.
func DiscoverSuperpowers() []*Skill {
	skills, _ := DiscoverSuperpowersWithStates()
	return skills
}

// DiscoverSuperpowersWithStates is like DiscoverSuperpowers but additionally
// returns a per-file state slice describing parse/validation outcomes.
func DiscoverSuperpowersWithStates() ([]*Skill, []*SkillState) {
	var discovered []*Skill
	var states []*SkillState

	fs.WalkDir(superpowersFS, "superpowers", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || d.Name() != SkillFileName {
			return nil
		}

		content, err := superpowersFS.ReadFile(path)
		if err != nil {
			slog.Warn("Failed to read superpowers skill file", "path", path, "error", err)
			states = append(states, &SkillState{Path: path, State: StateError, Err: err})
			return nil
		}

		skill, err := ParseContent(content)
		if err != nil {
			slog.Warn("Failed to parse superpowers skill file", "path", path, "error", err)
			states = append(states, &SkillState{Path: path, State: StateError, Err: err})
			return nil
		}

		// Set paths using the superpowers prefix. Strip the leading
		// "superpowers/" so the path is relative to the embedded root
		// (e.g., "crush://superpowers/brainstorming/SKILL.md").
		relPath, _ := filepath.Rel("superpowers", path)
		relPath = filepath.ToSlash(relPath)
		skill.SkillFilePath = SuperpowersPrefix + relPath
		skill.Path = SuperpowersPrefix + filepath.Dir(relPath)
		skill.Vendored = true

		if err := skill.Validate(); err != nil {
			slog.Warn("Superpowers skill validation failed", "path", path, "error", err)
			states = append(states, &SkillState{Name: skill.Name, Path: path, State: StateError, Err: err})
			return nil
		}

		slog.Debug("Successfully loaded superpowers skill", "name", skill.Name, "path", skill.SkillFilePath)
		discovered = append(discovered, skill)
		states = append(states, &SkillState{Name: skill.Name, Path: skill.SkillFilePath, State: StateNormal})
		return nil
	})

	return discovered, states
}
```

- [ ] **Step 4: Vendor the superpowers skills**

Run the initial clone to populate the directory:

```bash
git clone --depth 1 https://github.com/obra/superpowers.git /tmp/superpowers-init
cp -r /tmp/superpowers-init/skills internal/skills/superpowers
rm -rf /tmp/superpowers-init
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/skills/ -run TestDiscoverSuperpowers -v`
Expected: PASS.

- [ ] **Step 6: Run full test suite to check for regressions**

Run: `go test ./internal/skills/ -v`
Expected: ALL PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/skills/superpowers_embed.go internal/skills/superpowers/
git commit -m "feat: add superpowers embedding and discovery with vendored skills"
```

---

### Task 3: Update discovery pipeline in `coordinator.go`

**Files:**
- Modify: `internal/agent/coordinator.go:1135-1183` (discoverSkills)

- [ ] **Step 1: Add superpowers discovery to `discoverSkills`**

In `internal/agent/coordinator.go`, update `discoverSkills`. After the builtin discovery block (line 1140), add superpowers discovery. The full updated function:

Replace lines 1135-1183:

```go
func discoverSkills(cfg *config.ConfigStore) (allSkills, activeSkills []*skills.Skill) {
	builtin, builtinStates := skills.DiscoverBuiltinWithStates()
	discovered := append([]*skills.Skill(nil), builtin...)

	superpowers, superpowersStates := skills.DiscoverSuperpowersWithStates()
	discovered = append(discovered, superpowers...)

	var userStates []*skills.SkillState
	var userPaths []string

	opts := cfg.Config().Options
	if opts != nil && len(opts.SkillsPaths) > 0 {
		userPaths = make([]string, 0, len(opts.SkillsPaths))
		for _, pth := range opts.SkillsPaths {
			expanded := home.Long(pth)
			if strings.HasPrefix(expanded, "$") {
				if resolved, err := cfg.Resolver().ResolveValue(expanded); err == nil {
					expanded = resolved
				}
			}
			userPaths = append(userPaths, expanded)
		}
		var userSkills []*skills.Skill
		userSkills, userStates = skills.DiscoverWithStates(userPaths)
		discovered = append(discovered, userSkills...)
	}

	allSkills = skills.Deduplicate(discovered)
	var disabledSkills []string
	if opts != nil {
		disabledSkills = opts.DisabledSkills
	}
	activeSkills = skills.Filter(allSkills, disabledSkills)

	allStates := append([]*skills.SkillState(nil), builtinStates...)
	allStates = append(allStates, superpowersStates...)
	allStates = append(allStates, userStates...)

	allStates = skills.DeduplicateStates(allStates)

	slices.SortStableFunc(allStates, func(a, b *skills.SkillState) int {
		return strings.Compare(strings.ToLower(a.Path), strings.ToLower(b.Path))
	})
	skills.SetLatestStates(allStates)
	skills.PublishStates(allStates)

	logDiscoveryStats(builtin, builtinStates, userStates, userPaths, allSkills, activeSkills, disabledSkills)
	return allSkills, activeSkills
}
```

- [ ] **Step 2: Run build to verify compilation**

Run: `go build .`
Expected: Builds successfully.

- [ ] **Step 3: Commit**

```bash
git add internal/agent/coordinator.go
git commit -m "feat: add superpowers discovery to coordinator skill pipeline"
```

---

### Task 4: Update `crush_info` tool to report `vendored` origin

**Files:**
- Modify: `internal/agent/tools/crush_info.go:294-346` (writeSkills)

- [ ] **Step 1: Update `writeSkills` origin map**

In `internal/agent/tools/crush_info.go`, update the `writeSkills` function. Replace the origin map building block (lines 313-319):

```go
	// Build origin map from the pre-filter list.
	originMap := make(map[string]string, len(allSkills))
	for _, s := range allSkills {
		if s.Builtin {
			originMap[s.Name] = "builtin"
		} else if s.Vendored {
			originMap[s.Name] = "vendored"
		} else {
			originMap[s.Name] = "user"
		}
	}
```

- [ ] **Step 2: Run build to verify compilation**

Run: `go build .`
Expected: Builds successfully.

- [ ] **Step 3: Commit**

```bash
git add internal/agent/tools/crush_info.go
git commit -m "feat: report vendored skill origin in crush_info tool"
```

---

### Task 5: Update View tool to support `crush://superpowers/` prefix

**Files:**
- Modify: `internal/agent/tools/view.go:106-110` (prefix check in Run)
- Modify: `internal/agent/tools/view.go:440-491` (readBuiltinFile → readEmbeddedFile)

- [ ] **Step 1: Refactor `readBuiltinFile` to `readEmbeddedFile`**

In `internal/agent/tools/view.go`, rename `readBuiltinFile` to `readEmbeddedFile` and add superpowers prefix support. Replace the entire function (lines 440-491):

```go
// readEmbeddedFile reads a file from an embedded skill filesystem (builtin or
// superpowers). It resolves the prefix to determine which embedded FS to use.
func readEmbeddedFile(params ViewParams, skillTracker *skills.Tracker) (fantasy.ToolResponse, error) {
	var embeddedPath string
	var embeddedFS embed.FS

	switch {
	case strings.HasPrefix(params.FilePath, skills.SuperpowersPrefix):
		embeddedPath = "superpowers/" + strings.TrimPrefix(params.FilePath, skills.SuperpowersPrefix)
		embeddedFS = skills.SuperpowersFS()
	case strings.HasPrefix(params.FilePath, skills.BuiltinPrefix):
		embeddedPath = "builtin/" + strings.TrimPrefix(params.FilePath, skills.BuiltinPrefix)
		embeddedFS = skills.BuiltinFS()
	default:
		return fantasy.NewTextErrorResponse(fmt.Sprintf("Unknown embedded prefix: %s", params.FilePath)), nil
	}

	data, err := fs.ReadFile(embeddedFS, embeddedPath)
	if err != nil {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("Embedded file not found: %s", params.FilePath)), nil
	}

	content := string(data)
	if !utf8.ValidString(content) {
		return fantasy.NewTextErrorResponse("File content is not valid UTF-8"), nil
	}

	limit := params.Limit
	if limit <= 0 {
		limit = 1000000 // Effectively no limit for skill files.
	}

	lines := strings.Split(content, "\n")
	offset := min(params.Offset, len(lines))
	lines = lines[offset:]

	hasMore := len(lines) > limit
	if hasMore {
		lines = lines[:limit]
	}

	output := "<file>\n"
	output += addLineNumbers(strings.Join(lines, "\n"), offset+1)
	if hasMore {
		output += fmt.Sprintf("\n\n(File has more lines. Use 'offset' parameter to read beyond line %d)",
			offset+len(lines))
	}
	output += "\n</file>\n"

	meta := ViewResponseMetadata{
		FilePath: params.FilePath,
		Content:  strings.Join(lines, "\n"),
	}
	if skill, err := skills.ParseContent(data); err == nil {
		meta.ResourceType = ViewResourceSkill
		meta.ResourceName = skill.Name
		meta.ResourceDescription = skill.Description
		skillTracker.MarkLoaded(skill.Name)
	}

	return fantasy.WithResponseMetadata(
		fantasy.NewTextResponse(output),
		meta,
	), nil
}
```

Add the `embed` and `fs` imports if not already present:

```go
import (
	"embed"
	"io/fs"
	// ... existing imports
)
```

- [ ] **Step 2: Update the prefix check in Run method**

In `internal/agent/tools/view.go`, update the prefix check (lines 106-110). Replace:

```go
		// Handle builtin skill files (crush: prefix).
		if strings.HasPrefix(params.FilePath, skills.BuiltinPrefix) {
			resp, err := readBuiltinFile(params, skillTracker)
			return resp, err
		}
```

With:

```go
		// Handle embedded skill files (crush:// prefix).
		if strings.HasPrefix(params.FilePath, skills.BuiltinPrefix) ||
			strings.HasPrefix(params.FilePath, skills.SuperpowersPrefix) {
			resp, err := readEmbeddedFile(params, skillTracker)
			return resp, err
		}
```

- [ ] **Step 3: Run build to verify compilation**

Run: `go build .`
Expected: Builds successfully.

- [ ] **Step 4: Commit**

```bash
git add internal/agent/tools/view.go
git commit -m "feat: support crush://superpowers/ prefix in View tool"
```

---

### Task 6: Update `prompt.go` discovery pipeline and add bootstrap injection

**Files:**
- Modify: `internal/agent/prompt/prompt.go:31-42` (PromptDat struct)
- Modify: `internal/agent/prompt/prompt.go:150-224` (promptData function)

- [ ] **Step 1: Add `SuperpowersBootstrap` to `PromptDat`**

In `internal/agent/prompt/prompt.go`, add the field to `PromptDat` (after `AvailSkillXML` on line 41):

```go
type PromptDat struct {
	Provider             string
	Model                string
	Config               config.Config
	WorkingDir           string
	IsGitRepo            bool
	Platform             string
	Date                 string
	GitStatus            string
	ContextFiles         []ContextFile
	AvailSkillXML        string
	SuperpowersBootstrap string
}
```

- [ ] **Step 2: Update `promptData` to include superpowers tier and bootstrap**

Replace the skills section of `promptData` (lines 168-199). The full updated `promptData`:

```go
func (p *Prompt) promptData(ctx context.Context, provider, model string, store *config.ConfigStore) (PromptDat, error) {
	workingDir := cmp.Or(p.workingDir, store.WorkingDir())
	platform := cmp.Or(p.platform, runtime.GOOS)

	files := map[string][]ContextFile{}

	cfg := store.Config()
	for _, pth := range cfg.Options.ContextPaths {
		expanded := expandPath(pth, store)
		pathKey := strings.ToLower(expanded)
		if _, ok := files[pathKey]; ok {
			continue
		}
		content := processContextPath(expanded, store)
		files[pathKey] = content
	}

	// Discover and load skills metadata.
	var availSkillXML string

	// Start with builtin skills.
	allSkills := skills.DiscoverBuiltin()
	builtinNames := make(map[string]bool, len(allSkills))
	for _, s := range allSkills {
		builtinNames[s.Name] = true
	}

	// Add vendored superpowers skills.
	superpowersSkills := skills.DiscoverSuperpowers()
	for _, spSkill := range superpowersSkills {
		if builtinNames[spSkill.Name] {
			slog.Warn("Superpowers skill overrides builtin skill", "name", spSkill.Name)
		}
		allSkills = append(allSkills, spSkill)
	}

	// Discover user skills from configured paths.
	if len(cfg.Options.SkillsPaths) > 0 {
		expandedPaths := make([]string, 0, len(cfg.Options.SkillsPaths))
		for _, pth := range cfg.Options.SkillsPaths {
			expandedPaths = append(expandedPaths, expandPath(pth, store))
		}
		for _, userSkill := range skills.Discover(expandedPaths) {
			if builtinNames[userSkill.Name] {
				slog.Warn("User skill overrides builtin skill", "name", userSkill.Name)
			}
			allSkills = append(allSkills, userSkill)
		}
	}

	// Deduplicate: user > superpowers > builtin.
	allSkills = skills.Deduplicate(allSkills)

	// Filter out disabled skills.
	allSkills = skills.Filter(allSkills, cfg.Options.DisabledSkills)

	// Extract using-superpowers bootstrap and exclude from available skills XML.
	var superpowersBootstrap string
	skillsForXML := make([]*skills.Skill, 0, len(allSkills))
	for _, s := range allSkills {
		if s.Name == "using-superpowers" {
			superpowersBootstrap = s.Instructions
			// Skip adding to XML — it's already in the system prompt.
			continue
		}
		skillsForXML = append(skillsForXML, s)
	}

	if len(skillsForXML) > 0 {
		availSkillXML = skills.ToPromptXML(skillsForXML)
	}

	isGit := isGitRepo(store.WorkingDir())
	data := PromptDat{
		Provider:             provider,
		Model:                model,
		Config:               *cfg,
		WorkingDir:           filepath.ToSlash(workingDir),
		IsGitRepo:            isGit,
		Platform:             platform,
		Date:                 p.now().Format("1/2/2006"),
		AvailSkillXML:        availSkillXML,
		SuperpowersBootstrap: superpowersBootstrap,
	}
	if isGit {
		var err error
		data.GitStatus, err = getGitStatus(ctx, store.WorkingDir())
		if err != nil {
			return PromptDat{}, err
		}
	}

	for _, contextFiles := range files {
		data.ContextFiles = append(data.ContextFiles, contextFiles...)
	}
	return data, nil
}
```

- [ ] **Step 3: Run build to verify compilation**

Run: `go build .`
Expected: Builds successfully.

- [ ] **Step 4: Commit**

```bash
git add internal/agent/prompt/prompt.go
git commit -m "feat: add superpowers discovery tier and bootstrap injection to prompt"
```

---

### Task 7: Inject bootstrap into system prompt template

**Files:**
- Modify: `internal/agent/templates/coder.md.tpl:375-395` (skills section)

- [ ] **Step 1: Add bootstrap injection to template**

In `internal/agent/templates/coder.md.tpl`, update the skills section (lines 375-395). Replace:

```
{{- if .AvailSkillXML}}

{{.AvailSkillXML}}
```

With:

```
{{- if .SuperpowersBootstrap}}
<EXTREMELY_IMPORTANT>
{{.SuperpowersBootstrap}}
</EXTREMELY_IMPORTANT>
{{end}}

{{- if .AvailSkillXML}}

{{.AvailSkillXML}}
```

This inserts the full `using-superpowers` bootstrap content at the top of the skills section, before the `<available_skills>` XML.

- [ ] **Step 2: Run build to verify compilation**

Run: `go build .`
Expected: Builds successfully.

- [ ] **Step 3: Commit**

```bash
git add internal/agent/templates/coder.md.tpl
git commit -m "feat: inject using-superpowers bootstrap into system prompt"
```

---

### Task 8: Add three-tier dedup test

**Files:**
- Test: `internal/skills/skills_test.go`

- [ ] **Step 1: Write the test**

Add to `internal/skills/skills_test.go` after `TestDeduplicate`:

```go
func TestDeduplicateThreeTier(t *testing.T) {
	t.Parallel()

	// Simulate three-tier discovery: builtin -> superpowers -> user.
	input := []*Skill{
		{Name: "shared-skill", Path: "crush://skills/shared-skill", Builtin: true},
		{Name: "shared-skill", Path: "crush://superpowers/shared-skill", Vendored: true},
		{Name: "shared-skill", Path: "/user/shared-skill"},
		{Name: "builtin-only", Path: "crush://skills/builtin-only", Builtin: true},
		{Name: "superpowers-only", Path: "crush://superpowers/superpowers-only", Vendored: true},
	}

	result := Deduplicate(input)
	require.Len(t, result, 3)

	// User wins for shared-skill.
	var shared, builtinOnly, superpowersOnly *Skill
	for _, s := range result {
		switch s.Name {
		case "shared-skill":
			shared = s
		case "builtin-only":
			builtinOnly = s
		case "superpowers-only":
			superpowersOnly = s
		}
	}

	require.NotNil(t, shared)
	require.Equal(t, "/user/shared-skill", shared.Path)
	require.False(t, shared.Builtin)
	require.False(t, shared.Vendored)

	require.NotNil(t, builtinOnly)
	require.Equal(t, "crush://skills/builtin-only", builtinOnly.Path)
	require.True(t, builtinOnly.Builtin)

	require.NotNil(t, superpowersOnly)
	require.Equal(t, "crush://superpowers/superpowers-only", superpowersOnly.Path)
	require.True(t, superpowersOnly.Vendored)
}
```

- [ ] **Step 2: Run test to verify it passes**

Run: `go test ./internal/skills/ -run TestDeduplicateThreeTier -v`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/skills/skills_test.go
git commit -m "test: add three-tier skill deduplication test"
```

---

### Task 9: Create Crush-specific `using-superpowers` override

**Files:**
- Create: `internal/skills/superpowers/using-superpowers/SKILL.md`

- [ ] **Step 1: Create the Crush-specific `using-superpowers` skill**

This file is based on the upstream version with Crush-specific tool mappings. Create `internal/skills/superpowers/using-superpowers/SKILL.md` with the full `using-superpowers` skill content adapted for Crush. The key addition is a Crush tool mapping section.

Run this to get the upstream version as a starting point:

```bash
git clone --depth 1 https://github.com/obra/superpowers.git /tmp/superpowers-us
cat /tmp/superpowers-us/skills/using-superpowers/SKILL.md
```

Then modify it to add a Crush-specific section in the tool mapping area. The critical addition:

```markdown
**Tool Mapping for Crush:**
When skills reference tools you don't have, substitute Crush equivalents:
- `TodoWrite` → `todowrite`
- `Task` tool with subagents → Use the `task` tool with appropriate `subagent_type`
- `Skill` tool → Use the `view` tool to read skill files at their `<location>` path
- `Read`, `Write`, `Edit`, `Bash` → Native tools with same names

Use Crush's native `view` tool to load any skill by passing its `<location>` path verbatim.
```

Also update the skill's description to mention Crush alongside other platforms if it lists them.

Remove references to the `Skill` tool — Crush uses `view` directly.

- [ ] **Step 2: Verify the skill is discoverable**

Run: `go test ./internal/skills/ -run TestDiscoverSuperpowers -v`
Expected: PASS — `using-superpowers` is found with `Vendored=true`.

- [ ] **Step 3: Commit**

```bash
git add internal/skills/superpowers/using-superpowers/
git commit -m "feat: add Crush-specific using-superpowers skill with tool mappings"
```

---

### Task 10: Add Taskfile update script

**Files:**
- Modify: `Taskfile.yaml`

- [ ] **Step 1: Add `update-skills` task**

Add to `Taskfile.yaml` after the `sqlc` task:

```yaml
  update-skills:
    desc: Update vendored superpowers skills from GitHub
    cmds:
      - git clone --depth 1 https://github.com/obra/superpowers.git /tmp/superpowers-update
      - rsync -r --exclude='using-superpowers' /tmp/superpowers-update/skills/ internal/skills/superpowers/
      - rm -rf /tmp/superpowers-update
      - echo "Superpowers skills updated. Review and commit the changes."
```

- [ ] **Step 2: Verify the task runs**

Run: `task update-skills`
Expected: Clones, copies skills (excluding `using-superpowers`), cleans up.

- [ ] **Step 3: Verify tests still pass after update**

Run: `go test ./internal/skills/ -v`
Expected: ALL PASS.

- [ ] **Step 4: Commit**

```bash
git add Taskfile.yaml
git commit -m "feat: add update-skills task for vendored superpowers"
```

---

### Task 11: Format, lint, and full test suite

**Files:**
- All modified files

- [ ] **Step 1: Format all code**

Run: `task fmt`
Expected: No changes or clean formatting.

- [ ] **Step 2: Run linter**

Run: `task lint`
Expected: No new issues.

- [ ] **Step 3: Run full test suite**

Run: `task test`
Expected: ALL PASS.

- [ ] **Step 4: Run build**

Run: `go build .`
Expected: Clean build.

- [ ] **Step 5: Commit any formatting fixes**

```bash
git add -A
git commit -m "chore: format and lint after superpowers integration"
```
