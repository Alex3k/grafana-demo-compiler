package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDeleteSessionRequiresExplicitConfirmation(t *testing.T) {
	for _, body := range []string{"", "{}", `{"confirm":false}`} {
		req := httptest.NewRequest(http.MethodDelete, "/api/sessions/example", strings.NewReader(body))
		response := httptest.NewRecorder()
		(&Server{}).deleteSession(response, req)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("unconfirmed delete returned %d", response.Code)
		}
	}
}
