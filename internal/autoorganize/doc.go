// Package autoorganize moves documents between folders on the server according
// to per-user rules: source folder -> scope -> trigger -> action. One sweeper
// loop per sync15 user evaluates the rules against a snapshot of the tree, in
// dry (log only) or live mode, on a timer, after a tablet sync, or on request.
//
// # Files
//
//   - config.go    the rule model (Config, Rule, Trigger, Destination), JSON
//     defaults, Normalize/Validate, name patterns, tag
//     normalisation and the loop (cycle) warnings.
//   - registry.go  the trigger table: per type its defaults, validation,
//     page-based flag and evaluator. Everything type-specific
//     lives here or in the evaluator it points at.
//   - triggers.go  the evaluators (one function per trigger), the common
//     document guards (checkDoc) and the skip reasons.
//   - snapshot.go  Snapshot/DocSnap, an immutable copy of the tree; folder
//     paths and locations; ContentInfo read from .content blobs
//     and the per-blob ContentCache.
//   - plan.go      BuildPlan, the pure planner: rules top to bottom, first
//     match claims a document; destination resolution.
//   - move.go      moveDoc, the one write path: re-checks inside the
//     generation-conflict retry, patches only parent/version.
//   - state.go     the sweeper-owned state: holds, cycle-guard history,
//     first-seen, created folders, moves/runs logs.
//   - store.go     the two per-user JSON files and their atomic writes.
//   - service.go   the Service: loops, sweeps, Preview/View/Log, Undo and
//     Reapply. The HTTP layer is internal/ui/autoorganize.go.
//
// # Adding a trigger
//
//  1. Add its type constant to config.go and, if it needs new parameters,
//     fields on Trigger (with json tags; absent-means-default handling lives
//     in Trigger.UnmarshalJSON only for values where zero is meaningful).
//  2. Write its evaluator in triggers.go: func(t *Trigger, d *DocSnap,
//     ci *ContentInfo, fs *FirstSeen, now time.Time) evaluation. Set
//     ev.inputs to the values a hold should be keyed on, ev.detail to the
//     sentence shown in Preview and the log, and ev.skip (never a guess) when
//     the document cannot be evaluated.
//  3. Register it in registry.go: Defaults, Validate, PageBased, Eval. That
//     entry is what Normalize, Validate, PageBased and evalTrigger use, so
//     nothing else in the package needs to change.
//  4. Mirror it in the UI vocabulary: ui/src/pages/AutoOrganize/rules.js
//     (TRIGGERS, TRIGGER_DEFAULTS, triggerPhrase) and the parameter inputs in
//     RuleDrawer.jsx.
//  5. Cover it in config_test.go (defaults + validation) and plan_test.go or
//     service_test.go (matches / does not match / skip).
//
// # Adding an action
//
// Today the only action is "move". Its pieces are: Action.Type validation in
// Rule.Validate (config.go); the planner's PlannedMove (plan.go); the
// sweeper's applyMove and the shared recordLiveMove (service.go); the
// explicit variants Undo and Reapply (service.go); and the move closure
// itself (move.go). A second action would add a plan entry type next to
// PlannedMove, a sibling of applyMove switched on Action.Type, and its own
// undo semantics; the holds/cycle guard are move-specific and would not apply.
//
// # Adding a sweep trigger
//
// Sweeps start from the per-user loop in service.go: the interval timer, a
// debounced kick after a root write (Service.Trigger, wired from the storage
// app's OnRootUpdated and the sync-complete handlers), and RunNow from the
// UI. A new trigger sends its name on ul.kick; names are the Trigger*
// constants and show in the Runs tab.
package autoorganize
