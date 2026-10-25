package autoorganize

import (
	"strconv"
	"time"

	log "github.com/sirupsen/logrus"
)

func init() {
	log.SetLevel(log.WarnLevel)
}

func ms(t time.Time) string {
	return strconv.FormatInt(t.UnixMilli(), 10)
}

func seedRule() Rule {
	return Rule{
		ID: "seed", Enabled: true,
		Source:      FolderRef{ID: "rr", Name: "Research Reports"},
		Scope:       ScopePdfEpub,
		Trigger:     Trigger{Type: TriggerFinishedReading, MinPages: 3, SettleHours: 6},
		Destination: Destination{CreateIfMissing: &CreateIfMissing{Name: "Read", Parent: ParentSource}},
		Action:      Action{Type: ActionMove},
	}
}
