package terraform

import (
	"fmt"
	"strings"
)

const (
	jobTypeApply   = "apply"
	jobTypeDestroy = "destroy"
	defaultCommand = "terraform init && terraform apply -auto-approve"
)

type Terraform struct {
	Args []string
}

func NewTerraform(args []string) *Terraform {
	extraArgs := &Terraform{
		Args: args,
	}

	if extraArgs.Args == nil {
		extraArgs.Args = []string{"-auto-approve"}
	}

	return extraArgs
}

func (t *Terraform) Command(jobType string) string {
	var args string
	var command string

	if t.Args == nil {
		args = strings.Join(t.Args, "-auto-approve")
	}

	switch jobType {
	case jobTypeApply:
		command = fmt.Sprintf("terraform init && terraform apply %s", args)
	case jobTypeDestroy:
		command = fmt.Sprintf("terraform init && terraform plan -destroy && terraform destroy %s", args)
	default:
		command = defaultCommand
	}

	return command
}
