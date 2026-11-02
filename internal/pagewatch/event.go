package pagewatch

import "time"

// Event kinds
const (
	// KindEdit a page whose blob hash changed since the baseline
	KindEdit = "edit"
	// KindOpen the document's lastOpened changed (feed only)
	KindOpen = "open"
	// KindTranscribeOnly a page of a document that joined a pipeline rule:
	// it goes into the vault note, no task extraction, no timeline entry
	KindTranscribeOnly = "transcribe_only"
	// KindPageDeleted a page a pipeline rule had delivered is gone (no hashes)
	KindPageDeleted = "page_deleted"
)

// Doc types, from the .content fileType
const (
	DocNotebook = "notebook"
	DocPDF      = "pdf"
	DocEPUB     = "epub"
)

// Event one change sent to rm-pages. Feed events (edit, open) have no rule;
// pipeline events (edit, transcribe_only, page_deleted) carry the rule's id,
// prompt and model, Watched = true, and the doc's page order.
type Event struct {
	TraceID string `json:"trace_id"`
	Kind    string `json:"kind"`
	// PageOrder the doc's page ids in order, on every pipeline event
	PageOrder []string `json:"page_order,omitempty"`
	User      string   `json:"user"`
	DocID     string   `json:"doc_id"`
	DocName   string   `json:"doc_name"`
	DocPath   string   `json:"doc_path"`
	DocType   string   `json:"doc_type"`
	PageID    string   `json:"page_id,omitempty"`
	// PageIndex position in page order, from 0; -1 on page_deleted (the page is gone)
	PageIndex int `json:"page_index"`
	// BeforeHash the baseline; empty for a new page and on transcribe_only
	BeforeHash string `json:"before_hash,omitempty"`
	AfterHash  string `json:"after_hash,omitempty"`
	PDFHash    string `json:"pdf_hash,omitempty"`
	// ChangedAt the page blob's write time; the open time for opens; the
	// detection time for page_deleted
	ChangedAt time.Time  `json:"changed_at"`
	OpenedAt  *time.Time `json:"opened_at,omitempty"`
	// Watched the event belongs to a pipeline rule (rm-pages runs the LLM)
	Watched bool   `json:"watched"`
	RuleID  string `json:"rule_id,omitempty"`
	Prompt  string `json:"prompt,omitempty"`
	Model   string `json:"model,omitempty"`
	// Tasks false on every pipeline event of a "just save notes" rule
	// (Action.Tasks false): rm-pages saves the note and extracts no tasks.
	// Absent otherwise, so other payloads are unchanged.
	Tasks *bool `json:"tasks,omitempty"`
}

// Skip reasons
const (
	// SkipRecent the page blob is younger than Input.MinAge
	SkipRecent = "recent"
	// SkipMtime the blob mtime could not be read; never treated as old
	SkipMtime = "mtime"
	// SkipContent the doc's .content is unreadable or absent: the whole doc
	// is left alone this sweep (no events, baselines untouched)
	SkipContent = "content"
	// SkipLocation the doc's parent chain is broken: left alone this sweep
	SkipLocation = "location"
	// SkipNotAttached the snapshot has no page lists: nothing is detected
	SkipNotAttached = "pages_not_attached"
)

// SkipNote a page (or a doc, PageID empty) left for a later sweep
type SkipNote struct {
	DocID  string `json:"doc_id,omitempty"`
	PageID string `json:"page_id,omitempty"`
	Reason string `json:"reason"`
}
