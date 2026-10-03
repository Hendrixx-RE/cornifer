package companion

import (
	"context"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const MCPVersion = "0.2.0"

// NewMCPServer exposes the exact deterministic context/session contract used
// by the web API. Transport sessions are deliberately unrelated to the
// explicit application session_id returned by get_context.
func NewMCPServer(service *Service) *sdk.Server {
	s := sdk.NewServer(&sdk.Implementation{Name: "cornifer-companion", Version: MCPVersion}, nil)
	readOnly := &sdk.ToolAnnotations{ReadOnlyHint: true}
	sdk.AddTool(s, &sdk.Tool{Name: "list_repositories", Description: "List local Cornifer repository snapshots and their safe indexing status.", Annotations: readOnly}, func(ctx context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, []Repository, error) {
		v, err := service.ListRepositories(ctx)
		return nil, v, err
	})
	sdk.AddTool(s, &sdk.Tool{Name: "get_index_status", Description: "Get a repository snapshot and job progress. No repository code is executed.", Annotations: readOnly}, func(ctx context.Context, _ *sdk.CallToolRequest, in struct {
		RepositoryID string `json:"repository_id" jsonschema:"repository snapshot id"`
	}) (*sdk.CallToolResult, Repository, error) {
		v, err := service.GetRepository(ctx, in.RepositoryID)
		return nil, v, err
	})
	sdk.AddTool(s, &sdk.Tool{Name: "ingest_repository", Description: "Queue a public GitHub HTTPS URL for a client-local, detached-commit index. Hosted embedding credentials remain in the companion process.", Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false}}, func(ctx context.Context, _ *sdk.CallToolRequest, in struct {
		URL string `json:"url"`
		Ref string `json:"ref,omitempty"`
	}) (*sdk.CallToolResult, struct {
		Repository Repository `json:"repository"`
		Job        Job        `json:"job"`
		Reused     bool       `json:"reused"`
	}, error) {
		r, j, reused, err := service.Ingest(ctx, in.URL, in.Ref)
		return nil, struct {
			Repository Repository `json:"repository"`
			Job        Job        `json:"job"`
			Reused     bool       `json:"reused"`
		}{r, j, reused}, err
	})
	sdk.AddTool(s, &sdk.Tool{Name: "get_context", Description: "Return a bounded, source-cited context pack for one pinned snapshot. The result includes an explicit application session_id for optional second-brain memory.", Annotations: readOnly}, func(ctx context.Context, _ *sdk.CallToolRequest, in struct {
		RepositoryID  string `json:"repository_id"`
		Question      string `json:"question"`
		SessionID     string `json:"session_id,omitempty"`
		EvidenceLimit int    `json:"evidence_limit,omitempty"`
		ContextBytes  int    `json:"context_bytes,omitempty"`
	}) (*sdk.CallToolResult, struct {
		Context ContextPack `json:"context"`
		Session Session     `json:"session"`
	}, error) {
		p, session, err := service.BuildContext(ctx, in.RepositoryID, in.Question, in.SessionID, ContextOptions{EvidenceLimit: in.EvidenceLimit, ContextBytes: in.ContextBytes})
		return nil, struct {
			Context ContextPack `json:"context"`
			Session Session     `json:"session"`
		}{p, session}, err
	})
	sdk.AddTool(s, &sdk.Tool{Name: "remember_context", Description: "Persist an explicit bounded note in one application session. Sessions are isolated by repository snapshot.", Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false}}, func(ctx context.Context, _ *sdk.CallToolRequest, in struct {
		SessionID string `json:"session_id"`
		Note      string `json:"note"`
	}) (*sdk.CallToolResult, Session, error) {
		v, err := service.Remember(ctx, in.SessionID, in.Note)
		return nil, v, err
	})
	sdk.AddTool(s, &sdk.Tool{Name: "get_session_context", Description: "Inspect recent explicit notes and retrieved evidence for an application session.", Annotations: readOnly}, func(ctx context.Context, _ *sdk.CallToolRequest, in struct {
		SessionID string `json:"session_id"`
		Limit     int    `json:"limit,omitempty"`
	}) (*sdk.CallToolResult, struct {
		Session Session        `json:"session"`
		Events  []SessionEvent `json:"events"`
	}, error) {
		v, events, err := service.GetSession(ctx, in.SessionID, in.Limit)
		return nil, struct {
			Session Session        `json:"session"`
			Events  []SessionEvent `json:"events"`
		}{v, events}, err
	})
	sdk.AddTool(s, &sdk.Tool{Name: "clear_context", Description: "Permanently clear one explicit application session and its stored notes/evidence.", Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false}}, func(ctx context.Context, _ *sdk.CallToolRequest, in struct {
		SessionID string `json:"session_id"`
	}) (*sdk.CallToolResult, struct {
		Cleared bool `json:"cleared"`
	}, error) {
		if err := service.ClearSession(ctx, in.SessionID); err != nil {
			return nil, struct {
				Cleared bool `json:"cleared"`
			}{}, fmt.Errorf("clear session: %w", err)
		}
		return nil, struct {
			Cleared bool `json:"cleared"`
		}{true}, nil
	})
	return s
}
