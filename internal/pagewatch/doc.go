// Package pagewatch detects reMarkable page changes and turns them into
// events for the rm-pages service. The one hard requirement is that no note is
// ever missed: a baseline moves only when rm-pages accepted the event that
// carries it, so anything skipped or not delivered is sent again next sweep.
//
// # Files
//
//   - event.go   Event (the JSON rm-pages receives), event kinds, SkipNote
//     and the skip reasons.
//   - state.go   State: the feed and per-rule baselines, persisted between
//     sweeps.
//   - detect.go  Detect, the pure detector: Snapshot + Config + State →
//     Plan{Feed, Pipeline, Skipped, Advance}.
//   - store.go   the per-user files: page-watch.state.json (+ .bak, the
//     previous good state) and page-watch.runs.json.
//
// # State recovery
//
// Each save first copies the current state file to page-watch.state.json.bak
// (only when it parses). A state file that does not parse, or is missing
// while a .bak exists, is replaced by the .bak on load, with an Error; the changes since that save are re-sent under
// the same trace ids, which rm-pages recognises, so nothing is lost or
// doubled. When both are unreadable every run fails with an Error and nothing
// is sent. To recover by hand: stop rmfakecloud, copy a good .bak over the
// state file (or fix the JSON), start again. Deleting both files is a reset:
// the next run is a first run (baselines only, no events), so changes made
// since the last good state are not sent — prefer the .bak.
//
// # Two streams
//
// The timeline feed covers every document outside the trash and ignores the
// account's Enabled/Mode and the rules. Its first run only records baselines
// and lastOpened. Afterwards: an "edit" per page whose hash differs from the
// feed baseline, an "open" when lastOpened changed (the last-opened page).
// Feed events are never Watched and carry no rule.
//
// Pipeline events exist only when Settings.Enabled, per enabled page rule
// (automation.json, When.Subject "page", with an extract_tasks or save_notes
// action), for documents in one of its places (When.In: a folder at any
// depth, the root folder for the whole tree, or a document by id), without
// the "no automation" tag, that pass its document conditions
// (autoorganize.DocConditionsMatch: kind_is, tag_is, tag_is_not,
// name_matches). A condition that cannot be evaluated (an unknown or
// document-only condition, a bad pattern) leaves the doc alone for that rule:
// a "condition" skip, no events and no leave. Each rule has its own
// baselines and members:
//
//   - joining: a doc that is not yet a member gets one "transcribe_only" per
//     page with a hash not yet delivered whose blob mtime is no later than
//     the join's start (RuleState.JoinStartedAt: the rule's LastActive when
//     the join is first seen, the last sweep at which the doc was outside
//     the rule; that sweep's Now at the rule's first activation). A page
//     written after that is new writing: an "edit"
//     with an empty BeforeHash, so its tasks are extracted even while an old
//     page (recent, unreadable mtime) holds the join open. It becomes a
//     member when every event is accepted and none was skipped; the join
//     start is dropped then, or when the doc leaves.
//     Joining is for content that existed before the rule watched it: a doc
//     already seen when the rule last looked (moved in, watch widened, tag
//     removed, restored from the trash), or any doc at the rule's first
//     activation. A page delivered earlier in the join that changes again
//     is an edit (BeforeHash = what was delivered), never transcribe_only.
//   - new: a doc first seen (State.Seen) after the rule last ran
//     (RuleState.LastActive) is a member at once; its pages are edits with
//     an empty BeforeHash, so tasks are extracted. This covers docs created
//     while the rule or the account was disabled. Seen is recorded for
//     trashed docs always, for content/location-skipped docs only on the
//     very first run (otherwise from their first processable sweep).
//   - member: an "edit" per changed page, BeforeHash = the rule's baseline.
//   - both: a "page_deleted" (no hashes, PageIndex -1) per page the baseline
//     holds that is gone; the page leaves the baseline once that is accepted.
//   - leaving (moved out, trashed, tagged, out of scope, gone): no events;
//     membership and baselines are dropped, so a re-join starts over.
//
// A rule with page tags (detect_tagged.go; P4: the page_tagged trigger's tag
// plus every page_tag_is condition, Rule.PageTags) covers the same documents
// (page_tagged with no places = the whole tree) but only their pages that
// carry every tag (.content pageTags, NormalizePageTag); a page's tag time is
// the latest of them. A blank tag set counts no page:
//
//   - newly tagged (no baseline): one "edit" with an empty BeforeHash (the
//     whole page) when the rule had the doc at an earlier sweep
//     (RuleState.TagWatched) or the doc is new since the rule last looked.
//     Otherwise the tag pre-existed the rule's look at the doc (first
//     activation, moved in, watch widened): "transcribe_only", unless the
//     blob or the tag's timestamp is newer than that look (RuleState.TagJoinAt,
//     pinned at first sight so a re-send keeps its kind).
//   - tagged with a baseline: an "edit" per change against the baseline.
//   - untagged: no events; its baseline and pin are dropped.
//   - deleted while delivered: "page_deleted", as above.
//
// RuleState.Trigger records what a rule's state was built for (stateTrigger:
// "" = page_changed with no page tags, else "page_tagged:" and the sorted
// tags joined by 0x1f, so one tag is the v1 "page_tagged:<tag>" and
// "page_changed AND page_tag_is X" shares the key of "page_tagged X"); a rule
// whose trigger type or tags changed starts afresh, as at its first
// activation.
//
// Every pipeline event carries the doc's PageOrder; a pure reorder emits
// nothing. The prompt and model come from the rule's page action; a
// save_notes action puts "tasks": false on each of its events (rm-pages saves
// the note and extracts no tasks). extract_tasks events have no "tasks" key.
//
// # Skips (the page or doc keeps its old baseline)
//
//   - recent: the page blob is younger than Input.MinAge (default 10m).
//   - mtime: the blob mtime cannot be read; never treated as old.
//   - content: the doc's .content is unreadable or absent; the whole doc is
//     left alone (no events, baselines and membership untouched).
//   - location: the doc's parent chain is broken; left alone likewise.
//   - condition: one rule cannot evaluate a condition for the doc; that rule
//     leaves it alone this sweep (other rules and the feed are unaffected).
//   - pages_not_attached: Snapshot.AttachPages did not run; Detect plans
//     nothing rather than read missing pages as deletions.
//
// The service logs each skip at Info (doc_id, page_id, reason, rule_id when
// a rule saw it) and one Error (trace_id "skip", stage detect) when the same
// doc/page is skipped more than LagRuns runs in a row; the streak resets when
// the skip stops. Recent skips never alert: continuous writing is benign.
//
// # Guards
//
//   - A place whose folder or doc id is gone from the tree, or whose
//     parent chain ends in the trash (InTrash), is listed in
//     Plan.MissingWatches; the service logs one Error per (rule, id) streak
//     and the Pipeline panel flags the rule.
//   - A config that cannot be loaded (a corrupt automation.json, or an
//     auto-organize.json that cannot be migrated) fails the run before the
//     state is read: nothing is sent or saved (P6, fail closed).
//   - A snapshot with no documents while the state knows more than
//     EmptyTreeGuard is a broken tree read: Plan.Abort is set, the run sends
//     and saves nothing and logs an Error (pruning would drop every baseline).
//
// Trace ids are deterministic (TraceID): the same change gets the same id on
// every sweep until it is accepted.
//
// Detect never mutates Input.State; Plan.Advance(accepted) returns a new
// State. The delivery loop (service.go) owns I/O, Dry mode and sending.
package pagewatch
