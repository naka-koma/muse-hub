// Package phase1 contains the Compose recipe shared by the verifier and Hub.
package phase1

import _ "embed"

//go:embed compose.yaml
var Compose string
