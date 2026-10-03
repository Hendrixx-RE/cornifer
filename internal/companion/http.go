package companion

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// HTTPHandler is intentionally small: the browser and MCP call the same
// Service methods and receive the same citations/session boundary.
func HTTPHandler(service *Service, runtime RuntimeConfig) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "embedding_configured": runtime.configuredEmbedding(), "embedding_provider": runtime.EmbeddingProvider, "chat_configured": chatConfigured()})
	})
	mux.HandleFunc("GET /api/repos", func(w http.ResponseWriter, r *http.Request) {
		repos, err := service.ListRepositories(r.Context())
		respond(w, repos, err)
	})
	mux.HandleFunc("POST /api/repos", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			URL string `json:"url"`
			Ref string `json:"ref"`
		}
		if err := decodeJSON(r, &in); err != nil {
			respond(w, nil, err)
			return
		}
		repo, job, reused, err := service.Ingest(r.Context(), in.URL, in.Ref)
		respond(w, map[string]any{"repository": repo, "job": job, "reused": reused}, err)
	})
	mux.HandleFunc("GET /api/repos/{id}", func(w http.ResponseWriter, r *http.Request) {
		repo, err := service.GetRepository(r.Context(), r.PathValue("id"))
		respond(w, repo, err)
	})
	mux.HandleFunc("GET /api/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		job, err := service.GetJob(r.Context(), r.PathValue("id"))
		respond(w, job, err)
	})
	mux.HandleFunc("POST /api/jobs/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		respond(w, map[string]bool{"cancelled": true}, service.Cancel(r.Context(), r.PathValue("id")))
	})
	mux.HandleFunc("POST /api/context", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			RepositoryID  string `json:"repository_id"`
			Question      string `json:"question"`
			SessionID     string `json:"session_id"`
			EvidenceLimit int    `json:"evidence_limit"`
			ContextBytes  int    `json:"context_bytes"`
		}
		if err := decodeJSON(r, &in); err != nil {
			respond(w, nil, err)
			return
		}
		pack, session, err := service.BuildContext(r.Context(), in.RepositoryID, in.Question, in.SessionID, ContextOptions{EvidenceLimit: in.EvidenceLimit, ContextBytes: in.ContextBytes})
		respond(w, map[string]any{"context": pack, "session": session}, err)
	})
	mux.HandleFunc("POST /api/answer", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			RepositoryID string `json:"repository_id"`
			Question     string `json:"question"`
			SessionID    string `json:"session_id"`
		}
		if err := decodeJSON(r, &in); err != nil {
			respond(w, nil, err)
			return
		}
		pack, session, err := service.BuildContext(r.Context(), in.RepositoryID, in.Question, in.SessionID, ContextOptions{})
		if err != nil {
			respond(w, nil, err)
			return
		}
		answer, err := GenerateAnswer(r.Context(), ChatConfigFromEnv(), pack)
		if err != nil {
			respond(w, map[string]any{"answer": answer, "session": session}, err)
			return
		}
		respond(w, map[string]any{"answer": answer, "session": session}, nil)
	})
	mux.HandleFunc("GET /api/sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		session, events, err := service.GetSession(r.Context(), r.PathValue("id"), 0)
		respond(w, map[string]any{"session": session, "events": events}, err)
	})
	mux.HandleFunc("POST /api/sessions/{id}/remember", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Note string `json:"note"`
		}
		if err := decodeJSON(r, &in); err != nil {
			respond(w, nil, err)
			return
		}
		session, err := service.Remember(r.Context(), r.PathValue("id"), in.Note)
		respond(w, session, err)
	})
	mux.HandleFunc("DELETE /api/sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		respond(w, map[string]bool{"cleared": true}, service.ClearSession(r.Context(), r.PathValue("id")))
	})
	return mux
}

func decodeJSON(r *http.Request, dst any) error {
	if r.Body == nil {
		return errors.New("JSON request body is required")
	}
	r.Body = http.MaxBytesReader(nil, r.Body, 64<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return errors.New("invalid JSON request")
	}
	return nil
}
func respond(w http.ResponseWriter, value any, err error) {
	if err == nil {
		writeJSON(w, http.StatusOK, value)
		return
	}
	status := http.StatusBadRequest
	if errors.Is(err, ErrNotFound) {
		status = http.StatusNotFound
	}
	if strings.Contains(err.Error(), "credentials") {
		status = http.StatusPreconditionRequired
	}
	writeJSON(w, status, map[string]any{"error": err.Error()})
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// Chat is deliberately opt-in and separate from retrieval. It is a visible
// config state for the UI until a user chooses a compatible hosted provider.
func chatConfigured() bool {
	return ChatConfigFromEnv().Configured()
}
