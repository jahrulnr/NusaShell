//! Config fixture.
use std::path::Path;

/// Config carries runtime options.
pub struct Config {
    pub root: String,
    pub debug: bool,
}

/// Store abstracts persistence.
pub trait Store {
    fn get(&self, key: &str) -> Option<String>;
}

impl Config {
    pub fn load(path: &Path) -> Self {
        Config { root: path.display().to_string(), debug: false }
    }

    fn validate(&self) -> bool {
        !self.root.is_empty()
    }
}

mod inner {
    pub fn hidden() {}
}

macro_rules! make_config {
    () => { Config::load(Path::new(".")) };
}

pub fn load() -> Config {
    Config::load(Path::new("/etc/app"))
}
