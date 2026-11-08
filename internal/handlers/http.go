// Package handlers exposes the JSON HTTP API over net/http.
package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"

	"agentmem/internal/compute"
	"agentmem/internal/okf"
	"agentmem/internal/router"
	"agentmem/internal/service"
)

type documentInput struct {
	Type        string   `json:"type"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Body        string   `json:"body"`
	Tags        []string `json:"tags"`
	Visibility  string   `json:"visibility"`
	Owner       string   `json:"owner"`
}

type putRequest struct {
	ConceptID string        `json:"concept_id"`
	Document  documentInput `json:"document"`
}

type queryRequest struct {
	Query string `json:"query"`
	TopK  int    `json:"top_k"`
}

type computationRequest struct {
	ConceptID  string         `json:"concept_id"`
	Parameters map[string]any `json:"parameters"`
}

// NewHandler builds the API router. The subject of every request is taken from
// the trusted principal injected by the security middleware; request bodies and
// query strings can no longer assert an identity.
func NewHandler(svc *service.KnowledgeService, opts Options) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		writeJSON(w, http.StatusOK, svc.Health(r.Context()))
	})
	mux.HandleFunc("/v1/knowledge", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			handlePut(w, r, svc)
		case http.MethodGet:
			handleGet(w, r, svc)
		default:
			methodNotAllowed(w)
		}
	})
	mux.HandleFunc("/v1/query", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		var req queryRequest
		if !decode(w, r, &req) {
			return
		}
		if req.Query == "" {
			writeError(w, http.StatusBadRequest, "query is empty")
			return
		}
		if req.TopK < 0 || req.TopK > maxTopK {
			writeError(w, http.StatusBadRequest, "top_k is out of range")
			return
		}
		subject, ok := subjectFrom(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		res, err := svc.Query(r.Context(), subject, req.Query, req.TopK)
		if err != nil {
			serverError(w, "query", err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	})
	mux.HandleFunc("/v1/migrate", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		var opt router.MigrationOptions
		if !decode(w, r, &opt) {
			return
		}
		rep, err := svc.RunMigration(r.Context(), opt)
		if err != nil {
			redactedBadRequest(w, "migrate", err, "migration rejected")
			return
		}
		writeJSON(w, http.StatusOK, rep)
	})
	mux.HandleFunc("/v1/decisions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		limit, ok := parseLimit(w, r)
		if !ok {
			return
		}
		records, err := svc.Decisions(r.Context(), r.URL.Query().Get("concept_id"), limit)
		if err != nil {
			serverError(w, "decisions", err)
			return
		}
		writeJSON(w, http.StatusOK, records)
	})
	mux.HandleFunc("/v1/concepts", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		ids, err := svc.Concepts(r.Context())
		if err != nil {
			serverError(w, "concepts", err)
			return
		}
		writeJSON(w, http.StatusOK, ids)
	})
	mux.HandleFunc("/v1/computations", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			handleComputation(w, r, svc)
		case http.MethodGet:
			handleComputationAudit(w, r, svc)
		default:
			methodNotAllowed(w)
		}
	})
	return newSecurity(opts).wrap(mux)
}

func handleComputation(w http.ResponseWriter, r *http.Request, svc *service.KnowledgeService) {
	var req computationRequest
	if !decode(w, r, &req) {
		return
	}
	if req.ConceptID == "" {
		writeError(w, http.StatusBadRequest, "concept_id is required")
		return
	}
	subject, ok := subjectFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	env, err := svc.RunComputation(r.Context(), compute.Request{
		ConceptID: req.ConceptID, Subject: subject, Parameters: req.Parameters,
	})
	if err != nil {
		slog.Warn("computation rejected", "class", "computation", "error_type", fmt.Sprintf("%T", err))
		// The envelope never carries a display value on failure, and the error
		// body never carries the underlying SQL, DSN or driver message.
		writeJSON(w, computeStatus(err), map[string]any{"error": "computation rejected", "envelope": env})
		return
	}
	if !env.DisplayAllowed {
		writeJSON(w, http.StatusUnprocessableEntity, env)
		return
	}
	writeJSON(w, http.StatusOK, env)
}

func handleComputationAudit(w http.ResponseWriter, r *http.Request, svc *service.KnowledgeService) {
	limit, ok := parseLimit(w, r)
	if !ok {
		return
	}
	records, err := svc.ComputationAudit(r.Context(), r.URL.Query().Get("concept_id"), limit)
	if err != nil {
		serverError(w, "computation-audit", err)
		return
	}
	writeJSON(w, http.StatusOK, records)
}

func computeStatus(err error) int {
	switch {
	case errors.Is(err, compute.ErrUnsupported):
		return http.StatusUnprocessableEntity
	case errors.Is(err, compute.ErrExecution):
		return http.StatusBadGateway
	default:
		return http.StatusBadRequest
	}
}

func handlePut(w http.ResponseWriter, r *http.Request, svc *service.KnowledgeService) {
	var req putRequest
	if !decode(w, r, &req) {
		return
	}
	if req.ConceptID == "" || req.Document.Type == "" {
		writeError(w, http.StatusBadRequest, "concept_id and document.type are required")
		return
	}
	subject, ok := subjectFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	res, err := svc.Put(r.Context(), subject, req.ConceptID, toDocument(req.Document))
	if err != nil {
		redactedBadRequest(w, "put", err, "write rejected")
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func handleGet(w http.ResponseWriter, r *http.Request, svc *service.KnowledgeService) {
	id := r.URL.Query().Get("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}
	subject, ok := subjectFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	d, err := svc.Store.Get(r.Context(), subject, id)
	if err != nil {
		// A single, indistinguishable response avoids revealing whether a
		// private document exists to a subject without access.
		writeError(w, http.StatusNotFound, "document not found or access denied")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"concept_id": id, "type": d.Type, "title": d.Title,
		"description": d.Description, "body": d.Body,
		"status": d.EffectiveStatus(), "trust_tier": d.TrustTier(),
		"visibility": d.Visibility(), "owner": d.Owner(),
	})
}

func toDocument(in documentInput) *okf.Document {
	d := &okf.Document{
		Type: in.Type, Title: in.Title, Description: in.Description,
		Body: in.Body, Tags: in.Tags,
	}
	if in.Visibility != "" {
		d.SetVisibility(in.Visibility)
	}
	if in.Owner != "" {
		d.SetOwner(in.Owner)
	}
	return d
}

const (
	maxRequestBytes = 4 << 20
	maxTopK         = 100
	defaultLimit    = 20
	maxLimit        = 1000
)

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusBadRequest, "Content-Type must be application/json")
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return false
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "unexpected trailing data")
		return false
	}
	return true
}

func parseLimit(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return defaultLimit, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > maxLimit {
		writeError(w, http.StatusBadRequest, "limit is out of range")
		return 0, false
	}
	return n, true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}
