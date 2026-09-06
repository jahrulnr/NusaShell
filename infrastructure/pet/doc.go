// Package pet is the infrastructure adapter for the desktop pet overlay.
// The Go core installs the release, spawns and stops the subprocess, and
// injects NUSASHELL_HOST / NUSASHELL_PORT / NUSASHELL_WS_URL so the overlay
// connects to this process. Application policy lives in application/pets.
package pet
