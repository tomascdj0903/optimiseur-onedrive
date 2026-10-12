// Package analysis : détection des dossiers OneDrive, diagnostic et
// recommandations. Lecture seule.
package analysis

import (
	"path/filepath"
	"sort"
	"strings"

	P "optimiseur-onedrive/internal/platform"
	"optimiseur-onedrive/internal/scan"
)

// Inputs regroupe ce que le système a révélé (permet les tests sans Windows).
type Inputs struct {
	OneDrive     P.OneDriveInfo
	Known        []P.KnownFolder
	Volumes      []P.Volume
	Profile      string
	LocalAppData string
	ReadDir      func(string) ([]P.Entry, error)
	Exists       func(string) bool
	Same         func(a, b string) bool
}

// Discovery : cibles à analyser + remarques.
type Discovery struct {
	Targets     []scan.Target `json:"targets"`
	OtherClouds []P.SyncRoot  `json:"otherClouds"`
	Notes       []string      `json:"notes"`
}

func accountLabel(a P.Account) string {
	if !a.Business {
		return "OneDrive – Personnel"
	}
	name := a.DisplayName
	if name == "" {
		name = a.Email
	}
	if name == "" {
		name = a.Key
	}
	return "OneDrive – " + name
}

// Discover détermine les dossiers à analyser, sans jamais supposer
// l'emplacement : registre, racines déclarées à Windows, variables
// d'environnement, puis recherche d'anciens dossiers.
func Discover(in Inputs) Discovery {
	var d Discovery
	same := in.Same
	if same == nil {
		same = func(a, b string) bool { return strings.EqualFold(filepath.Clean(a), filepath.Clean(b)) }
	}
	add := func(t scan.Target) {
		if t.Path == "" {
			return
		}
		t.Path = filepath.Clean(t.Path)
		for _, x := range d.Targets {
			if same(x.Path, t.Path) {
				return
			}
		}
		d.Targets = append(d.Targets, t)
	}
	for _, a := range in.OneDrive.Accounts {
		acc := a.Email
		if acc == "" {
			acc = a.Key
		}
		add(scan.Target{Path: a.UserFolder, Kind: scan.KindOneDrive, Label: accountLabel(a), Account: acc,
			Source: "compte OneDrive (registre " + a.Key + ")", Active: true})
		for _, l := range a.Libraries {
			add(scan.Target{Path: l.Path, Kind: scan.KindLibrary, Label: "Bibliothèque synchronisée : " + filepath.Base(l.Path),
				Account: acc, Source: "bibliothèque SharePoint/Teams (" + l.Name + ")", Active: true})
		}
	}
	for _, r := range in.OneDrive.SyncRoots {
		if !r.OneDrive {
			d.OtherClouds = append(d.OtherClouds, r)
			continue
		}
		add(scan.Target{Path: r.Path, Kind: scan.KindLibrary, Label: "Dossier synchronisé : " + filepath.Base(r.Path),
			Source: "racine de synchronisation Windows (" + r.ID + ")", Active: true})
	}
	for _, e := range in.OneDrive.EnvFolders {
		add(scan.Target{Path: e, Kind: scan.KindOneDrive, Label: "OneDrive : " + filepath.Base(e),
			Source: "variable d'environnement", Active: len(in.OneDrive.Accounts) > 0})
	}
	// Supprime les cibles imbriquées dans une autre (évite les doubles comptages).
	var kept []scan.Target
	for i, t := range d.Targets {
		nested := false
		for j, o := range d.Targets {
			if i != j && !same(t.Path, o.Path) && scan.IsWithin(t.Path, o.Path, same) {
				nested = true
				d.Notes = append(d.Notes, "« "+t.Path+" » est inclus dans « "+o.Path+" » : compté une seule fois.")
				break
			}
		}
		if !nested {
			kept = append(kept, t)
		}
	}
	d.Targets = kept
	isActive := func(p string) bool {
		for _, t := range d.Targets {
			if scan.IsWithin(p, t.Path, same) || scan.IsWithin(t.Path, p, same) {
				return true
			}
		}
		return false
	}
	// Anciens dossiers « OneDrive… » (comptes déconnectés, anciennes copies).
	if in.ReadDir != nil {
		parents := []string{in.Profile}
		for _, v := range in.Volumes {
			if v.Type == "fixe" {
				parents = append(parents, v.Root)
			}
		}
		for _, parent := range parents {
			if parent == "" {
				continue
			}
			ents, _ := in.ReadDir(parent)
			for _, e := range ents {
				if !e.IsDir || e.IsLink() || !strings.HasPrefix(strings.ToLower(e.Name), "onedrive") {
					continue
				}
				p := filepath.Join(parent, e.Name)
				if isActive(p) {
					continue
				}
				if strings.EqualFold(e.Name, "OneDriveTemp") {
					add(scan.Target{Path: p, Kind: scan.KindCache, Label: "Fichiers temporaires OneDrive (" + p + ")",
						Source: "dossier temporaire créé par l'application OneDrive"})
					continue
				}
				add(scan.Target{Path: p, Kind: scan.KindOrphan, Label: "Ancien dossier : " + e.Name,
					Source: "dossier non relié à un compte OneDrive connecté"})
			}
		}
	}
	// Anciens emplacements des dossiers Bureau / Documents / Images redirigés.
	for _, k := range in.Known {
		if k.Path == "" || k.DefaultPath == "" || same(k.Path, k.DefaultPath) {
			continue
		}
		if !isActive(k.Path) || isActive(k.DefaultPath) {
			continue
		}
		if in.Exists != nil && !in.Exists(k.DefaultPath) {
			continue
		}
		add(scan.Target{Path: k.DefaultPath, Kind: scan.KindLegacy, Label: "Ancien emplacement local : " + k.Label,
			Source: "dossier « " + k.Label + " » redirigé vers OneDrive"})
	}
	if in.LocalAppData != "" {
		c := filepath.Join(in.LocalAppData, "Microsoft", "OneDrive")
		if in.Exists == nil || in.Exists(c) {
			add(scan.Target{Path: c, Kind: scan.KindCache, Label: "Données de l'application OneDrive",
				Source: "dossier de l'application (journaux, installation)"})
		}
	}
	sort.SliceStable(d.Targets, func(i, j int) bool { return kindOrder(d.Targets[i].Kind) < kindOrder(d.Targets[j].Kind) })
	return d
}

func kindOrder(k scan.TargetKind) int {
	switch k {
	case scan.KindOneDrive:
		return 0
	case scan.KindLibrary:
		return 1
	case scan.KindOrphan:
		return 2
	case scan.KindLegacy:
		return 3
	}
	return 4
}

// KnownFolderStatus : dossier personnel et sa redirection éventuelle.
type KnownFolderStatus struct {
	Label      string `json:"label"`
	Path       string `json:"path"`
	InOneDrive string `json:"inOneDrive"`
}

func KnownStatus(known []P.KnownFolder, targets []scan.Target, same func(a, b string) bool) []KnownFolderStatus {
	var out []KnownFolderStatus
	for _, k := range known {
		s := KnownFolderStatus{Label: k.Label, Path: k.Path}
		for _, t := range targets {
			if (t.Kind == scan.KindOneDrive || t.Kind == scan.KindLibrary) && k.Path != "" && scan.IsWithin(k.Path, t.Path, same) {
				s.InOneDrive = t.Label
			}
		}
		out = append(out, s)
	}
	return out
}
