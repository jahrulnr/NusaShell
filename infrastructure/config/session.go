package config

import "time"

type Session struct {
	MinPasswordLen   int           `default:"4"`
	MaxPasswordBytes int           `default:"72"`
	LoginBurst       int           `default:"5"`
	LoginWindow      time.Duration `default:"15m"`
	TTL              time.Duration `default:"720h"`
}
