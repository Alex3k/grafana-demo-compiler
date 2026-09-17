package httpapi

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Alex3k/grafana-demo-compiler/internal/application"
)

func TestWriteFaultMapsApplicationCodes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		code application.FaultCode
		want int
	}{
		{application.FaultInvalid, http.StatusBadRequest},
		{application.FaultTooLarge, http.StatusRequestEntityTooLarge},
		{application.FaultNotFound, http.StatusNotFound},
		{application.FaultConflict, http.StatusConflict},
		{application.FaultUnprocessable, http.StatusUnprocessableEntity},
		{application.FaultBadGateway, http.StatusBadGateway},
		{application.FaultInternal, http.StatusInternalServerError},
	}
	server := &Server{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	for _, test := range tests {
		recorder := httptest.NewRecorder()
		server.writeFault(recorder, &application.Fault{Code: test.code, Public: "public", Cause: errors.New("private")})
		if recorder.Code != test.want {
			t.Errorf("writeFault(%q) status = %d, want %d", test.code, recorder.Code, test.want)
		}
		if got := recorder.Body.String(); got != "{\"error\":\"public\"}\n" {
			t.Errorf("writeFault(%q) body = %q", test.code, got)
		}
	}
}
