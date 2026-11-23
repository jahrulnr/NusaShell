package config

// BrowserConfig configures the threads' browser.
type BrowserConfig struct {
	// Exec is the Chrome or Chromium binary; empty finds one on the host.
	Exec string `json:"exec"`
	// Dir holds one profile folder per named profile, each keeping its
	// cookies, storage, and history across restarts. Empty keeps them
	// under `<storage.dir>/browser`.
	//
	// A browser profile cannot live on a filesystem that refuses symlinks:
	// Chrome keeps its process-singleton lock as one, and aborts the launch
	// when it cannot create it. An NTFS mount without the `symlinks` option
	// is such a filesystem. Left empty, the folder moves to the user's config
	// directory (`nusashell/browser`) when the data folder is on one; a folder
	// set here is used as given, and a launch there fails with advice.
	Dir string `json:"dir"`
}

func (c *BrowserConfig) Normalize() {}
