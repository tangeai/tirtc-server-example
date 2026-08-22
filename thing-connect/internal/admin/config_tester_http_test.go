package admin

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	casbin "github.com/casbin/casbin/v3"
	casbinmodel "github.com/casbin/casbin/v3/model"
	"github.com/gin-gonic/gin"
)

func TestMQTTPublishFailureReturnsGuidanceBeforeDatabaseWrite(t *testing.T) {
	model, err := casbinmodel.NewModelFromString(rbacModel)
	if err != nil {
		t.Fatal(err)
	}
	enforcer, err := casbin.NewEnforcer(model)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enforcer.AddPolicy("operator", "config.secret.write"); err != nil {
		t.Fatal(err)
	}
	if _, err := enforcer.AddGroupingPolicy(subjectForUser(1), "operator"); err != nil {
		t.Fatal(err)
	}

	probe := &recordingMQTTProbe{err: errors.New("dial tcp 10.0.0.8:1883: connection refused")}
	configs := NewConfigService(nil, DefaultConfigRegistry(), nil)
	server := &HTTPServer{
		access:       &AccessController{e: enforcer},
		configs:      configs,
		configTester: NewConfigTester(configs, probe),
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.PUT("/:namespace/:config_key", func(c *gin.Context) {
		c.Set(identityKey, AccessIdentity{UserID: 1, Roles: []string{"operator"}})
		server.putConfig(c)
	})

	body := `{"value":{"broker":"mqtt://broker.internal:1883","auth_mode":"username","username":"usersrv","client_id":""},"secrets":{"password":"secret"},"reason":"verify before publish","confirm":true}`
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/user-server/mqtt.connection", strings.NewReader(body)))

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	response := recorder.Body.String()
	for _, want := range []string{"MQTT 连接或认证测试失败", "suggestions", "TLS"} {
		if !strings.Contains(response, want) {
			t.Fatalf("response has no actionable %q guidance: %s", want, response)
		}
	}
	for _, leaked := range []string{"10.0.0.8", "broker.internal", "connection refused", "secret"} {
		if strings.Contains(response, leaked) {
			t.Fatalf("response leaked %q: %s", leaked, response)
		}
	}
}
