package http

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/gen"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/reqctx"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/pkg/validatorx"
)

// Auditor records audit logs.
type Auditor interface {
	Log(ctx context.Context, l *repo.AuditLog)
}

// audited is nestjs AuditInterceptor: a successful Audit operation writes one audit log.
func audited(auditor Auditor, policies map[string]Policy) gen.StrictMiddlewareFunc {
	return func(f gen.StrictHandlerFunc, operationID string) gen.StrictHandlerFunc {
		spec := policyOf(policies, operationID).Audit
		if spec == nil || auditor == nil {
			return f
		}

		return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
			resp, err := f(ctx, w, r, request)
			if err == nil {
				entry := auditLog(r, spec, resp)
				auditor.Log(context.WithoutCancel(ctx), &entry)
			}

			return resp, err
		}
	}
}

func optional(s string) *string {
	if s == "" {
		return nil
	}

	return &s
}

func auditLog(r *http.Request, spec *Audit, resp any) repo.AuditLog {
	ctx := r.Context()
	data := responseObject(resp)

	var userID *string
	if p := reqctx.Principal(ctx); p != nil {
		userID = optional(strconv.Itoa(p.UserID))
	}

	return repo.AuditLog{
		UserID:     userID,
		Action:     spec.Action,
		EntityType: spec.EntityType,
		EntityID:   entityID(r, spec, data),
		OldValue:   nil,
		NewValue:   recordedValue(ctx, spec, data),
		IPAddress:  auditIP(r),
		UserAgent:  optional(r.Header.Get("User-Agent")),
		RequestID:  optional(r.Header.Get(_requestIDHeader)),
	}
}

func responseObject(resp any) map[string]any {
	raw, err := json.Marshal(resp)
	if err != nil {
		return nil
	}

	var obj map[string]any
	if json.Unmarshal(raw, &obj) != nil {
		return nil
	}

	return obj
}

func entityID(r *http.Request, spec *Audit, data map[string]any) string {
	params := spec.IDParams
	if len(params) == 0 {
		params = []string{"id"}
	}

	values := make([]string, 0, len(params))

	for _, name := range params {
		v, err := decodeURIComponent(chi.URLParam(r, name))
		if err != nil || v == "" {
			values = nil

			break
		}

		values = append(values, v)
	}

	paramID := strings.Join(values, ":")
	responseID := responseIDOf(data)

	first, second := paramID, responseID
	if spec.FromResponse {
		first, second = responseID, paramID
	}

	switch {
	case first != "":
		return first
	case second != "":
		return second
	default:
		return "unknown"
	}
}

func responseIDOf(data map[string]any) string {
	switch id := data["id"].(type) {
	case string:
		return id
	case float64:
		return strconv.FormatFloat(id, 'f', -1, 64)
	default:
		return ""
	}
}

func recordedValue(ctx context.Context, spec *Audit, data map[string]any) map[string]any {
	if spec.IncludeResponse && data != nil {
		return data
	}

	if spec.OmitBody {
		return nil
	}

	v := reqctx.ValidatedValues(ctx)
	if v == nil {
		return nil
	}

	o, ok := v.Body.(*validatorx.Object)
	if !ok {
		return nil
	}

	raw, ok := validatorx.Stringify(o)
	if !ok {
		return nil
	}

	var out map[string]any
	if json.Unmarshal([]byte(raw), &out) != nil {
		return nil
	}

	return out
}

// auditIP is the first X-Forwarded-For entry, else the client IP.
func auditIP(r *http.Request) *string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first, _, _ := strings.Cut(xff, ",")

		return optional(strings.TrimSpace(first))
	}

	return reqctx.Client(r.Context()).IP
}
