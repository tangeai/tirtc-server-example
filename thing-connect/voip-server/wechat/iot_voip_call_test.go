package wechat

import (
	"errors"
	"testing"
)

func TestParseIotVoipCallResponsePreservesErrcode(t *testing.T) {
	err := parseIotVoipCallResponse(200, []byte(`{"errcode":9,"errmsg":"not authorized"}`))
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error type=%T, want *APIError", err)
	}
	if apiErr.Errcode != 9 || apiErr.Errmsg != "not authorized" {
		t.Fatalf("unexpected APIError: %+v", apiErr)
	}
}

func TestParseIotVoipCallResponseSuccess(t *testing.T) {
	if err := parseIotVoipCallResponse(200, []byte(`{"errcode":0,"errmsg":"ok"}`)); err != nil {
		t.Fatalf("success response returned error: %v", err)
	}
}

func TestParseIotVoipCallResponseRejectsHTTPFailure(t *testing.T) {
	if err := parseIotVoipCallResponse(502, []byte(`{"errcode":0,"errmsg":"ok"}`)); err == nil {
		t.Fatal("HTTP failure should return an error")
	}
}
