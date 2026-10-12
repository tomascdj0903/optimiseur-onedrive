package main

import (
	"net/http/httptest"
	"testing"
)

func TestGUIAuthorization(t *testing.T) {
	a := &App{gui: &guiState{token: "secret", host: "127.0.0.1:5000"}}
	ok := httptest.NewRequest("GET", "http://127.0.0.1:5000/api/state", nil)
	ok.Header.Set("X-Token", "secret")
	if !a.authorized(ok) {
		t.Fatal("requête légitime refusée")
	}
	bad := httptest.NewRequest("GET", "http://127.0.0.1:5000/api/state", nil)
	bad.Header.Set("X-Token", "autre")
	if a.authorized(bad) {
		t.Fatal("mauvais jeton accepté")
	}
	rebind := httptest.NewRequest("GET", "http://evil.example:5000/api/state", nil)
	rebind.Header.Set("X-Token", "secret")
	if a.authorized(rebind) {
		t.Fatal("hôte étranger accepté (rebinding DNS)")
	}
	none := httptest.NewRequest("GET", "http://127.0.0.1:5000/api/state", nil)
	if a.authorized(none) {
		t.Fatal("requête sans jeton acceptée")
	}
}

func TestEmbeddedUI(t *testing.T) {
	if len(uiHTML) < 1000 {
		t.Fatal("interface non embarquée")
	}
}
