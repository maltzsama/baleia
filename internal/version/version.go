// Package version holds the Baleia build version, injected at build time via
// -ldflags "-X github.com/galileostudio/baleia/internal/version.Version=...".
// It defaults to "dev" for local development builds.
package version

// Version is the build version reported by the API. Set at build time with
// -ldflags, e.g. release builds stamp the tag (v0.1.0) here.
var Version = "dev"

// Commit is the source revision stamped at build time. It defaults to "dev"
// for local builds.
var Commit = "dev"
