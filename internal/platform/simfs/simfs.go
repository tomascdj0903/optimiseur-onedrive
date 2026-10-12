// Package simfs est un système de fichiers en mémoire qui simule le
// comportement de OneDrive Fichiers à la demande. Il sert uniquement aux tests
// automatisés : il n'est jamais utilisé par l'application livrée.
package simfs

import (
	"errors"
	"io"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	P "optimiseur-onedrive/internal/platform"
)

type node struct {
	name     string
	dir      bool
	size     int64
	alloc    int64
	attrs    uint32
	tag      uint32
	mtime    time.Time
	seed     string
	children map[string]*node
	link     bool
	inSync   bool
}

type FS struct {
	mu            sync.Mutex
	root          *node
	DenyRead      map[string]bool // ReadDir échoue (accès refusé)
	DenySet       map[string]bool // SetAttrs échoue
	Locked        map[string]bool // OneDrive refuse de libérer (fichier ouvert)
	Hydrations    int             // nombre de fichiers en ligne téléchargés par une lecture
	SetCalls      int
	AutoDehydrate bool // simule OneDrive en marche
}

func New() *FS {
	return &FS{root: &node{dir: true, children: map[string]*node{}}, DenyRead: map[string]bool{},
		DenySet: map[string]bool{}, Locked: map[string]bool{}, AutoDehydrate: true}
}

func split(p string) []string {
	p = path.Clean("/" + p)
	if p == "/" {
		return nil
	}
	return strings.Split(p[1:], "/")
}

func (f *FS) lookup(p string) *node {
	n := f.root
	for _, part := range split(p) {
		if n.children == nil {
			return nil
		}
		n = n.children[part]
		if n == nil {
			return nil
		}
	}
	return n
}

func (f *FS) mkdirAll(p string) *node {
	n := f.root
	for _, part := range split(p) {
		c := n.children[part]
		if c == nil {
			c = &node{name: part, dir: true, attrs: P.AttrDirectory, children: map[string]*node{}}
			n.children[part] = c
		}
		n = c
	}
	return n
}

// CloudDir crée un dossier géré par OneDrive (espace réservé cloud).
func (f *FS) CloudDir(p string, pinned bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := f.mkdirAll(p)
	n.attrs = P.AttrDirectory | P.AttrReparsePoint
	n.tag = P.TagCloud
	if pinned {
		n.attrs |= P.AttrPinned
	}
}

// AddFile ajoute un fichier dans l'état demandé.
func (f *FS) AddFile(p string, size int64, st P.FileState, mtime time.Time, seed string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	dir, name := path.Split(path.Clean("/" + p))
	parent := f.mkdirAll(dir)
	n := &node{name: name, size: size, mtime: mtime, seed: seed, inSync: true}
	switch st {
	case P.StateNotCloud:
		n.attrs = P.AttrArchive
		n.alloc = roundUp(size)
	case P.StateLocal:
		n.attrs = P.AttrArchive | P.AttrReparsePoint | P.AttrSparse
		n.tag = P.TagCloud | 0x1000
		n.alloc = roundUp(size)
	case P.StatePinned:
		n.attrs = P.AttrArchive | P.AttrReparsePoint | P.AttrSparse | P.AttrPinned
		n.tag = P.TagCloud | 0x1000
		n.alloc = roundUp(size)
	case P.StateOnline:
		n.attrs = P.AttrArchive | P.AttrReparsePoint | P.AttrSparse | P.AttrOffline | P.AttrUnpinned | P.AttrRecallOnDataAccess
		n.tag = P.TagCloud | 0x1000
		n.alloc = 0
	}
	parent.children[name] = n
}

// SetNotInSync marque un fichier comme non encore synchronisé.
func (f *FS) SetNotInSync(p string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if n := f.lookup(p); n != nil {
		n.inSync = false
	}
}

// AddJunction ajoute un point de jonction (jamais suivi par le scanner).
func (f *FS) AddJunction(p string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	dir, name := path.Split(path.Clean("/" + p))
	parent := f.mkdirAll(dir)
	parent.children[name] = &node{name: name, dir: true, link: true,
		attrs: P.AttrDirectory | P.AttrReparsePoint, tag: P.TagMountPoint, children: map[string]*node{}}
}

func roundUp(s int64) int64 { return (s + 4095) / 4096 * 4096 }

func (f *FS) ReadDir(dir string) ([]P.Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.DenyRead[path.Clean(dir)] {
		return nil, errors.New("Accès refusé.")
	}
	n := f.lookup(dir)
	if n == nil || !n.dir {
		return nil, errors.New("Le chemin d'accès spécifié est introuvable.")
	}
	names := make([]string, 0, len(n.children))
	for k := range n.children {
		names = append(names, k)
	}
	sort.Strings(names)
	out := make([]P.Entry, 0, len(names))
	for _, k := range names {
		c := n.children[k]
		out = append(out, P.Entry{Name: c.name, IsDir: c.dir, Size: c.size, Attrs: c.attrs, ReparseTag: c.tag, ModTime: c.mtime})
	}
	return out, nil
}

func (f *FS) Allocated(p string, e P.Entry) (int64, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if n := f.lookup(p); n != nil {
		return n.alloc, true
	}
	return e.Size, false
}

func (f *FS) Attrs(p string) (uint32, uint32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := f.lookup(p)
	if n == nil {
		return 0, 0, errors.New("introuvable")
	}
	return n.attrs, n.tag, nil
}

const onlineBits = P.AttrOffline | P.AttrRecallOnDataAccess

func (f *FS) SetAttrs(p string, attrs uint32) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.SetCalls++
	if f.DenySet[path.Clean(p)] {
		return errors.New("Accès refusé.")
	}
	n := f.lookup(p)
	if n == nil {
		return errors.New("introuvable")
	}
	keep := n.attrs &^ (P.AttrPinned | P.AttrUnpinned)
	n.attrs = keep | attrs&(P.AttrPinned|P.AttrUnpinned)
	if n.dir || !P.IsCloud(n.attrs, n.tag) || !f.AutoDehydrate {
		return nil
	}
	// Comportement simulé du client OneDrive.
	if n.attrs&P.AttrUnpinned != 0 && f.Locked[path.Clean(p)] == false && n.inSync {
		n.attrs |= onlineBits
		n.alloc = 0
	}
	if n.attrs&P.AttrPinned != 0 && n.attrs&onlineBits != 0 {
		n.attrs &^= onlineBits
		n.alloc = roundUp(n.size)
	}
	return nil
}

type patternReader struct {
	seed string
	left int64
	pos  int64
}

func (r *patternReader) Read(b []byte) (int, error) {
	if r.left <= 0 {
		return 0, io.EOF
	}
	n := len(b)
	if int64(n) > r.left {
		n = int(r.left)
	}
	s := r.seed
	if s == "" {
		s = "x"
	}
	for i := 0; i < n; i++ {
		b[i] = s[(r.pos+int64(i))%int64(len(s))]
	}
	r.pos += int64(n)
	r.left -= int64(n)
	return n, nil
}

func (f *FS) OpenRead(p string) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := f.lookup(p)
	if n == nil || n.dir {
		return nil, errors.New("introuvable")
	}
	if n.attrs&onlineBits != 0 {
		// Une lecture déclencherait le téléchargement : on le compte.
		f.Hydrations++
		n.attrs &^= onlineBits
		n.alloc = roundUp(n.size)
	}
	return io.NopCloser(&patternReader{seed: n.seed, left: n.size}), nil
}

// State renvoie l'état actuel d'un fichier (pour les assertions des tests).
func (f *FS) State(p string) P.FileState {
	a, t, err := f.Attrs(p)
	if err != nil {
		return -1
	}
	return P.Classify(a, t)
}
