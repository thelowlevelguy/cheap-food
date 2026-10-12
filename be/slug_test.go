package main

import "testing"

func TestSlugify(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"Cantine Étudiants Sor"}, "cantine-etudiants-sor"},
		{[]string{"Chez Awa", "Sud"}, "chez-awa-sud"},
		{[]string{"  L'Escale -- du Fleuve!  "}, "l-escale-du-fleuve"},
		{[]string{"Café Œuf"}, "cafe-oeuf"},
		{[]string{"???"}, ""},
	}
	for _, c := range cases {
		if got := slugify(c.in...); got != c.want {
			t.Errorf("slugify(%q) = %q, attendu %q", c.in, got, c.want)
		}
	}
}

func TestSlugsAreUniqueAndStable(t *testing.T) {
	d := newTestDB(t)
	read := func(nom, quartier string) string {
		var s string
		if err := d.QueryRow(`SELECT slug FROM restaurants WHERE nom = ? AND quartier = ?`, nom, quartier).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	add := func(nom, quartier string) {
		if _, err := insertRestaurantOnly(d, NewRestaurantInput{Nom: nom, Quartier: quartier}); err != nil {
			t.Fatalf("ajout de %s (%s): %v", nom, quartier, err)
		}
	}

	add("Chez Awa", "Sud")
	add("Chez Awa", "Sor")  // meme nom, autre quartier
	add("Chez Awa", "Sor")  // renvoye une seconde fois
	add("Chez Awa!", "Sor") // autre nom, meme adresse apres nettoyage

	if got := read("Chez Awa", "Sud"); got != "chez-awa" {
		t.Errorf("premier restaurant : %q", got)
	}
	if got := read("Chez Awa", "Sor"); got != "chez-awa-sor" {
		t.Errorf("meme nom, autre quartier : %q", got)
	}
	if got := read("Chez Awa!", "Sor"); got != "chez-awa-sor-2" {
		t.Errorf("collision apres nettoyage : %q", got)
	}
	if n := countRows(t, d, "restaurants"); n != 3 {
		t.Errorf("attendu 3 restaurants, obtenu %d", n)
	}
}
