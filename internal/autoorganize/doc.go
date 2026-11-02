// Package autoorganize moves documents between folders on the server according
// to per-user rules: source folder -> scope -> trigger -> action. One sweeper
// loop per sync15 user evaluates the rules against a snapshot of the tree, in
// dry (log only) or live mode, on a timer, after a tablet sync, or on request.
//
// # Files
//
//   - config.go    the rule model (Config, Rule, Trigger, Destination), JSON
//     defaults, Normalize/Validate and the loop (cycle)
//     warnings. Name patterns and tag normalisation are thin
//     wrappers over internal/automation.
//   - registry.go  the evaluator tables (evaluators, conditionEvaluators),
//     the v1 rule's implicit conditions (ruleConditions) and the
//     Trigger <-> automation.Params bridge. Parameters, defaults,
//     validation and the page-based flag live in the
//     internal/automation catalogue.
//   - triggers.go  the evaluators (one function per trigger or condition),
//     mergeEval/evalRule, the common document guards (checkDoc,
//     evalKind) and the skip reasons.
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
//  1. Add its type constant and catalogue entry (Subjects, Fields, PageBased,
//     Defaults, Validate) to internal/automation/catalogue.go, and alias the
//     constant in config.go. New parameters are fields on automation.Params
//     and on Trigger, mapped in Trigger.params/setParams (absent-means-default
//     handling lives in Trigger.UnmarshalJSON only for values where zero is
//     meaningful).
//  2. Write its evaluator in triggers.go: an evalFunc, func(p
//     *automation.Params, d *DocSnap, ci *ContentInfo, fs *FirstSeen, now
//     time.Time) evaluation. Set ev.inputs to the values a hold should be
//     keyed on, ev.detail to the sentence shown in Preview and the log, and
//     ev.skip (never a guess) when the document cannot be evaluated.
//  3. Register it in registry.go (evaluators; conditionEvaluators for a
//     condition). Normalize, Validate and PageBased read the catalogue and
//     evalRule the tables, so nothing else in the package needs to change.
//  4. Mirror it in the UI vocabulary: ui/src/pages/AutoOrganize/rules.js
//     (TRIGGERS, TRIGGER_DEFAULTS, triggerPhrase) and the parameter inputs in
//     RuleDrawer.jsx.
//  5. Cover it in config_test.go (defaults + validation) and plan_test.go or
//     service_test.go (matches / does not match / skip).
//
// # Adding an action
//
// There are two actions. "move" is run by the sweeper (service.go) over the
// plan from BuildPlan; "pipeline" is run by internal/pagewatch and is
// invisible to the planner. Where an action lives:
//
//   - config.go: the Action* constant, any Action/Rule fields (pipeline adds
//     Action.Prompt, Action.Model, Action.Tasks and Rule.Watch), the Normalize
//     defaults and the per-action branch at the top of Rule.Validate. Move
//     validates source, destination and trigger; pipeline (validatePipeline)
//     requires a page trigger — page_changed with at least one Watch entry, or
//     page_tagged with a tag (no Watch entry = the whole tree) — valid,
//     non-trash Watch entries, and uses neither Source nor Destination.
//   - catalogue: a trigger whose only subject is "page" belongs to the
//     pipeline action, so move rules reject it (page_changed and page_tagged).
//     Its evaluator must still exist; both use evalNotAMoveTrigger, a skip.
//   - plan.go: BuildPlan and LoopWarnings (config.go) consider move rules only.
//     A new action that must not claim documents is skipped at the top of the
//     BuildPlan loop (as IsPipeline rules are); rule numbers keep the config
//     position.
//   - move: PlannedMove (plan.go), applyMove and recordLiveMove (service.go),
//     Undo and Reapply, and moveDoc (move.go). The holds/cycle guard are
//     move-specific. A further action adds its own plan entry type and a
//     sibling of applyMove switched on Action.Type, with its own undo
//     semantics.
//   - pipeline: DefaultPipelineModel and DefaultPipelinePrompt are the values
//     Normalize fills for an empty model/prompt; the page watch reads
//     Rule.Watch, Action.Prompt/Model and Enabled from the saved config.
//
// # Adding a sweep trigger
//
// Sweeps start from the per-user loop in service.go: the interval timer, a
// debounced kick after a root write (Service.Trigger, wired from the storage
// app's OnRootUpdated and the sync-complete handlers), and RunNow from the
// UI. A new trigger sends its name on ul.kick; names are the Trigger*
// constants and show in the Runs tab.
package autoorganize
