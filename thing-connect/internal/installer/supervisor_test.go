package installer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestSupervisorRejectsPortOwnedByAnotherProcessWithGuidance(t *testing.T) {
	controllerScript := filepath.Join(t.TempDir(), "controller")
	if err := os.WriteFile(controllerScript, []byte(`#!/usr/bin/env bash
if [ "$1" = status ]; then
  case "$2" in
    *:call-server) printf '%s STOPPED\n' "$2" ;;
    *) printf '%s RUNNING\n' "$2" ;;
  esac
fi
`), 0o700); err != nil {
		t.Fatal(err)
	}
	controller := NewSupervisorController(controllerScript, "thing-connect", 9000)
	controller.timeout = 100 * time.Millisecond
	controller.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("ok")),
			Header:     make(http.Header),
		}, nil
	})}
	controller.portAvailable = func(port int) error {
		if port == 9005 {
			return errors.New("injected occupied port")
		}
		return nil
	}
	states := make([]ServiceState, 0)
	err := controller.StartAndWait(context.Background(), []string{"call-server"}, func(state ServiceState) {
		states = append(states, state)
	})
	if err == nil {
		t.Fatal("occupied call-server port was accepted because another process answered readiness")
	}
	raw, marshalErr := json.Marshal(states)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	got := string(raw)
	if !strings.Contains(got, `"code":"PORT_IN_USE"`) ||
		!strings.Contains(got, "端口") || !strings.Contains(got, "停止占用") {
		t.Fatalf("service state does not contain actionable port guidance: %s", got)
	}
}

func TestRuntimeFailurePersistsCustomerGuidance(t *testing.T) {
	bootstrap := New(testOptions(t), Dependencies{})
	state := journal{Snapshot: Snapshot{
		Mode: ModeRecovery, OperationID: "operation-1", Phase: "starting_services", Percent: 75,
	}}
	problem := portInUseProblem(serviceSpec{Name: "call-server", DisplayName: "设备通话服务", HTTPPort: 9005})
	bootstrap.fail(state, "BUSINESS_NOT_READY", "部分业务服务尚未就绪", true,
		runtimeFailure(problem, errors.New("sensitive port probe cause")))

	saved, err := bootstrap.loadJournal()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Problem == nil || saved.Problem.Code != "PORT_IN_USE" ||
		len(saved.Problem.Suggestions) == 0 || strings.Contains(saved.Problem.Message, "sensitive") {
		t.Fatalf("persisted problem = %+v", saved.Problem)
	}
}
