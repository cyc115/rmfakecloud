// Package autoorganize moves and tags documents on the server according to
// the document rules of automation.json: folder -> trigger + conditions ->
// action (move, add_tag, remove_tag). One sweeper loop per sync15 user evaluates the
// rules against a snapshot of the tree, in dry (log only) or live mode, on a
// timer, after a tablet sync, or on request. Page rules (When.Subject "page")
// belong to internal/pagewatch; the sweeper skips them, keeping their config
// position in rule numbers.
//
// # Files
//
//   - config.go    the v1 rule model (V1Config, V1Rule, V1Trigger, V1Action of
//     auto-organize.json), its JSON defaults and Normalize/Validate. Kept for
//     the migration on load and for rollback; the engines never run it.
//     Settings, FolderRef, CreateIfMissing and Destination are the shared
//     automation types.
//   - migrate.go   MigrateV1 (v1 -> v2, on load) and ToV1 (rollback).
//   - registry.go  the evaluator tables (evaluators, conditionEvaluators) and
//     the V1Trigger <-> automation.Params bridge. Parameters, defaults,
//     validation and the page-based flag live in the internal/automation
//     catalogue; the v2 rule model and its accessors in automation/config.go.
//   - triggers.go  the evaluators (one function per trigger or condition),
//     mergeEval/evalRule, the common document guards (checkDoc, evalKind),
//     DocConditionsMatch (the page watch's document conditions) and the skip
//     reasons.
//   - snapshot.go  Snapshot/DocSnap, an immutable copy of the tree; folder
//     paths and locations; ContentInfo read from .content blobs and the
//     per-blob ContentCache.
//   - plan.go      BuildPlan, the pure planner: document rules top to bottom,
//     the first move that matches claims a document (tag rules claim
//     nothing); destination resolution; LoopWarnings.
//   - move.go      moveDoc, the move write path: re-checks inside the
//     generation-conflict retry, patches only parent/version.
//   - tag.go       tagDoc, the tag write path: the same retry and re-check,
//     patches only the .content "tags" list and the metadata version.
//   - state.go     the sweeper-owned state: holds, cycle-guard history,
//     first-seen, created folders, moves/runs logs.
//   - store.go     the per-user files and their atomic writes: automation.json
//     (the only config the server writes; migrated from auto-organize.json on
//     the first load, under the user's config lock) and the state.
//   - service.go   the Service: loops, sweeps, Preview/View/Log, Undo and
//     Reapply. The HTTP layer is internal/ui/autoorganize.go.
//
// # Evaluation
//
// A document rule has one folder (When.In[0], Rule.Source) and covers the
// documents directly inside it. checkDoc runs the guards (trash, unreadable,
// no automation, pinned unless the trigger or a condition is pinned_is,
// already in the destination, kind_is), then evalRule: the trigger, then the
// document conditions in stored order (Rule.DocConditions). Every one is
// evaluated, and the hold inputs are the union of all (ruling a′), so a hold
// set while the trigger does not match keeps the condition inputs. The
// outcome (match, skip, detail joined by " · ") stops at the first skip or
// non-match, so a later skip never outranks an earlier non-match.
//
// # Adding a trigger
//
//  1. Add its type constant and catalogue entry (Subjects, Fields, PageBased,
//     Defaults, Validate) to internal/automation/catalogue.go, and alias the
//     constant in config.go. A new parameter is a field on automation.Params
//     (Spec.Fields names the ones the type stores).
//  2. Write its evaluator in triggers.go: an evalFunc, func(p
//     *automation.Params, d *DocSnap, ci *ContentInfo, fs *FirstSeen, now
//     time.Time) evaluation. Set ev.inputs to the values a hold should be
//     keyed on, ev.detail to the sentence shown in Preview and the log, and
//     ev.skip (never a guess) when the document cannot be evaluated.
//  3. Register it in registry.go (evaluators). Normalize, Validate and
//     PageBased read the catalogue and evalRule the table, so nothing else in
//     the package changes. A page trigger (subject "page" only) is the page
//     watch's (pagewatch.Detect); its evaluator here is evalNotAMoveTrigger,
//     a skip.
//  4. Mirror it in the UI vocabulary (ui/src/pages/Automation).
//  5. Cover it in internal/automation (defaults + validation) and plan_test.go
//     or service_test.go (matches / does not match / skip). A v1 trigger also
//     needs its mapping in migrate.go and migrate_test.go.
//
// # Adding a condition
//
//  1. Add its catalogue entry to automation.Conditions (Subjects, Fields,
//     Repeatable, Defaults, Validate). A condition named like a trigger
//     shares its parameters and evaluator.
//  2. A document condition: an evalFunc in triggers.go, registered in
//     conditionEvaluators. evalRule then runs it after the trigger, in the
//     rule's order; its inputs join the hold key, so adding it to a rule
//     clears that rule's holds once (the inputs differ).
//  3. A condition with both subjects also runs in the page watch through
//     DocConditionsMatch (it must not need FirstSeen there: it gets nil). A
//     page-only condition is read by the page watch itself, as page_tag_is
//     is through Rule.PageTags.
//  4. Mirror it in the UI and cover it in plan_test.go and, for page rules,
//     internal/pagewatch/conditions_test.go.
//
// # Adding an action
//
// There are five actions. "move", "add_tag" and "remove_tag" are run by the
// sweeper (service.go) over the plan from BuildPlan; "extract_tasks" and
// "save_notes" are run by internal/pagewatch and are invisible to the
// planner. Where an action lives:
//
//   - internal/automation: the Action* constant, any Action fields (To for
//     move; Prompt and Model for the page actions), the Normalize defaults,
//     the accessor (Rule.Move, Rule.TagAction, Rule.PageAction) and
//     validateActions. A rule has exactly one action for now (R-B2).
//   - plan.go: BuildPlan and LoopWarnings consider document rules with a
//     move only; rule numbers keep the config position.
//   - move: PlannedMove (plan.go), applyMove and recordLiveMove (service.go),
//     Undo and Reapply, and moveDoc (move.go). The holds/cycle guard are
//     move-specific.
//   - tags: a PlannedMove and MoveEntry with Action/Tag set and To == From,
//     so Preview, the log and Runs show them without a second entry type;
//     applyTag (service.go) and tagDoc (tag.go) write them. They set no hold
//     and no history, cannot be undone (Undo refuses, Log gives no canUndo),
//     and newestLiveMove skips them. A further document action follows the
//     same shape.
//   - page actions: pagewatch.pipelineEvent reads the prompt and model from
//     Rule.PageAction; save_notes sets the event's tasks false.
//   - migrate.go: ToV1 must refuse (RuleIssue) an action v1 cannot express.
//
// # Adding a sweep trigger
//
// Sweeps start from the per-user loop in service.go: the interval timer, a
// debounced kick after a root write (Service.Trigger, wired from the storage
// app's OnRootUpdated and the sync-complete handlers), and RunNow from the
// UI. A new trigger sends its name on ul.kick; names are the Trigger*
// constants and show in the Runs tab.
package autoorganize
