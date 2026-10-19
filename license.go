package main

import (
	_ "embed"

	"github.com/yazawa-ichio/sedi-review/internal/server"
)

//go:embed LICENSE
var license string

func init() {
	server.ToolLicense = license
}
