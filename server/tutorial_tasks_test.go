package server

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTutorialTasksPublicReadOnly(t *testing.T) {
	handler := NewTutorialTaskHandler(t.TempDir(), "/uploads")

	putReq := httptest.NewRequest(http.MethodPut, "/api/tutorial-tasks", strings.NewReader(`{"tasks":[]}`))
	putRec := httptest.NewRecorder()
	handler.HandleTasks(putRec, putReq)
	if putRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT status=%d body=%s", putRec.Code, putRec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/tutorial-tasks", nil)
	getRec := httptest.NewRecorder()
	handler.HandleTasks(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", getRec.Code, getRec.Body.String())
	}
	var payload tutorialTaskPayload
	if err := json.NewDecoder(getRec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if len(payload.Tasks) != 0 {
		t.Fatalf("expected no default server tasks, got %d", len(payload.Tasks))
	}
}

func TestTutorialAdminTasksRequireLocalAddress(t *testing.T) {
	handler := NewTutorialTaskHandler(t.TempDir(), "/uploads")
	body := `{"tasks":[{"title":"读图","intro":"介绍","files":[{"name":"a.png","url":"/uploads/tutorial/a.png"}],"prompt":"请读图"}]}`

	publicReq := httptest.NewRequest(http.MethodPut, "/local/tutorial-admin/tasks", strings.NewReader(body))
	publicReq.RemoteAddr = "203.0.113.20:40200"
	publicRec := httptest.NewRecorder()
	handler.HandleAdminTasks(publicRec, publicReq)
	if publicRec.Code != http.StatusForbidden {
		t.Fatalf("public status=%d body=%s", publicRec.Code, publicRec.Body.String())
	}

	localReq := httptest.NewRequest(http.MethodPut, "/local/tutorial-admin/tasks", strings.NewReader(body))
	localReq.RemoteAddr = "127.0.0.1:40200"
	localRec := httptest.NewRecorder()
	handler.HandleAdminTasks(localRec, localReq)
	if localRec.Code != http.StatusOK {
		t.Fatalf("local status=%d body=%s", localRec.Code, localRec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/tutorial-tasks", nil)
	getRec := httptest.NewRecorder()
	handler.HandleTasks(getRec, getReq)
	var payload tutorialTaskPayload
	if err := json.NewDecoder(getRec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode saved payload: %v", err)
	}
	if len(payload.Tasks) != 1 || payload.Tasks[0].Title != "读图" || len(payload.Tasks[0].Files) != 1 {
		t.Fatalf("unexpected saved payload: %+v", payload)
	}
}

func TestTutorialAdminUploadRequiresLocalAddress(t *testing.T) {
	handler := NewTutorialTaskHandler(t.TempDir(), "/uploads")

	publicBody, publicContentType := tutorialMultipartBody(t, "sample.png", []byte("png"))
	publicReq := httptest.NewRequest(http.MethodPost, "/local/tutorial-admin/upload", publicBody)
	publicReq.Header.Set("Content-Type", publicContentType)
	publicReq.RemoteAddr = "203.0.113.20:40200"
	publicRec := httptest.NewRecorder()
	handler.HandleAdminUpload(publicRec, publicReq)
	if publicRec.Code != http.StatusForbidden {
		t.Fatalf("public status=%d body=%s", publicRec.Code, publicRec.Body.String())
	}

	localBody, localContentType := tutorialMultipartBody(t, "sample.png", []byte("png"))
	localReq := httptest.NewRequest(http.MethodPost, "/local/tutorial-admin/upload", localBody)
	localReq.Header.Set("Content-Type", localContentType)
	localReq.RemoteAddr = "127.0.0.1:40200"
	localRec := httptest.NewRecorder()
	handler.HandleAdminUpload(localRec, localReq)
	if localRec.Code != http.StatusOK {
		t.Fatalf("local status=%d body=%s", localRec.Code, localRec.Body.String())
	}
	var payload struct {
		File tutorialTaskFile `json:"file"`
	}
	if err := json.NewDecoder(localRec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode upload payload: %v", err)
	}
	if payload.File.Name != "sample.png" || !strings.HasPrefix(payload.File.URL, "/uploads/tutorial/") {
		t.Fatalf("unexpected upload payload: %+v", payload)
	}
}

func tutorialMultipartBody(t *testing.T, filename string, content []byte) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	return &body, writer.FormDataContentType()
}
