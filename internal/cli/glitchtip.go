package cli

import (
	"fmt"
	"os"

	"github.com/ddvk/rmfakecloud/internal/glitchtip"
)

// GlitchtipTest sends one event to GLITCHTIP_DSN and prints the id GlitchTip
// accepted; exits 1 otherwise. The deploy gate runs it inside the container,
// so it proves the server's own path to GlitchTip.
func (cli *Cli) GlitchtipTest(args []string) {
	message := "rmfakecloud glitchtip-test"
	if len(args) > 0 {
		message = args[0]
	}
	c, err := glitchtip.New(cli.cfg.GlitchtipDSN, cli.cfg.Version)
	if err == nil && c == nil {
		err = fmt.Errorf("GLITCHTIP_DSN is not set")
	}
	if err == nil {
		var id string
		if id, err = c.CaptureSync(glitchtip.Event{Level: "info", Type: "glitchtip-test", Value: message}); err == nil {
			fmt.Println(id)
			return
		}
	}
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
