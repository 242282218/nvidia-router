package admin

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"nvidia-router/internal/adminaudit"
)

// auditLogReader is the query-side surface the audit-logs endpoint needs.
type auditLogReader interface {
	List(context.Context, adminaudit.ListQuery) (adminaudit.Page, error)
}

// AuditLogs exposes the admin-audit trail to the management UI.
type AuditLogs struct {
	reader auditLogReader
}

func NewAuditLogs(reader auditLogReader) *AuditLogs {
	return &AuditLogs{reader: reader}
}

type auditEntryDTO struct {
	ID         int64     `json:"id"`
	Action     string    `json:"action"`
	TargetType string    `json:"target_type"`
	TargetID   string    `json:"target_id,omitempty"`
	Detail     string    `json:"detail,omitempty"`
	SessionID  *string   `json:"session_id,omitempty"`
	ClientIP   string    `json:"client_ip,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type auditLogsPageDTO struct {
	Items   []auditEntryDTO `json:"items"`
	Total   int             `json:"total"`
	HasMore bool            `json:"has_more"`
	Next    *int            `json:"next,omitempty"`
}

func (h *AuditLogs) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/admin/api/audit-logs" || request.Method != http.MethodGet {
		http.NotFound(writer, request)
		return
	}
	query := request.URL.Query()
	limit := parsePositiveInt(query.Get("limit"), 100)
	offset := parseNonNegativeInt(query.Get("offset"), 0)
	page, err := h.reader.List(request.Context(), adminaudit.ListQuery{
		Limit:  limit,
		Offset: offset,
		Action: query.Get("action"),
	})
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	dto := auditLogsPageDTO{Total: page.Total, HasMore: page.HasMore, Next: page.Next}
	dto.Items = make([]auditEntryDTO, 0, len(page.Items))
	for _, entry := range page.Items {
		dto.Items = append(dto.Items, toAuditEntryDTO(entry))
	}
	writeJSON(writer, http.StatusOK, map[string]any{"data": dto})
}

func toAuditEntryDTO(entry adminaudit.Entry) auditEntryDTO {
	return auditEntryDTO{
		ID: entry.ID, Action: entry.Action, TargetType: entry.TargetType, TargetID: entry.TargetID,
		Detail: entry.Detail, SessionID: entry.SessionID, ClientIP: entry.ClientIP, CreatedAt: entry.CreatedAt,
	}
}

func parsePositiveInt(value string, fallback int) int {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func parseNonNegativeInt(value string, fallback int) int {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}
