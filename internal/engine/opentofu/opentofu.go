package opentofu

import (
	"fmt"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	jobTypeApply   = "apply"
	jobTypeDestroy = "destroy"
	defaultCommand = "tofu init && tofu apply -auto-approve"
)

type Opentofu struct {
	Args []string
}

func NewOpentofu(args []string) *Opentofu {
	logger := log.Log.WithName("opentofu_engine")

	if len(args) == 0 {
		args = []string{"-auto-approve"}
		logger.V(1).Info("no extra args provided for opentofu, using default", "args", args)
	}

	return &Opentofu{
		Args: args,
	}
}

func (o *Opentofu) Command(jobType string) string {
	logger := log.Log.WithName("opentofu_engine")
	args := strings.Join(o.Args, " ")

	var command string

	switch jobType {
	case jobTypeApply:
		command = fmt.Sprintf("tofu init && tofu apply %s", args)
	case jobTypeDestroy:
		command = fmt.Sprintf("tofu init && tofu plan -destroy && tofu destroy %s", args)
	default:
		command = defaultCommand
	}

	logger.V(1).Info("built opentofu command", "jobType", jobType, "command", command)
	return command
}
