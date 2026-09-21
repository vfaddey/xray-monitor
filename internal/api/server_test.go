package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthRequiresBearerScheme(t *testing.T) {
	s := &Server{token: "123456789012345678901234"}
	handler := s.auth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, tc := range []struct {
		header string
		want   int
	}{
		{"", http.StatusUnauthorized},
		{"123456789012345678901234", http.StatusUnauthorized},
		{"Basic 123456789012345678901234", http.StatusUnauthorized},
		{"Bearer wrong", http.StatusUnauthorized},
		{"Bearer 123456789012345678901234", http.StatusNoContent},
	} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", tc.header)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != tc.want {
			t.Fatalf("header %q: got %d, want %d", tc.header, response.Code, tc.want)
		}
	}
}
