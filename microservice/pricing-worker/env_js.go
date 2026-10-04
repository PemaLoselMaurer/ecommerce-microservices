//go:build js

package main

import "github.com/syumai/workers-go/cloudflare"

// getenv reads the Worker's variables and secrets on Cloudflare.
func getenv(name string) string { return cloudflare.Getenv(name) }
