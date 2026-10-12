package main

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

var accentReplacer = strings.NewReplacer(
	"à", "a", "â", "a", "ä", "a", "é", "e", "è", "e", "ê", "e", "ë", "e",
	"î", "i", "ï", "i", "ô", "o", "ö", "o", "ù", "u", "û", "u", "ü", "u",
	"ç", "c", "ñ", "n", "œ", "oe", "æ", "ae",
)

// slugify transforme des mots en identifiant lisible pour une adresse web :
// minuscules, sans accents, mots separes par des tirets.
func slugify(parts ...string) string {
	s := accentReplacer.Replace(strings.ToLower(strings.Join(parts, " ")))

	var b strings.Builder
	prevDash := true
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
		} else if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// rowQuerier est satisfait par *sql.DB et par *sql.Tx.
type rowQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
}

func slugFree(q rowQuerier, slug, nom, quartier string) (bool, error) {
	var n int
	err := q.QueryRow(
		`SELECT COUNT(*) FROM restaurants WHERE slug = ? AND NOT (nom = ? AND quartier = ?)`,
		slug, nom, quartier).Scan(&n)
	return n == 0, err
}

// uniqueSlug choisit l'adresse courte d'un restaurant : son nom d'abord,
// puis nom + quartier, puis un numero. Un restaurant deja enregistre
// garde la meme adresse, pour que les QR codes imprimes restent valides.
func uniqueSlug(q rowQuerier, nom, quartier string) (string, error) {
	base := slugify(nom)
	if base == "" {
		base = "resto"
	}
	candidates := []string{base}
	if withQuartier := slugify(nom, quartier); withQuartier != "" && withQuartier != base {
		candidates = append(candidates, withQuartier)
	}

	for _, c := range candidates {
		free, err := slugFree(q, c, nom, quartier)
		if err != nil {
			return "", err
		}
		if free {
			return c, nil
		}
	}

	last := candidates[len(candidates)-1]
	for i := 2; i < 1000; i++ {
		c := fmt.Sprintf("%s-%d", last, i)
		free, err := slugFree(q, c, nom, quartier)
		if err != nil {
			return "", err
		}
		if free {
			return c, nil
		}
	}
	return "", errors.New("impossible de choisir une adresse courte unique")
}
