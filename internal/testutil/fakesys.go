package testutil

import (
	"errors"
	"os"

	P "optimiseur-onedrive/internal/platform"
)

// FakeSystem simule un PC Windows avec OneDrive pour les tests et la démo de
// développement (jamais utilisé par l'exécutable livré).
type FakeSystem struct {
	Running bool
	Free    int64
}

func (f *FakeSystem) OSInfo() P.OSInfo {
	return P.OSInfo{Name: "Windows 11 Professional (SIMULATION)", Version: "24H2", Build: 26100, User: "jean", Windows: true}
}
func (f *FakeSystem) Volumes() []P.Volume {
	return []P.Volume{
		{Root: "/C", Label: "Windows", FileSystem: "NTFS", Type: "fixe", Total: 476 << 30, Free: 21 << 30, System: true},
		{Root: "/D", Label: "Données", FileSystem: "NTFS", Type: "fixe", Total: 931 << 30, Free: 300 << 30},
	}
}
func (f *FakeSystem) OneDrive() P.OneDriveInfo {
	return P.OneDriveInfo{Installed: true, Version: "25.170.0901.0003", Running: f.Running, RunningKnown: true,
		FilterDriver: "démarrage automatique",
		Accounts:     []P.Account{{Key: "Personal", Email: "jean.dupont@example.com", UserFolder: Root}}}
}
func (f *FakeSystem) KnownFolders() []P.KnownFolder {
	return []P.KnownFolder{{ID: "Documents", Label: "Documents", Path: Root + "/Documents", DefaultPath: "/C/Users/jean/Documents"},
		{ID: "Desktop", Label: "Bureau", Path: "/C/Users/jean/Desktop", DefaultPath: "/C/Users/jean/Desktop"}}
}
func (f *FakeSystem) ProfileDir() string   { return "/C/Users/jean" }
func (f *FakeSystem) LocalAppData() string { return os.TempDir() }
func (f *FakeSystem) ComputerName() string { return "MONPC" }
func (f *FakeSystem) RecycleBin(root string) P.RecycleInfo {
	return P.RecycleInfo{Root: root, Size: 3 << 30, Items: 120, OK: root == "/C"}
}
func (f *FakeSystem) ClusterSize(string) int64 { return 4096 }
func (f *FakeSystem) DiskFree(path string) (int64, int64, error) {
	return f.Free, 476 << 30, nil
}
func (f *FakeSystem) OpenInExplorer(string, bool) error { return errors.New("simulation") }
func (f *FakeSystem) OpenSettings(string) error         { return errors.New("simulation") }
func (f *FakeSystem) OpenURL(string) error              { return errors.New("simulation") }
func (f *FakeSystem) BeginBackground() func()           { return func() {} }
func (f *FakeSystem) SamePath(a, b string) bool         { return a == b }
