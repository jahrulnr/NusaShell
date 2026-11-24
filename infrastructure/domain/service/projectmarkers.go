package service

import "slices"

// Project markers are the shared definition of "this folder is a project":
// the index gate (infrastructure/service/symbol) and the hydration gate
// (application/job) both read this one list, so they cannot disagree about
// what a project root is. The list is vocabulary, not I/O — each gate owns
// how it reads the root (a walk, a stat, a directory listing).
var projectMarkers = []string{
	"go.mod", "package.json", "Cargo.toml", "pyproject.toml", "pom.xml",
	"build.gradle", "build.gradle.kts", "composer.json", "Gemfile",
	".git", ".svn", ".hg",
}

// projectMarkerGlobs are the root-level name patterns that prove a project
// but cannot be probed by name: they need a directory listing to match.
var projectMarkerGlobs = []string{"*.csproj", "*.sln", "build.gradle.*"}

// ProjectMarkers lists the root-level names whose presence proves a folder
// is a project: manifests plus the version-control directories.
func ProjectMarkers() []string { return slices.Clone(projectMarkers) }

// ProjectMarkerGlobs lists the root-level name patterns that prove a
// project, for callers that can list a directory.
func ProjectMarkerGlobs() []string { return slices.Clone(projectMarkerGlobs) }
