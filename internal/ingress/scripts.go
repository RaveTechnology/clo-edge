package ingress

import _ "embed"

//go:embed scripts/prepare_node.py
var prepareNodeScript string

//go:embed scripts/apply_edge.py
var applyEdgeScript string

//go:embed scripts/publish_discovery.py
var publishDiscoveryScript string
