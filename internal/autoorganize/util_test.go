package autoorganize

import (
	"strconv"
	"time"

	"github.com/ddvk/rmfakecloud/internal/automation"
	log "github.com/sirupsen/logrus"
)

func init() {
	log.SetLevel(log.WarnLevel)
}

func ms(t time.Time) string {
	return strconv.FormatInt(t.UnixMilli(), 10)
}

// P the catalogue parameters, short for rule literals in tests
type P = automation.Params

// trig a trigger of type typ with params p
func trig(typ string, p P) automation.Trigger {
	return automation.Trigger{Type: typ, Params: p}
}

// cond a condition of type typ with params p
func cond(typ string, p P) automation.Condition {
	return automation.Condition{Type: typ, Params: p}
}

// kindIs the kind_is condition (the v1 scope)
func kindIs(kind string) automation.Condition {
	return cond(automation.CondKindIs, P{Kind: kind})
}

// seedRule finished reading in Research Reports moves to a Read subfolder
// created on first use (v2)
func seedRule() automation.Rule {
	return automation.Rule{
		ID: "seed", Enabled: true,
		When: automation.When{Subject: automation.SubjectDocument,
			In:      []automation.Place{{ID: "rr", Kind: automation.PlaceFolder, Name: "Research Reports"}},
			Trigger: trig(TriggerFinishedReading, P{MinPages: 3, SettleHours: 6})},
		Conditions: []automation.Condition{kindIs(ScopePdfEpub)},
		Actions: []automation.Action{{Type: automation.ActionMove,
			To: &automation.Destination{CreateIfMissing: &automation.CreateIfMissing{Name: "Read", Parent: ParentSource}}}},
	}
}

// seedV1 seedRule in the v1 form
func seedV1() V1Rule {
	return V1Rule{
		ID: "seed", Enabled: true,
		Source:      FolderRef{ID: "rr", Name: "Research Reports"},
		Scope:       ScopePdfEpub,
		Trigger:     V1Trigger{Type: TriggerFinishedReading, MinPages: 3, SettleHours: 6},
		Destination: Destination{CreateIfMissing: &CreateIfMissing{Name: "Read", Parent: ParentSource}},
		Action:      V1Action{Type: ActionMove},
	}
}
