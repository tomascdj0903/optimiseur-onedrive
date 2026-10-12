// Package format : présentation des tailles et durées en français.
package format

import (
	"fmt"
	"strings"
	"time"
)

// Bytes affiche une taille comme l'Explorateur Windows (base 1024, « Go »).
func Bytes(b int64) string {
	neg := b < 0
	if neg {
		b = -b
	}
	units := []string{"octets", "Ko", "Mo", "Go", "To"}
	v := float64(b)
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	var s string
	switch {
	case i == 0:
		s = fmt.Sprintf("%d %s", b, units[0])
	case v >= 100:
		s = fmt.Sprintf("%.0f %s", v, units[i])
	default:
		s = strings.Replace(fmt.Sprintf("%.1f %s", v, units[i]), ".", ",", 1)
	}
	if neg {
		return "-" + s
	}
	return s
}

// Int affiche un entier avec séparateur de milliers.
func Int(n int64) string {
	s := fmt.Sprintf("%d", n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteRune(' ')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

func Pct(part, total int64) string {
	if total <= 0 {
		return "0 %"
	}
	return strings.Replace(fmt.Sprintf("%.0f %%", float64(part)*100/float64(total)), ".", ",", 1)
}

func Duration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%d s", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%d min %02d s", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%d h %02d min", int(d.Hours()), int(d.Minutes())%60)
}

func Date(t time.Time) string {
	if t.IsZero() {
		return "?"
	}
	return t.Format("02/01/2006")
}

// GB convertit en gigaoctets (base 1024).
func GB(n int64) int64 { return n << 30 }
