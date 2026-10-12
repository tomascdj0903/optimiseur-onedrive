//go:build windows

package platform

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	advapi32 = syscall.NewLazyDLL("advapi32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	ole32    = syscall.NewLazyDLL("ole32.dll")
	ntdll    = syscall.NewLazyDLL("ntdll.dll")

	procFindFirstFileExW         = kernel32.NewProc("FindFirstFileExW")
	procFindNextFileW            = kernel32.NewProc("FindNextFileW")
	procFindClose                = kernel32.NewProc("FindClose")
	procGetCompressedFileSizeW   = kernel32.NewProc("GetCompressedFileSizeW")
	procGetFileAttributesW       = kernel32.NewProc("GetFileAttributesW")
	procSetFileAttributesW       = kernel32.NewProc("SetFileAttributesW")
	procGetDiskFreeSpaceExW      = kernel32.NewProc("GetDiskFreeSpaceExW")
	procGetDiskFreeSpaceW        = kernel32.NewProc("GetDiskFreeSpaceW")
	procGetLogicalDrives         = kernel32.NewProc("GetLogicalDrives")
	procGetDriveTypeW            = kernel32.NewProc("GetDriveTypeW")
	procGetVolumeInformationW    = kernel32.NewProc("GetVolumeInformationW")
	procGetCurrentThread         = kernel32.NewProc("GetCurrentThread")
	procSetThreadPriority        = kernel32.NewProc("SetThreadPriority")
	procSetConsoleTitleW         = kernel32.NewProc("SetConsoleTitleW")
	procSetConsoleOutputCP       = kernel32.NewProc("SetConsoleOutputCP")
	procSetErrorMode             = kernel32.NewProc("SetErrorMode")
	procCreateToolhelp32Snapshot = kernel32.NewProc("CreateToolhelp32Snapshot")
	procProcess32FirstW          = kernel32.NewProc("Process32FirstW")
	procProcess32NextW           = kernel32.NewProc("Process32NextW")
	procCloseHandle              = kernel32.NewProc("CloseHandle")
	procGetStdHandle             = kernel32.NewProc("GetStdHandle")
	procGetConsoleMode           = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode           = kernel32.NewProc("SetConsoleMode")

	procRegOpenKeyExW    = advapi32.NewProc("RegOpenKeyExW")
	procRegCloseKey      = advapi32.NewProc("RegCloseKey")
	procRegQueryValueExW = advapi32.NewProc("RegQueryValueExW")
	procRegEnumKeyExW    = advapi32.NewProc("RegEnumKeyExW")
	procRegEnumValueW    = advapi32.NewProc("RegEnumValueW")

	procSHGetKnownFolderPath = shell32.NewProc("SHGetKnownFolderPath")
	procSHQueryRecycleBinW   = shell32.NewProc("SHQueryRecycleBinW")
	procShellExecuteW        = shell32.NewProc("ShellExecuteW")
	procCoTaskMemFree        = ole32.NewProc("CoTaskMemFree")

	procRtlGetVersion                             = ntdll.NewProc("RtlGetVersion")
	procRtlSetProcessPlaceholderCompatibilityMode = ntdll.NewProc("RtlSetProcessPlaceholderCompatibilityMode")
)

const invalidHandle = ^uintptr(0)

// win32FindData reproduit exactement WIN32_FIND_DATAW.
type win32FindData struct {
	FileAttributes    uint32
	CreationTime      syscall.Filetime
	LastAccessTime    syscall.Filetime
	LastWriteTime     syscall.Filetime
	FileSizeHigh      uint32
	FileSizeLow       uint32
	Reserved0         uint32 // reparse tag
	Reserved1         uint32
	FileName          [260]uint16
	AlternateFileName [14]uint16
}

// LongPath ajoute le préfixe \\?\ pour dépasser la limite des 260 caractères.
func LongPath(p string) string {
	if strings.HasPrefix(p, `\\?\`) {
		return p
	}
	if strings.HasPrefix(p, `\\`) {
		return `\\?\UNC\` + p[2:]
	}
	return `\\?\` + p
}

func utf16Ptr(s string) *uint16 {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		return nil
	}
	return p
}

func errnoOf(e error) syscall.Errno {
	var en syscall.Errno
	if errors.As(e, &en) {
		return en
	}
	return 0
}

// ExposePlaceholders demande à Windows de montrer les espaces réservés cloud
// tels qu'ils sont (sinon certains attributs peuvent être masqués).
func ExposePlaceholders() string {
	if procRtlSetProcessPlaceholderCompatibilityMode.Find() != nil {
		return "indisponible (Windows trop ancien)"
	}
	const phcmExpose = 2
	r, _, _ := procRtlSetProcessPlaceholderCompatibilityMode.Call(uintptr(phcmExpose))
	if int8(r) < 0 {
		return "refusé"
	}
	return "actif"
}

// SetupConsole donne un titre à la console, active l'UTF-8 et les couleurs.
// Renvoie true si les couleurs ANSI sont disponibles.
func SetupConsole(title string) bool {
	// Pas de boîte « insérez un disque » sur les lecteurs vides.
	procSetErrorMode.Call(0x0001 | 0x8000)
	procSetConsoleOutputCP.Call(65001)
	if p := utf16Ptr(title); p != nil {
		procSetConsoleTitleW.Call(uintptr(unsafe.Pointer(p)))
	}
	const stdOutput = ^uintptr(10) // (DWORD)-11
	h, _, _ := procGetStdHandle.Call(stdOutput)
	var mode uint32
	if r, _, _ := procGetConsoleMode.Call(h, uintptr(unsafe.Pointer(&mode))); r == 0 {
		return false
	}
	const enableVT = 0x0004
	r, _, _ := procSetConsoleMode.Call(h, uintptr(mode|enableVT))
	return r != 0
}

// ---------------------------------------------------------------- FS

type winFS struct{ clusters map[string]int64 }

func NewFS() FS { return &winFS{clusters: map[string]int64{}} }

func filetimeToTime(ft syscall.Filetime) time.Time {
	if ft.HighDateTime == 0 && ft.LowDateTime == 0 {
		return time.Time{}
	}
	return time.Unix(0, ft.Nanoseconds())
}

func (w *winFS) ReadDir(dir string) ([]Entry, error) {
	pattern := LongPath(filepath.Join(dir, "*"))
	var fd win32FindData
	const findExInfoBasic = 1
	const findExSearchNameMatch = 0
	const findFirstExLargeFetch = 2
	h, _, e := procFindFirstFileExW.Call(uintptr(unsafe.Pointer(utf16Ptr(pattern))), findExInfoBasic,
		uintptr(unsafe.Pointer(&fd)), findExSearchNameMatch, 0, findFirstExLargeFetch)
	if h == invalidHandle {
		en := errnoOf(e)
		if en == syscall.ERROR_FILE_NOT_FOUND || en == 18 /*ERROR_NO_MORE_FILES*/ {
			return nil, nil
		}
		return nil, &os.PathError{Op: "lecture du dossier", Path: dir, Err: en}
	}
	defer procFindClose.Call(h)
	var out []Entry
	for {
		name := syscall.UTF16ToString(fd.FileName[:])
		if name != "." && name != ".." {
			out = append(out, Entry{
				Name:       name,
				IsDir:      fd.FileAttributes&AttrDirectory != 0,
				Size:       int64(fd.FileSizeHigh)<<32 | int64(fd.FileSizeLow),
				Attrs:      fd.FileAttributes,
				ReparseTag: tagIf(fd),
				ModTime:    filetimeToTime(fd.LastWriteTime),
			})
		}
		r, _, e := procFindNextFileW.Call(h, uintptr(unsafe.Pointer(&fd)))
		if r == 0 {
			if en := errnoOf(e); en != 18 && en != 0 {
				return out, &os.PathError{Op: "lecture du dossier", Path: dir, Err: en}
			}
			break
		}
	}
	return out, nil
}

func tagIf(fd win32FindData) uint32 {
	if fd.FileAttributes&AttrReparsePoint != 0 {
		return fd.Reserved0
	}
	return 0
}

func (w *winFS) clusterFor(path string) int64 {
	vol := filepath.VolumeName(path)
	if c, ok := w.clusters[strings.ToUpper(vol)]; ok {
		return c
	}
	c := clusterSize(vol + `\`)
	w.clusters[strings.ToUpper(vol)] = c
	return c
}

func (w *winFS) Allocated(path string, e Entry) (int64, bool) {
	special := e.Attrs&(AttrSparse|AttrCompressed|AttrReparsePoint|AttrOffline|AttrRecallOnDataAccess|AttrRecallOnOpen) != 0
	if special {
		var high uint32
		low, _, err := procGetCompressedFileSizeW.Call(uintptr(unsafe.Pointer(utf16Ptr(LongPath(path)))), uintptr(unsafe.Pointer(&high)))
		if !(uint32(low) == 0xFFFFFFFF && errnoOf(err) != 0) {
			return int64(high)<<32 | int64(uint32(low)), true
		}
		// Impossible de mesurer : on estime de façon prudente.
		if Classify(e.Attrs, e.ReparseTag) == StateOnline {
			return 0, false
		}
	}
	c := w.clusterFor(path)
	if c <= 0 {
		return e.Size, false
	}
	return (e.Size + c - 1) / c * c, false
}

func (w *winFS) Attrs(path string) (uint32, uint32, error) {
	r, _, e := procGetFileAttributesW.Call(uintptr(unsafe.Pointer(utf16Ptr(LongPath(path)))))
	if uint32(r) == 0xFFFFFFFF {
		return 0, 0, &os.PathError{Op: "lecture des attributs", Path: path, Err: errnoOf(e)}
	}
	attrs := uint32(r)
	var tag uint32
	if attrs&AttrReparsePoint != 0 {
		var fd win32FindData
		h, _, _ := procFindFirstFileExW.Call(uintptr(unsafe.Pointer(utf16Ptr(LongPath(path)))), 1,
			uintptr(unsafe.Pointer(&fd)), 0, 0, 0)
		if h != invalidHandle {
			tag = fd.Reserved0
			procFindClose.Call(h)
		}
	}
	return attrs, tag, nil
}

// settableMask : attributs que SetFileAttributes accepte (on conserve les
// autres tels quels côté système ; ils ne sont pas transmis).
const settableMask = AttrReadOnly | AttrHidden | AttrSystem | AttrArchive | AttrTemporary |
	AttrOffline | AttrNotContentIndexed | AttrPinned | AttrUnpinned

// SetAttrs applique les attributs comme le fait l'outil officiel attrib.exe :
// lecture des attributs actuels, modification des seuls bits voulus, écriture.
func (w *winFS) SetAttrs(path string, attrs uint32) error {
	v := attrs & settableMask
	if v == 0 {
		v = AttrNormal
	}
	r, _, e := procSetFileAttributesW.Call(uintptr(unsafe.Pointer(utf16Ptr(LongPath(path)))), uintptr(v))
	if r == 0 {
		return &os.PathError{Op: "modification des attributs", Path: path, Err: errnoOf(e)}
	}
	return nil
}

func (w *winFS) OpenRead(path string) (io.ReadCloser, error) {
	return os.Open(LongPath(path))
}

// ---------------------------------------------------------------- System

type winSys struct{}

func NewSystem() System { return winSys{} }

func clusterSize(root string) int64 {
	var spc, bps, fc, tc uint32
	r, _, _ := procGetDiskFreeSpaceW.Call(uintptr(unsafe.Pointer(utf16Ptr(root))),
		uintptr(unsafe.Pointer(&spc)), uintptr(unsafe.Pointer(&bps)), uintptr(unsafe.Pointer(&fc)), uintptr(unsafe.Pointer(&tc)))
	if r == 0 {
		return 4096
	}
	return int64(spc) * int64(bps)
}

func (winSys) ClusterSize(path string) int64 { return clusterSize(filepath.VolumeName(path) + `\`) }

func (winSys) DiskFree(path string) (int64, int64, error) {
	var avail, total, totalFree uint64
	r, _, e := procGetDiskFreeSpaceExW.Call(uintptr(unsafe.Pointer(utf16Ptr(path))),
		uintptr(unsafe.Pointer(&avail)), uintptr(unsafe.Pointer(&total)), uintptr(unsafe.Pointer(&totalFree)))
	if r == 0 {
		return 0, 0, errnoOf(e)
	}
	return int64(avail), int64(total), nil
}

type osVersionInfoEx struct {
	Size             uint32
	Major, Minor     uint32
	Build            uint32
	PlatformID       uint32
	CSDVersion       [128]uint16
	SPMajor, SPMinor uint16
	SuiteMask        uint16
	ProductType      byte
	Reserved         byte
}

func (s winSys) OSInfo() OSInfo {
	var v osVersionInfoEx
	v.Size = uint32(unsafe.Sizeof(v))
	procRtlGetVersion.Call(uintptr(unsafe.Pointer(&v)))
	name := "Windows"
	if v.Major == 10 {
		if v.Build >= 22000 {
			name = "Windows 11"
		} else {
			name = "Windows 10"
		}
	}
	disp := regString(hkeyLocalMachine, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, "DisplayVersion")
	if edition := regString(hkeyLocalMachine, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, "EditionID"); edition != "" {
		name += " " + edition
	}
	return OSInfo{Name: name, Version: disp, Build: v.Build, Machine: runtime.GOARCH, User: os.Getenv("USERNAME"), Windows: true}
}

func (winSys) ProfileDir() string   { return os.Getenv("USERPROFILE") }
func (winSys) LocalAppData() string { return os.Getenv("LOCALAPPDATA") }
func (winSys) ComputerName() string { return os.Getenv("COMPUTERNAME") }

func (s winSys) Volumes() []Volume {
	mask, _, _ := procGetLogicalDrives.Call()
	sysDrive := strings.ToUpper(os.Getenv("SystemDrive"))
	var out []Volume
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		root := string(rune('A'+i)) + `:\`
		t, _, _ := procGetDriveTypeW.Call(uintptr(unsafe.Pointer(utf16Ptr(root))))
		var typ string
		switch t {
		case 2:
			typ = "amovible"
		case 3:
			typ = "fixe"
		default:
			continue // CD-ROM, disque RAM, inconnu
		}
		v := Volume{Root: root, Type: typ, System: strings.EqualFold(root[:2], sysDrive)}
		var label, fsName [261]uint16
		var serial, maxComp, flags uint32
		r, _, _ := procGetVolumeInformationW.Call(uintptr(unsafe.Pointer(utf16Ptr(root))),
			uintptr(unsafe.Pointer(&label[0])), 261, uintptr(unsafe.Pointer(&serial)),
			uintptr(unsafe.Pointer(&maxComp)), uintptr(unsafe.Pointer(&flags)), uintptr(unsafe.Pointer(&fsName[0])), 261)
		if r == 0 {
			continue // lecteur vide / indisponible
		}
		v.Label = syscall.UTF16ToString(label[:])
		v.FileSystem = syscall.UTF16ToString(fsName[:])
		v.Free, v.Total, _ = s.DiskFree(root)
		out = append(out, v)
	}
	return out
}

// ---- registre (lecture seule)

const (
	hkeyCurrentUser  = uintptr(0x80000001)
	hkeyLocalMachine = uintptr(0x80000002)
	keyRead          = 0x20019
)

func regOpen(root uintptr, path string) (uintptr, bool) {
	var h uintptr
	r, _, _ := procRegOpenKeyExW.Call(root, uintptr(unsafe.Pointer(utf16Ptr(path))), 0, keyRead, uintptr(unsafe.Pointer(&h)))
	return h, r == 0
}

func regQuery(h uintptr, name string) (string, uint32, bool) {
	var typ, n uint32
	r, _, _ := procRegQueryValueExW.Call(h, uintptr(unsafe.Pointer(utf16Ptr(name))), 0, uintptr(unsafe.Pointer(&typ)), 0, uintptr(unsafe.Pointer(&n)))
	if r != 0 || n == 0 {
		return "", 0, false
	}
	buf := make([]byte, n+2)
	r, _, _ = procRegQueryValueExW.Call(h, uintptr(unsafe.Pointer(utf16Ptr(name))), 0, uintptr(unsafe.Pointer(&typ)), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n)))
	if r != 0 {
		return "", 0, false
	}
	switch typ {
	case 1, 2: // REG_SZ, REG_EXPAND_SZ
		u := unsafe.Slice((*uint16)(unsafe.Pointer(&buf[0])), len(buf)/2)
		s := syscall.UTF16ToString(u)
		if typ == 2 {
			s = ExpandPercent(s)
		}
		return s, typ, true
	case 4: // DWORD
		if n >= 4 {
			return itoa(uint64(*(*uint32)(unsafe.Pointer(&buf[0])))), typ, true
		}
	}
	return "", typ, false
}

func itoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}

func regString(root uintptr, path, name string) string {
	h, ok := regOpen(root, path)
	if !ok {
		return ""
	}
	defer procRegCloseKey.Call(h)
	s, _, _ := regQuery(h, name)
	return s
}

func regSubKeys(root uintptr, path string) []string {
	h, ok := regOpen(root, path)
	if !ok {
		return nil
	}
	defer procRegCloseKey.Call(h)
	var out []string
	for i := uint32(0); i < 4096; i++ {
		var name [512]uint16
		n := uint32(len(name))
		r, _, _ := procRegEnumKeyExW.Call(h, uintptr(i), uintptr(unsafe.Pointer(&name[0])), uintptr(unsafe.Pointer(&n)), 0, 0, 0, 0)
		if r != 0 {
			break
		}
		out = append(out, syscall.UTF16ToString(name[:n]))
	}
	return out
}

// regValues énumère les valeurs chaîne d'une clé (nom -> données).
func regValues(root uintptr, path string) map[string]string {
	h, ok := regOpen(root, path)
	if !ok {
		return nil
	}
	defer procRegCloseKey.Call(h)
	out := map[string]string{}
	for i := uint32(0); i < 4096; i++ {
		var name [2048]uint16
		n := uint32(len(name))
		data := make([]byte, 8192)
		dn := uint32(len(data))
		var typ uint32
		r, _, _ := procRegEnumValueW.Call(h, uintptr(i), uintptr(unsafe.Pointer(&name[0])), uintptr(unsafe.Pointer(&n)), 0,
			uintptr(unsafe.Pointer(&typ)), uintptr(unsafe.Pointer(&data[0])), uintptr(unsafe.Pointer(&dn)))
		if r == 234 /*ERROR_MORE_DATA*/ {
			continue
		}
		if r != 0 {
			break
		}
		k := syscall.UTF16ToString(name[:n])
		if typ == 1 || typ == 2 {
			u := unsafe.Slice((*uint16)(unsafe.Pointer(&data[0])), dn/2)
			out[k] = syscall.UTF16ToString(u)
		} else {
			out[k] = ""
		}
	}
	return out
}

func (s winSys) OneDrive() OneDriveInfo {
	var info OneDriveInfo
	info.Version = regString(hkeyCurrentUser, `Software\Microsoft\OneDrive`, "Version")
	for _, c := range []string{
		filepath.Join(os.Getenv("LOCALAPPDATA"), `Microsoft\OneDrive\OneDrive.exe`),
		filepath.Join(os.Getenv("ProgramFiles"), `Microsoft OneDrive\OneDrive.exe`),
		filepath.Join(os.Getenv("ProgramFiles(x86)"), `Microsoft OneDrive\OneDrive.exe`),
	} {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			info.Installed, info.ExePath = true, c
			break
		}
	}
	info.Running, info.RunningKnown = processRunning("onedrive.exe")
	if info.Running {
		info.Installed = true
	}
	for _, key := range regSubKeys(hkeyCurrentUser, `Software\Microsoft\OneDrive\Accounts`) {
		base := `Software\Microsoft\OneDrive\Accounts\` + key
		folder := regString(hkeyCurrentUser, base, "UserFolder")
		email := regString(hkeyCurrentUser, base, "UserEmail")
		if folder == "" && email == "" {
			continue
		}
		a := Account{Key: key, Business: strings.HasPrefix(strings.ToLower(key), "business"),
			Email: email, DisplayName: regString(hkeyCurrentUser, base, "DisplayName"), UserFolder: folder}
		// Bibliothèques SharePoint / dossiers partagés synchronisés.
		for _, tenant := range regSubKeys(hkeyCurrentUser, base+`\Tenants`) {
			for p := range regValues(hkeyCurrentUser, base+`\Tenants\`+tenant) {
				if len(p) > 3 && p[1] == ':' {
					a.Libraries = append(a.Libraries, Library{Name: tenant, Path: p})
				}
			}
		}
		sort.Slice(a.Libraries, func(i, j int) bool { return a.Libraries[i].Path < a.Libraries[j].Path })
		info.Accounts = append(info.Accounts, a)
	}
	// Racines de synchronisation déclarées à Windows (tous fournisseurs cloud).
	sid := currentUserSID()
	const srm = `SOFTWARE\Microsoft\Windows\CurrentVersion\Explorer\SyncRootManager`
	for _, id := range regSubKeys(hkeyLocalMachine, srm) {
		vals := regValues(hkeyLocalMachine, srm+`\`+id+`\UserSyncRoots`)
		for k, p := range vals {
			if sid != "" && !strings.EqualFold(k, sid) {
				continue
			}
			if p == "" {
				continue
			}
			prov := id
			if i := strings.Index(id, "!"); i > 0 {
				prov = id[:i]
			}
			info.SyncRoots = append(info.SyncRoots, SyncRoot{ID: id, Provider: prov, Path: p,
				OneDrive: strings.HasPrefix(strings.ToLower(id), "onedrive")})
		}
	}
	for _, env := range []string{"OneDrive", "OneDriveConsumer", "OneDriveCommercial"} {
		if v := os.Getenv(env); v != "" {
			info.EnvFolders = append(info.EnvFolders, v)
		}
	}
	switch regString(hkeyLocalMachine, `SYSTEM\CurrentControlSet\Services\CldFlt`, "Start") {
	case "2":
		info.FilterDriver = "démarrage automatique"
	case "3":
		info.FilterDriver = "démarrage manuel"
	case "4":
		info.FilterDriver = "désactivé"
	case "":
		info.FilterDriver = "inconnu"
	default:
		info.FilterDriver = "autre"
	}
	return info
}

func currentUserSID() string {
	cmd := exec.Command("whoami", "/user", "/fo", "csv", "/nh")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	f := strings.Split(strings.TrimSpace(string(out)), ",")
	if len(f) < 2 {
		return ""
	}
	return strings.Trim(f[len(f)-1], `"`)
}

type processEntry32 struct {
	Size            uint32
	Usage           uint32
	ProcessID       uint32
	DefaultHeapID   uintptr
	ModuleID        uint32
	Threads         uint32
	ParentProcessID uint32
	PriClassBase    int32
	Flags           uint32
	ExeFile         [260]uint16
}

func processRunning(exe string) (bool, bool) {
	snap, _, _ := procCreateToolhelp32Snapshot.Call(2, 0)
	if snap == invalidHandle {
		return false, false
	}
	defer procCloseHandle.Call(snap)
	var pe processEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	r, _, _ := procProcess32FirstW.Call(snap, uintptr(unsafe.Pointer(&pe)))
	for r != 0 {
		if strings.EqualFold(syscall.UTF16ToString(pe.ExeFile[:]), exe) {
			return true, true
		}
		r, _, _ = procProcess32NextW.Call(snap, uintptr(unsafe.Pointer(&pe)))
	}
	return false, true
}

type guid struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

func mkGUID(d1 uint32, d2, d3 uint16, d4 [8]byte) guid { return guid{d1, d2, d3, d4} }

var knownFolderIDs = []struct {
	id, label, def string
	g              guid
}{
	{"Desktop", "Bureau", "Desktop", mkGUID(0xB4BFCC3A, 0xDB2C, 0x424C, [8]byte{0xB0, 0x29, 0x7F, 0xE9, 0x9A, 0x87, 0xC6, 0x41})},
	{"Documents", "Documents", "Documents", mkGUID(0xFDD39AD0, 0x238F, 0x46AF, [8]byte{0xAD, 0xB4, 0x6C, 0x85, 0x48, 0x03, 0x69, 0xC7})},
	{"Pictures", "Images", "Pictures", mkGUID(0x33E28130, 0x4E1E, 0x4676, [8]byte{0x83, 0x5A, 0x98, 0x39, 0x5C, 0x3B, 0xC3, 0xBB})},
	{"Music", "Musique", "Music", mkGUID(0x4BD8D571, 0x6D19, 0x48D3, [8]byte{0xBE, 0x97, 0x42, 0x22, 0x20, 0x08, 0x0E, 0x43})},
	{"Videos", "Vidéos", "Videos", mkGUID(0x18989B1D, 0x99B5, 0x455B, [8]byte{0x84, 0x1C, 0xAB, 0x7C, 0x74, 0xE4, 0xDD, 0xFC})},
	{"Downloads", "Téléchargements", "Downloads", mkGUID(0x374DE290, 0x123F, 0x4565, [8]byte{0x91, 0x64, 0x39, 0xC4, 0x92, 0x5E, 0x46, 0x7B})},
}

func (s winSys) KnownFolders() []KnownFolder {
	prof := s.ProfileDir()
	var out []KnownFolder
	for _, k := range knownFolderIDs {
		var p *uint16
		g := k.g
		r, _, _ := procSHGetKnownFolderPath.Call(uintptr(unsafe.Pointer(&g)), 0, 0, uintptr(unsafe.Pointer(&p)))
		path := ""
		if r == 0 && p != nil {
			path = syscall.UTF16ToString(unsafe.Slice(p, 32768))
		}
		if p != nil {
			procCoTaskMemFree.Call(uintptr(unsafe.Pointer(p)))
		}
		out = append(out, KnownFolder{ID: k.id, Label: k.label, Path: path, DefaultPath: filepath.Join(prof, k.def)})
	}
	return out
}

type shQueryRBInfo struct {
	Size     uint32
	_        uint32
	Bytes    int64
	NumItems int64
}

func (winSys) RecycleBin(root string) RecycleInfo {
	var q shQueryRBInfo
	q.Size = uint32(unsafe.Sizeof(q))
	r, _, _ := procSHQueryRecycleBinW.Call(uintptr(unsafe.Pointer(utf16Ptr(root))), uintptr(unsafe.Pointer(&q)))
	return RecycleInfo{Root: root, Size: q.Bytes, Items: q.NumItems, OK: r == 0}
}

func shellOpen(file, params string) error {
	var pp uintptr
	if params != "" {
		pp = uintptr(unsafe.Pointer(utf16Ptr(params)))
	}
	r, _, _ := procShellExecuteW.Call(0, uintptr(unsafe.Pointer(utf16Ptr("open"))), uintptr(unsafe.Pointer(utf16Ptr(file))), pp, 0, 1)
	if r <= 32 {
		return errors.New("ouverture impossible")
	}
	return nil
}

func (winSys) OpenInExplorer(path string, selectItem bool) error {
	if selectItem {
		return shellOpen("explorer.exe", `/select,"`+path+`"`)
	}
	return shellOpen(path, "")
}

func (winSys) OpenSettings(uri string) error { return shellOpen(uri, "") }
func (winSys) OpenURL(url string) error      { return shellOpen(url, "") }

func (winSys) BeginBackground() func() {
	runtime.LockOSThread()
	const threadModeBackgroundBegin = 0x00010000
	const threadModeBackgroundEnd = 0x00020000
	th, _, _ := procGetCurrentThread.Call()
	procSetThreadPriority.Call(th, threadModeBackgroundBegin)
	return func() {
		procSetThreadPriority.Call(th, threadModeBackgroundEnd)
		runtime.UnlockOSThread()
	}
}

func (winSys) SamePath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}
