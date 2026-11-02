// Command tagpage adds a page tag through the fake tablet, for the e2e seed
// (cmd/rmseed has no page-tag command):
//
//	tagpage --data DIR --user UID DOCID PAGEID TAG
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/ddvk/rmfakecloud/internal/testkit/tablet"
)

func main() {
	log.SetLevel(log.WarnLevel)
	data := flag.String("data", "", "rmfakecloud data dir")
	uid := flag.String("user", "", "user id")
	flag.Parse()
	if *data == "" || *uid == "" || flag.NArg() != 3 {
		fmt.Fprintln(os.Stderr, "usage: tagpage --data DIR --user UID DOCID PAGEID TAG")
		os.Exit(2)
	}
	ft := tablet.New(nil, tablet.NewStorage(*data), *uid)
	err := ft.Load()
	if err == nil {
		err = ft.TagPage(flag.Arg(0), flag.Arg(1), flag.Arg(2), time.Now())
	}
	if err == nil {
		err = ft.Sync()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "tagpage:", err)
		os.Exit(1)
	}
}
