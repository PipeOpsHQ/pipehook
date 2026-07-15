package handler

import (
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/PipeOpsHQ/pipehook/internal/store"
	"github.com/go-chi/chi/v5"
)

const exportPageSize = 10

type exportedRequest struct {
	ID            int64           `json:"id"`
	EndpointID    string          `json:"endpoint_id"`
	Method        string          `json:"method"`
	Path          string          `json:"path"`
	QueryString   string          `json:"query_string"`
	Host          string          `json:"host"`
	Scheme        string          `json:"scheme"`
	RemoteAddr    string          `json:"remote_addr"`
	Headers       json.RawMessage `json:"headers"`
	BodyBase64    string          `json:"body_base64"`
	ContentLength int64           `json:"content_length"`
	BodyTruncated bool            `json:"body_truncated"`
	StatusCode    int             `json:"status_code"`
	CreatedAt     time.Time       `json:"created_at"`
}

func exportRequest(request *store.Request) exportedRequest {
	headers := json.RawMessage(request.Headers)
	if !json.Valid(headers) {
		headers = json.RawMessage(`{}`)
	}
	return exportedRequest{
		ID: request.ID, EndpointID: request.EndpointID, Method: request.Method, Path: request.Path,
		QueryString: request.QueryString, Host: request.Host, Scheme: request.Scheme,
		RemoteAddr: request.RemoteAddr, Headers: headers, BodyBase64: base64.StdEncoding.EncodeToString(request.Body),
		ContentLength: request.ContentLength, BodyTruncated: request.BodyTruncated,
		StatusCode: request.StatusCode, CreatedAt: request.CreatedAt,
	}
}

func (h *Handler) ExportRequestsJSON(w http.ResponseWriter, r *http.Request) {
	h.exportRequests(w, r, false)
}

func (h *Handler) ExportRequestsCSV(w http.ResponseWriter, r *http.Request) {
	h.exportRequests(w, r, true)
}

func (h *Handler) exportRequests(w http.ResponseWriter, r *http.Request, asCSV bool) {
	endpointID := chi.URLParam(r, "endpointID")
	if _, ok := h.requireEndpointAccess(w, r, endpointID); !ok {
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(query) > 200 {
		query = query[:200]
	}

	if asCSV {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="pipehook-%s.csv"`, endpointID))
		writer := csv.NewWriter(w)
		_ = writer.Write([]string{"id", "method", "path", "query", "host", "scheme", "remote_addr", "headers", "body_base64", "content_length", "body_truncated", "status_code", "created_at"})
		for offset := 0; ; offset += exportPageSize {
			requests, err := h.Store.SearchRequests(r.Context(), endpointID, query, exportPageSize, offset)
			if err != nil {
				return
			}
			for _, request := range requests {
				_ = writer.Write([]string{
					strconv.FormatInt(request.ID, 10), request.Method, request.Path, request.QueryString,
					request.Host, request.Scheme, request.RemoteAddr, request.Headers,
					base64.StdEncoding.EncodeToString(request.Body), strconv.FormatInt(request.ContentLength, 10),
					strconv.FormatBool(request.BodyTruncated), strconv.Itoa(request.StatusCode), request.CreatedAt.Format(time.RFC3339Nano),
				})
			}
			writer.Flush()
			if len(requests) < exportPageSize || writer.Error() != nil || r.Context().Err() != nil {
				return
			}
		}
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="pipehook-%s.json"`, endpointID))
	_, _ = w.Write([]byte("["))
	first := true
	for offset := 0; ; offset += exportPageSize {
		requests, err := h.Store.SearchRequests(r.Context(), endpointID, query, exportPageSize, offset)
		if err != nil {
			return
		}
		for _, request := range requests {
			if !first {
				_, _ = w.Write([]byte(","))
			}
			first = false
			payload, _ := json.Marshal(exportRequest(request))
			_, _ = w.Write(payload)
		}
		if len(requests) < exportPageSize || r.Context().Err() != nil {
			break
		}
	}
	_, _ = w.Write([]byte("]"))
}
