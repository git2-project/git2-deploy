// Package deploy embeds what git2ctl installs: the Compose files and the
// image build files they refer to, in the repo's own layout (compose/ next
// to images/), so the installed stack is the one tests/smoke tests.
package deploy

import "embed"

// Stack holds compose/compose.yaml, compose/compose.tls.yaml and images/.
//
//go:embed compose/compose.yaml compose/compose.tls.yaml images
var Stack embed.FS
