package oauth

// ScopeInfo describes a known OAuth scope.
type ScopeInfo struct {
	Scope       string // full scope URL
	Short       string // short name for display
	Description string
}

// GoogleScopeCatalog lists known Google OAuth scopes.
var GoogleScopeCatalog = []ScopeInfo{
	{"openid", "openid", "OpenID Connect authentication"},
	{"email", "email", "View email address"},
	{"https://www.googleapis.com/auth/gmail.readonly", "gmail.readonly", "Read Gmail messages"},
	{"https://www.googleapis.com/auth/gmail.send", "gmail.send", "Send Gmail messages"},
	{"https://www.googleapis.com/auth/gmail.modify", "gmail.modify", "Read, send, and manage Gmail"},
	{"https://www.googleapis.com/auth/calendar.readonly", "calendar.readonly", "Read Google Calendar events"},
	{"https://www.googleapis.com/auth/calendar", "calendar", "Read and write Google Calendar"},
	{"https://www.googleapis.com/auth/drive.readonly", "drive.readonly", "Read Google Drive files"},
	{"https://www.googleapis.com/auth/drive", "drive", "Read and write Google Drive files"},
	{"https://www.googleapis.com/auth/drive.file", "drive.file", "Access files created by this app"},
	{"https://www.googleapis.com/auth/spreadsheets.readonly", "spreadsheets.readonly", "Read Google Sheets"},
	{"https://www.googleapis.com/auth/spreadsheets", "spreadsheets", "Read and write Google Sheets"},
	{"https://www.googleapis.com/auth/documents.readonly", "docs.readonly", "Read Google Docs"},
	{"https://www.googleapis.com/auth/documents", "docs", "Read and write Google Docs"},
	{"https://www.googleapis.com/auth/presentations.readonly", "slides.readonly", "Read Google Slides"},
	{"https://www.googleapis.com/auth/presentations", "slides", "Read and write Google Slides"},
	{"https://www.googleapis.com/auth/tasks.readonly", "tasks.readonly", "Read Google Tasks"},
	{"https://www.googleapis.com/auth/tasks", "tasks", "Read and write Google Tasks"},
	{"https://www.googleapis.com/auth/contacts.readonly", "contacts.readonly", "Read Google Contacts"},
	{"https://www.googleapis.com/auth/youtube.readonly", "youtube.readonly", "Read YouTube account"},
}

// googleScopeIndex maps full scope URLs and short names to ScopeInfo.
var googleScopeIndex map[string]*ScopeInfo

func init() {
	googleScopeIndex = make(map[string]*ScopeInfo, len(GoogleScopeCatalog)*2)
	for i := range GoogleScopeCatalog {
		s := &GoogleScopeCatalog[i]
		googleScopeIndex[s.Scope] = s
		googleScopeIndex[s.Short] = s
	}
}

// ResolveGoogleScope resolves a scope string (short name or full URL) to its full URL.
// Returns the input unchanged if not found in the catalog.
func ResolveGoogleScope(s string) string {
	if info, ok := googleScopeIndex[s]; ok {
		return info.Scope
	}
	return s
}

// LookupGoogleScope returns ScopeInfo for a scope (by short name or full URL), or nil.
func LookupGoogleScope(s string) *ScopeInfo {
	return googleScopeIndex[s]
}
