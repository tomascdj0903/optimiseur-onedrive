package share

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestShareFlow(t *testing.T) {
	var mu sync.Mutex
	calls := map[string]int{}
	lines := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("apikey") != "cle" {
			w.WriteHeader(401)
			return
		}
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		json.Unmarshal(b, &body)
		mu.Lock()
		defer mu.Unlock()
		calls[r.URL.Path]++
		if r.URL.Path == "/rest/v1/rpc/odo_push_logs" {
			lines += len(body["p_lines"].([]any))
		}
		w.WriteHeader(204)
	}))
	defer srv.Close()
	c := New(srv.URL, "cle")
	if len(c.Code) != 6 || len(c.SessionID) != 36 {
		t.Fatalf("identifiants : %s %s", c.Code, c.SessionID)
	}
	c.Log("info", "ignoré avant démarrage")
	if err := c.Start(context.Background(), "1.0.0", "PC", "Windows 11", false, nil); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 450; i++ {
		c.Log("info", "ligne")
	}
	if err := c.Report(context.Background(), "diagnostic", "texte", map[string]int{"a": 1}); err != nil {
		t.Fatal(err)
	}
	c.Close(5 * time.Second)
	mu.Lock()
	defer mu.Unlock()
	if lines != 450 || calls["/rest/v1/rpc/odo_start_session"] != 1 || calls["/rest/v1/rpc/odo_push_report"] != 1 {
		t.Fatalf("lignes=%d appels=%v", lines, calls)
	}
}

func TestShareServerDown(t *testing.T) {
	c := New("http://127.0.0.1:1", "cle")
	if err := c.Start(context.Background(), "1", "PC", "W", false, nil); err == nil {
		t.Fatal("erreur attendue")
	}
	c.Log("info", "x") // ne doit pas bloquer ni paniquer
	if c.Enabled() {
		t.Fatal("ne doit pas être actif")
	}
}
