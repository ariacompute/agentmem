// Package handlers exposes the JSON HTTP API over net/http.
package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

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
	Subject   string        `json:"subject"`
	Document  documentInput `json:"document"`
}

type queryRequest struct {
	Query   string `json:"query"`
	Subject string `json:"subject"`
	TopK    int    `json:"top_k"`
}

// NewHandler builds the API router.
func NewHandler(svc *service.KnowledgeService) http.Handler {
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
		res, err := svc.Query(r.Context(), req.Subject, req.Query, req.TopK)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
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
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, rep)
	})
	mux.HandleFunc("/v1/decisions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		records, err := svc.Decisions(r.Context(), r.URL.Query().Get("concept_id"), limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
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
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, ids)
	})
	return mux
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
	res, err := svc.Put(r.Context(), req.Subject, req.ConceptID, toDocument(req.Document))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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
	d, err := svc.Store.Get(r.Context(), r.URL.Query().Get("subject"), id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
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

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20))
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return false
	}
	return true
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
