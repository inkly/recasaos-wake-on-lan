// Package web holds the module's page, one HTML file served as it is.
package web

import _ "embed"

// Page is index.html.
//
//go:embed index.html
var Page string
