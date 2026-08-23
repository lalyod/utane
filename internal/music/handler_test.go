package music

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestManageRoutesArePublic(t *testing.T) {
	handler := &Handler{}
	request := httptest.NewRequest(http.MethodDelete, "/artists/not-a-uuid", nil)
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestWriteServiceError(t *testing.T) {
	tests := []struct {
		err    error
		status int
	}{
		{ErrNotFound, http.StatusNotFound},
		{errors.Join(errors.New("details"), ErrConflict), http.StatusConflict},
		{ErrInvalid, http.StatusBadRequest},
		{ErrUpstream, http.StatusBadGateway},
		{errors.New("database unavailable"), http.StatusInternalServerError},
	}

	for _, test := range tests {
		response := httptest.NewRecorder()
		writeServiceError(response, test.err)
		if response.Code != test.status {
			t.Errorf("error %q: status = %d, want %d", test.err, response.Code, test.status)
		}
	}
}

func TestValidUploadType(t *testing.T) {
	if !validUploadType("audio/mpeg", "audio") {
		t.Error("audio/mpeg should be accepted")
	}
	if !validUploadType("image/jpeg; charset=binary", "image") {
		t.Error("image/jpeg should be accepted")
	}
	if validUploadType("image/svg+xml", "image") {
		t.Error("SVG should be rejected")
	}
	if validUploadType("text/html", "audio") {
		t.Error("HTML should be rejected")
	}
}

func TestValidUploadRejectsHTMLBody(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "upload")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	if _, err := file.WriteString("<!doctype html><script>alert(1)</script>"); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	if validUpload(file, "audio/mpeg", "audio") {
		t.Error("HTML body should be rejected even with an audio content type")
	}
}

func TestValidUploadRejectsFakeImage(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "upload")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	if _, err := file.WriteString("%PDF-1.7 fake image"); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	if validUpload(file, "image/png", "image") {
		t.Error("PDF body should be rejected even with an image content type")
	}
}
