//go:build !js

package main

import "os"

// getenv reads process environment variables when the Worker is
// run as a plain Go server (`go run .`) for local debugging.
func getenv(name string) string { return os.Getenv(name) }
