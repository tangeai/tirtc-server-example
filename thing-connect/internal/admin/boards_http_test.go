package admin

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestUploadBoardImage(t *testing.T) {
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)
	part, err := writer.CreateFormFile("file", "board.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(imageBytes.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	service, err := NewBoardImageService(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := &HTTPServer{boardImages: service}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/upload", server.uploadBoardImage)
	request := httptest.NewRequest(http.MethodPost, "/upload", &requestBody)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	var body struct {
		Code int        `json:"code"`
		Data BoardImage `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != 200 || body.Data.Type != "image/png" || body.Data.URL == "" {
		t.Fatalf("body = %+v", body)
	}
}
