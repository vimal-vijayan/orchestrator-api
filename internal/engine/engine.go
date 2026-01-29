package engine

import (
	"infra.essity.com/orchestrator-api/internal/engine/opentofu"
	"infra.essity.com/orchestrator-api/internal/engine/terraform"
)

type Engine interface {
	Command(jobType string) string
}

func ForEngine(engineType string, args []string) Engine {
	switch engineType {
	case "terraform":
		return terraform.NewTerraform(args)
	case "opentofu":
		return opentofu.NewOpentofu(args)
	default:
		return opentofu.NewOpentofu(args)
	}
}
