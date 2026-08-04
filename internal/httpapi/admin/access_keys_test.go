package admin

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"nvidia-router/internal/accesskey"
)

type fakeAccessKeys struct {
	keys         []accesskey.Key
	created      accesskey.CreatedKey
	revoked      int64
	policyCalled bool
	policyID     int64
	policyRPM    int
	policyTPM    int
	policyExpiry *time.Time
}

func (f *fakeAccessKeys) List(context.Context) ([]accesskey.Key, error) {
	return append([]accesskey.Key(nil), f.keys...), nil
}
func (f *fakeAccessKeys) Create(context.Context, string) (accesskey.CreatedKey, error) {
	return f.created, nil
}
func (f *fakeAccessKeys) Revoke(_ context.Context, id int64) error { f.revoked = id; return nil }
func (f *fakeAccessKeys) UpdatePolicy(_ context.Context, id int64, expiresAt *time.Time, rpm, tpm, _ int) error {
	f.policyCalled, f.policyID, f.policyRPM, f.policyTPM, f.policyExpiry = true, id, rpm, tpm, expiresAt
	return nil
}

func TestAccessKeyAPIShowsPlaintextOnlyOnCreate(t *testing.T) {
	now := time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC)
	plaintext := "nvr_once_only_secret"
	service := &fakeAccessKeys{keys: []accesskey.Key{{ID: 2, Name: "ci", Prefix: "nvr_prefix", CreatedAt: now}}, created: accesskey.CreatedKey{Key: accesskey.Key{ID: 3, Name: "new", Prefix: "nvr_new", CreatedAt: now}, Plaintext: plaintext}}
	handler := NewAccessKeys(service)
	response := performAdminRequest(handler, http.MethodGet, "/admin/api/access-keys", "")
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), plaintext) || strings.Contains(response.Body.String(), "digest") {
		t.Fatalf("list status=%d body=%s", response.Code, response.Body.String())
	}
	response = performAdminRequest(handler, http.MethodPost, "/admin/api/access-keys", `{"name":"new"}`)
	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), plaintext) {
		t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
	}
	response = performAdminRequest(handler, http.MethodDelete, "/admin/api/access-keys/3", "")
	if response.Code != http.StatusNoContent || service.revoked != 3 {
		t.Fatalf("delete status=%d revoked=%d", response.Code, service.revoked)
	}
	response = performAdminRequest(handler, http.MethodGet, "/admin/api/access-keys", "")
	if strings.Contains(response.Body.String(), plaintext) {
		t.Fatalf("later list leaked plaintext: %s", response.Body.String())
	}

	expires := "2026-08-06T00:00:00Z"
	response = performAdminRequest(handler, http.MethodPatch, "/admin/api/access-keys/3", `{"expires_at":"`+expires+`","rpm_limit":10,"tpm_limit":1000,"max_concurrent":2}`)
	if response.Code != http.StatusOK || !service.policyCalled || service.policyID != 3 || service.policyRPM != 10 || service.policyTPM != 1000 || service.policyExpiry == nil || service.policyExpiry.Format(time.RFC3339) != expires {
		t.Fatalf("policy status=%d body=%s service=%+v", response.Code, response.Body.String(), service)
	}
}
