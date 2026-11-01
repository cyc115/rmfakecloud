// Command rmseed seeds a rmfakecloud data dir through the fake tablet, for the
// e2e harness:
//
//	rmseed --data DIR --user UID <cmd> [args]
//
// Commands: init, notebook ID NAME PARENT, folder ID NAME PARENT,
// page DOCID PAGEID FILE.rm, delete-page DOCID PAGEID, open DOCID PAGE.
// Every command except init loads the current state and ends with a sync.
package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/ddvk/rmfakecloud/internal/testkit/tablet"
)

func main() {
	log.SetLevel(log.WarnLevel)
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "rmseed:", err)
		os.Exit(1)
	}
}

func run(argv []string) error {
	fl := flag.NewFlagSet("rmseed", flag.ContinueOnError)
	data := fl.String("data", "", "rmfakecloud data dir")
	uid := fl.String("user", "", "user id")
	fl.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: rmseed --data DIR --user UID <init|notebook ID NAME PARENT|folder ID NAME PARENT|page DOCID PAGEID FILE.rm|delete-page DOCID PAGEID|open DOCID PAGE>")
		fl.PrintDefaults()
	}
	if err := fl.Parse(argv); err != nil {
		return err
	}
	args := fl.Args()
	if *data == "" || *uid == "" || len(args) == 0 {
		fl.Usage()
		return fmt.Errorf("--data, --user and a command are required")
	}
	need := func(n int) error {
		if len(args)-1 != n {
			return fmt.Errorf("%s takes %d argument(s), got %d", args[0], n, len(args)-1)
		}
		return nil
	}

	st := tablet.NewStorage(*data)
	if args[0] == "init" {
		if err := need(0); err != nil {
			return err
		}
		if err := tablet.RegisterUser(st, *uid, true); err != nil {
			return err
		}
		return tablet.New(nil, st, *uid).Sync()
	}

	ft := tablet.New(nil, st, *uid)
	if err := ft.Load(); err != nil {
		return err
	}
	switch args[0] {
	case "notebook":
		if err := need(3); err != nil {
			return err
		}
		ft.Notebook(args[1], args[2], args[3])
	case "folder":
		if err := need(3); err != nil {
			return err
		}
		ft.Folder(args[1], args[2], args[3])
	case "page":
		if err := need(3); err != nil {
			return err
		}
		rm, err := os.ReadFile(args[3])
		if err != nil {
			return err
		}
		if err := ft.SetPage(args[1], args[2], rm); err != nil {
			return err
		}
	case "delete-page":
		if err := need(2); err != nil {
			return err
		}
		if err := ft.DeletePage(args[1], args[2]); err != nil {
			return err
		}
	case "open":
		if err := need(2); err != nil {
			return err
		}
		page, err := strconv.Atoi(args[2])
		if err != nil {
			return fmt.Errorf("page: %w", err)
		}
		if err := ft.Open(args[1], page, time.Now()); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
	return ft.Sync()
}
