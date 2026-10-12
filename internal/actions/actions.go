// Package actions : les SEULES opérations qui modifient quelque chose.
// Elles ne suppriment, ne déplacent et ne renomment jamais de fichier :
// elles changent uniquement l'état « Fichiers à la demande » (épinglage),
// exactement comme les commandes officielles attrib +U -P / attrib +P -U
// ou les menus « Libérer de l'espace » / « Toujours conserver sur cet appareil ».
package actions

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	F "optimiseur-onedrive/internal/format"
	P "optimiseur-onedrive/internal/platform"
	"optimiseur-onedrive/internal/scan"
)

type Kind string

const (
	Free    Kind = "liberer"   // rendre disponible uniquement en ligne
	Keep    Kind = "conserver" // toujours conserver sur cet appareil (retélécharge)
	Restore Kind = "restaurer" // remettre l'épinglage d'avant une opération
)

var (
	ErrReadOnly    = errors.New("mode lecture seule : aucune modification n'est autorisée")
	ErrBadToken    = errors.New("récapitulatif inconnu, expiré ou déjà utilisé : recommencez la sélection")
	ErrNotConfirm  = errors.New("confirmation incorrecte : opération annulée, rien n'a été modifié")
	ErrRiskConfirm = errors.New("confirmation du risque manquante : opération annulée, rien n'a été modifié")
	ErrOneDriveOff = errors.New("OneDrive n'est pas démarré : il doit fonctionner pour appliquer ce réglage")
	ErrNothing     = errors.New("aucun dossier valide sélectionné")
)

type Manager struct {
	FS              P.FS
	Same            func(a, b string) bool
	Roots           func() []string // dossiers OneDrive actifs autorisés
	OneDriveRunning func() (running, known bool)
	DiskFree        func(path string) int64
	JournalDir      string
	ReadOnly        bool
	TokenTTL        time.Duration
	Now             func() time.Time

	mu       sync.Mutex
	previews map[string]*Preview
}

type Preview struct {
	Token       string    `json:"token"`
	Kind        Kind      `json:"kind"`
	Paths       []string  `json:"paths"`
	Rejected    []string  `json:"rejected"`
	Files       int64     `json:"files"`
	EstGain     int64     `json:"estGain"`     // espace libéré estimé (Free)
	EstDownload int64     `json:"estDownload"` // volume à retélécharger (Keep/Restore)
	PinnedFiles int64     `json:"pinnedFiles"`
	PinnedBytes int64     `json:"pinnedBytes"`
	OnlineFiles int64     `json:"onlineFiles"`
	NotCloud    int64     `json:"notCloudFiles"`
	Warnings    []string  `json:"warnings"`
	HighRisk    bool      `json:"highRisk"`
	ConfirmWord string    `json:"confirmWord"`
	RiskWord    string    `json:"riskWord,omitempty"`
	Expires     time.Time `json:"expires"`
	journal     string
	used        bool
}

type Run struct {
	ID           string           `json:"id"`
	Kind         Kind             `json:"kind"`
	Paths        []string         `json:"paths"`
	Started      time.Time        `json:"started"`
	Finished     time.Time        `json:"finished"`
	Seen         int64            `json:"seen"`
	Changed      int64            `json:"changed"`
	Already      int64            `json:"already"`
	NotCloud     int64            `json:"notCloud"`
	Errors       int64            `json:"errors"`
	ErrorSamples []string         `json:"errorSamples"`
	Canceled     bool             `json:"canceled"`
	LocalBefore  int64            `json:"localBefore"` // espace local des fichiers traités, avant
	DiskBefore   map[string]int64 `json:"diskBefore"`
	Journal      string           `json:"journal"`
	EstGain      int64            `json:"estGain"`
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func randomID(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// validate nettoie la sélection : chemins absolus situés dans un dossier
// OneDrive actif, sans doublons ni chemins imbriqués.
func (m *Manager) validate(paths []string) (ok, rejected []string) {
	roots := m.Roots()
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		c := filepath.Clean(p)
		if !filepath.IsAbs(c) {
			rejected = append(rejected, p+" (chemin non absolu)")
			continue
		}
		inside := false
		for _, r := range roots {
			if scan.IsWithin(c, r, m.Same) {
				inside = true
				break
			}
		}
		if !inside {
			rejected = append(rejected, p+" (hors des dossiers OneDrive synchronisés)")
			continue
		}
		if _, _, err := m.FS.Attrs(c); err != nil {
			rejected = append(rejected, p+" (introuvable)")
			continue
		}
		ok = append(ok, c)
	}
	sort.Slice(ok, func(i, j int) bool { return len(ok[i]) < len(ok[j]) })
	var out []string
	for _, p := range ok {
		dup := false
		for _, q := range out {
			if scan.IsWithin(p, q, m.Same) {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, p)
		}
	}
	return out, rejected
}

func (m *Manager) store(pv *Preview) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.previews == nil {
		m.previews = map[string]*Preview{}
	}
	m.previews[pv.Token] = pv
}

func (m *Manager) ttl() time.Duration {
	if m.TokenTTL > 0 {
		return m.TokenTTL
	}
	return 15 * time.Minute
}

// Prepare calcule le récapitulatif (en relisant le disque, sans rien modifier).
func (m *Manager) Prepare(ctx context.Context, kind Kind, paths []string) (*Preview, error) {
	if m.ReadOnly {
		return nil, ErrReadOnly
	}
	if kind != Free && kind != Keep {
		return nil, errors.New("type d'opération inconnu")
	}
	ok, rej := m.validate(paths)
	pv := &Preview{Token: randomID(16), Kind: kind, Paths: ok, Rejected: rej, Expires: m.now().Add(m.ttl())}
	if len(ok) == 0 {
		return pv, ErrNothing
	}
	for _, p := range ok {
		m.walk(ctx, p, func(path string, e P.Entry, alloc int64) {
			if e.IsDir {
				return
			}
			pv.Files++
			st := P.Classify(e.Attrs, e.ReparseTag)
			switch st {
			case P.StateNotCloud:
				pv.NotCloud++
			case P.StateOnline:
				pv.OnlineFiles++
				if kind == Keep {
					pv.EstDownload += e.Size
				}
			case P.StatePinned:
				pv.PinnedFiles++
				pv.PinnedBytes += alloc
				if kind == Free {
					pv.EstGain += alloc
				}
			case P.StateLocal:
				if kind == Free {
					pv.EstGain += alloc
				}
			}
		}, nil)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	pv.ConfirmWord = "LIBERER"
	if kind == Keep {
		pv.ConfirmWord = "CONSERVER"
	}
	if kind == Free {
		if pv.PinnedFiles > 0 {
			pv.HighRisk = true
			pv.Warnings = append(pv.Warnings, fmt.Sprintf("%s fichiers (%s) sont réglés sur « Toujours conserver sur cet appareil » : ils ne seront plus disponibles hors connexion.", F.Int(pv.PinnedFiles), F.Bytes(pv.PinnedBytes)))
		}
		if pv.EstGain >= 50<<30 {
			pv.HighRisk = true
			pv.Warnings = append(pv.Warnings, "Opération de grande ampleur : vérifiez que vous n'avez pas besoin de ces fichiers sans Internet.")
		}
		if pv.NotCloud > 0 {
			pv.Warnings = append(pv.Warnings, fmt.Sprintf("%s fichier(s) non géré(s) à la demande : ignoré(s), donc non modifié(s).", F.Int(pv.NotCloud)))
		}
	}
	if kind == Keep && m.DiskFree != nil {
		free := m.DiskFree(ok[0])
		if pv.EstDownload+(2<<30) > free {
			pv.HighRisk = true
			pv.Warnings = append(pv.Warnings, fmt.Sprintf("Espace insuffisant : %s à télécharger pour %s libres.", F.Bytes(pv.EstDownload), F.Bytes(free)))
		}
	}
	if pv.HighRisk {
		pv.RiskWord = "OUI"
	}
	if len(rej) > 0 {
		pv.Warnings = append(pv.Warnings, fmt.Sprintf("%d élément(s) refusé(s) et exclu(s) de l'opération.", len(rej)))
	}
	m.store(pv)
	return pv, nil
}

// walk parcourt un chemin (dossier ou fichier) sans suivre les liens.
func (m *Manager) walk(ctx context.Context, root string, visit func(path string, e P.Entry, alloc int64), onErr func(path string, err error)) {
	attrs, tag, err := m.FS.Attrs(root)
	if err != nil {
		if onErr != nil {
			onErr(root, err)
		}
		return
	}
	re := P.Entry{Name: filepath.Base(root), IsDir: attrs&P.AttrDirectory != 0, Attrs: attrs, ReparseTag: tag}
	if re.IsLink() {
		return
	}
	if !re.IsDir {
		// Un fichier seul : on relit sa taille via son dossier parent.
		ents, _ := m.FS.ReadDir(filepath.Dir(root))
		for _, e := range ents {
			if m.Same(e.Name, re.Name) && !e.IsDir {
				alloc, _ := m.FS.Allocated(root, e)
				visit(root, e, alloc)
				return
			}
		}
		visit(root, re, 0)
		return
	}
	visit(root, re, 0)
	var rec func(dir string)
	rec = func(dir string) {
		if ctx.Err() != nil {
			return
		}
		ents, err := m.FS.ReadDir(dir)
		if err != nil && onErr != nil {
			onErr(dir, err)
		}
		for _, e := range ents {
			if ctx.Err() != nil {
				return
			}
			if e.IsLink() {
				continue
			}
			p := filepath.Join(dir, e.Name)
			var alloc int64
			if !e.IsDir {
				alloc, _ = m.FS.Allocated(p, e)
			}
			visit(p, e, alloc)
			if e.IsDir {
				rec(p)
			}
		}
	}
	rec(root)
}

func (m *Manager) take(token string) (*Preview, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pv := m.previews[token]
	if pv == nil || pv.used || m.now().After(pv.Expires) {
		return nil, ErrBadToken
	}
	pv.used = true
	return pv, nil
}

type journalEntry struct {
	Path string `json:"p"`
	Prev uint32 `json:"prev"`
	New  uint32 `json:"new"`
	OK   bool   `json:"ok"`
	Err  string `json:"err,omitempty"`
}

type journalHeader struct {
	Run   string    `json:"run"`
	Kind  Kind      `json:"kind"`
	Paths []string  `json:"paths"`
	Start time.Time `json:"start"`
}

const pinBits = P.AttrPinned | P.AttrUnpinned

// Execute applique l'opération après double vérification de la confirmation.
func (m *Manager) Execute(ctx context.Context, token, typed, riskTyped string, progress func(*Run)) (*Run, error) {
	if m.ReadOnly {
		return nil, ErrReadOnly
	}
	m.mu.Lock()
	pv := m.previews[token]
	m.mu.Unlock()
	if pv == nil {
		return nil, ErrBadToken
	}
	if strings.ToUpper(strings.TrimSpace(typed)) != pv.ConfirmWord {
		return nil, ErrNotConfirm
	}
	if pv.HighRisk && strings.ToUpper(strings.TrimSpace(riskTyped)) != pv.RiskWord {
		return nil, ErrRiskConfirm
	}
	if m.OneDriveRunning != nil {
		if running, known := m.OneDriveRunning(); known && !running {
			return nil, ErrOneDriveOff
		}
	}
	pv, err := m.take(token)
	if err != nil {
		return nil, err
	}
	run := &Run{ID: m.now().Format("20060102-150405") + "-" + randomID(3), Kind: pv.Kind, Paths: pv.Paths,
		Started: m.now(), DiskBefore: map[string]int64{}, EstGain: pv.EstGain}
	if m.DiskFree != nil {
		for _, p := range pv.Paths {
			v := volumeKey(p)
			if _, ok := run.DiskBefore[v]; !ok {
				run.DiskBefore[v] = m.DiskFree(p)
			}
		}
	}
	jw, jpath, err := m.openJournal(run)
	if err != nil {
		return nil, fmt.Errorf("impossible de créer le journal des opérations (%v) : opération annulée par sécurité", err)
	}
	run.Journal = jpath
	defer jw.close()

	if pv.Kind == Restore {
		m.applyRestore(ctx, pv.journal, run, jw, progress)
	} else {
		for _, p := range pv.Paths {
			m.walk(ctx, p, func(path string, e P.Entry, alloc int64) {
				m.applyOne(path, e, alloc, pv.Kind, run, jw)
				if progress != nil && run.Seen%200 == 0 {
					progress(run)
				}
			}, func(path string, err error) {
				run.Errors++
				run.sample(path, err)
			})
		}
	}
	run.Canceled = ctx.Err() != nil
	run.Finished = m.now()
	if progress != nil {
		progress(run)
	}
	jw.footer(run)
	return run, nil
}

func (r *Run) sample(path string, err error) {
	if len(r.ErrorSamples) < 50 {
		r.ErrorSamples = append(r.ErrorSamples, path+" : "+scan.FriendlyError(err))
	}
}

func volumeKey(p string) string {
	if v := filepath.VolumeName(p); v != "" {
		return strings.ToUpper(v) + `\`
	}
	return "/"
}

func (m *Manager) applyOne(path string, e P.Entry, alloc int64, kind Kind, run *Run, jw *journal) {
	if !e.IsDir {
		run.Seen++
	}
	if !P.IsCloud(e.Attrs, e.ReparseTag) {
		if !e.IsDir {
			run.NotCloud++
		}
		return
	}
	var want uint32
	switch kind {
	case Free:
		want = e.Attrs&^P.AttrPinned | P.AttrUnpinned
		if !e.IsDir {
			st := P.Classify(e.Attrs, e.ReparseTag)
			if st == P.StateLocal || st == P.StatePinned {
				run.LocalBefore += alloc
			}
		}
	case Keep:
		want = e.Attrs&^P.AttrUnpinned | P.AttrPinned
	default:
		return
	}
	if want&pinBits == e.Attrs&pinBits {
		if !e.IsDir {
			run.Already++
		}
		return
	}
	m.setPin(path, e.Attrs, want, e.IsDir, run, jw)
}

func (m *Manager) setPin(path string, cur, want uint32, isDir bool, run *Run, jw *journal) {
	err := m.FS.SetAttrs(path, want)
	ent := journalEntry{Path: path, Prev: cur & pinBits, New: want & pinBits}
	if err == nil {
		// Vérification immédiate : l'attribut est-il bien en place ?
		now, _, rerr := m.FS.Attrs(path)
		if rerr != nil {
			err = rerr
		} else if now&pinBits != want&pinBits {
			err = errors.New("le réglage n'a pas été accepté par le système")
		}
	}
	if err != nil {
		ent.Err = scan.FriendlyError(err)
		run.Errors++
		run.sample(path, err)
	} else {
		ent.OK = true
		if !isDir {
			run.Changed++
		}
	}
	jw.write(ent)
}

// ---- journal

type journal struct {
	f *os.File
	w *bufio.Writer
	n int
}

func (m *Manager) openJournal(run *Run) (*journal, string, error) {
	dir := m.JournalDir
	if dir == "" {
		dir = os.TempDir()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, "", err
	}
	p := filepath.Join(dir, "operation-"+run.ID+".jsonl")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, "", err
	}
	j := &journal{f: f, w: bufio.NewWriter(f)}
	b, _ := json.Marshal(journalHeader{Run: run.ID, Kind: run.Kind, Paths: run.Paths, Start: run.Started})
	j.w.Write(append(b, '\n'))
	j.w.Flush()
	return j, p, nil
}

func (j *journal) write(e journalEntry) {
	b, _ := json.Marshal(e)
	j.w.Write(append(b, '\n'))
	j.n++
	if j.n%100 == 0 {
		j.w.Flush()
	}
}

func (j *journal) footer(r *Run) {
	b, _ := json.Marshal(map[string]any{"fin": r.Finished, "modifies": r.Changed, "erreurs": r.Errors, "annule": r.Canceled})
	j.w.Write(append(b, '\n'))
}

func (j *journal) close() {
	j.w.Flush()
	j.f.Close()
}

// ---- restauration

// PrepareRestore prépare le retour à l'épinglage d'avant une opération.
// Limite honnête : cela remet le RÉGLAGE ; les fichiers redeviennent locaux
// en étant retéléchargés (Internet et espace disque nécessaires).
func (m *Manager) PrepareRestore(journalPath string) (*Preview, error) {
	if m.ReadOnly {
		return nil, ErrReadOnly
	}
	entries, hdr, err := readJournal(journalPath)
	if err != nil {
		return nil, err
	}
	pv := &Preview{Token: randomID(16), Kind: Restore, Paths: hdr.Paths, journal: journalPath,
		Expires: m.now().Add(m.ttl()), ConfirmWord: "RESTAURER"}
	for _, e := range entries {
		if !e.OK {
			continue
		}
		pv.Files++
		if e.Prev&P.AttrPinned != 0 && hdr.Kind == Free {
			pv.PinnedFiles++
		}
	}
	pv.Warnings = append(pv.Warnings, "Les éléments retrouveront leur réglage d'avant l'opération. Ceux qui étaient « Toujours conserver » seront retéléchargés : Internet et de l'espace disque sont nécessaires.")
	if pv.PinnedFiles > 0 {
		pv.HighRisk = true
		pv.RiskWord = "OUI"
	}
	m.store(pv)
	return pv, nil
}

func readJournal(p string) ([]journalEntry, journalHeader, error) {
	var hdr journalHeader
	f, err := os.Open(p)
	if err != nil {
		return nil, hdr, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	first := true
	var out []journalEntry
	for sc.Scan() {
		if first {
			first = false
			if err := json.Unmarshal(sc.Bytes(), &hdr); err != nil || hdr.Run == "" {
				return nil, hdr, errors.New("journal illisible")
			}
			continue
		}
		var e journalEntry
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.Path != "" {
			out = append(out, e)
		}
	}
	return out, hdr, sc.Err()
}

func (m *Manager) applyRestore(ctx context.Context, jpath string, run *Run, jw *journal, progress func(*Run)) {
	entries, _, err := readJournal(jpath)
	if err != nil {
		run.Errors++
		run.sample(jpath, err)
		return
	}
	// Ordre inverse : fichiers avant leurs dossiers.
	for i := len(entries) - 1; i >= 0; i-- {
		if ctx.Err() != nil {
			return
		}
		e := entries[i]
		if !e.OK {
			continue
		}
		cur, _, err := m.FS.Attrs(e.Path)
		if err != nil {
			run.Errors++
			run.sample(e.Path, err)
			continue
		}
		isDir := cur&P.AttrDirectory != 0
		if !isDir {
			run.Seen++
		}
		if cur&pinBits != e.New {
			// Modifié depuis par l'utilisateur : on ne touche pas.
			if !isDir {
				run.Already++
			}
			continue
		}
		want := cur&^pinBits | e.Prev
		m.setPin(e.Path, cur, want, isDir, run, jw)
		if progress != nil && run.Seen%200 == 0 {
			progress(run)
		}
	}
}

// ---- vérification

type Verify struct {
	Files          int64            `json:"files"`
	StillLocal     int64            `json:"stillLocal"`
	StillLocalSize int64            `json:"stillLocalSize"`
	Online         int64            `json:"online"`
	PinnedNow      int64            `json:"pinnedNow"`
	Freed          int64            `json:"freed"` // estimation : avant - restant local
	DiskNow        map[string]int64 `json:"diskNow"`
	DiskDelta      map[string]int64 `json:"diskDelta"`
	Errors         int64            `json:"errors"`
	Examples       []string         `json:"examples"`
}

// VerifyRun remesure l'état réel des éléments traités.
func (m *Manager) VerifyRun(ctx context.Context, run *Run) *Verify {
	v := &Verify{DiskNow: map[string]int64{}, DiskDelta: map[string]int64{}}
	for _, p := range run.Paths {
		m.walk(ctx, p, func(path string, e P.Entry, alloc int64) {
			if e.IsDir {
				return
			}
			v.Files++
			switch P.Classify(e.Attrs, e.ReparseTag) {
			case P.StateOnline:
				v.Online++
			case P.StateLocal:
				v.StillLocal++
				v.StillLocalSize += alloc
				if len(v.Examples) < 20 && e.Attrs&P.AttrUnpinned != 0 {
					v.Examples = append(v.Examples, path)
				}
			case P.StatePinned:
				v.PinnedNow++
				v.StillLocal++
				v.StillLocalSize += alloc
			}
		}, func(string, error) { v.Errors++ })
	}
	if run.Kind == Free {
		v.Freed = run.LocalBefore - v.StillLocalSize
		if v.Freed < 0 {
			v.Freed = 0
		}
	}
	if m.DiskFree != nil {
		for vol, before := range run.DiskBefore {
			for _, p := range run.Paths {
				if volumeKey(p) == vol {
					now := m.DiskFree(p)
					v.DiskNow[vol] = now
					v.DiskDelta[vol] = now - before
					break
				}
			}
		}
	}
	return v
}

// ListJournals renvoie les journaux d'opérations existants (plus récents d'abord).
func ListJournals(dir string) []string {
	m, _ := filepath.Glob(filepath.Join(dir, "operation-*.jsonl"))
	sort.Sort(sort.Reverse(sort.StringSlice(m)))
	return m
}

// RunFromJournal reconstitue une opération passée (pour la vérifier).
func RunFromJournal(p string) (*Run, error) {
	_, hdr, err := readJournal(p)
	if err != nil {
		return nil, err
	}
	return &Run{ID: hdr.Run, Kind: hdr.Kind, Paths: hdr.Paths, Started: hdr.Start, Journal: p, DiskBefore: map[string]int64{}}, nil
}
