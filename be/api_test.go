package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// useTestDB remplace la base globale utilisee par les handlers.
func useTestDB(t *testing.T) {
	t.Helper()
	old := db
	db = newTestDB(t)
	t.Cleanup(func() { db = old })
}

func call(h http.HandlerFunc, target string, pathValues map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for k, v := range pathValues {
		req.SetPathValue(k, v)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func seedYassa(t *testing.T) {
	t.Helper()
	// Le restaurant partenaire (actif) est cree par l'admin.
	if _, err := insertRestaurantOnly(db, NewRestaurantInput{Nom: "Cantine Etudiants Sor", Quartier: "Sor", Type: "cantine"}); err != nil {
		t.Fatal(err)
	}
	entries := []SeedEntry{
		{Resto: "Cantine Etudiants Sor", Quartier: "Sor", Type: "cantine", Plat: "Yassa poulet", Prix: 900, Portion: "petite"},
		{Resto: "Chez Awa", Quartier: "Sud", Type: "cantine", Plat: "Yassa poulet", Prix: 1100, Portion: "moyenne"},
		{Resto: "Chez Fatou", Quartier: "Sor", Type: "cantine", Plat: "Yassa poulet", Prix: 1200, Portion: "moyenne"},
	}
	for _, e := range entries {
		if err := insertEntry(db, e); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMoinsCherReturnsIdsAndHidesUnavailable(t *testing.T) {
	useTestDB(t)
	seedYassa(t)
	if _, err := db.Exec(`UPDATE prix SET disponible = 0 WHERE restaurant_id = (SELECT id FROM restaurants WHERE nom = 'Chez Fatou')`); err != nil {
		t.Fatal(err)
	}

	rec := call(handleMoinsCher, "/plats/Yassa%20poulet/moins-cher", map[string]string{"nom": "Yassa poulet"})
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d : %s", rec.Code, rec.Body.String())
	}
	var res []PrixResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Fatalf("attendu 2 resultats (le plat epuise est cache), obtenu %d", len(res))
	}
	if res[0].PrixFCFA != 900 || res[1].PrixFCFA != 1100 {
		t.Errorf("tri par prix incorrect : %d puis %d", res[0].PrixFCFA, res[1].PrixFCFA)
	}
	if !uuidV4.MatchString(res[0].RestoID) || !uuidV4.MatchString(res[0].PrixID) {
		t.Errorf("identifiants attendus au format UUID : %q %q", res[0].RestoID, res[0].PrixID)
	}
	if res[0].RestoSlug != "cantine-etudiants-sor" {
		t.Errorf("adresse courte inattendue : %q", res[0].RestoSlug)
	}
	if !res[0].Commandable || res[1].Commandable {
		t.Errorf("seul le restaurant partenaire doit etre commandable : %v %v", res[0].Commandable, res[1].Commandable)
	}
	if res[0].Photo != nil {
		t.Error("sans photo, le champ doit etre null")
	}
}

func TestMoinsCherUnknownDish(t *testing.T) {
	useTestDB(t)
	rec := call(handleMoinsCher, "/plats/Inconnu/moins-cher", map[string]string{"nom": "Inconnu"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("attendu 404, obtenu %d", rec.Code)
	}
}

func TestRestosListHasIdsAndSlugs(t *testing.T) {
	useTestDB(t)
	seedYassa(t)

	rec := call(handleRestos, "/restos", nil)
	var list []RestoResume
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("attendu 3 restaurants, obtenu %d", len(list))
	}
	for _, r := range list {
		if !uuidV4.MatchString(r.ID) || r.Slug == "" {
			t.Errorf("identifiant ou adresse courte manquants : %+v", r)
		}
	}
}

func TestMenuRestoBySlug(t *testing.T) {
	useTestDB(t)
	seedYassa(t)
	if _, err := db.Exec(`UPDATE prix SET disponible = 0`); err != nil {
		t.Fatal(err)
	}

	rec := call(handleMenuResto, "/restos/cantine-etudiants-sor/menu", map[string]string{"slug": "cantine-etudiants-sor"})
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d : %s", rec.Code, rec.Body.String())
	}
	var m MenuResto
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m.Nom != "Cantine Etudiants Sor" || !m.Commandable {
		t.Errorf("restaurant inattendu : %+v", m.RestoResume)
	}
	if len(m.Plats) != 1 || m.Plats[0].Disponible || !uuidV4.MatchString(m.Plats[0].PrixID) {
		t.Errorf("le plat epuise doit rester dans la liste, marque indisponible : %+v", m.Plats)
	}

	rec = call(handleMenuResto, "/restos/nexiste-pas/menu", map[string]string{"slug": "nexiste-pas"})
	if rec.Code != http.StatusNotFound {
		t.Errorf("attendu 404 pour un restaurant inconnu, obtenu %d", rec.Code)
	}
}
