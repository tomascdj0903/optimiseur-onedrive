package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"optimiseur-onedrive/internal/actions"
	"optimiseur-onedrive/internal/analysis"
	"optimiseur-onedrive/internal/dupes"
	F "optimiseur-onedrive/internal/format"
	P "optimiseur-onedrive/internal/platform"
	"optimiseur-onedrive/internal/report"
	"optimiseur-onedrive/internal/scan"
)

//go:embed ui/index.html
var uiHTML []byte

// guiState : opérations de fond lancées depuis l'interface graphique.
type guiState struct {
	token string
	host  string

	mu         sync.Mutex
	scanTr     *scan.Tracker
	scanCancel context.CancelFunc
	scanning   bool
	scanErr    string

	dupTr     *dupes.Tracker
	dupCancel context.CancelFunc
	dupRunning bool

	op opState

	lastBeat time.Time
	beats    int
}

type opState struct {
	Running  bool         `json:"running"`
	Kind     string       `json:"kind"`
	Seen     int64        `json:"seen"`
	Changed  int64        `json:"changed"`
	Errors   int64        `json:"errors"`
	Err      string       `json:"error,omitempty"`
	Run      *actions.Run `json:"run,omitempty"`
	cancel   context.CancelFunc
}

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// runGUI démarre le serveur local (127.0.0.1 uniquement) et ouvre la fenêtre.
func (a *App) runGUI() {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		a.out.Err("Impossible de démarrer l'interface graphique (%v). Passage en mode texte.", err)
		a.consoleFlow()
		return
	}
	g := &guiState{token: randHex(24), host: ln.Addr().String(), lastBeat: time.Now()}
	a.gui = g
	mux := http.NewServeMux()
	mux.HandleFunc("/", a.handleIndex)
	api := func(path string, h func(w http.ResponseWriter, r *http.Request) (any, error)) {
		mux.HandleFunc("/api/"+path, func(w http.ResponseWriter, r *http.Request) {
			if !a.authorized(r) {
				http.Error(w, "accès refusé", http.StatusForbidden)
				return
			}
			v, err := h(w, r)
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			json.NewEncoder(w).Encode(v)
		})
	}
	api("state", a.apiState)
	api("heartbeat", a.apiHeartbeat)
	api("share", a.apiShare)
	api("scan/start", a.apiScanStart)
	api("scan/cancel", a.apiScanCancel)
	api("diag", a.apiDiag)
	api("tree", a.apiTree)
	api("big", a.apiBig)
	api("dupes/start", a.apiDupesStart)
	api("dupes/cancel", a.apiDupesCancel)
	api("dupes", a.apiDupes)
	api("prepare", a.apiPrepare)
	api("execute", a.apiExecute)
	api("op", a.apiOp)
	api("op/cancel", a.apiOpCancel)
	api("verify", a.apiVerify)
	api("history", a.apiHistory)
	api("restore/prepare", a.apiRestorePrepare)
	api("export", a.apiExport)
	api("open", a.apiOpen)
	api("settings", a.apiSettings)
	api("quit", a.apiQuit)
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(ln)

	url := "http://" + g.host + "/?t=" + g.token
	a.out.Blank()
	how := P.OpenAppWindow(url)
	if how != "" {
		a.out.Ok("L'interface s'est ouverte (%s).", how)
	} else {
		a.out.Warn("Ouvrez cette adresse dans votre navigateur : %s", url)
	}
	a.out.Line("Cette fenêtre noire affiche le journal détaillé. Laissez-la ouverte ; elle se fermera avec l'interface.")
	a.out.Dim("(Si l'interface a été fermée par erreur, rouvrez-la avec cette adresse : %s)", url)
	a.watchdog()
}

// watchdog : quitte quand la fenêtre est fermée depuis un moment et qu'aucune
// opération n'est en cours.
func (a *App) watchdog() {
	g := a.gui
	for {
		time.Sleep(5 * time.Second)
		g.mu.Lock()
		idle := time.Since(g.lastBeat)
		busy := g.scanning || g.dupRunning || g.op.Running
		beats := g.beats
		g.mu.Unlock()
		if beats > 0 && idle > 45*time.Second && !busy {
			a.out.Line("Interface fermée : fin du programme.")
			a.quit(0)
		}
	}
}

func (a *App) authorized(r *http.Request) bool {
	g := a.gui
	if r.Host != g.host { // protège contre les attaques par rebinding DNS
		return false
	}
	t := r.Header.Get("X-Token")
	return subtle.ConstantTimeCompare([]byte(t), []byte(g.token)) == 1
}

func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.Host != a.gui.host || r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; connect-src 'self'")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Write(uiHTML)
}

func decode(r *http.Request, v any) error {
	if r.Method != http.MethodPost {
		return errors.New("méthode non autorisée")
	}
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(v)
}

// ---------------------------------------------------------------- état

func (a *App) apiState(w http.ResponseWriter, r *http.Request) (any, error) {
	g := a.gui
	g.mu.Lock()
	scanP := g.scanTr.Snapshot()
	scanning, scanErr := g.scanning, g.scanErr
	var dupP dupes.Progress
	if g.dupTr != nil {
		dupP = g.dupTr.Snapshot()
	}
	op := g.op
	g.mu.Unlock()
	a.mu.Lock()
	hasDiag := a.diag != nil
	hasDupes := a.dupRes != nil
	a.mu.Unlock()
	sent, pending, serr := a.share.Status()
	osi := a.sys.OSInfo()
	od := a.sys.OneDrive()
	return map[string]any{
		"version": version, "os": osi.Name + " " + osi.Version, "readOnly": a.readOnly,
		"oneDriveRunning": od.Running || !od.RunningKnown,
		"share": map[string]any{"enabled": a.share.Enabled(), "code": shareCode(a), "sent": sent, "pending": pending,
			"error": serr, "masked": a.mask.Enabled, "available": shareURL != "" && !a.noShare},
		"scan": map[string]any{"running": scanning, "progress": scanP, "error": scanErr},
		"hasDiag": hasDiag, "hasDupes": hasDupes, "dupes": dupP, "op": op,
	}, nil
}

func shareCode(a *App) string {
	if a.share.Enabled() {
		return a.share.Code
	}
	return ""
}

func (a *App) apiHeartbeat(w http.ResponseWriter, r *http.Request) (any, error) {
	g := a.gui
	g.mu.Lock()
	g.lastBeat = time.Now()
	g.beats++
	g.mu.Unlock()
	return map[string]bool{"ok": true}, nil
}

func (a *App) apiShare(w http.ResponseWriter, r *http.Request) (any, error) {
	var req struct {
		Enable bool `json:"enable"`
		Masked bool `json:"masked"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	if !req.Enable {
		if a.share.Enabled() {
			a.share.Disable()
			a.out.share = nil
			a.out.Ok("Partage en direct désactivé par l'utilisateur.")
		}
		return map[string]bool{"enabled": false}, nil
	}
	if a.share.Enabled() {
		return map[string]any{"enabled": true, "code": a.share.Code}, nil
	}
	if err := a.startShare(req.Masked); err != nil {
		return nil, fmt.Errorf("le partage n'a pas pu démarrer : %v", err)
	}
	a.mu.Lock()
	has := a.diag != nil
	a.mu.Unlock()
	if has {
		go a.sendReport("diagnostic")
	}
	return map[string]any{"enabled": true, "code": a.share.Code}, nil
}

// ---------------------------------------------------------------- analyse

func (a *App) apiScanStart(w http.ResponseWriter, r *http.Request) (any, error) {
	g := a.gui
	g.mu.Lock()
	if g.scanning {
		g.mu.Unlock()
		return nil, errors.New("une analyse est déjà en cours")
	}
	if g.op.Running {
		g.mu.Unlock()
		return nil, errors.New("attendez la fin de l'opération en cours")
	}
	ctx, cancel := context.WithCancel(context.Background())
	g.scanning, g.scanCancel, g.scanErr = true, cancel, ""
	g.scanTr = &scan.Tracker{}
	tr := g.scanTr
	g.mu.Unlock()
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				g.mu.Lock()
				g.scanErr = fmt.Sprintf("erreur interne : %v", rec)
				g.mu.Unlock()
				a.out.Err("Erreur interne pendant l'analyse : %v", rec)
			}
			g.mu.Lock()
			g.scanning, g.scanCancel = false, nil
			g.mu.Unlock()
			cancel()
		}()
		a.detect()
		res := a.measure(ctx, tr)
		a.finish(res)
		a.showSummary()
		a.sendReport("diagnostic")
	}()
	return map[string]bool{"started": true}, nil
}

func (a *App) apiScanCancel(w http.ResponseWriter, r *http.Request) (any, error) {
	g := a.gui
	g.mu.Lock()
	c := g.scanCancel
	g.mu.Unlock()
	if c != nil {
		a.out.Warn("Annulation de l'analyse demandée.")
		c()
	}
	return map[string]bool{"ok": true}, nil
}

func (a *App) snapshot() (*scan.Result, *analysis.Diagnosis, *dupes.Result) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.res, a.diag, a.dupRes
}

func (a *App) apiDiag(w http.ResponseWriter, r *http.Request) (any, error) {
	_, d, _ := a.snapshot()
	if d == nil {
		return nil, errors.New("aucune analyse disponible")
	}
	return d, nil
}

// ---------------------------------------------------------------- arborescence

type treeItem struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Alloc    int64  `json:"alloc"`
	Freeable int64  `json:"freeable"`
	Pinned   int64  `json:"pinned"`
	Files    int64  `json:"files"`
	Pin      string `json:"pin"`
	Link     bool   `json:"link"`
	Error    string `json:"error,omitempty"`
	HasKids  bool   `json:"hasChildren"`
	Active   bool   `json:"active"`
	Kind     string `json:"kind,omitempty"`
}

func (a *App) item(n *scan.Node) treeItem {
	kids := false
	for _, c := range n.Children {
		if !c.Link {
			kids = true
			break
		}
	}
	return treeItem{Name: n.Name, Path: n.Path, Alloc: n.Agg.Alloc, Freeable: n.Agg.Freeable(),
		Pinned: n.Agg.States[P.StatePinned].Alloc, Files: n.Agg.Files, Pin: n.PinState(), Link: n.Link,
		Error: n.ErrMsg, HasKids: kids, Active: a.isActivePath(n.Path)}
}

func (a *App) apiTree(w http.ResponseWriter, r *http.Request) (any, error) {
	res, _, _ := a.snapshot()
	if res == nil {
		return nil, errors.New("aucune analyse disponible")
	}
	path := r.URL.Query().Get("path")
	out := map[string]any{"path": path}
	var items []treeItem
	if path == "" {
		for _, t := range res.Targets {
			if t.Root != nil {
				it := a.item(t.Root)
				it.Name = t.Label
				it.Kind = report.KindLabel(t.Kind)
				items = append(items, it)
			}
		}
	} else {
		n, _ := res.Find(path, a.same)
		if n == nil {
			return nil, errors.New("dossier introuvable dans l'analyse")
		}
		out["node"] = a.item(n)
		for i, c := range n.Children {
			if i >= 300 {
				break
			}
			items = append(items, a.item(c))
		}
		if n.Parent != nil {
			out["parent"] = n.Parent.Path
		}
	}
	out["items"] = items
	return out, nil
}

// ---------------------------------------------------------------- fichiers

type fileItem struct {
	Path     string `json:"path"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Alloc    int64  `json:"alloc"`
	Modified string `json:"modified"`
	State    string `json:"state"`
	StateKey string `json:"stateKey"`
	Active   bool   `json:"active"`
}

func (a *App) file(f scan.FileRec) fileItem {
	return fileItem{Path: f.Path, Name: f.Name, Size: f.Size, Alloc: f.Alloc, Modified: F.Date(f.ModTime),
		State: report.StateLabel(f.State), StateKey: f.State.Key(), Active: a.isActivePath(f.Path)}
}

func (a *App) apiBig(w http.ResponseWriter, r *http.Request) (any, error) {
	res, _, _ := a.snapshot()
	if res == nil {
		return nil, errors.New("aucune analyse disponible")
	}
	var out []fileItem
	for i, f := range res.Big {
		if i >= 300 {
			break
		}
		out = append(out, a.file(f))
	}
	return out, nil
}

// ---------------------------------------------------------------- doublons

func (a *App) apiDupesStart(w http.ResponseWriter, r *http.Request) (any, error) {
	res, _, _ := a.snapshot()
	if res == nil {
		return nil, errors.New("lancez d'abord l'analyse")
	}
	g := a.gui
	g.mu.Lock()
	if g.dupRunning {
		g.mu.Unlock()
		return nil, errors.New("recherche déjà en cours")
	}
	ctx, cancel := context.WithCancel(context.Background())
	g.dupRunning, g.dupCancel, g.dupTr = true, cancel, &dupes.Tracker{}
	tr := g.dupTr
	g.mu.Unlock()
	a.out.Title("Recherche de doublons (lecture seule, SHA-256)")
	go func() {
		defer func() {
			g.mu.Lock()
			g.dupRunning, g.dupCancel = false, nil
			g.mu.Unlock()
			cancel()
		}()
		end := a.sys.BeginBackground()
		dr := dupes.Find(ctx, a.fs, res.DupCands, tr)
		end()
		a.mu.Lock()
		a.dupRes = dr
		a.diag = withDupes(a, dr)
		a.mu.Unlock()
		a.out.Ok("%d groupe(s) de doublons confirmés · %s d'espace local redondant · %d non vérifiables.", len(dr.Groups), F.Bytes(dr.Gain), len(dr.Unverified))
		for i, gr := range dr.Groups {
			if i >= 10 {
				break
			}
			a.out.Line("  %d copies de %s : %s", len(gr.Files), F.Bytes(gr.Size), pth(gr.Files[0].Path))
		}
		a.sendReport("doublons")
	}()
	return map[string]bool{"started": true}, nil
}

func (a *App) apiDupesCancel(w http.ResponseWriter, r *http.Request) (any, error) {
	g := a.gui
	g.mu.Lock()
	c := g.dupCancel
	g.mu.Unlock()
	if c != nil {
		c()
	}
	return map[string]bool{"ok": true}, nil
}

func (a *App) apiDupes(w http.ResponseWriter, r *http.Request) (any, error) {
	_, _, dr := a.snapshot()
	if dr == nil {
		return map[string]any{"done": false}, nil
	}
	type grp struct {
		Size  int64      `json:"size"`
		Gain  int64      `json:"gain"`
		Files []fileItem `json:"files"`
	}
	var gs []grp
	for i, g := range dr.Groups {
		if i >= 100 {
			break
		}
		x := grp{Size: g.Size, Gain: g.Gain}
		for _, f := range g.Files {
			x.Files = append(x.Files, a.file(f))
		}
		gs = append(gs, x)
	}
	return map[string]any{"done": true, "complete": dr.Complete, "gain": dr.Gain, "groups": gs,
		"unverified": len(dr.Unverified), "total": len(dr.Groups)}, nil
}

// ---------------------------------------------------------------- libération

func (a *App) apiPrepare(w http.ResponseWriter, r *http.Request) (any, error) {
	var req struct {
		Paths []string `json:"paths"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	if a.readOnly {
		return nil, actions.ErrReadOnly
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	a.out.Line("Préparation d'un récapitulatif pour %d élément(s) (aucune modification).", len(req.Paths))
	pv, err := a.mgr.Prepare(ctx, actions.Free, req.Paths)
	if err != nil {
		if pv != nil && len(pv.Rejected) > 0 {
			return nil, fmt.Errorf("%v (%s)", err, strings.Join(pv.Rejected, " ; "))
		}
		return nil, err
	}
	a.out.Est("Récapitulatif : %s fichiers, gain estimé %s, %s épinglés.", F.Int(pv.Files), F.Bytes(pv.EstGain), F.Int(pv.PinnedFiles))
	return pv, nil
}

func (a *App) apiRestorePrepare(w http.ResponseWriter, r *http.Request) (any, error) {
	var req struct {
		Journal string `json:"journal"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	if !a.isJournal(req.Journal) {
		return nil, errors.New("journal inconnu")
	}
	return a.mgr.PrepareRestore(req.Journal)
}

func (a *App) apiExecute(w http.ResponseWriter, r *http.Request) (any, error) {
	var req struct {
		Token   string `json:"token"`
		Confirm string `json:"confirm"`
		Risk    string `json:"risk"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	g := a.gui
	g.mu.Lock()
	if g.op.Running || g.scanning {
		g.mu.Unlock()
		return nil, errors.New("une autre opération est en cours")
	}
	ctx, cancel := context.WithCancel(context.Background())
	g.op = opState{Running: true, cancel: cancel}
	g.mu.Unlock()
	started := make(chan error, 1)
	go func() {
		defer cancel()
		first := true
		run, err := a.mgr.Execute(ctx, req.Token, req.Confirm, req.Risk, func(rr *actions.Run) {
			if first {
				first = false
				started <- nil
			}
			g.mu.Lock()
			g.op.Kind, g.op.Seen, g.op.Changed, g.op.Errors = string(rr.Kind), rr.Seen, rr.Changed, rr.Errors
			g.mu.Unlock()
		})
		if first {
			started <- err
		}
		g.mu.Lock()
		g.op.Running = false
		if err != nil {
			g.op.Err = err.Error()
		} else {
			g.op.Run = run
			g.op.Kind, g.op.Seen, g.op.Changed, g.op.Errors = string(run.Kind), run.Seen, run.Changed, run.Errors
		}
		g.mu.Unlock()
		if err == nil {
			a.mu.Lock()
			a.lastRun = run
			a.mu.Unlock()
			a.out.Title("Opération « %s » réalisée depuis l'interface", run.Kind)
			for _, p := range run.Paths {
				a.out.Line("  • %s", pth(p))
			}
			a.showRun(run)
			a.share.Nudge()
		} else {
			a.out.Warn("Opération refusée : %v", err)
		}
	}()
	if err := <-started; err != nil {
		return nil, err
	}
	return map[string]bool{"started": true}, nil
}

func (a *App) apiOp(w http.ResponseWriter, r *http.Request) (any, error) {
	g := a.gui
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.op, nil
}

func (a *App) apiOpCancel(w http.ResponseWriter, r *http.Request) (any, error) {
	g := a.gui
	g.mu.Lock()
	c := g.op.cancel
	g.mu.Unlock()
	if c != nil {
		c()
	}
	return map[string]bool{"ok": true}, nil
}

func (a *App) isJournal(p string) bool {
	dir := filepath.Join(a.dataDir, "operations")
	for _, j := range actions.ListJournals(dir) {
		if a.same(j, p) {
			return true
		}
	}
	return false
}

func (a *App) apiVerify(w http.ResponseWriter, r *http.Request) (any, error) {
	var req struct {
		Journal string `json:"journal"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	a.mu.Lock()
	run := a.lastRun
	a.mu.Unlock()
	if req.Journal != "" && (run == nil || !a.same(run.Journal, req.Journal)) {
		if !a.isJournal(req.Journal) {
			return nil, errors.New("journal inconnu")
		}
		var err error
		if run, err = actions.RunFromJournal(req.Journal); err != nil {
			return nil, err
		}
	}
	if run == nil {
		return nil, errors.New("aucune opération à vérifier")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	v := a.mgr.VerifyRun(ctx, run)
	a.out.Title("Vérification depuis l'interface")
	a.out.Line("Fichiers : %s · en ligne : %s · encore locaux : %s (%s) · libéré mesuré : %s",
		F.Int(v.Files), F.Int(v.Online), F.Int(v.StillLocal), F.Bytes(v.StillLocalSize), F.Bytes(v.Freed))
	a.share.Nudge()
	return map[string]any{"verify": v, "run": run}, nil
}

func (a *App) apiHistory(w http.ResponseWriter, r *http.Request) (any, error) {
	type h struct {
		Journal string    `json:"journal"`
		Kind    string    `json:"kind"`
		Started time.Time `json:"started"`
		Paths   []string  `json:"paths"`
	}
	var out []h
	for i, j := range actions.ListJournals(filepath.Join(a.dataDir, "operations")) {
		if i >= 30 {
			break
		}
		if run, err := actions.RunFromJournal(j); err == nil {
			out = append(out, h{Journal: j, Kind: string(run.Kind), Started: run.Started, Paths: run.Paths})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out, nil
}

// ---------------------------------------------------------------- divers

func (a *App) apiExport(w http.ResponseWriter, r *http.Request) (any, error) {
	_, d, _ := a.snapshot()
	if d == nil {
		return nil, errors.New("lancez d'abord l'analyse")
	}
	a.mu.Lock()
	a.export(false)
	a.mu.Unlock()
	return map[string]bool{"ok": true}, nil
}

func (a *App) allowedOpen(p string) bool {
	for _, t := range a.disc.Targets {
		if scan.IsWithin(p, t.Path, a.same) {
			return true
		}
	}
	return false
}

func (a *App) apiOpen(w http.ResponseWriter, r *http.Request) (any, error) {
	var req struct {
		Path   string `json:"path"`
		Select bool   `json:"select"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	if !a.allowedOpen(req.Path) {
		return nil, errors.New("emplacement non autorisé")
	}
	return map[string]bool{"ok": true}, a.sys.OpenInExplorer(req.Path, req.Select)
}

func (a *App) apiSettings(w http.ResponseWriter, r *http.Request) (any, error) {
	var req struct {
		Which string `json:"which"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	switch req.Which {
	case "storagesense":
		return map[string]bool{"ok": true}, a.sys.OpenSettings("ms-settings:storagesense")
	case "recycle":
		return map[string]bool{"ok": true}, a.sys.OpenInExplorer("shell:RecycleBinFolder", false)
	}
	return nil, errors.New("réglage inconnu")
}

func (a *App) apiQuit(w http.ResponseWriter, r *http.Request) (any, error) {
	go func() {
		time.Sleep(300 * time.Millisecond)
		a.out.Line("Fermeture demandée depuis l'interface.")
		a.quit(0)
	}()
	return map[string]bool{"ok": true}, nil
}
