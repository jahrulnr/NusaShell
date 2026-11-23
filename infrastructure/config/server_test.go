package config

import "testing"

// A local caller reaches a specific bind through its own address — a
// loopback alias does not answer there — while a wildcard bind is
// reached through loopback as before.
func TestLocalAuthorityPrefersTheAcceptedBind(t *testing.T) {
	hosts := []string{"127.0.0.1", "localhost", "192.168.18.81"}
	if got := (ServerConfig{Bind: "192.168.18.81", Hosts: hosts}).LocalAuthority(10994); got != "192.168.18.81:10994" {
		t.Fatalf("LocalAuthority = %q, want the bind address", got)
	}
	if got := (ServerConfig{Bind: "0.0.0.0", Hosts: hosts}).LocalAuthority(10994); got != "127.0.0.1:10994" {
		t.Fatalf("wildcard LocalAuthority = %q, want loopback", got)
	}
}

// public_url is a reachability declaration, not a listener: it trims to
// a canonical URL, folds its host into the accepted aliases so a proxy's
// Host header passes the guard, and rejects everything but http(s) with
// a host.
func TestPublicURL(t *testing.T) {
	c := ServerConfig{PublicURL: "  https://Shell.Example.com/  ", Hosts: []string{"127.0.0.1"}}
	c.Normalize()
	if c.PublicURL != "https://Shell.Example.com" {
		t.Fatalf("PublicURL = %q", c.PublicURL)
	}
	found := 0
	for _, h := range c.Hosts {
		if h == "shell.example.com" {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("public host folded %d times into %v", found, c.Hosts)
	}

	// A host already listed is not duplicated.
	c = ServerConfig{PublicURL: "https://shell.example.com", Hosts: []string{"shell.example.com"}}
	c.Normalize()
	if len(c.Hosts) != 1 {
		t.Fatalf("Hosts = %v", c.Hosts)
	}

	// Ports stay out of Hosts; only the hostname is an accepted alias.
	c = ServerConfig{PublicURL: "https://shell.example.com:8443"}
	c.Normalize()
	for _, h := range c.Hosts {
		if h == "shell.example.com:8443" {
			t.Fatalf("port leaked into hosts: %v", c.Hosts)
		}
	}

	for _, u := range []string{
		"ftp://shell.example.com", "shell.example.com",
		"https://", "https://user:pw@shell.example.com",
	} {
		c = ServerConfig{PublicURL: u}
		c.Normalize()
		if err := c.validate(); err == nil {
			t.Errorf("public_url %q must fail validation", u)
		}
	}
	c = ServerConfig{PublicURL: "https://shell.example.com"}
	c.Normalize()
	if err := c.validate(); err != nil {
		t.Errorf("public_url rejected: %v", err)
	}
}
