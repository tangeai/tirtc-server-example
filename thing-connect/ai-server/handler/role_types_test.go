package handler

import (
	"context"
	"errors"
	"strings"
	"testing"
	"thing-connect/internal/ai/roletypes"
)

type roleTypesStub struct {
	err   error
	calls int
}

func (s *roleTypesStub) Published(context.Context) ([]roletypes.Type, error) {
	s.calls++
	return []roletypes.Type{{ID: "one", Name: "类型", Prompt: "默认设定"}}, s.err
}
func TestRoleTypesRequireUserJWTAndHandleUnavailableCatalog(t *testing.T) {
	catalog := &roleTypesStub{}
	h := &AgentHandler{agentAPI: &mockAgentAPI{}, roleTypes: catalog}
	r := setupTestRouter(h)
	if w := doRequest(r, "GET", "/v1/ai/role-types", "", ""); w.Code != 401 || catalog.calls != 0 {
		t.Fatal("missing authentication accepted")
	}
	if w := doRequest(r, "GET", "/v1/ai/role-types", "", makeToken(1)); w.Code != 200 || !strings.Contains(w.Body.String(), "默认设定") {
		t.Fatal(w.Body.String())
	}
	catalog.err = errors.New("private internal address")
	if w := doRequest(r, "GET", "/v1/ai/role-types", "", makeToken(1)); w.Code != 503 || strings.Contains(w.Body.String(), "private") {
		t.Fatal(w.Body.String())
	}
}
