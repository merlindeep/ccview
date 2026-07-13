// Command ccview is a compact console monitor for AI coding-agent usage limits
// (Claude, Codex, …).
//
// See the package documentation in internal/cli and the project README for
// usage details. This entry point simply delegates to cli.Execute.
package main

import "github.com/merlindeep/ccview/internal/cli"

func main() {
	cli.Execute()
}
