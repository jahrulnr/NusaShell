package config

type App struct {
	Name    string `json:"-" default:"NusaShell"`
	Source  string `json:"-" default:"https://github.com/jahrulnr/NusaShell"`
	Version string `json:"-" default:"0.0.0"`
}
