package main

import (
	"os"
	"runtime/debug"

	"github.com/QuietSugar/reup/internal/cli"
)

// version is injected at release time via -ldflags "-X main.version=...".
// go-install builds skip ldflags, so fall back to the module version
// embedded by the Go toolchain.
var version = "dev"

func resolveVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return version
}

func main() {
	os.Exit(cli.Run(os.Args[1:], resolveVersion()))
}
