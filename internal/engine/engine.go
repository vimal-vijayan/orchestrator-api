package engine

import (
	"infra.essity.com/orchstrator-api/internal/engine/opentofu"
	"infra.essity.com/orchstrator-api/internal/engine/terraform"
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
