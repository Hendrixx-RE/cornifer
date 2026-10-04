package companion

import "time"

const IndexVersion = "companion-v1"

type RepositoryStatus string

const (
	StatusQueued             RepositoryStatus = "queued"
	StatusCloning            RepositoryStatus = "cloning"
	StatusResolving          RepositoryStatus = "resolving"
	StatusIndexing           RepositoryStatus = "indexing"
	StatusReady              RepositoryStatus = "ready"
	StatusAwaitingCredential RepositoryStatus = "awaiting_credentials"
	StatusFailed             RepositoryStatus = "failed"
	StatusCancelled          RepositoryStatus = "cancelled"
	StatusStale              RepositoryStatus = "stale"
)

func (s RepositoryStatus) Ready() bool { return s == StatusReady }

type Repository struct {
	ID                  string           `json:"id"`
	CanonicalURL        string           `json:"canonical_url"`
	RequestedRef        string           `json:"requested_ref,omitempty"`
	ResolvedCommitSHA   string           `json:"resolved_commit_sha,omitempty"`
	CheckoutPath        string           `json:"-"`
	CacheDir            string           `json:"-"`
	EngineRepoID        int64            `json:"engine_repo_id,omitempty"`
	Status              RepositoryStatus `json:"status"`
	Capabilities        []string         `json:"capabilities"`
	ErrorCode           string           `json:"error_code,omitempty"`
	SafeMessage         string           `json:"safe_message,omitempty"`
	IndexVersion        string           `json:"index_version"`
	ProviderFingerprint string           `json:"provider_fingerprint,omitempty"`
	CreatedAt           time.Time        `json:"created_at"`
	UpdatedAt           time.Time        `json:"updated_at"`
}

type Job struct {
	ID              string    `json:"id"`
	RepositoryID    string    `json:"repository_id"`
	Phase           string    `json:"phase"`
	FilesSeen       int       `json:"files_seen"`
	FilesIndexed    int       `json:"files_indexed"`
	Chunks          int       `json:"chunks"`
	Edges           int       `json:"edges"`
	Cancellable     bool      `json:"cancellable"`
	CancelRequested bool      `json:"cancel_requested"`
	ErrorCode       string    `json:"error_code,omitempty"`
	SafeMessage     string    `json:"safe_message,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type Progress struct {
	Phase        string
	FilesSeen    int
	FilesIndexed int
	Chunks       int
	Edges        int
	Cancellable  bool
}

type Citation struct {
	ID         string   `json:"id"`
	Path       string   `json:"path"`
	StartLine  int      `json:"start_line"`
	EndLine    int      `json:"end_line"`
	Symbol     string   `json:"symbol,omitempty"`
	SymbolID   string   `json:"symbol_id,omitempty"`
	Snippet    string   `json:"snippet"`
	ExcerptSHA string   `json:"excerpt_sha256"`
	Sources    []string `json:"retrieval_sources"`
	Truncated  bool     `json:"truncated"`
}

type Relationship struct {
	FromSymbolID   string  `json:"from_symbol_id,omitempty"`
	ToSymbolID     string  `json:"to_symbol_id,omitempty"`
	FromCitationID string  `json:"from_citation_id,omitempty"`
	ToCitationID   string  `json:"to_citation_id,omitempty"`
	FromSymbol     string  `json:"from_symbol,omitempty"`
	ToSymbol       string  `json:"to_symbol,omitempty"`
	Kind           string  `json:"kind"`
	Confidence     float64 `json:"confidence"`
	Depth          int     `json:"depth"`
}

// ContextSymbol is bounded structural metadata for evidence and its immediate
// dependencies. Both website and MCP receive it in the same context pack.
type ContextSymbol struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	QualifiedName string `json:"qualified_name"`
	Kind          string `json:"kind"`
	Path          string `json:"path"`
	StartLine     int    `json:"start_line"`
	EndLine       int    `json:"end_line"`
	Signature     string `json:"signature,omitempty"`
	Evidence      bool   `json:"evidence"`
}

type ContextPack struct {
	Status     string `json:"status"`
	Repository struct {
		ID           string   `json:"id"`
		CanonicalURL string   `json:"canonical_url"`
		CommitSHA    string   `json:"commit_sha"`
		Capabilities []string `json:"capabilities"`
	} `json:"repository"`
	Query         string          `json:"query"`
	Evidence      []Citation      `json:"evidence"`
	Relationships []Relationship  `json:"relationships"`
	Symbols       []ContextSymbol `json:"symbols"`
	Omitted       struct {
		EvidenceCount int    `json:"evidence_count"`
		Reason        string `json:"reason,omitempty"`
	} `json:"omitted"`
	Retrieval struct {
		SystemsUsed []string `json:"systems_used"`
		Degraded    bool     `json:"degraded"`
		CacheKey    string   `json:"cache_key"`
		ContextByte int      `json:"context_bytes"`
	} `json:"retrieval"`
}

type Session struct {
	ID             string    `json:"id"`
	RepositoryID   string    `json:"repository_id"`
	CommitSHA      string    `json:"commit_sha"`
	RollingSummary string    `json:"rolling_summary,omitempty"`
	Stale          bool      `json:"stale"`
	ExpiresAt      time.Time `json:"expires_at"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type SessionEvent struct {
	ID        int64     `json:"id"`
	SessionID string    `json:"session_id"`
	Kind      string    `json:"kind"`
	Payload   any       `json:"payload"`
	CreatedAt time.Time `json:"created_at"`
}
