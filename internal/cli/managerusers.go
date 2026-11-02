package cli

import (
	"flag"
	"fmt"
	"os"

	"github.com/ddvk/rmfakecloud/internal/autoorganize"
	"github.com/ddvk/rmfakecloud/internal/config"
	"github.com/ddvk/rmfakecloud/internal/model"
	"github.com/ddvk/rmfakecloud/internal/storage/fs"
	log "github.com/sirupsen/logrus"
)

// ListUsers lists
func (cli *Cli) ListUsers(args []string) {
	users, err := cli.storage.GetUsers()
	if err != nil {
		log.Fatal(err)
	}
	for _, u := range users {
		fmt.Print(u.ID)
		if u.IsAdmin {
			fmt.Println("\tadmin")
		} else {
			fmt.Println()

		}
	}
}

// SetUser updates or creates the users if not exists
func (cli *Cli) SetUser(args []string) {
	userParam := flag.NewFlagSet("adduser", flag.ExitOnError)
	username := userParam.String("u", "", "username")
	pass := userParam.String("p", "", "password")
	admin := userParam.Bool("a", false, "isadmmin")
	sync15 := userParam.Bool("s", false, "should the user use the new sync")

	userParam.Parse(args)
	if *username == "" {
		userParam.PrintDefaults()
		return
	}

	usr, err := cli.storage.GetUser(*username)
	if err != nil {
		if *pass == "" {
			*pass, err = model.GenPassword()
			if err != nil {
				log.Fatal(err)
			}
			log.Info("new password:", *pass)
		}
		usr, err = model.NewUser(*username, *pass)
		if err != nil {
			log.Error(err)
			return
		}
	}
	if *pass != "" {
		usr.SetPassword(*pass)
	}
	usr.IsAdmin = *admin
	usr.Sync15 = *sync15

	err = cli.storage.UpdateUser(usr)
	if err != nil {
		log.Fatal(err)
	}
	log.Info("Updated/created the user")
}

// Cli cli interface
type Cli struct {
	storage *fs.FileSystemStorage
	cfg     *config.Config
}

// New creates
func New(cfg *config.Config) *Cli {
	storage := &fs.FileSystemStorage{
		Cfg: cfg,
	}
	return &Cli{
		storage: storage,
		cfg:     cfg,
	}

}

// Handle handles the args
func (cli *Cli) Handle(args []string) bool {
	if len(args) > 1 {
		cmd := args[1]
		otherarg := args[2:]
		switch cmd {
		case "setuser":
			cli.SetUser(otherarg)
		case "listusers":
			cli.ListUsers(otherarg)
		case "glitchtip-test":
			cli.GlitchtipTest(otherarg)
		case "automation":
			store := autoorganize.NewStore(cli.storage.UserDataPath, cli.cfg.AutoOrganizeInterval)
			if code := runAutomation(otherarg, store, os.Stdout, os.Stderr); code != 0 {
				os.Exit(code)
			}
		case "rmuser":
		default:
			log.Warn("unknown command: ", cmd)
		}
		return true
	}

	return false
}

func Usage() string {
	return `Commands:
	setuser		create users / reset passwords
	glitchtip-test	send one test event to GLITCHTIP_DSN, print its id (exit 1 if not accepted)
	listusers	list available users
	automation migrate --dry-run --user <uid>   print the automation.json the migration would write (read-only)
	automation rollback --user <uid> [--force]   write auto-organize.json from automation.json for the old binary
		(stop the rmfakecloud server first: a running server re-migrates and undoes the rollback)
`
}
