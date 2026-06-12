package server

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	tutorialAdminToken = "catsco_tutorial_admin:catsco_tutorial_2026"
	tutorialMaxUpload = 50 << 20
)

type TutorialTaskHandler struct {
	baseDir string
	baseURL string
}

func NewTutorialTaskHandler(baseDir, baseURL string) *TutorialTaskHandler {
	_ = os.MkdirAll(filepath.Join(baseDir, "tutorial"), 0755)
	return &TutorialTaskHandler{baseDir: baseDir, baseURL: baseURL}
}

func (h *TutorialTaskHandler) HandleTasks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		raw, err := os.ReadFile(filepath.Join(h.baseDir, "tutorial", "tasks.json"))
		if os.IsNotExist(err) {
			WriteJSONPublic(w, http.StatusOK, map[string]interface{}{"tasks": []interface{}{}, "limit": 6})
			return
		}
		if err != nil {
			WriteJSONPublic(w, http.StatusInternalServerError, map[string]string{"error": "read failed"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(raw)
	case http.MethodPut:
		if !isTutorialAdmin(r) {
			WriteJSONPublic(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		var payload map[string]interface{}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&payload); err != nil {
			WriteJSONPublic(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		raw, _ := json.MarshalIndent(payload, "", "  ")
		path := filepath.Join(h.baseDir, "tutorial", "tasks.json")
		_ = os.MkdirAll(filepath.Dir(path), 0755)
		if err := os.WriteFile(path, raw, 0644); err != nil {
			WriteJSONPublic(w, http.StatusInternalServerError, map[string]string{"error": "save failed"})
			return
		}
		WriteJSONPublic(w, http.StatusOK, payload)
	default:
		WriteJSONPublic(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (h *TutorialTaskHandler) HandleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteJSONPublic(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !isTutorialAdmin(r) {
		WriteJSONPublic(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, tutorialMaxUpload)
	if err := r.ParseMultipartForm(tutorialMaxUpload); err != nil {
		WriteJSONPublic(w, http.StatusBadRequest, map[string]string{"error": "file too large"})
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		WriteJSONPublic(w, http.StatusBadRequest, map[string]string{"error": "no file"})
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	if !allowedFileExts[ext] {
		WriteJSONPublic(w, http.StatusBadRequest, map[string]string{"error": "file type not allowed"})
		return
	}
	name := generateFileKey(ext)
	dstPath := filepath.Join(h.baseDir, "tutorial", name)
	dst, err := os.Create(dstPath)
	if err != nil {
		WriteJSONPublic(w, http.StatusInternalServerError, map[string]string{"error": "upload failed"})
		return
	}
	defer dst.Close()
	size, err := io.Copy(dst, file)
	if err != nil {
		WriteJSONPublic(w, http.StatusInternalServerError, map[string]string{"error": "upload failed"})
		return
	}
	WriteJSONPublic(w, http.StatusOK, map[string]interface{}{"url": h.baseURL + "/tutorial/" + name, "name": header.Filename, "size": size})
}

func isTutorialAdmin(r *http.Request) bool {
	user, pass, ok := r.BasicAuth()
	return (ok && user+":"+pass == tutorialAdminToken) || r.Header.Get("X-Tutorial-Admin-Token") == tutorialAdminToken
}
