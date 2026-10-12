// Package platform isole tout ce qui dépend du système (Windows en production,
// un système de fichiers générique pour le développement et les tests).
package platform

import (
	"io"
	"time"
)

// Attributs de fichiers Windows (winnt.h). Les attributs PINNED / UNPINNED /
// RECALL_* sont ceux utilisés par la fonction Fichiers à la demande de OneDrive.
const (
	AttrReadOnly           uint32 = 0x00000001
	AttrHidden             uint32 = 0x00000002
	AttrSystem             uint32 = 0x00000004
	AttrDirectory          uint32 = 0x00000010
	AttrArchive            uint32 = 0x00000020
	AttrNormal             uint32 = 0x00000080
	AttrTemporary          uint32 = 0x00000100
	AttrSparse             uint32 = 0x00000200
	AttrReparsePoint       uint32 = 0x00000400
	AttrCompressed         uint32 = 0x00000800
	AttrOffline            uint32 = 0x00001000
	AttrNotContentIndexed  uint32 = 0x00002000
	AttrEncrypted          uint32 = 0x00004000
	AttrRecallOnOpen       uint32 = 0x00040000
	AttrPinned             uint32 = 0x00080000
	AttrUnpinned           uint32 = 0x00100000
	AttrRecallOnDataAccess uint32 = 0x00400000
)

// Étiquettes de points d'analyse (reparse tags).
const (
	TagMountPoint       uint32 = 0xA0000003 // point de jonction / montage
	TagSymlink          uint32 = 0xA000000C // lien symbolique
	TagCloud            uint32 = 0x9000001A // fichiers cloud (cldflt), variantes 0x9000x01A
	tagNameSurrogateBit        = 0x20000000
)

// IsCloudTag indique si l'étiquette correspond au pilote Cloud Files (OneDrive).
func IsCloudTag(tag uint32) bool { return tag&0xFFFF0FFF == TagCloud }

// Entry décrit un élément d'un dossier tel que renvoyé par l'énumération.
type Entry struct {
	Name       string
	IsDir      bool
	Size       int64 // taille logique
	Attrs      uint32
	ReparseTag uint32
	ModTime    time.Time
}

// IsLink indique un lien symbolique ou un point de jonction : on ne le suit
// jamais (évite les boucles et les doubles comptages).
func (e Entry) IsLink() bool {
	if e.Attrs&AttrReparsePoint == 0 {
		return false
	}
	if e.ReparseTag == TagMountPoint || e.ReparseTag == TagSymlink {
		return true
	}
	return e.ReparseTag&tagNameSurrogateBit != 0
}

// FileState est l'état OneDrive « Fichiers à la demande » d'un fichier.
type FileState int

const (
	StateNotCloud FileState = iota // fichier ordinaire (pas un espace réservé OneDrive)
	StateOnline                    // disponible uniquement en ligne (n'occupe ~rien)
	StateLocal                     // disponible localement (téléchargé, libérable)
	StatePinned                    // « Toujours conserver sur cet appareil »
	NumStates
)

var stateKeys = [...]string{"non_cloud", "en_ligne", "local", "epingle"}

func (s FileState) Key() string {
	if s < 0 || int(s) >= len(stateKeys) {
		return "inconnu"
	}
	return stateKeys[s]
}

// IsCloud indique si les attributs révèlent un élément géré par Cloud Files.
func IsCloud(attrs, tag uint32) bool {
	if attrs&AttrReparsePoint != 0 && IsCloudTag(tag) {
		return true
	}
	return attrs&(AttrPinned|AttrUnpinned|AttrRecallOnDataAccess|AttrRecallOnOpen) != 0
}

// Classify déduit l'état d'un fichier de ses attributs.
func Classify(attrs, tag uint32) FileState {
	cloud := IsCloud(attrs, tag)
	if attrs&(AttrRecallOnDataAccess|AttrRecallOnOpen) != 0 {
		return StateOnline
	}
	if !cloud {
		return StateNotCloud
	}
	if attrs&AttrOffline != 0 {
		return StateOnline
	}
	if attrs&AttrPinned != 0 {
		return StatePinned
	}
	return StateLocal
}

// FS : accès au système de fichiers.
type FS interface {
	ReadDir(dir string) ([]Entry, error)
	// Allocated renvoie l'espace réellement occupé sur le disque ; exact=false
	// signifie que la valeur est une estimation (arrondi au cluster).
	Allocated(path string, e Entry) (alloc int64, exact bool)
	Attrs(path string) (uint32, uint32, error) // attributs, reparse tag
	SetAttrs(path string, attrs uint32) error
	OpenRead(path string) (io.ReadCloser, error)
}

type OSInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Build   uint32 `json:"build"`
	Machine string `json:"machine"`
	User    string `json:"user"`
	Windows bool   `json:"windows"`
}

type Volume struct {
	Root       string `json:"root"`
	Label      string `json:"label"`
	FileSystem string `json:"fileSystem"`
	Type       string `json:"type"`
	Total      int64  `json:"total"`
	Free       int64  `json:"free"`
	System     bool   `json:"system"`
}

type Library struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type Account struct {
	Key         string    `json:"key"`
	Business    bool      `json:"business"`
	Email       string    `json:"email"`
	DisplayName string    `json:"displayName"`
	UserFolder  string    `json:"userFolder"`
	Libraries   []Library `json:"libraries"`
}

type SyncRoot struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Path     string `json:"path"`
	OneDrive bool   `json:"oneDrive"`
}

type OneDriveInfo struct {
	Installed    bool       `json:"installed"`
	ExePath      string     `json:"exePath"`
	Version      string     `json:"version"`
	Running      bool       `json:"running"`
	RunningKnown bool       `json:"runningKnown"`
	Accounts     []Account  `json:"accounts"`
	SyncRoots    []SyncRoot `json:"syncRoots"`
	EnvFolders   []string   `json:"envFolders"`
	FilterDriver string     `json:"filterDriver"` // état du pilote CldFlt
}

type KnownFolder struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Path        string `json:"path"`
	DefaultPath string `json:"defaultPath"`
}

type RecycleInfo struct {
	Root  string `json:"root"`
	Size  int64  `json:"size"`
	Items int64  `json:"items"`
	OK    bool   `json:"ok"`
}

// System : informations système et OneDrive (lecture seule), plus l'ouverture
// de l'Explorateur / des Paramètres (aucune modification).
type System interface {
	OSInfo() OSInfo
	Volumes() []Volume
	OneDrive() OneDriveInfo
	KnownFolders() []KnownFolder
	ProfileDir() string
	LocalAppData() string
	ComputerName() string
	RecycleBin(root string) RecycleInfo
	ClusterSize(path string) int64
	DiskFree(path string) (free, total int64, err error)
	OpenInExplorer(path string, selectItem bool) error
	OpenSettings(uri string) error
	OpenURL(url string) error
	// BeginBackground baisse la priorité E/S du thread courant pendant un scan.
	BeginBackground() (end func())
	// SamePath compare deux chemins selon les règles du système (casse sur Windows).
	SamePath(a, b string) bool
}
