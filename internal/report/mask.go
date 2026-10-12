package report

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"sort"
	"strings"
)

// Masker remplace les noms de dossiers/fichiers par des codes stables quand
// l'utilisateur ne souhaite pas les partager. Les noms des dossiers racine
// (« OneDrive – Personnel »…) et les extensions sont conservés.
type Masker struct {
	Enabled bool
	roots   []root
	profile string
	same    func(a, b string) bool
}

type root struct{ path, label string }

func NewMasker(enabled bool, profile string, same func(a, b string) bool) *Masker {
	if same == nil {
		same = func(a, b string) bool { return strings.EqualFold(a, b) }
	}
	return &Masker{Enabled: enabled, profile: profile, same: same}
}

func (m *Masker) AddRoot(path, label string) {
	if m == nil {
		return
	}
	m.roots = append(m.roots, root{filepath.Clean(path), label})
	sort.SliceStable(m.roots, func(i, j int) bool { return len(m.roots[i].path) > len(m.roots[j].path) })
}

func code(s string) string {
	h := sha256.Sum256([]byte(strings.ToLower(s)))
	return hex.EncodeToString(h[:3])
}

func maskSeg(seg string, last bool) string {
	if seg == "" {
		return seg
	}
	ext := ""
	if last {
		ext = strings.ToLower(filepath.Ext(seg))
		if len(ext) > 8 {
			ext = ""
		}
	}
	return "«" + code(seg) + "»" + ext
}

// Path masque un chemin (sans effet si le masquage est désactivé).
func (m *Masker) Path(p string) string {
	if m == nil || !m.Enabled || p == "" {
		return p
	}
	clean := filepath.Clean(p)
	sep := string(filepath.Separator)
	for _, r := range m.roots {
		if m.same(clean, r.path) {
			return "[" + r.label + "]"
		}
		prefix := r.path
		if !strings.HasSuffix(prefix, sep) {
			prefix += sep
		}
		if len(clean) > len(prefix) && m.same(clean[:len(prefix)], prefix) {
			parts := strings.Split(clean[len(prefix):], sep)
			for i := range parts {
				parts[i] = maskSeg(parts[i], i == len(parts)-1)
			}
			return "[" + r.label + "]" + sep + strings.Join(parts, sep)
		}
	}
	vol := filepath.VolumeName(clean)
	rest := strings.TrimPrefix(clean[len(vol):], sep)
	if rest == "" {
		return clean
	}
	parts := strings.Split(rest, sep)
	// On garde le premier niveau (Users, Program Files…) et masque le reste.
	for i := 1; i < len(parts); i++ {
		parts[i] = maskSeg(parts[i], i == len(parts)-1)
	}
	return vol + sep + strings.Join(parts, sep)
}

// Name masque un nom isolé.
func (m *Masker) Name(n string) string {
	if m == nil || !m.Enabled {
		return n
	}
	return maskSeg(n, true)
}

// Email masque une adresse e-mail.
func (m *Masker) Email(e string) string {
	if m == nil || !m.Enabled || e == "" {
		return e
	}
	at := strings.Index(e, "@")
	if at <= 0 {
		return "***"
	}
	return e[:1] + "***" + e[at:]
}

// Text remplace dans un texte libre les chemins racine connus.
func (m *Masker) Text(s string) string {
	if m == nil || !m.Enabled {
		return s
	}
	for _, r := range m.roots {
		if i := indexFold(s, r.path); i >= 0 {
			s = s[:i] + "[" + r.label + "]" + s[i+len(r.path):]
		}
	}
	if m.profile != "" {
		if i := indexFold(s, m.profile); i >= 0 {
			s = s[:i] + filepath.Dir(m.profile) + string(filepath.Separator) + "«utilisateur»" + s[i+len(m.profile):]
		}
	}
	return s
}

func indexFold(s, sub string) int {
	return strings.Index(strings.ToLower(s), strings.ToLower(sub))
}
