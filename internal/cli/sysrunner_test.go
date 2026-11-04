package cli

import (
	"context"
	"errors"

	"github.com/fsvxavier/accordion-context/internal/sysproxy"
)

// Tests of the CLI must not run the real nft or iptables: doctor's firewall
// probes use sysRunner, so the default is a runner whose every command is
// "not installed". The probes treat that as absent.
func init() {
	sysRunner = notInstalledRunner{}
}

type notInstalledRunner struct{}

func (notInstalledRunner) Run(_ context.Context, c sysproxy.Command) (string, error) {
	return "", errors.New("run " + c.Name + ": not installed in tests")
}
