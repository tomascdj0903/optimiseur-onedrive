// Package report produit le rapport texte (lisible + annexe technique).
package report

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"optimiseur-onedrive/internal/analysis"
	"optimiseur-onedrive/internal/dupes"
	F "optimiseur-onedrive/internal/format"
	P "optimiseur-onedrive/internal/platform"
	"optimiseur-onedrive/internal/scan"
)

var stateLabel = map[P.FileState]string{
	P.StateNotCloud: "non géré à la demande",
	P.StateOnline:   "en ligne uniquement",
	P.StateLocal:    "téléchargé (libérable)",
	P.StatePinned:   "toujours conservé",
}

func StateLabel(s P.FileState) string { return stateLabel[s] }

var kindLabel = map[scan.TargetKind]string{
	scan.KindOneDrive: "dossier OneDrive",
	scan.KindLibrary:  "bibliothèque synchronisée",
	scan.KindOrphan:   "ancien dossier (non synchronisé)",
	scan.KindLegacy:   "ancien emplacement local",
	scan.KindCache:    "données de l'application",
}

func KindLabel(k scan.TargetKind) string { return kindLabel[k] }

var pinLabel = map[string]string{
	"toujours_conserver": "Toujours conserver sur cet appareil",
	"en_ligne":           "Libérer de l'espace (en ligne)",
	"standard":           "Standard (OneDrive décide)",
	"non_cloud":          "Non géré par Fichiers à la demande",
}

func PinLabel(s string) string { return pinLabel[s] }

type w struct{ b strings.Builder }

func (x *w) l(format string, a ...any) { fmt.Fprintf(&x.b, format+"\n", a...) }
func (x *w) h(title string) {
	x.l("")
	x.l("%s", title)
	x.l("%s", strings.Repeat("─", len([]rune(title))))
}

// Text rend le rapport complet.
func Text(d *analysis.Diagnosis, res *scan.Result, dr *dupes.Result, m *Masker) string {
	x := &w{}
	x.l("RAPPORT DE DIAGNOSTIC ONEDRIVE — Optimiseur OneDrive %s", d.AppVersion)
	x.l("Généré le %s", d.Generated.Format("02/01/2006 à 15:04"))
	x.l("Ordinateur : %s %s (build %d)", d.OS.Name, d.OS.Version, d.OS.Build)
	if m != nil && m.Enabled {
		x.l("Noms de dossiers et fichiers masqués à la demande de l'utilisateur.")
	}
	x.l("Analyse en lecture seule : aucun fichier n'a été modifié pendant ce diagnostic.")

	t := d.Totals
	x.h("1. RÉSUMÉ")
	x.l("Espace occupé sur le disque par les dossiers OneDrive synchronisés : %s (mesuré)", F.Bytes(t.ActiveAlloc))
	x.l("  • téléchargé, libérable sans perte (déjà dans le cloud) : %s (estimation)", F.Bytes(t.Local))
	x.l("  • « Toujours conserver sur cet appareil »               : %s (libérable si vous le choisissez)", F.Bytes(t.Pinned))
	x.l("  • fichiers non gérés « à la demande »                    : %s", F.Bytes(t.NotCloud))
	x.l("  • fichiers en ligne uniquement : %s fichiers, %s dans le cloud, ~0 octet sur le disque", F.Int(t.OnlineFiles), F.Bytes(t.Online))
	if t.Orphan+t.Legacy > 0 {
		x.l("Autres données locales liées à OneDrive (non synchronisées) : %s", F.Bytes(t.Orphan+t.Legacy))
	}
	x.l("Total des données locales associées à OneDrive : %s", F.Bytes(t.AllLocal))
	x.l("")
	x.l("POURQUOI ONEDRIVE OCCUPE AUTANT D'ESPACE :")
	for i, c := range d.Causes {
		x.l("  %d) %s — %s (%s du total)", i+1, c.Title, F.Bytes(c.Bytes), F.Pct(c.Bytes, t.AllLocal+t.Recycle))
		x.l("     %s", c.Explanation)
	}
	if len(d.Causes) == 0 {
		x.l("  Aucune donnée OneDrive locale significative n'a été trouvée.")
	}
	if len(d.Alerts) > 0 {
		x.l("")
		x.l("ALERTES :")
		for _, a := range d.Alerts {
			x.l("  [%s] %s", strings.ToUpper(a.Level), m.Text(a.Text))
		}
	}

	x.h("2. DISQUES")
	for _, v := range d.Volumes {
		sys := ""
		if v.System {
			sys = " (système)"
		}
		x.l("  %s%s %-10s %-6s total %s, libre %s (%s), utilisé %s", v.Root, sys, v.Type, v.FileSystem,
			F.Bytes(v.Total), F.Bytes(v.Free), F.Pct(v.Free, v.Total), F.Bytes(v.Total-v.Free))
	}

	x.h("3. ONEDRIVE")
	od := d.OneDrive
	x.l("  Application : %s ; version : %s ; en cours d'exécution : %s", yesNo(od.Installed), orUnknown(od.Version), runningLabel(od))
	x.l("  Pilote Windows des fichiers cloud (CldFlt) : %s ; affichage des espaces réservés : %s", od.FilterDriver, d.Placeholders)
	if len(od.Accounts) == 0 {
		x.l("  Aucun compte connecté trouvé.")
	}
	for _, a := range od.Accounts {
		typ := "personnel"
		if a.Business {
			typ = "professionnel/scolaire"
		}
		x.l("  Compte %s (%s) : %s → %s", a.Key, typ, m.Email(a.Email), m.Text(a.UserFolder))
		for _, l := range a.Libraries {
			x.l("     bibliothèque : %s", m.Text(l.Path))
		}
	}
	for _, k := range d.Known {
		where := "non redirigé"
		if k.InOneDrive != "" {
			where = "redirigé dans " + k.InOneDrive
		}
		x.l("  Dossier %s : %s (%s)", k.Label, m.Text(k.Path), where)
	}
	for _, c := range d.OtherClouds {
		x.l("  Autre service cloud détecté (non analysé) : %s", c.Provider)
	}

	x.h("4. DOSSIERS ANALYSÉS")
	for _, ts := range d.Targets {
		x.l("  ■ %s — %s", ts.Label, KindLabel(ts.Kind))
		x.l("    Emplacement : %s (détecté via %s)", m.Text(ts.Path), ts.Source)
		if !ts.Exists {
			x.l("    %s", ts.Err)
			continue
		}
		a := ts.Agg
		x.l("    %s fichiers, %s dossiers ; taille %s ; sur le disque %s", F.Int(a.Files), F.Int(a.Dirs), F.Bytes(a.Logical), F.Bytes(a.Alloc))
		if ts.Kind == scan.KindOneDrive || ts.Kind == scan.KindLibrary {
			x.l("    Réglage du dossier : %s ; fichiers gérés à la demande : %.0f %%", PinLabel(ts.RootPin), ts.CloudShare*100)
			for s := P.FileState(0); s < P.NumStates; s++ {
				st := a.States[s]
				if st.Count == 0 {
					continue
				}
				x.l("      - %-24s %9s fichiers  taille %10s  disque %10s", StateLabel(s), F.Int(st.Count), F.Bytes(st.Logical), F.Bytes(st.Alloc))
			}
		}
		cats := sortedCats(ts.Cats)
		if len(cats) > 0 {
			var parts []string
			for _, c := range cats[:min(5, len(cats))] {
				parts = append(parts, fmt.Sprintf("%s %s", catLabel(c.name), F.Bytes(c.Alloc)))
			}
			x.l("    Types principaux (disque) : %s", strings.Join(parts, ", "))
		}
		if a.Links > 0 {
			x.l("    %s liens/jonctions ignorés (non suivis)", F.Int(a.Links))
		}
		if a.Errors > 0 {
			x.l("    %s erreurs de lecture", F.Int(a.Errors))
		}
	}

	x.h("5. DOSSIERS LES PLUS INTÉRESSANTS À LIBÉRER")
	if len(d.Folders) == 0 {
		x.l("  Aucun dossier ne contient plus de 512 Mo de fichiers libérables.")
	}
	for i, f := range d.Folders {
		if i >= 25 {
			x.l("  … %d autres dossiers", len(d.Folders)-25)
			break
		}
		extra := ""
		if f.Pinned > 0 {
			extra += ", dont épinglé " + F.Bytes(f.Pinned)
		}
		if f.Old > 0 {
			extra += ", non modifié depuis 1 an " + F.Bytes(f.Old)
		}
		x.l("  %2d. %10s libérables%s", i+1, F.Bytes(f.Freeable), extra)
		x.l("      %s", m.Path(f.Path))
	}

	x.h("6. RECOMMANDATIONS (de la plus sûre à la plus délicate)")
	for i, r := range d.Recs {
		x.l("")
		x.l("  %d) %s", i+1, m.Text(r.Title))
		x.l("     Problème détecté  : %s", m.Text(r.Problem))
		x.l("     Explication       : %s", r.Explanation)
		x.l("     Action proposée   : %s", r.Action)
		for j, s := range r.Steps {
			x.l("        %d. %s", j+1, s)
		}
		x.l("     Gain estimé       : %s", gainLabel(r))
		if r.Consequences != "" {
			x.l("     Conséquences      : %s", r.Consequences)
		}
		x.l("     Niveau de risque  : %s", r.RiskLabel)
		x.l("     Conditions        : %s", r.Prerequisites)
		x.l("     Vérification      : %s", r.Verification)
		if r.InApp {
			x.l("     → Réalisable directement avec cet outil (menu 6).")
		}
	}

	x.h("7. COMMENT RÉCUPÉRER 50, 100 OU 200 GO")
	for _, p := range d.Plan {
		g := F.Bytes(p.Goal)
		if !p.Reachable {
			x.l("  %s : %s", g, p.Note)
			continue
		}
		x.l("  %s : libérer %d dossier(s) → %s (estimation)", g, len(p.Folders), F.Bytes(p.Gain))
		for _, f := range p.Folders[:min(6, len(p.Folders))] {
			x.l("      %s", m.Path(f))
		}
		if len(p.Folders) > 6 {
			x.l("      … et %d autres", len(p.Folders)-6)
		}
	}

	x.h("8. PLUS GROS FICHIERS (espace disque)")
	for i, f := range res.Big {
		if i >= 25 {
			break
		}
		x.l("  %10s  %-24s %s  %s", F.Bytes(f.Alloc), StateLabel(f.State), F.Date(f.ModTime), m.Path(f.Path))
	}
	if len(res.Big) == 0 {
		x.l("  Aucun fichier de plus de 50 Mo.")
	}

	x.h("9. DOUBLONS")
	if dr == nil {
		x.l("  Recherche non effectuée (menu 5 de l'outil).")
	} else {
		x.l("  %d groupes confirmés (SHA-256), %s d'espace local redondant ; %d groupes non vérifiables (copies en ligne).", len(dr.Groups), F.Bytes(dr.Gain), len(dr.Unverified))
		for i, g := range dr.Groups {
			if i >= 15 {
				break
			}
			x.l("  • %d copies de %s — gain possible %s", len(g.Files), F.Bytes(g.Size), F.Bytes(g.Gain))
			for _, f := range g.Files {
				x.l("      %s  %s  %s", F.Date(f.ModTime), StateLabel(f.State), m.Path(f.Path))
			}
		}
		if !dr.Complete {
			x.l("  (recherche interrompue : résultats partiels)")
		}
	}

	x.h("10. ERREURS ET LIMITES")
	x.l("  Analyse : %s fichiers, %s dossiers en %s ; %s erreurs.", F.Int(d.Scan.Files), F.Int(d.Scan.Dirs),
		F.Duration(time.Duration(d.Scan.Seconds*float64(time.Second))), F.Int(d.Scan.Errors))
	for _, e := range d.Scan.Samples[:min(10, len(d.Scan.Samples))] {
		x.l("    - %s : %s", m.Path(e.Path), e.Msg)
	}
	for _, l := range d.Limits {
		x.l("  • %s", l)
	}
	for _, n := range d.Notes {
		x.l("  • %s", m.Text(n))
	}

	x.h("ANNEXE TECHNIQUE (pour l'assistance)")
	b, _ := json.Marshal(BuildAnnex(d, res, dr, m))
	x.l("%s", string(b))
	return x.b.String()
}

func gainLabel(r analysis.Recommendation) string {
	switch r.GainKind {
	case analysis.GainNone:
		return "aucun gain direct"
	case analysis.GainPotential:
		return F.Bytes(r.Gain) + " (potentiel, seulement après vérification par vous)"
	case analysis.GainMeasured:
		return F.Bytes(r.Gain) + " (mesuré)"
	}
	return F.Bytes(r.Gain) + " (estimation)"
}

func yesNo(b bool) string {
	if b {
		return "trouvée"
	}
	return "introuvable"
}

func orUnknown(s string) string {
	if s == "" {
		return "inconnue"
	}
	return s
}

func runningLabel(od P.OneDriveInfo) string {
	if !od.RunningKnown {
		return "inconnu"
	}
	if od.Running {
		return "oui"
	}
	return "NON"
}

type catEntry struct {
	name string
	*scan.CatAgg
}

func sortedCats(m map[string]*scan.CatAgg) []catEntry {
	var out []catEntry
	for k, v := range m {
		out = append(out, catEntry{k, v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Alloc > out[j].Alloc })
	return out
}

var catNames = map[string]string{"video": "vidéos", "image": "images", "audio": "audio", "archive": "archives",
	"disque": "images disque", "document": "documents", "messagerie": "Outlook pst/ost", "programme": "programmes", "autre": "autres"}

func catLabel(c string) string {
	if n, ok := catNames[c]; ok {
		return n
	}
	return c
}

// ---- Annexe technique (JSON compact, lue par l'assistance)

type Annex struct {
	V       string                       `json:"v"`
	OS      P.OSInfo                     `json:"os"`
	Volumes []P.Volume                   `json:"vol"`
	OD      annexOD                      `json:"od"`
	PHCM    string                       `json:"phcm"`
	Totals  analysis.Totals              `json:"tot"`
	Targets []annexTarget                `json:"tgt"`
	Folders []annexFolder                `json:"fold"`
	Big     []annexFile                  `json:"big"`
	Recs    []annexRec                   `json:"rec"`
	Alerts  []analysis.Alert             `json:"alert"`
	Scan    annexScan                    `json:"scan"`
	Recycle []P.RecycleInfo              `json:"rb"`
	Dupes   *annexDupes                  `json:"dup,omitempty"`
	Known   []analysis.KnownFolderStatus `json:"kf"`
}

type annexOD struct {
	Installed bool     `json:"inst"`
	Version   string   `json:"ver"`
	Running   string   `json:"run"`
	Driver    string   `json:"cldflt"`
	Accounts  []string `json:"acc"`
	Roots     []string `json:"roots"`
	Env       []string `json:"env"`
}

type annexTarget struct {
	Label  string                  `json:"label"`
	Kind   scan.TargetKind         `json:"kind"`
	Path   string                  `json:"path"`
	Exists bool                    `json:"ok"`
	Vol    string                  `json:"vol"`
	Pin    string                  `json:"pin"`
	Cloud  float64                 `json:"cloud"`
	Agg    scan.Agg                `json:"agg"`
	Cats   map[string]*scan.CatAgg `json:"cat"`
	Conf   int64                   `json:"conf"`
	RootA  string                  `json:"rootAttrs"`
}

type annexFolder struct {
	P string `json:"p"`
	F int64  `json:"free"`
	A int64  `json:"alloc"`
	K int64  `json:"pin"`
	O int64  `json:"old"`
}

type annexFile struct {
	P  string `json:"p"`
	S  int64  `json:"size"`
	A  int64  `json:"alloc"`
	St int    `json:"st"`
	At string `json:"attr"`
	M  string `json:"mod"`
}

type annexRec struct {
	ID   string `json:"id"`
	Gain int64  `json:"gain"`
	Kind string `json:"kind"`
	Risk string `json:"risk"`
}

type annexScan struct {
	Files  int64    `json:"files"`
	Dirs   int64    `json:"dirs"`
	Errors int64    `json:"err"`
	Sec    float64  `json:"sec"`
	Cancel bool     `json:"cancel"`
	Est    int64    `json:"est"`
	ErrS   []string `json:"errs"`
}

type annexDupes struct {
	Groups     int   `json:"g"`
	Gain       int64 `json:"gain"`
	Unverified int   `json:"unv"`
	Hashed     int64 `json:"hashed"`
	Complete   bool  `json:"done"`
}

func hexAttr(a uint32) string { return fmt.Sprintf("0x%X", a) }

func BuildAnnex(d *analysis.Diagnosis, res *scan.Result, dr *dupes.Result, m *Masker) Annex {
	od := d.OneDrive
	run := runningLabel(od)
	an := Annex{V: d.AppVersion, OS: d.OS, Volumes: d.Volumes, PHCM: d.Placeholders, Totals: d.Totals,
		Alerts: d.Alerts, Recycle: d.Recycle, Known: d.Known,
		OD: annexOD{Installed: od.Installed, Version: od.Version, Running: run, Driver: od.FilterDriver}}
	an.OS.User = m.Name(an.OS.User)
	for i := range an.Known {
		an.Known[i].Path = m.Text(an.Known[i].Path)
	}
	for _, a := range od.Accounts {
		an.OD.Accounts = append(an.OD.Accounts, a.Key+"|"+m.Email(a.Email)+"|"+m.Text(a.UserFolder)+fmt.Sprintf("|libs=%d", len(a.Libraries)))
	}
	for _, r := range od.SyncRoots {
		an.OD.Roots = append(an.OD.Roots, r.Provider+"|"+m.Text(r.Path))
	}
	for _, e := range od.EnvFolders {
		an.OD.Env = append(an.OD.Env, m.Text(e))
	}
	for i, ts := range d.Targets {
		at := annexTarget{Label: ts.Label, Kind: ts.Kind, Path: m.Text(ts.Path), Exists: ts.Exists, Vol: ts.Volume,
			Pin: ts.RootPin, Cloud: ts.CloudShare, Agg: ts.Agg, Cats: ts.Cats, Conf: ts.ConflictCount}
		if i < len(res.Targets) && res.Targets[i].Root != nil {
			at.RootA = hexAttr(res.Targets[i].Root.Attrs) + "/" + hexAttr(res.Targets[i].Root.Tag)
		}
		an.Targets = append(an.Targets, at)
	}
	for i, f := range d.Folders {
		if i >= 40 {
			break
		}
		an.Folders = append(an.Folders, annexFolder{P: m.Path(f.Path), F: f.Freeable, A: f.Alloc, K: f.Pinned, O: f.Old})
	}
	for i, f := range res.Big {
		if i >= 40 {
			break
		}
		an.Big = append(an.Big, annexFile{P: m.Path(f.Path), S: f.Size, A: f.Alloc, St: int(f.State), At: hexAttr(f.Attrs), M: F.Date(f.ModTime)})
	}
	for _, r := range d.Recs {
		an.Recs = append(an.Recs, annexRec{ID: m.Text(r.ID), Gain: r.Gain, Kind: r.GainKind, Risk: r.RiskLabel})
	}
	an.Scan = annexScan{Files: d.Scan.Files, Dirs: d.Scan.Dirs, Errors: d.Scan.Errors, Sec: d.Scan.Seconds,
		Cancel: d.Scan.Canceled, Est: d.Scan.Estimated}
	for _, e := range d.Scan.Samples[:min(15, len(d.Scan.Samples))] {
		an.Scan.ErrS = append(an.Scan.ErrS, m.Path(e.Path)+" : "+e.Msg)
	}
	if dr != nil {
		an.Dupes = &annexDupes{Groups: len(dr.Groups), Gain: dr.Gain, Unverified: len(dr.Unverified), Hashed: dr.Hashed, Complete: dr.Complete}
	}
	return an
}

// RelPath affiche un chemin relatif à la racine de sa cible, pour la console.
func RelPath(path, root string) string {
	r, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(r, "..") {
		return path
	}
	return r
}
