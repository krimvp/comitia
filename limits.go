package comitia

import "unicode/utf8"

// Content limits bound new writes in bytes; rows stored before them stay
// readable. Bodies match the adapters' 1 MiB request cap, so one maximal
// message or task, even fully JSON-escaped, stays below the HTTP client's
// 16 MiB response cap that always-returned first page items rely on.
const (
	MaxMessageBodyBytes   = 1 << 20
	MaxTaskTitleBytes     = 4096
	MaxTaskBodyBytes      = 1 << 20
	MaxReferenceKindBytes = 256
	MaxReferenceURIBytes  = 8192
)

// ContentLimits advertises the write limits in server_info.
type ContentLimits struct {
	MessageBodyBytes   int `json:"message_body_bytes"`
	TaskTitleBytes     int `json:"task_title_bytes"`
	TaskBodyBytes      int `json:"task_body_bytes"`
	ReferenceKindBytes int `json:"reference_kind_bytes"`
	ReferenceURIBytes  int `json:"reference_uri_bytes"`
}

func contentLimits() *ContentLimits {
	return &ContentLimits{MaxMessageBodyBytes, MaxTaskTitleBytes, MaxTaskBodyBytes, MaxReferenceKindBytes, MaxReferenceURIBytes}
}

func validText(field, s string, limit int) error {
	if len(s) > limit {
		return Invalidf("%s must be at most %d bytes", field, limit)
	}
	if !utf8.ValidString(s) {
		return Invalidf("%s must be valid UTF-8", field)
	}
	return nil
}
