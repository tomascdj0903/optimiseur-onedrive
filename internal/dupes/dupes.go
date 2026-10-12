// Package dupes recherche les doublons par empreinte SHA-256, en lecture seule.
// Les fichiers « en ligne uniquement » ne sont JAMAIS lus (cela déclencherait
// leur téléchargement) : ils sont rapprochés par nom et taille, sans garantie.
package dupes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	P "optimiseur-onedrive/internal/platform"
	"optimiseur-onedrive/internal/scan"
)

type Group struct {
	Hash     string         `json:"hash"`
	Size     int64          `json:"size"`
	Files    []scan.FileRec `json:"files"`
	Verified bool           `json:"verified"`
	Gain     int64          `json:"gain"` // espace local redondant (toutes les copies sauf une)
}

type Result struct {
	Groups     []Group `json:"groups"`
	Unverified []Group `json:"unverified"`
	Gain       int64   `json:"gain"`
	Hashed     int64   `json:"hashed"`
	BytesRead  int64   `json:"bytesRead"`
	Errors     int     `json:"errors"`
	Complete   bool    `json:"complete"`
	Seconds    float64 `json:"seconds"`
}

type Progress struct {
	Running   bool    `json:"running"`
	Phase     string  `json:"phase"`
	Done      int64   `json:"done"`
	Total     int64   `json:"total"`
	BytesRead int64   `json:"bytesRead"`
	Elapsed   float64 `json:"elapsed"`
}

type Tracker struct {
	mu sync.Mutex
	p  Progress
	t0 time.Time
}

func (t *Tracker) Snapshot() Progress {
	t.mu.Lock()
	defer t.mu.Unlock()
	p := t.p
	if p.Running {
		p.Elapsed = time.Since(t.t0).Seconds()
	}
	return p
}

func (t *Tracker) set(f func(*Progress)) {
	if t == nil {
		return
	}
	t.mu.Lock()
	f(&t.p)
	t.mu.Unlock()
}

const edge = 64 << 10

// readable : on ne lit que les fichiers entièrement présents sur le disque.
func readable(fs P.FS, f scan.FileRec) bool {
	attrs, tag, err := fs.Attrs(f.Path) // état actuel (il a pu changer depuis le scan)
	if err != nil {
		return false
	}
	st := P.Classify(attrs, tag)
	if st == P.StateOnline {
		return false
	}
	if st != P.StateNotCloud && f.AllocExact && f.Alloc < f.Size-int64(4096) {
		return false // partiellement téléchargé : la lecture compléterait le téléchargement
	}
	return true
}

func hashFile(ctx context.Context, fs P.FS, path string, quick bool, size int64, read *int64) (string, error) {
	r, err := fs.OpenRead(path)
	if err != nil {
		return "", err
	}
	defer r.Close()
	h := sha256.New()
	buf := make([]byte, 1<<20)
	var limit int64 = size
	if quick && size > 2*edge {
		limit = edge
	}
	var done int64
	for done < limit {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		n := len(buf)
		if int64(n) > limit-done {
			n = int(limit - done)
		}
		m, err := io.ReadFull(r, buf[:n])
		h.Write(buf[:m])
		done += int64(m)
		*read += int64(m)
		if err != nil {
			if err == io.ErrUnexpectedEOF || err == io.EOF {
				break
			}
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Find regroupe par taille, puis empreinte rapide (64 Ko), puis SHA-256 complet.
func Find(ctx context.Context, fs P.FS, cands []scan.FileRec, tr *Tracker) *Result {
	t0 := time.Now()
	if tr != nil {
		tr.mu.Lock()
		tr.t0 = t0
		tr.p = Progress{Running: true, Phase: "Regroupement par taille"}
		tr.mu.Unlock()
	}
	res := &Result{}
	bySize := map[int64][]scan.FileRec{}
	for _, c := range cands {
		bySize[c.Size] = append(bySize[c.Size], c)
	}
	var sizes []int64
	var total int64
	for s, l := range bySize {
		if len(l) > 1 {
			sizes = append(sizes, s)
			total += int64(len(l))
		}
	}
	sort.Slice(sizes, func(i, j int) bool { return sizes[i] > sizes[j] })
	tr.set(func(p *Progress) { p.Phase = "Comparaison du contenu (SHA-256)"; p.Total = total })
	var done int64
	for _, s := range sizes {
		if ctx.Err() != nil {
			break
		}
		var local, online []scan.FileRec
		for _, f := range bySize[s] {
			if readable(fs, f) {
				local = append(local, f)
			} else {
				online = append(online, f)
			}
		}
		// Empreinte rapide puis complète.
		quick := map[string][]scan.FileRec{}
		if len(local) > 1 {
			for _, f := range local {
				hq, err := hashFile(ctx, fs, f.Path, true, f.Size, &res.BytesRead)
				if err != nil {
					if ctx.Err() == nil {
						res.Errors++
					}
					continue
				}
				quick[hq] = append(quick[hq], f)
			}
		}
		for _, l := range quick {
			if len(l) < 2 {
				continue
			}
			full := map[string][]scan.FileRec{}
			for _, f := range l {
				var hf string
				var err error
				if f.Size <= 2*edge {
					hf, err = hashFile(ctx, fs, f.Path, false, f.Size, &res.BytesRead)
				} else {
					hf, err = hashFile(ctx, fs, f.Path, false, f.Size, &res.BytesRead)
				}
				if err != nil {
					if ctx.Err() == nil {
						res.Errors++
					}
					continue
				}
				res.Hashed++
				full[hf] = append(full[hf], f)
			}
			for h, g := range full {
				if len(g) < 2 {
					continue
				}
				grp := Group{Hash: h, Size: s, Files: g, Verified: true, Gain: redundant(g)}
				res.Groups = append(res.Groups, grp)
				res.Gain += grp.Gain
			}
		}
		// Non vérifiables : même nom + même taille, au moins une copie en ligne.
		if len(online) > 0 {
			byName := map[string][]scan.FileRec{}
			for _, f := range append(append([]scan.FileRec{}, local...), online...) {
				byName[strings.ToLower(f.Name)] = append(byName[strings.ToLower(f.Name)], f)
			}
			for _, g := range byName {
				hasOnline := false
				for _, f := range g {
					if f.State == P.StateOnline {
						hasOnline = true
					}
				}
				if len(g) > 1 && hasOnline {
					res.Unverified = append(res.Unverified, Group{Size: s, Files: g})
				}
			}
		}
		done += int64(len(bySize[s]))
		d, br := done, res.BytesRead
		tr.set(func(p *Progress) { p.Done = d; p.BytesRead = br })
	}
	sort.Slice(res.Groups, func(i, j int) bool { return res.Groups[i].Gain > res.Groups[j].Gain })
	res.Complete = ctx.Err() == nil
	res.Seconds = time.Since(t0).Seconds()
	tr.set(func(p *Progress) {
		p.Running = false
		if res.Complete {
			p.Phase = "Recherche terminée"
		} else {
			p.Phase = "Recherche annulée"
		}
	})
	return res
}

// redundant : espace local des copies au-delà d'une seule (on garde la plus grosse
// présence locale).
func redundant(g []scan.FileRec) int64 {
	var sum, max int64
	for _, f := range g {
		sum += f.Alloc
		if f.Alloc > max {
			max = f.Alloc
		}
	}
	return sum - max
}
