// Package assets embeds the factory genome: the pristine UI and the seed
// skills/prompts a newborn organism boots with.
package assets

import "embed"

//go:embed all:ui all:seeds
var FS embed.FS
