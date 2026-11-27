package wire

import "time"

type PreparePathNotesRequest struct {
	Mode  string `json:"mode"`
	Focus string `json:"focus,omitempty"`
}

type PreparePathNotesResponse struct {
	Ready     bool   `json:"ready"`
	Reused    bool   `json:"reused,omitempty"`
	Limited   bool   `json:"limited,omitempty"`
	Message   string `json:"message,omitempty"`
	NoteSaved bool   `json:"note_saved"`
	Cancelled bool   `json:"cancelled,omitempty"`
}

type PathNotesStatus struct {
	State           string     `json:"state"`
	Mode            string     `json:"mode,omitempty"`
	Focus           string     `json:"focus,omitempty"`
	SourceSessionID string     `json:"source_session_id,omitempty"`
	SourceMessages  *int       `json:"source_messages,omitempty"`
	OmittedMessages int        `json:"omitted_messages,omitempty"`
	ReadFiles       []string   `json:"read_files,omitempty"`
	ModifiedFiles   []string   `json:"modified_files,omitempty"`
	Model           string     `json:"model,omitempty"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	WritingAt       *time.Time `json:"writing_at,omitempty"`
	Preview         string     `json:"preview,omitempty"`
}

type PathNotesCancellation struct {
	Cancelled bool             `json:"cancelled"`
	NoteSaved bool             `json:"note_saved"`
	Status    *PathNotesStatus `json:"status,omitempty"`
}
