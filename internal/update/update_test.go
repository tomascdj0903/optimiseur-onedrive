package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func server(t *testing.T, exe []byte, hash string) *httptest.Server {
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/latest.json", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Manifest{Version: "1.2.0", Exe: srv.URL + "/exe", SHA256: hash})
	})
	mux.HandleFunc("/exe", func(w http.ResponseWriter, r *http.Request) { w.Write(exe) })
	mux.HandleFunc("/sha", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(hash + "  OptimiseurOneDrive.exe\n")) })
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestNewer(t *testing.T) {
	cases := map[[2]string]bool{{"v1.2.0", "1.1.9"}: true, {"1.2.0", "1.2.0"}: false, {"v1.10.0", "1.9.3"}: true,
		{"1.0.0", "1.0.1"}: false, {"garbage", "1.0.0"}: false, {"v2", "1.9.9"}: true}
	for c, want := range cases {
		if Newer(c[0], c[1]) != want {
			t.Errorf("Newer(%s,%s) != %v", c[0], c[1], want)
		}
	}
}

func TestInstallVerified(t *testing.T) {
	newExe := []byte("NOUVEAU PROGRAMME")
	h := sha256.Sum256(newExe)
	srv := server(t, newExe, hex.EncodeToString(h[:]))
	dir := t.TempDir()
	exe := filepath.Join(dir, "OptimiseurOneDrive.exe")
	os.WriteFile(exe, []byte("ANCIEN"), 0o755)
	u := &Updater{Manifest: srv.URL + "/latest.json", AssetName: "OptimiseurOneDrive.exe"}
	rel, err := u.Latest(context.Background())
	if err != nil || rel.Tag != "1.2.0" {
		t.Fatal(rel, err)
	}
	if err := u.Install(context.Background(), rel, exe, nil); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(exe)
	if string(b) != "NOUVEAU PROGRAMME" {
		t.Fatal("exécutable non remplacé")
	}
	Cleanup(exe)
	if _, err := os.Stat(exe + ".old"); !os.IsNotExist(err) {
		t.Fatal("ancienne version non nettoyée")
	}
}

func TestInstallRejectsBadHash(t *testing.T) {
	srv := server(t, []byte("PROGRAMME MODIFIE"), "0000000000000000000000000000000000000000000000000000000000000000")
	dir := t.TempDir()
	exe := filepath.Join(dir, "OptimiseurOneDrive.exe")
	os.WriteFile(exe, []byte("ANCIEN"), 0o755)
	u := &Updater{Manifest: srv.URL + "/latest.json", AssetName: "OptimiseurOneDrive.exe"}
	rel, _ := u.Latest(context.Background())
	if err := u.Install(context.Background(), rel, exe, nil); err == nil {
		t.Fatal("empreinte incorrecte acceptée")
	}
	b, _ := os.ReadFile(exe)
	if string(b) != "ANCIEN" {
		t.Fatal("l'exécutable d'origine a été altéré")
	}
	if _, err := os.Stat(exe + ".new"); !os.IsNotExist(err) {
		t.Fatal("fichier temporaire laissé")
	}
}
