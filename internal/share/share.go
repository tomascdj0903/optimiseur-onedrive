// Package share envoie, UNIQUEMENT avec l'accord de l'utilisateur, le journal
// de la console et le rapport vers l'espace de suivi (Supabase).
// Aucun contenu de fichier n'est jamais envoyé.
package share

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Line struct {
	Seq   int       `json:"s"`
	Time  time.Time `json:"t"`
	Level string    `json:"l"`
	Text  string    `json:"m"`
}

type Client struct {
	BaseURL string
	Key     string
	HTTP    *http.Client

	SessionID string
	Code      string

	mu      sync.Mutex
	buf     []Line
	seq     int
	enabled bool
	lastErr string
	sent    int
	dropped int
	stop    chan struct{}
	done    chan struct{}
	wake    chan struct{}
}

func NewUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func New(baseURL, key string) *Client {
	id := NewUUID()
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Key: key, HTTP: &http.Client{Timeout: 20 * time.Second},
		SessionID: id, Code: strings.ToUpper(strings.ReplaceAll(id, "-", "")[:6])}
}

func (c *Client) rpc(ctx context.Context, fn string, body any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/rest/v1/rpc/"+fn, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apikey", c.Key)
	if strings.HasPrefix(c.Key, "eyJ") {
		req.Header.Set("Authorization", "Bearer "+c.Key)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 400))
		return fmt.Errorf("HTTP %d : %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	io.Copy(io.Discard, resp.Body)
	return nil
}

// Start crée la session distante puis lance l'envoi périodique.
func (c *Client) Start(ctx context.Context, version, machine, osInfo string, masked bool, info map[string]any) error {
	err := c.rpc(ctx, "odo_start_session", map[string]any{"p_id": c.SessionID, "p_version": version,
		"p_machine": machine, "p_os": osInfo, "p_masked": masked, "p_info": info})
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.enabled = true
	c.stop, c.done, c.wake = make(chan struct{}), make(chan struct{}), make(chan struct{}, 1)
	c.mu.Unlock()
	go c.loop()
	return nil
}

func (c *Client) Enabled() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.enabled
}

// Log ajoute une ligne à envoyer (non bloquant).
func (c *Client) Log(level, text string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.enabled {
		return
	}
	c.seq++
	if len(c.buf) >= 20000 {
		c.buf = c.buf[1:]
		c.dropped++
	}
	c.buf = append(c.buf, Line{Seq: c.seq, Time: time.Now().UTC(), Level: level, Text: text})
}

func (c *Client) loop() {
	defer close(c.done)
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-c.stop:
			c.flush(context.Background())
			return
		case <-t.C:
		case <-c.wake:
		}
		c.flush(context.Background())
	}
}

func (c *Client) flush(ctx context.Context) {
	for {
		c.mu.Lock()
		n := len(c.buf)
		if n > 400 {
			n = 400
		}
		batch := append([]Line(nil), c.buf[:n]...)
		c.mu.Unlock()
		if len(batch) == 0 {
			return
		}
		cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := c.rpc(cctx, "odo_push_logs", map[string]any{"p_session": c.SessionID, "p_lines": batch})
		cancel()
		c.mu.Lock()
		if err != nil {
			c.lastErr = err.Error()
			c.mu.Unlock()
			return // nouvel essai au prochain tour
		}
		c.lastErr = ""
		c.buf = c.buf[len(batch):]
		c.sent += len(batch)
		c.mu.Unlock()
	}
}

// Report envoie un rapport complet (texte + résumé JSON).
func (c *Client) Report(ctx context.Context, kind, text string, summary any) error {
	if !c.Enabled() {
		return fmt.Errorf("partage désactivé")
	}
	return c.rpc(ctx, "odo_push_report", map[string]any{"p_session": c.SessionID, "p_kind": kind,
		"p_summary": summary, "p_text": text})
}

// Status : lignes envoyées, en attente, dernière erreur.
func (c *Client) Status() (sent, pending int, lastErr string) {
	if c == nil {
		return 0, 0, ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sent, len(c.buf), c.lastErr
}

// Disable arrête le partage (les lignes en attente sont abandonnées).
func (c *Client) Disable() {
	if c == nil {
		return
	}
	c.mu.Lock()
	was := c.enabled
	c.enabled = false
	c.buf = nil
	c.mu.Unlock()
	if was {
		close(c.stop)
		<-c.done
	}
}

// Close envoie ce qui reste (délai maximum) puis arrête.
func (c *Client) Close(timeout time.Duration) {
	if !c.Enabled() {
		return
	}
	c.mu.Lock()
	c.enabled = false
	c.mu.Unlock()
	close(c.stop)
	select {
	case <-c.done:
	case <-time.After(timeout):
	}
}

// Nudge déclenche un envoi immédiat.
func (c *Client) Nudge() {
	if c == nil || c.wake == nil {
		return
	}
	select {
	case c.wake <- struct{}{}:
	default:
	}
}
