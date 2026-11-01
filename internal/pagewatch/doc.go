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
//
// # Two streams
//
// The timeline feed covers every document outside the trash and ignores the
// account's Enabled/Mode and the rules. Its first run only records baselines
// and lastOpened. Afterwards: an "edit" per page whose hash differs from the
// feed baseline, an "open" when lastOpened changed (the last-opened page).
// Feed events are never Watched and carry no rule.
//
// Pipeline events exist only when Settings.Enabled, per enabled pipeline
// rule, for documents under a watched folder (any depth; the root watches
// the whole tree) or watched by id, in the rule's scope, without the
// "no automation" tag. Each rule has its own baselines and members:
//
//   - joining: a doc that is not yet a member gets one "transcribe_only" per
//     page with a hash not yet delivered. It becomes a member when every one
//     of those is accepted and none was skipped.
//     Joining is for content that existed before the rule watched it: a doc
//     already seen when the rule last looked (moved in, watch widened, tag
//     removed, restored from the trash), or any doc at the rule's first
//     activation. A page delivered earlier in the join that changes again
//     is an edit (BeforeHash = what was delivered), never transcribe_only.
//   - new: a doc first seen (State.Seen) after the rule last ran
//     (RuleState.LastActive) is a member at once; its pages are edits with
//     an empty BeforeHash, so tasks are extracted. This covers docs created
//     while the rule or the account was disabled.
//   - member: an "edit" per changed page, BeforeHash = the rule's baseline.
//   - both: a "page_deleted" (no hashes, PageIndex -1) per page the baseline
//     holds that is gone; the page leaves the baseline once that is accepted.
//   - leaving (moved out, trashed, tagged, out of scope, gone): no events;
//     membership and baselines are dropped, so a re-join starts over.
//
// Every pipeline event carries the doc's PageOrder; a pure reorder emits
// nothing.
//
// # Skips (the page or doc keeps its old baseline)
//
//   - recent: the page blob is younger than Input.MinAge (default 10m).
//   - mtime: the blob mtime cannot be read; never treated as old.
//   - content: the doc's .content is unreadable or absent; the whole doc is
//     left alone (no events, baselines and membership untouched).
//   - location: the doc's parent chain is broken; left alone likewise.
//   - pages_not_attached: Snapshot.AttachPages did not run; Detect plans
//     nothing rather than read missing pages as deletions.
//   - trace_id: Input.NewID returned ""; the event is not sent.
//
// Detect never mutates Input.State; Plan.Advance(accepted) returns a new
// State. The delivery loop (service.go) owns I/O, Dry mode and sending.
package pagewatch
