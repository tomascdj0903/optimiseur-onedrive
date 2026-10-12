// Package scan parcourt les dossiers en lecture seule et agrège les tailles.
// Il ne modifie jamais rien et n'ouvre jamais le contenu des fichiers.
package scan

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	P "optimiseur-onedrive/internal/platform"
)

type TargetKind string

const (
	KindOneDrive TargetKind = "onedrive"           // dossier OneDrive actif
	KindLibrary  TargetKind = "bibliotheque"       // bibliothèque SharePoint / Teams synchronisée
	KindOrphan   TargetKind = "ancien_dossier"     // dossier « OneDrive… » non relié à un compte actif
	KindLegacy   TargetKind = "ancien_emplacement" // ancien emplacement local d'un dossier redirigé
	KindCache    TargetKind = "cache"              // données de l'application OneDrive
)

type Target struct {
	Path    string     `json:"path"`
	Kind    TargetKind `json:"kind"`
	Label   string     `json:"label"`
	Account string     `json:"account"`
	Source  string     `json:"source"`
	Active  bool       `json:"active"` // géré par un client OneDrive connecté
}

type StateAgg struct {
	Count   int64 `json:"count"`
	Logical int64 `json:"logical"`
	Alloc   int64 `json:"alloc"`
}

type Agg struct {
	Files         int64                 `json:"files"`
	Dirs          int64                 `json:"dirs"`
	Links         int64                 `json:"links"`
	Logical       int64                 `json:"logical"`
	Alloc         int64                 `json:"alloc"`
	States        [P.NumStates]StateAgg `json:"states"`
	OldLocalAlloc int64                 `json:"oldLocalAlloc"`
	Errors        int64                 `json:"errors"`
	Estimated     int64                 `json:"estimated"` // fichiers dont l'espace disque est estimé
}

func (a *Agg) Add(b *Agg) {
	a.Files += b.Files
	a.Dirs += b.Dirs
	a.Links += b.Links
	a.Logical += b.Logical
	a.Alloc += b.Alloc
	for i := range a.States {
		a.States[i].Count += b.States[i].Count
		a.States[i].Logical += b.States[i].Logical
		a.States[i].Alloc += b.States[i].Alloc
	}
	a.OldLocalAlloc += b.OldLocalAlloc
	a.Errors += b.Errors
	a.Estimated += b.Estimated
}

// Freeable : espace occupé par des fichiers OneDrive téléchargés (libérables).
func (a *Agg) Freeable() int64 {
	return a.States[P.StateLocal].Alloc + a.States[P.StatePinned].Alloc
}

type Node struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	Parent   *Node     `json:"-"`
	Children []*Node   `json:"-"`
	Attrs    uint32    `json:"attrs"`
	Tag      uint32    `json:"tag"`
	Link     bool      `json:"link"`
	ModTime  time.Time `json:"modTime"`
	Agg      Agg       `json:"agg"`
	ErrMsg   string    `json:"error,omitempty"`
}

// PinState renvoie l'état « Fichiers à la demande » du dossier lui-même.
func (n *Node) PinState() string {
	switch {
	case n.Attrs&P.AttrPinned != 0:
		return "toujours_conserver"
	case n.Attrs&P.AttrUnpinned != 0:
		return "en_ligne"
	case P.IsCloud(n.Attrs, n.Tag):
		return "standard"
	}
	return "non_cloud"
}

type FileRec struct {
	Path       string      `json:"path"`
	Name       string      `json:"name"`
	Size       int64       `json:"size"`
	Alloc      int64       `json:"alloc"`
	AllocExact bool        `json:"allocExact"`
	ModTime    time.Time   `json:"modTime"`
	State      P.FileState `json:"state"`
	Attrs      uint32      `json:"attrs"`
	Target     int         `json:"target"`
}

type CatAgg struct {
	Count    int64 `json:"count"`
	Logical  int64 `json:"logical"`
	Alloc    int64 `json:"alloc"`
	Freeable int64 `json:"freeable"`
}

type TargetResult struct {
	Target
	Exists        bool               `json:"exists"`
	Err           string             `json:"error,omitempty"`
	Root          *Node              `json:"-"`
	Cats          map[string]*CatAgg `json:"categories"`
	Conflicts     []FileRec          `json:"conflicts"`
	ConflictCount int64              `json:"conflictCount"`
	ConflictAlloc int64              `json:"conflictAlloc"`
	Volume        string             `json:"volume"`
}

type ScanError struct {
	Path string `json:"path"`
	Msg  string `json:"msg"`
}

type Result struct {
	Started      time.Time       `json:"started"`
	Finished     time.Time       `json:"finished"`
	Canceled     bool            `json:"canceled"`
	Targets      []*TargetResult `json:"targets"`
	Big          []FileRec       `json:"-"`
	BigTruncated bool            `json:"bigTruncated"`
	DupCands     []FileRec       `json:"-"`
	DupTruncated bool            `json:"dupTruncated"`
	Errors       []ScanError     `json:"errors"`
	ErrorCount   int64           `json:"errorCount"`
	Files        int64           `json:"files"`
	Dirs         int64           `json:"dirs"`
	OldCutoff    time.Time       `json:"oldCutoff"`
}

type Options struct {
	BigMin       int64
	MaxBig       int
	DupMin       int64
	MaxDup       int
	OldAge       time.Duration
	ComputerName string
	MaxErrors    int
	Now          time.Time
}

func DefaultOptions() Options {
	return Options{BigMin: 50 << 20, MaxBig: 20000, DupMin: 1 << 20, MaxDup: 400000,
		OldAge: 365 * 24 * time.Hour, MaxErrors: 500}
}

type Progress struct {
	Running     bool    `json:"running"`
	Phase       string  `json:"phase"`
	Target      string  `json:"target"`
	TargetIndex int     `json:"targetIndex"`
	TargetCount int     `json:"targetCount"`
	Path        string  `json:"path"`
	Files       int64   `json:"files"`
	Dirs        int64   `json:"dirs"`
	Bytes       int64   `json:"bytes"`
	Errors      int64   `json:"errors"`
	Elapsed     float64 `json:"elapsed"`
	Done        bool    `json:"done"`
	Canceled    bool    `json:"canceled"`
}

// Tracker partage la progression entre le scan et l'interface.
type Tracker struct {
	mu    sync.Mutex
	p     Progress
	start time.Time
}

func (t *Tracker) Snapshot() Progress {
	if t == nil {
		return Progress{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	p := t.p
	if p.Running {
		p.Elapsed = time.Since(t.start).Seconds()
	}
	return p
}

func (t *Tracker) update(f func(p *Progress)) {
	if t == nil {
		return
	}
	t.mu.Lock()
	f(&t.p)
	t.mu.Unlock()
}

type scanner struct {
	ctx     context.Context
	fs      P.FS
	opts    Options
	res     *Result
	tr      *Tracker
	cutoff  time.Time
	sinceUp int
	cname   string
	files   int64
	dirs    int64
	bytes   int64
}

// Run scanne les cibles. Annulation possible via ctx (résultat partiel marqué).
func Run(ctx context.Context, fs P.FS, targets []Target, opts Options, tr *Tracker, volumeOf func(string) string) *Result {
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	res := &Result{Started: time.Now(), OldCutoff: opts.Now.Add(-opts.OldAge)}
	s := &scanner{ctx: ctx, fs: fs, opts: opts, res: res, tr: tr, cutoff: res.OldCutoff,
		cname: strings.ToLower(opts.ComputerName)}
	if tr != nil {
		tr.mu.Lock()
		tr.start = time.Now()
		tr.p = Progress{Running: true, Phase: "Analyse des dossiers", TargetCount: len(targets)}
		tr.mu.Unlock()
	}
	for i, t := range targets {
		tr.update(func(p *Progress) { p.TargetIndex = i + 1; p.Target = t.Label })
		tres := &TargetResult{Target: t, Cats: map[string]*CatAgg{}}
		if volumeOf != nil {
			tres.Volume = volumeOf(t.Path)
		}
		res.Targets = append(res.Targets, tres)
		attrs, tag, err := fs.Attrs(t.Path)
		if err != nil {
			tres.Err = "Dossier introuvable ou inaccessible"
			continue
		}
		tres.Exists = true
		root := &Node{Name: t.Label, Path: t.Path, Attrs: attrs, Tag: tag}
		tres.Root = root
		s.walk(root, tres, i)
		if ctx.Err() != nil {
			break
		}
	}
	res.Canceled = ctx.Err() != nil
	res.Finished = time.Now()
	res.Files, res.Dirs = s.files, s.dirs
	for _, t := range res.Targets {
		if t.Root != nil {
			sortTree(t.Root)
		}
	}
	sort.Slice(res.Big, func(i, j int) bool { return res.Big[i].Alloc > res.Big[j].Alloc })
	tr.update(func(p *Progress) {
		p.Running, p.Done, p.Canceled = false, true, res.Canceled
		p.Files, p.Dirs, p.Bytes, p.Errors = s.files, s.dirs, s.bytes, res.ErrorCount
		p.Elapsed = res.Finished.Sub(res.Started).Seconds()
		if res.Canceled {
			p.Phase = "Analyse annulée"
		} else {
			p.Phase = "Analyse terminée"
		}
	})
	return res
}

func (s *scanner) addErr(path string, err error) {
	s.res.ErrorCount++
	if len(s.res.Errors) < s.opts.MaxErrors {
		s.res.Errors = append(s.res.Errors, ScanError{Path: path, Msg: FriendlyError(err)})
	}
}

// FriendlyError traduit les erreurs système en messages compréhensibles.
func FriendlyError(err error) string {
	if err == nil {
		return ""
	}
	m := strings.ToLower(err.Error())
	switch {
	case strings.Contains(m, "denied") || strings.Contains(m, "refusé") || strings.Contains(m, "permission"):
		return "Accès refusé (autorisations Windows insuffisantes)"
	case strings.Contains(m, "used by another") || strings.Contains(m, "utilisé par un autre") || strings.Contains(m, "sharing"):
		return "Fichier utilisé par un autre programme"
	case strings.Contains(m, "not find") || strings.Contains(m, "introuvable") || strings.Contains(m, "no such"):
		return "Élément introuvable (supprimé ou déplacé pendant l'analyse)"
	case strings.Contains(m, "cloud") || strings.Contains(m, "provider"):
		return "Le fournisseur cloud (OneDrive) n'a pas répondu"
	}
	return err.Error()
}

func (s *scanner) progress(path string) {
	s.sinceUp++
	if s.sinceUp < 200 {
		return
	}
	s.sinceUp = 0
	files, dirs, bytes, errs := s.files, s.dirs, s.bytes, s.res.ErrorCount
	s.tr.update(func(p *Progress) {
		p.Path, p.Files, p.Dirs, p.Bytes, p.Errors = path, files, dirs, bytes, errs
	})
}

func (s *scanner) walk(n *Node, t *TargetResult, ti int) {
	if s.ctx.Err() != nil {
		return
	}
	entries, err := s.fs.ReadDir(n.Path)
	if err != nil {
		n.ErrMsg = FriendlyError(err)
		n.Agg.Errors++
		s.addErr(n.Path, err)
	}
	for _, e := range entries {
		p := filepath.Join(n.Path, e.Name)
		if e.IsDir {
			c := &Node{Name: e.Name, Path: p, Parent: n, Attrs: e.Attrs, Tag: e.ReparseTag, ModTime: e.ModTime}
			n.Children = append(n.Children, c)
			if e.IsLink() {
				c.Link = true
				n.Agg.Links++
				continue
			}
			n.Agg.Dirs++
			s.dirs++
			s.walk(c, t, ti)
			n.Agg.Add(&c.Agg)
			if s.ctx.Err() != nil {
				return
			}
			continue
		}
		if e.IsLink() {
			n.Agg.Links++
			continue
		}
		s.files++
		st := P.Classify(e.Attrs, e.ReparseTag)
		alloc, exact := s.fs.Allocated(p, e)
		s.bytes += alloc
		a := &n.Agg
		a.Files++
		a.Logical += e.Size
		a.Alloc += alloc
		if !exact {
			a.Estimated++
		}
		a.States[st].Count++
		a.States[st].Logical += e.Size
		a.States[st].Alloc += alloc
		freeable := int64(0)
		if st == P.StateLocal || st == P.StatePinned {
			freeable = alloc
			if !e.ModTime.IsZero() && e.ModTime.Before(s.cutoff) {
				a.OldLocalAlloc += alloc
			}
		}
		cat := Category(e.Name)
		ca := t.Cats[cat]
		if ca == nil {
			ca = &CatAgg{}
			t.Cats[cat] = ca
		}
		ca.Count++
		ca.Logical += e.Size
		ca.Alloc += alloc
		ca.Freeable += freeable
		var rec *FileRec
		mk := func() FileRec {
			if rec == nil {
				rec = &FileRec{Path: p, Name: e.Name, Size: e.Size, Alloc: alloc, AllocExact: exact,
					ModTime: e.ModTime, State: st, Attrs: e.Attrs, Target: ti}
			}
			return *rec
		}
		if e.Size >= s.opts.BigMin || alloc >= s.opts.BigMin {
			if len(s.res.Big) < s.opts.MaxBig {
				s.res.Big = append(s.res.Big, mk())
			} else {
				s.res.BigTruncated = true
			}
		}
		if e.Size >= s.opts.DupMin {
			if len(s.res.DupCands) < s.opts.MaxDup {
				s.res.DupCands = append(s.res.DupCands, mk())
			} else {
				s.res.DupTruncated = true
			}
		}
		if s.cname != "" && IsConflictCopy(e.Name, s.cname) {
			t.ConflictCount++
			t.ConflictAlloc += alloc
			if len(t.Conflicts) < 200 {
				t.Conflicts = append(t.Conflicts, mk())
			}
		}
		s.progress(p)
	}
}

func sortTree(n *Node) {
	sort.Slice(n.Children, func(i, j int) bool {
		if n.Children[i].Agg.Alloc != n.Children[j].Agg.Alloc {
			return n.Children[i].Agg.Alloc > n.Children[j].Agg.Alloc
		}
		return n.Children[i].Name < n.Children[j].Name
	})
	for _, c := range n.Children {
		sortTree(c)
	}
}

// IsConflictCopy reconnaît les copies créées par OneDrive lors d'un conflit
// de synchronisation : « Rapport-NOMDUPC.docx » ou « Rapport-NOMDUPC-2.docx ».
func IsConflictCopy(name, computer string) bool {
	stem := strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name)))
	suffix := "-" + strings.ToLower(computer)
	if strings.HasSuffix(stem, suffix) && len(stem) > len(suffix) {
		return true
	}
	i := strings.LastIndex(stem, suffix+"-")
	if i <= 0 {
		return false
	}
	rest := stem[i+len(suffix)+1:]
	if rest == "" {
		return false
	}
	for _, c := range rest {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

var catByExt = map[string]string{}

func init() {
	groups := map[string]string{
		"video":      "mp4 mov avi mkv wmv m4v mts m2ts 3gp webm mpg mpeg flv vob",
		"image":      "jpg jpeg png heic heif gif bmp tif tiff raw cr2 cr3 nef arw dng orf rw2 webp psd ai svg",
		"audio":      "mp3 wav flac m4a aac ogg wma aiff aif opus",
		"archive":    "zip rar 7z tar gz tgz bz2 xz cab",
		"disque":     "iso img vhd vhdx vmdk dmg ova",
		"document":   "pdf doc docx docm xls xlsx xlsm ppt pptx odt ods odp txt rtf csv one onepkg md epub",
		"messagerie": "pst ost",
		"programme":  "exe msi dll apk",
	}
	for cat, list := range groups {
		for _, e := range strings.Fields(list) {
			catByExt["."+e] = cat
		}
	}
}

// Category classe un fichier selon son extension.
func Category(name string) string {
	if c, ok := catByExt[strings.ToLower(filepath.Ext(name))]; ok {
		return c
	}
	return "autre"
}

// Find retrouve un nœud par chemin dans les résultats.
func (r *Result) Find(path string, same func(a, b string) bool) (*Node, *TargetResult) {
	for _, t := range r.Targets {
		if t.Root == nil {
			continue
		}
		if !IsWithin(path, t.Root.Path, same) {
			continue
		}
		n := t.Root
		if same(n.Path, path) {
			return n, t
		}
	descend:
		for {
			for _, c := range n.Children {
				if same(c.Path, path) {
					return c, t
				}
				if !c.Link && IsWithin(path, c.Path, same) {
					n = c
					continue descend
				}
			}
			return nil, t
		}
	}
	return nil, nil
}

// IsWithin indique si path est égal à root ou situé dedans.
func IsWithin(path, root string, same func(a, b string) bool) bool {
	path, root = filepath.Clean(path), filepath.Clean(root)
	if same(path, root) {
		return true
	}
	if len(path) <= len(root) {
		return false
	}
	sep := string(filepath.Separator)
	prefix := root
	if !strings.HasSuffix(prefix, sep) {
		prefix += sep
	}
	return same(path[:len(prefix)], prefix)
}
