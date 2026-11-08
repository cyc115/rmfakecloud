package archive

import (
	"encoding/json"

	"github.com/juruen/rmapi/encoding/rm"
)

// Set the default pagedata template to Blank
const defaultPagadata string = "Blank"

// Zip represents an entire Remarkable archive file.
type Zip struct {
	Content Content
	Pages   []Page
	Payload []byte
	UUID    string
	pageMap map[string]int
}

// NewZip creates a File with sane defaults.
func NewZip() *Zip {
	content := Content{
		DummyDocument: false,
		ExtraMetadata: ExtraMetadata{
			LastBrushColor:           "Black",
			LastBrushThicknessScale:  "2",
			LastColor:                "Black",
			LastEraserThicknessScale: "2",
			LastEraserTool:           "Eraser",
			LastPen:                  "Ballpoint",
			LastPenColor:             "Black",
			LastPenThicknessScale:    "2",
			LastPencil:               "SharpPencil",
			LastPencilColor:          "Black",
			LastPencilThicknessScale: "2",
			LastTool:                 "SharpPencil",
			ThicknessScale:           "2",
			LastFinelinerv2Size:      "1",
		},
		FileType:       "",
		FontName:       "",
		LastOpenedPage: 0,
		LineHeight:     -1,
		Margins:        100,
		Orientation:    "portrait",
		PageCount:      0,
		Pages:          []string{},
		TextScale:      1,
		Transform: Transform{
			M11: 1,
			M12: 0,
			M13: 0,
			M21: 0,
			M22: 1,
			M23: 0,
			M31: 0,
			M32: 0,
			M33: 1,
		},
	}

	return &Zip{
		Content: content,
	}
}

// A Page represents a note page.
type Page struct {
	// Data is the rm binary encoded file representing the drawn content
	Data *rm.Rm
	// Metadata is a json file containing information about layers
	Metadata Metadata
	// Thumbnail is a small image of the overall page
	Thumbnail []byte
	// Pagedata contains the name of the selected background template
	Pagedata string
	// page number of the underlying document
	DocPage int
}

// Metadata represents the structure of a .metadata json file associated to a page.
type Metadata struct {
	Layers []Layer `json:"layers"`
}

// Layers is a struct contained into a Metadata struct.
type Layer struct {
	Name string `json:"name"`
}

// Content represents the structure of a .content json file.
type Content struct {
	DummyDocument bool          `json:"dummyDocument"`
	ExtraMetadata ExtraMetadata `json:"extraMetadata"`

	// FileType is "pdf", "epub" or empty for a simple note
	FileType       string `json:"fileType"`
	FontName       string `json:"fontName"`
	LastOpenedPage int    `json:"lastOpenedPage"`
	LineHeight     int    `json:"lineHeight"`
	Margins        int    `json:"margins"`
	// Orientation can take "portrait" or "landscape".
	Orientation string `json:"orientation"`
	PageCount   int    `json:"pageCount"`
	// Pages is a list of page IDs
	Pages []string `json:"pages"`
	// Tags is the list of page tag names. Firmware 3.x and auto-organize
	// write pageTags as objects ({name, pageId, timestamp}); older
	// firmware wrote plain strings. UnmarshalJSON accepts both and keeps
	// only the names; see also internal/ui/doctags.go rawTags.
	Tags []string `json:"pageTags"`
	// CPages is the firmware 3.x (formatVersion 2) page list. Modern
	// notebooks carry no flat "pages" key, so readers must fall back to
	// CPages.Pages when Pages is empty.
	CPages         CPages  `json:"cPages,omitempty"`
	RedirectionMap []int   `json:"redirectionPageMap"`
	TextScale      float64 `json:"textScale"`

	Transform Transform `json:"transform"`
}

// CPages the firmware 3.x page list of a .content blob.
type CPages struct {
	Pages []CPage `json:"pages"`
}

// CPage one entry of a .content cPages.pages list.
type CPage struct {
	ID  string   `json:"id"`
	Idx CPageIdx `json:"idx"`
	// Redir the PDF page this page shows; nil when the page has no
	// redirection (an inserted page, -1 in the snapshot package's terms).
	Redir *CPageRedir `json:"redir,omitempty"`
	// Deleted the firmware's "deleted" register. The tablet writes it as
	// a timestamped register ({"timestamp", "value": 1}); a restored page
	// can keep the key with value 0.
	Deleted DeletedFlag `json:"deleted,omitempty"`
}

// CPageIdx the idx register of a cPages entry; Value orders the pages.
type CPageIdx struct {
	Timestamp string `json:"timestamp,omitempty"`
	Value     string `json:"value"`
}

// CPageRedir the redir register of a cPages entry; Value is the PDF page
// index the page shows.
type CPageRedir struct {
	Timestamp string `json:"timestamp,omitempty"`
	Value     int    `json:"value"`
}

// DeletedFlag whether a cPages "deleted" register is set: absent or value 0
// means the page is present. Accepts the shapes the tablet writes (a bool,
// a number, a string, or a {"value": ...} register object); anything else
// counts as present, mirroring autoorganize.deletedFlag.
//
// Note: marshals back as a plain bool, not the tablet's register object.
// Nothing in this repo writes a tablet .content blob through this struct
// (archive.Zip.Write has no callers; .content writes go through raw maps
// in internal/autoorganize), so the simplified form never reaches a tablet.
type DeletedFlag bool

// UnmarshalJSON decodes a "deleted" register in any shape the tablet writes.
func (d *DeletedFlag) UnmarshalJSON(b []byte) error {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*d = DeletedFlag(deletedFlagValue(v))
	return nil
}

// MarshalJSON writes the flag as a plain bool.
func (d DeletedFlag) MarshalJSON() ([]byte, error) {
	return json.Marshal(bool(d))
}

func deletedFlagValue(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case float64:
		return t != 0
	case string:
		return t == "1" || t == "true"
	case map[string]any:
		return deletedFlagValue(t["value"])
	}
	return false
}

// UnmarshalJSON decodes a cPages entry leniently: junk in one entry (a
// non-object item, a mistyped idx/redir/deleted) must not fail the whole
// .content blob. Unreadable entries decode to a zero CPage, which callers
// skip by the empty ID.
func (p *CPage) UnmarshalJSON(b []byte) error {
	var raw struct {
		ID      string          `json:"id"`
		Idx     json.RawMessage `json:"idx"`
		Redir   json.RawMessage `json:"redir"`
		Deleted json.RawMessage `json:"deleted"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil
	}
	p.ID = raw.ID
	if len(raw.Idx) > 0 {
		var idx CPageIdx
		if err := json.Unmarshal(raw.Idx, &idx); err == nil {
			p.Idx = idx
		}
	}
	if len(raw.Redir) > 0 {
		var redir CPageRedir
		if err := json.Unmarshal(raw.Redir, &redir); err == nil {
			p.Redir = &redir
		}
	}
	if len(raw.Deleted) > 0 {
		var deleted DeletedFlag
		if err := json.Unmarshal(raw.Deleted, &deleted); err == nil {
			p.Deleted = deleted
		}
	}
	return nil
}

// UnmarshalJSON decodes a .content blob, accepting pageTags as plain strings
// (old firmware) or as {name, pageId, timestamp} objects (firmware 3.x and
// auto-organize), and cPages in whatever shape it arrives in. A malformed
// pageTags entry is skipped, like rawTags; a malformed cPages list is
// ignored rather than failing the whole blob.
func (c *Content) UnmarshalJSON(b []byte) error {
	type contentAlias Content
	var raw struct {
		contentAlias
		Tags   []json.RawMessage `json:"pageTags"`
		CPages json.RawMessage   `json:"cPages"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*c = Content(raw.contentAlias)
	c.Tags = []string{}
	for _, t := range raw.Tags {
		var name string
		if json.Unmarshal(t, &name) == nil {
			if name != "" {
				c.Tags = append(c.Tags, name)
			}
			continue
		}
		var obj struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(t, &obj) == nil && obj.Name != "" {
			c.Tags = append(c.Tags, obj.Name)
		}
	}
	if len(raw.CPages) > 0 {
		var cp struct {
			Pages []json.RawMessage `json:"pages"`
		}
		if err := json.Unmarshal(raw.CPages, &cp); err == nil {
			for _, item := range cp.Pages {
				var pg CPage
				if json.Unmarshal(item, &pg) == nil && pg.ID != "" {
					c.CPages.Pages = append(c.CPages.Pages, pg)
				}
			}
		}
	}
	return nil
}

// ExtraMetadata is a struct contained into a Content struct.
type ExtraMetadata struct {
	LastBrushColor           string `json:"LastBrushColor"`
	LastBrushThicknessScale  string `json:"LastBrushThicknessScale"`
	LastColor                string `json:"LastColor"`
	LastEraserThicknessScale string `json:"LastEraserThicknessScale"`
	LastEraserTool           string `json:"LastEraserTool"`
	LastPen                  string `json:"LastPen"`
	LastPenColor             string `json:"LastPenColor"`
	LastPenThicknessScale    string `json:"LastPenThicknessScale"`
	LastPencil               string `json:"LastPencil"`
	LastPencilColor          string `json:"LastPencilColor"`
	LastPencilThicknessScale string `json:"LastPencilThicknessScale"`
	LastTool                 string `json:"LastTool"`
	ThicknessScale           string `json:"ThicknessScale"`
	LastFinelinerv2Size      string `json:"LastFinelinerv2Size"`
}

// Transform is a struct contained into a Content struct.
type Transform struct {
	M11 float32 `json:"m11"`
	M12 float32 `json:"m12"`
	M13 float32 `json:"m13"`
	M21 float32 `json:"m21"`
	M22 float32 `json:"m22"`
	M23 float32 `json:"m23"`
	M31 float32 `json:"m31"`
	M32 float32 `json:"m32"`
	M33 float32 `json:"m33"`
}

// MetadataFile content
type MetadataFile struct {
	DocName        string `json:"visibleName"`
	CollectionType string `json:"type"`
	Parent         string `json:"parent"`
	//LastModified in milliseconds
	LastModified     string `json:"lastModified"`
	LastOpened       string `json:"lastOpened"`
	LastOpenedPage   int    `json:"lastOpenedPage"`
	Version          int    `json:"version"`
	Pinned           bool   `json:"pinned"`
	Synced           bool   `json:"synced"`
	Modified         bool   `json:"modified"`
	Deleted          bool   `json:"deleted"`
	MetadataModified bool   `json:"metadatamodified"`
}
