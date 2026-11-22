// Package taskdelegation embeds the single published MCP tool schema source.
package taskdelegation

import _ "embed"

//go:embed tools.json
var Tools []byte
