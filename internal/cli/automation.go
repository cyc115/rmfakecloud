package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"

	"github.com/ddvk/rmfakecloud/internal/autoorganize"
)

const rollbackWarning = "stop the rmfakecloud server first: a running server re-migrates and undoes the rollback"

const automationUsage = `usage:
  automation migrate --dry-run --user <uid>   print the automation.json the migration would write (read-only)
  automation rollback --user <uid> [--force]   write auto-organize.json from automation.json for the old binary
                                             stop the rmfakecloud server first: a running server re-migrates and undoes the rollback
`

// runAutomation the "automation" command; returns the exit code (0 ok,
// 1 failure, 2 usage)
func runAutomation(args []string, store *autoorganize.Store, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, automationUsage)
		return 2
	}
	sub, rest := args[0], args[1:]
	fset := flag.NewFlagSet("automation "+sub, flag.ContinueOnError)
	fset.SetOutput(stderr)
	user := fset.String("user", "", "user id")
	dry := fset.Bool("dry-run", false, "migrate: print only, write nothing")
	force := fset.Bool("force", false, "rollback: write the expressible rules even if some are left out")
	switch sub {
	case "migrate", "rollback":
	default:
		fmt.Fprintf(stderr, "unknown automation command %q\n%s", sub, automationUsage)
		return 2
	}
	if err := fset.Parse(rest); err != nil {
		return 2
	}
	if *user == "" || fset.NArg() > 0 {
		fmt.Fprint(stderr, automationUsage)
		return 2
	}
	if sub == "migrate" {
		return runMigrate(*user, *dry, store, stdout, stderr)
	}
	return runRollback(*user, *force, store, stdout, stderr)
}

func runMigrate(uid string, dry bool, store *autoorganize.Store, stdout, stderr io.Writer) int {
	if !dry {
		fmt.Fprintln(stderr, "only --dry-run is supported; the server migrates on start")
		return 2
	}
	out, issues, err := store.MigrateDryRunIssues(uid)
	if errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(stderr, "nothing to migrate for %s: no %s\n", uid, autoorganize.V1ConfigFileName)
		return 0
	}
	if err != nil && !errors.Is(err, autoorganize.ErrAlreadyMigrated) {
		fmt.Fprintf(stderr, "migrate %s: %v\n", uid, err)
		return 1
	}
	if err != nil {
		fmt.Fprintf(stderr, "note: %s already exists; showing the migration of %s\n", autoorganize.ConfigFileName, autoorganize.V1ConfigFileName)
	}
	for _, is := range issues {
		fmt.Fprintf(stderr, "rule %d (%s) would be disabled: %s\n", is.Index+1, is.RuleID, is.Reason)
	}
	fmt.Fprintf(stdout, "%s\n", out)
	return 0
}

func runRollback(uid string, force bool, store *autoorganize.Store, stdout, stderr io.Writer) int {
	fmt.Fprintln(stderr, "warning: "+rollbackWarning)
	res, err := store.Rollback(uid, force)
	if errors.Is(err, autoorganize.ErrRollbackIssues) {
		fmt.Fprintf(stderr, "rollback refused for %s: v1 cannot express these rules, nothing was written (--force writes the rest and keeps the full v2 file):\n", uid)
		printIssues(stderr, res.Issues)
		return 1
	}
	if err != nil {
		fmt.Fprintf(stderr, "rollback %s: %v\n", uid, err)
		return 1
	}
	if len(res.Issues) > 0 {
		fmt.Fprintln(stderr, "left out of the v1 file (still in the renamed v2 file):")
		printIssues(stderr, res.Issues)
	}
	fmt.Fprintf(stdout, "wrote %s\nrenamed %s -> %s\n", res.V1Path, autoorganize.ConfigFileName, res.RenamedV2)
	return 0
}

func printIssues(w io.Writer, issues []autoorganize.RuleIssue) {
	for _, is := range issues {
		fmt.Fprintf(w, "rule %d (%s): %s\n", is.Index+1, is.RuleID, is.Reason)
	}
}
