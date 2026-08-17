package domain

// PluginInstallSource identifies where a plugin install request comes from.
type PluginInstallSource string

const (
	InstallSourceCatalog PluginInstallSource = "catalog"
	InstallSourceGitHub  PluginInstallSource = "github"
	InstallSourceZip     PluginInstallSource = "zip"
)

// PluginInstallRequest is a domain value object describing the source and
// coordinates for a plugin install.
type PluginInstallRequest struct {
	Source PluginInstallSource
	ID     string // catalog: plugin id
	URL    string // github: repository URL or owner/repo shorthand
	Subdir string // github: optional subdirectory inside a monorepo
	Ref    string // github: optional branch or tag
	Data   []byte // zip: raw archive bytes
}

// PluginCatalogEntry is an item in the curated first-party plugin catalog.
type PluginCatalogEntry struct {
	ID          string // catalog key (e.g. "notes")
	PluginID    string // manifest id (e.g. "nusashell.notes")
	Name        string
	Version     string
	Description string
	Icon        string
	Tag         string
	ReleasedAt  string
}
