// Package claude contains the Compose recipe shared by the verifier and Hub.
package claude

import _ "embed"

//go:embed compose.yaml
var Compose string
