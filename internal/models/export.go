package models

import "fmt"

// ExportArtifact records one output file written for a finished run: the Markdown
// or PDF stored so a result outlives the browser session it was produced in.
type ExportArtifact struct {
	// Role is how the artifact was produced: "markdown" or "pdf".
	Role string `json:"role"`
	// Format is the file extension it was written with.
	Format string `json:"format,omitempty"`
	// Name is the object name (or relative path) it was stored under.
	Name string `json:"name"`
	// URI is the canonical location: a gs:// reference, or an absolute path.
	URI string `json:"uri,omitempty"`
	// URL opens the artifact in a browser, when the destination supports it.
	URL string `json:"url,omitempty"`
	// Bytes is the stored size.
	Bytes int `json:"bytes,omitempty"`
	// ContentType is the media type it was stored with.
	ContentType string `json:"content_type,omitempty"`
	// Error is set when this artifact could not be written. The other artifacts of
	// the same run are written independently, so one failure never hides the rest.
	Error string `json:"error,omitempty"`
}

// Failed reports whether writing this artifact failed.
func (artifact ExportArtifact) Failed() bool { return artifact.Error != "" }

// SizeLabel renders the artifact size for display.
func (artifact ExportArtifact) SizeLabel() string {
	switch {
	case artifact.Bytes >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(artifact.Bytes)/(1<<20))
	case artifact.Bytes >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(artifact.Bytes)/(1<<10))
	case artifact.Bytes > 0:
		return fmt.Sprintf("%d B", artifact.Bytes)
	default:
		return "—"
	}
}
