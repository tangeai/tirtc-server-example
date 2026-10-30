// Package webui serves shared Web language assets from each service's own origin.
package webui

import (
	_ "embed"
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"thing-connect/internal/weblanguage"
)

//go:embed runtime.js
var runtimeJS string

//go:embed common.json
var commonJSON string

// Register publishes only this service's policy and dictionary. No request to
// another business service is needed to initialize its language.
func Register(r *gin.Engine, prefix string, state *weblanguage.State, messages string) {
	r.GET(prefix+"/config/language.js", func(c *gin.Context) {
		config, err := json.Marshal(state.Current())
		if err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.Header("Cache-Control", "no-store")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Data(http.StatusOK, "text/javascript; charset=utf-8", []byte("window.WebLanguageConfig="+string(config)+";\n"+runtimeJS+"\nWebI18n.addMessages("+commonJSON+");\nWebI18n.addMessages("+messages+");\n"))
	})
}
