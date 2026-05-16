package skills

import (
	"embed"
	"io/fs"
	"log/slog"
	"path/filepath"
)

// SuperpowersPrefix is the path prefix for vendored superpowers skill files.
const SuperpowersPrefix = "crush://superpowers/"

//go:embed superpowers/*
var superpowersFS embed.FS

// SuperpowersFS returns the embedded filesystem containing vendored superpowers skills.
func SuperpowersFS() embed.FS {
	return superpowersFS
}

// DiscoverSuperpowers finds all valid superpowers skills embedded in the binary.
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

		// Set paths using the superpowers prefix.
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
