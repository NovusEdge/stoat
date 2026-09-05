package recipes

import _ "embed"

// SampleManifest is the annotated recipe.toml copied by `stoat recipe new`.
// Keeping the source beside the embedded asset makes the docs and scaffold
// share one contract; docs/reference/samples/recipe.toml links to this file.
//
//go:embed samples/recipe.toml
var SampleManifest string
