//go:build !windows

package platform

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// Implémentation générique (Linux/macOS) utilisée uniquement pour le
// développement et les tests : il n'y a pas de OneDrive ici.

type genFS struct{}

func NewFS() FS { return genFS{} }

func (genFS) ReadDir(dir string) ([]Entry, error) {
	des, err := os.ReadDir(dir)
	if err != nil && len(des) == 0 {
		return nil, err
	}
	out := make([]Entry, 0, len(des))
	for _, d := range des {
		info, ierr := d.Info()
		if ierr != nil {
			continue
		}
		e := Entry{Name: d.Name(), IsDir: d.IsDir(), Size: info.Size(), ModTime: info.ModTime()}
		if info.Mode()&os.ModeSymlink != 0 {
			e.Attrs |= AttrReparsePoint
			e.ReparseTag = TagSymlink
			if st, err := os.Stat(filepath.Join(dir, d.Name())); err == nil && st.IsDir() {
				e.IsDir = true
			}
		}
		if e.IsDir {
			e.Attrs |= AttrDirectory
			e.Size = 0
		}
		out = append(out, e)
	}
	return out, err
}

func (genFS) Allocated(path string, e Entry) (int64, bool) {
	if st, err := os.Lstat(path); err == nil {
		if s, ok := st.Sys().(*syscall.Stat_t); ok {
			return int64(s.Blocks) * 512, true
		}
	}
	return e.Size, false
}

func (genFS) Attrs(path string) (uint32, uint32, error) {
	st, err := os.Lstat(path)
	if err != nil {
		return 0, 0, err
	}
	var a uint32
	if st.IsDir() {
		a |= AttrDirectory
	}
	return a, 0, nil
}

func (genFS) SetAttrs(path string, attrs uint32) error {
	return errors.New("modification des attributs non prise en charge hors Windows")
}

func (genFS) OpenRead(path string) (io.ReadCloser, error) { return os.Open(path) }

type genSys struct{}

func NewSystem() System { return genSys{} }

func (genSys) OSInfo() OSInfo {
	h, _ := os.Hostname()
	return OSInfo{Name: "Système non Windows (mode développement)", Machine: h, User: os.Getenv("USER")}
}

func (s genSys) Volumes() []Volume {
	free, total, err := s.DiskFree("/")
	if err != nil {
		return nil
	}
	return []Volume{{Root: "/", Label: "racine", FileSystem: "?", Type: "fixe", Total: total, Free: free, System: true}}
}

func (genSys) OneDrive() OneDriveInfo      { return OneDriveInfo{FilterDriver: "absent (non Windows)"} }
func (genSys) KnownFolders() []KnownFolder { return nil }
func (genSys) ProfileDir() string {
	h, _ := os.UserHomeDir()
	return h
}
func (genSys) LocalAppData() string {
	d, _ := os.UserCacheDir()
	return d
}
func (genSys) ComputerName() string {
	h, _ := os.Hostname()
	return h
}
func (genSys) RecycleBin(root string) RecycleInfo { return RecycleInfo{Root: root} }
func (genSys) ClusterSize(string) int64           { return 4096 }
func (genSys) DiskFree(path string) (int64, int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), int64(st.Blocks) * int64(st.Bsize), nil
}
func (genSys) OpenInExplorer(string, bool) error {
	return errors.New("disponible uniquement sous Windows")
}
func (genSys) OpenSettings(string) error { return errors.New("disponible uniquement sous Windows") }
func (genSys) OpenURL(string) error      { return errors.New("disponible uniquement sous Windows") }
func (genSys) BeginBackground() func()   { return func() {} }
func (genSys) SamePath(a, b string) bool { return filepath.Clean(a) == filepath.Clean(b) }

func ExposePlaceholders() string { return "sans objet" }
func SetupConsole(string) bool   { return os.Getenv("TERM") != "" && os.Getenv("NO_COLOR") == "" }
func LongPath(p string) string   { return p }

// OpenAppWindow : sans objet hors Windows (l'adresse est affichée dans la console).
func OpenAppWindow(url string) string { return "" }
