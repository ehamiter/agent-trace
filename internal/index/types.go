package index

import "database/sql"

type Session struct {
	ID             string
	Source         string
	LastActivityTS int64
	MessageCount   int
	Workdir        string
	Preview        string
}

type Message struct {
	ID         int64
	SessionID  string
	TS         sql.NullInt64
	Role       string
	Content    string
	Type       string
	Source     string
	SourcePath string
	Workdir    string
}

type TranscriptToggles struct {
	IncludeTools   bool
	IncludeAborted bool
	IncludeEvents  bool
}
