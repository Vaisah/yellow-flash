// Package download contains the download domain model and its state machine.
package download

import (
	"net/http"
	"strings"
	"time"
)

// UnknownSize marks a resource whose length is not known.
const UnknownSize int64 = -1

// Download is a single file transfer. Byte counts and offsets are int64, so
// files larger than 4 GiB are represented safely on every platform.
type Download struct {
	ID               string `json:"id"`
	URL              string `json:"url"`
	FinalURL         string `json:"finalUrl"`
	Filename         string `json:"filename"`
	FilenameExplicit bool   `json:"filenameExplicit"`
	Destination      string `json:"destination"`
	TempPath         string `json:"tempPath"`
	MIMEType         string `json:"mimeType"`
	ETag             string `json:"etag"`
	LastModified     string `json:"lastModified"`
	Error            string `json:"error"`
	Size             int64  `json:"size"`
	Downloaded       int64  `json:"downloaded"`
	RangeSupported   bool   `json:"rangeSupported"`
	Connections      int    `json:"connections"`
	Retries          int    `json:"retries"`
	Status           Status `json:"status"`
	QueueID          string `json:"queueId,omitempty"`
	// SpeedLimit caps this download in bytes per second; 0 is unlimited.
	SpeedLimit int64 `json:"speedLimit"`
	// ExpectedChecksum is a hash the user gave ("sha256:<hex>"); it takes
	// precedence over one the server announces.
	ExpectedChecksum string `json:"expectedChecksum,omitempty"`
	// Restricted: the address came from a web page (the browser), so it may
	// not reach loopback, private or link-local addresses, through redirects
	// and resumes too.
	Restricted  bool              `json:"restricted,omitempty"`
	Headers     map[string]string `json:"-"`
	CreatedAt   time.Time         `json:"createdAt"`
	StartedAt   time.Time         `json:"startedAt"`
	CompletedAt time.Time         `json:"completedAt"`
	UpdatedAt   time.Time         `json:"updatedAt"`
}

// Clone returns a deep copy that shares no mutable state.
func (d *Download) Clone() *Download {
	c := *d
	if d.Headers != nil {
		c.Headers = make(map[string]string, len(d.Headers))
		for k, v := range d.Headers {
			c.Headers[k] = v
		}
	}
	return &c
}

// SegmentStatus is the state of one byte range.
type SegmentStatus string

// Segment states. SegmentCompleted matches the value stored by schema v1.
const (
	SegmentPending   SegmentStatus = "PENDING"
	SegmentCompleted SegmentStatus = "COMPLETED"
	SegmentFailed    SegmentStatus = "FAILED"
)

// Segment is an inclusive byte range [Start, End] of a download. Downloaded is
// the number of contiguous bytes from Start that are durably written.
type Segment struct {
	ID         string        `json:"id"`
	DownloadID string        `json:"downloadId"`
	Index      int           `json:"index"`
	Start      int64         `json:"start"`
	End        int64         `json:"end"`
	Downloaded int64         `json:"downloaded"`
	Status     SegmentStatus `json:"status"`
	Retries    int           `json:"retries"`
}

// Length is the number of bytes covered by the segment.
func (s Segment) Length() int64 { return s.End - s.Start + 1 }

// Remaining is the number of bytes still to fetch.
func (s Segment) Remaining() int64 { return s.Length() - s.Downloaded }

// CreateRequest describes a new download. Nothing in it is trusted.
type CreateRequest struct {
	URL         string            `json:"url"`
	Directory   string            `json:"directory"`
	Filename    string            `json:"filename"`
	Connections int               `json:"connections"`
	Headers     map[string]string `json:"-"`
	QueueID     string            `json:"queueId"`
	// SpeedLimit caps the download in bytes per second; 0 is unlimited.
	SpeedLimit int64 `json:"speedLimit"`
	// Checksum is an expected hash in any form ParseChecksum accepts.
	Checksum string `json:"checksum"`
	// Restricted marks an address from a web page (Download.Restricted).
	// Only the app sets it, never a request from the UI.
	Restricted bool `json:"-"`
}

// sensitiveHeaders are credentials. They are never persisted in plaintext,
// logged, or included in snapshots.
var sensitiveHeaders = map[string]bool{
	"Authorization":       true,
	"Proxy-Authorization": true,
	"Cookie":              true,
}

// IsSensitiveHeader reports whether a header carries credentials.
func IsSensitiveHeader(name string) bool {
	return sensitiveHeaders[http.CanonicalHeaderKey(strings.TrimSpace(name))]
}

// RemoteMetadata is what the server reports about a resource.
type RemoteMetadata struct {
	URL            string `json:"url"`
	Size           int64  `json:"size"`
	MIMEType       string `json:"mimeType"`
	Filename       string `json:"filename"`
	ETag           string `json:"etag"`
	LastModified   string `json:"lastModified"`
	RangeSupported bool   `json:"rangeSupported"`
	// Checksum is the strongest whole-file hash the server announced.
	Checksum Checksum `json:"checksum"`
	// Through names the service account the file came through (a debrid
	// service); URL is then its direct link, which is never stored.
	Through string `json:"-"`
	// MaxConnections is the most connections the source allows (0: no cap).
	MaxConnections int `json:"-"`
}

// LinkInfo previews what a link points to, before a download is created.
type LinkInfo struct {
	URL            string `json:"url"`
	FinalURL       string `json:"finalUrl"`
	Filename       string `json:"filename"`
	Size           int64  `json:"size"`
	MIMEType       string `json:"mimeType"`
	RangeSupported bool   `json:"rangeSupported"`
	// WebPage is true when the link returns an HTML page instead of a file.
	WebPage bool `json:"webPage"`
}

// Progress is a point-in-time transfer statistic. Unknown values are -1.
type Progress struct {
	DownloadID        string  `json:"downloadId"`
	Downloaded        int64   `json:"downloaded"`
	Total             int64   `json:"total"`
	Percent           float64 `json:"percent"`
	Speed             float64 `json:"speed"`
	AverageSpeed      float64 `json:"averageSpeed"`
	ETASeconds        float64 `json:"etaSeconds"`
	ActiveConnections int     `json:"activeConnections"`
}
