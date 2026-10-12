// Package update : mise à jour automatique depuis les « releases » GitHub.
// Le nouvel exécutable n'est installé que si son empreinte SHA-256 correspond
// à celle publiée dans la même release.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

type Release struct {
	Tag    string  `json:"tag_name"`
	Name   string  `json:"name"`
	Body   string  `json:"body"`
	Assets []Asset `json:"assets"`
}

type Updater struct {
	API       string // https://api.github.com/repos/<owner>/<repo>
	AssetName string // OptimiseurOneDrive.exe
	HTTP      *http.Client
}

func (u *Updater) client() *http.Client {
	if u.HTTP != nil {
		return u.HTTP
	}
	return &http.Client{Timeout: 10 * time.Minute}
}

// Latest interroge la dernière version publiée.
func (u *Updater) Latest(ctx context.Context) (*Release, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", u.API+"/releases/latest", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "optimiseur-onedrive")
	resp, err := u.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, errors.New("aucune version publiée")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("réponse GitHub %d", resp.StatusCode)
	}
	var r Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&r); err != nil {
		return nil, err
	}
	return &r, nil
}

func parse(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+ "); i >= 0 {
		v = v[:i]
	}
	var out []int
	for _, p := range strings.Split(v, ".") {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		out = append(out, n)
	}
	return out
}

// Newer indique si candidate > current (versions x.y.z).
func Newer(candidate, current string) bool {
	a, b := parse(candidate), parse(current)
	if a == nil || b == nil {
		return false
	}
	for len(a) < 3 {
		a = append(a, 0)
	}
	for len(b) < 3 {
		b = append(b, 0)
	}
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

func (r *Release) asset(name string) *Asset {
	for i := range r.Assets {
		if strings.EqualFold(r.Assets[i].Name, name) {
			return &r.Assets[i]
		}
	}
	return nil
}

func (u *Updater) get(ctx context.Context, url string, w io.Writer, limit int64, progress func(done, total int64)) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("User-Agent", "optimiseur-onedrive")
	resp, err := u.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("téléchargement refusé (HTTP %d)", resp.StatusCode)
	}
	total := resp.ContentLength
	buf := make([]byte, 256<<10)
	var done int64
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			done += int64(n)
			if done > limit {
				return errors.New("fichier anormalement gros")
			}
			if _, werr := w.Write(buf[:n]); werr != nil {
				return werr
			}
			if progress != nil {
				progress(done, total)
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// ExpectedHash lit l'empreinte publiée (fichier « <asset>.sha256 »).
func (u *Updater) ExpectedHash(ctx context.Context, r *Release) (string, error) {
	a := r.asset(u.AssetName + ".sha256")
	if a == nil {
		return "", errors.New("empreinte SHA-256 absente de la version publiée : mise à jour refusée")
	}
	var sb strings.Builder
	if err := u.get(ctx, a.URL, &sb, 4096, nil); err != nil {
		return "", err
	}
	f := strings.Fields(sb.String())
	for _, x := range f {
		if len(x) == 64 {
			if _, err := hex.DecodeString(x); err == nil {
				return strings.ToLower(x), nil
			}
		}
	}
	return "", errors.New("empreinte SHA-256 illisible")
}

// Install télécharge, vérifie et remplace l'exécutable courant.
// L'ancien est conservé sous « .old » jusqu'au prochain démarrage.
func (u *Updater) Install(ctx context.Context, r *Release, exePath string, progress func(done, total int64)) error {
	a := r.asset(u.AssetName)
	if a == nil {
		return errors.New("fichier du programme absent de la version publiée")
	}
	want, err := u.ExpectedHash(ctx, r)
	if err != nil {
		return err
	}
	tmp := exePath + ".new"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return fmt.Errorf("dossier du programme non modifiable (%v)", err)
	}
	h := sha256.New()
	err = u.get(ctx, a.URL, io.MultiWriter(f, h), 200<<20, progress)
	cerr := f.Close()
	if err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		os.Remove(tmp)
		return fmt.Errorf("empreinte incorrecte (attendu %s…, reçu %s…) : mise à jour refusée", want[:12], got[:12])
	}
	old := exePath + ".old"
	os.Remove(old)
	if err := os.Rename(exePath, old); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("impossible de remplacer le programme (%v)", err)
	}
	if err := os.Rename(tmp, exePath); err != nil {
		os.Rename(old, exePath) // retour arrière
		return fmt.Errorf("impossible d'installer la nouvelle version (%v)", err)
	}
	return nil
}

// Cleanup supprime l'ancienne version laissée par une mise à jour précédente.
func Cleanup(exePath string) {
	os.Remove(exePath + ".old")
	os.Remove(exePath + ".new")
}

// ExePath renvoie le chemin réel de l'exécutable.
func ExePath() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return p, nil
}
