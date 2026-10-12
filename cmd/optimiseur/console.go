package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"optimiseur-onedrive/internal/report"
	"optimiseur-onedrive/internal/share"
)

// Les chemins sont encadrés par ces marqueurs : affichés tels quels à l'écran,
// masqués dans le partage si l'utilisateur l'a demandé.
const pathOpen, pathClose = "\x01", "\x02"

func pth(p string) string { return pathOpen + p + pathClose }

const (
	cReset  = "\x1b[0m"
	cBold   = "\x1b[1m"
	cDim    = "\x1b[2m"
	cRed    = "\x1b[91m"
	cGreen  = "\x1b[92m"
	cYellow = "\x1b[93m"
	cBlue   = "\x1b[94m"
	cCyan   = "\x1b[96m"
	cMag    = "\x1b[95m"
)

type Console struct {
	mu       sync.Mutex
	w        io.Writer
	color    bool
	logFile  *os.File
	share    *share.Client
	mask     *report.Masker
	progress bool // une ligne de progression est affichée
}

func expand(s string, f func(string) string) string {
	if !strings.Contains(s, pathOpen) {
		return s
	}
	var b strings.Builder
	for {
		i := strings.Index(s, pathOpen)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		j := strings.Index(s[i:], pathClose)
		if j < 0 {
			b.WriteString(strings.ReplaceAll(s, pathOpen, ""))
			return b.String()
		}
		b.WriteString(s[:i])
		b.WriteString(f(s[i+1 : i+j]))
		s = s[i+j+1:]
	}
}

func stripANSI(s string) string {
	if !strings.Contains(s, "\x1b[") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && !(s[j] >= 'A' && s[j] <= 'Z' || s[j] >= 'a' && s[j] <= 'z') {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// emit affiche une ligne et la transmet au journal local et au partage.
func (c *Console) emit(level, color, s string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	screen := expand(s, func(p string) string { return p })
	if c.progress {
		c.clearProgress()
	}
	if c.color && color != "" {
		fmt.Fprintln(c.w, color+screen+cReset)
	} else {
		fmt.Fprintln(c.w, stripANSI(screen))
	}
	plain := stripANSI(screen)
	if c.logFile != nil {
		fmt.Fprintf(c.logFile, "%s [%s] %s\n", time.Now().Format("15:04:05"), level, plain)
	}
	if c.share != nil {
		masked := stripANSI(expand(s, func(p string) string { return c.mask.Path(p) }))
		c.share.Log(level, c.mask.Text(masked))
	}
}

func (c *Console) Line(s string, a ...any)  { c.emit("info", "", sprintf(s, a...)) }
func (c *Console) Title(s string, a ...any) { c.emit("titre", cBold+cCyan, sprintf(s, a...)) }
func (c *Console) Ok(s string, a ...any)    { c.emit("ok", cGreen, sprintf(s, a...)) }
func (c *Console) Warn(s string, a ...any)  { c.emit("attention", cYellow, sprintf(s, a...)) }
func (c *Console) Err(s string, a ...any)   { c.emit("erreur", cRed, sprintf(s, a...)) }
func (c *Console) Dim(s string, a ...any)   { c.emit("info", cDim, sprintf(s, a...)) }
func (c *Console) Est(s string, a ...any)   { c.emit("estimation", cMag, sprintf(s, a...)) }
func (c *Console) Blank()                   { c.emit("info", "", "") }

func sprintf(s string, a ...any) string {
	if len(a) == 0 {
		return s
	}
	return fmt.Sprintf(s, a...)
}

func (c *Console) Rule() { c.emit("info", cDim, strings.Repeat("─", 78)) }

func (c *Console) clearProgress() {
	if c.color {
		fmt.Fprint(c.w, "\r\x1b[2K")
	} else {
		fmt.Fprint(c.w, "\r"+strings.Repeat(" ", 110)+"\r")
	}
	c.progress = false
}

// Progress affiche une ligne qui se réécrit (non journalisée).
func (c *Console) Progress(s string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s = expand(s, func(p string) string { return p })
	if utf8.RuneCountInString(s) > 105 {
		r := []rune(s)
		s = string(r[:50]) + "…" + string(r[len(r)-54:])
	}
	if c.color {
		fmt.Fprint(c.w, "\r\x1b[2K"+cCyan+s+cReset)
	} else {
		fmt.Fprintf(c.w, "\r%-108s", s)
	}
	c.progress = true
}

func (c *Console) C(color, s string) string {
	if !c.color {
		return s
	}
	return color + s + cReset
}

// ---- saisie

type Input struct{ r *bufio.Reader }

func (in *Input) ReadLine() (string, bool) {
	s, err := in.r.ReadString('\n')
	if err != nil && s == "" {
		return "", false
	}
	return strings.TrimSpace(strings.TrimRight(s, "\r\n")), true
}

// Ask pose une question ; la réponse est journalisée.
func (a *App) Ask(prompt string) string {
	a.out.mu.Lock()
	if a.out.progress {
		a.out.clearProgress()
	}
	if a.out.color {
		fmt.Fprint(a.out.w, cBold+cYellow+"› "+cReset+prompt+" ")
	} else {
		fmt.Fprint(a.out.w, "> "+prompt+" ")
	}
	a.out.mu.Unlock()
	s, ok := a.in.ReadLine()
	if !ok {
		a.quit(0)
	}
	if a.out.logFile != nil {
		fmt.Fprintf(a.out.logFile, "%s [saisie] %s %s\n", time.Now().Format("15:04:05"), stripANSI(prompt), s)
	}
	if a.out.share != nil {
		a.out.share.Log("saisie", stripANSI(prompt)+" "+s)
	}
	return s
}

// YesNo : réponse oui/non avec valeur par défaut.
func (a *App) YesNo(prompt string, def bool) bool {
	suffix := "[O/n]"
	if !def {
		suffix = "[o/N]"
	}
	for {
		s := strings.ToLower(a.Ask(prompt + " " + suffix))
		switch s {
		case "":
			return def
		case "o", "oui", "y", "yes":
			return true
		case "n", "non", "no":
			return false
		}
		a.out.Warn("Répondez par O (oui) ou N (non).")
	}
}

func (a *App) Pause() { a.Ask("Appuyez sur Entrée pour continuer…") }

// bar dessine une barre proportionnelle.
func bar(part, total int64, width int) string {
	if total <= 0 || width <= 0 {
		return strings.Repeat("░", width)
	}
	n := int(float64(part) / float64(total) * float64(width))
	if part > 0 && n == 0 {
		n = 1
	}
	if n > width {
		n = width
	}
	return strings.Repeat("█", n) + strings.Repeat("░", width-n)
}

func pad(s string, n int) string {
	l := utf8.RuneCountInString(s)
	if l >= n {
		return s
	}
	return s + strings.Repeat(" ", n-l)
}

func lpad(s string, n int) string {
	l := utf8.RuneCountInString(s)
	if l >= n {
		return s
	}
	return strings.Repeat(" ", n-l) + s
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n < 10 {
		return string(r[:n])
	}
	return string(r[:n/2-1]) + "…" + string(r[len(r)-(n-n/2):])
}

// wrap coupe un texte en lignes de largeur max (pour la console).
func wrap(s string, width int) []string {
	var out []string
	for _, para := range strings.Split(s, "\n") {
		words := strings.Fields(para)
		line := ""
		for _, w := range words {
			if line != "" && utf8.RuneCountInString(line)+1+utf8.RuneCountInString(w) > width {
				out = append(out, line)
				line = w
			} else if line == "" {
				line = w
			} else {
				line += " " + w
			}
		}
		out = append(out, line)
	}
	return out
}
