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
	keys    []accesskey.Key
	created accesskey.CreatedKey
	revoked int64
}

func (f *fakeAccessKeys) List(context.Context) ([]accesskey.Key, error) {
	return append([]accesskey.Key(nil), f.keys...), nil
}
func (f *fakeAccessKeys) Create(context.Context, string) (accesskey.CreatedKey, error) {
	return f.created, nil
}
func (f *fakeAccessKeys) Revoke(_ context.Context, id int64) error { f.revoked = id; return nil }

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
}
