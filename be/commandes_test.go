package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

var code4 = regexp.MustCompile(`^[0-9]{4}$`)

func TestFraisRestoTiers(t *testing.T) {
	cases := map[int]int{1: 50, 2000: 50, 2001: 100, 5000: 100, 5001: 200, 100000: 200}
	for sous, want := range cases {
		if got := fraisResto(sous); got != want {
			t.Errorf("fraisResto(%d) = %d, attendu %d", sous, got, want)
		}
	}
}

func TestFraisRestoFactures(t *testing.T) {
	jusquau := time.Date(2027, 1, 31, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		label string
		now   time.Time
		want  int
	}{
		{"pendant la periode gratuite", time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC), 0},
		{"le dernier jour est inclus", time.Date(2027, 1, 31, 23, 59, 0, 0, time.UTC), 0},
		{"le lendemain, les frais sont factures", time.Date(2027, 2, 1, 0, 0, 0, 0, time.UTC), 100},
	}
	for _, c := range cases {
		if got := fraisRestoFactures(100, c.now, jusquau); got != c.want {
			t.Errorf("%s : %d, attendu %d", c.label, got, c.want)
		}
	}
	if got := fraisRestoFactures(100, time.Now(), time.Time{}); got != 100 {
		t.Errorf("sans periode gratuite, les frais doivent etre factures : %d", got)
	}
}

func TestNormaliserTelephone(t *testing.T) {
	for _, in := range []string{"77 123 45 67", "+221 77 123 45 67", "00221771234567", "771234567", "77.123.45.67", "221771234567"} {
		got, ok := normaliserTelephone(in)
		if !ok || got != "771234567" {
			t.Errorf("normaliserTelephone(%q) = %q, %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "12345", "77a123456", "331234567", "+22177123456", "7712345678", "<script>"} {
		if got, ok := normaliserTelephone(in); ok {
			t.Errorf("%q aurait du etre refuse (obtenu %q)", in, got)
		}
	}
}

type jeu struct{ restoID, yassaID, thieboID string }

func prixID(t *testing.T, d *sql.DB, restoID, plat string) string {
	t.Helper()
	var id string
	err := d.QueryRow(`SELECT pr.id FROM prix pr JOIN plats p ON p.id = pr.plat_id
		WHERE p.nom = ? AND pr.restaurant_id = ?`, plat, restoID).Scan(&id)
	if err != nil {
		t.Fatalf("prix de %s introuvable : %v", plat, err)
	}
	return id
}

// jeuDeTest cree un restaurant partenaire avec deux plats.
func jeuDeTest(t *testing.T, d *sql.DB) jeu {
	t.Helper()
	id, err := insertRestaurantOnly(d, NewRestaurantInput{Nom: "Cantine Test", Quartier: "Sor", Type: "cantine"})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []SeedEntry{
		{Resto: "Cantine Test", Quartier: "Sor", Type: "cantine", Plat: "Yassa poulet", Prix: 900, Portion: "petite"},
		{Resto: "Cantine Test", Quartier: "Sor", Type: "cantine", Plat: "Thiéboudienne", Prix: 700, Portion: "petite"},
	} {
		if err := insertEntry(d, e); err != nil {
			t.Fatal(err)
		}
	}
	return jeu{id, prixID(t, d, id, "Yassa poulet"), prixID(t, d, id, "Thiéboudienne")}
}

func commandeValide(j jeu) CommandeInput {
	return CommandeInput{
		RestaurantID: j.restoID, Mode: "retrait", ClientNom: "Awa", ClientTelephone: "77 123 45 67",
		Lignes: []LigneInput{{PrixID: j.yassaID, Quantite: 2}, {PrixID: j.thieboID, Quantite: 1}},
	}
}

func TestCreerCommandeCalculatesOnServer(t *testing.T) {
	d := newTestDB(t)
	j := jeuDeTest(t, d)

	vue, err := creerCommande(d, commandeValide(j), time.Now(), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	// 2 x 900 + 1 x 700 = 2500 F -> frais restaurant 100 F
	if vue.SousTotal != 2500 || vue.TotalAPayer != 2500 || vue.Statut != "attente_paiement" {
		t.Errorf("totaux ou statut inattendus : %+v", vue)
	}
	if !uuidV4.MatchString(vue.TokenSuivi) || len(vue.Lignes) != 2 || vue.CodeRetrait != "" {
		t.Errorf("reponse inattendue : %+v", vue)
	}

	var fraisR, factures int
	var tel, code string
	err = d.QueryRow(`SELECT frais_resto, frais_resto_facture, client_telephone, code_retrait
		FROM commandes WHERE token_suivi = ?`, vue.TokenSuivi).Scan(&fraisR, &factures, &tel, &code)
	if err != nil {
		t.Fatal(err)
	}
	if fraisR != 100 || factures != 100 || tel != "771234567" || !code4.MatchString(code) {
		t.Errorf("base : frais=%d factures=%d tel=%q code=%q", fraisR, factures, tel, code)
	}

	// Periode gratuite : les frais sont calcules mais factures 0 F.
	gratuit := time.Now().AddDate(0, 0, 10)
	vue2, err := creerCommande(d, commandeValide(j), time.Now(), gratuit)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT frais_resto, frais_resto_facture FROM commandes WHERE token_suivi = ?`, vue2.TokenSuivi).
		Scan(&fraisR, &factures); err != nil {
		t.Fatal(err)
	}
	if fraisR != 100 || factures != 0 {
		t.Errorf("periode gratuite : frais=%d factures=%d", fraisR, factures)
	}
}

func TestCreerCommandeMergesDuplicateLines(t *testing.T) {
	d := newTestDB(t)
	j := jeuDeTest(t, d)
	in := commandeValide(j)
	in.Lignes = []LigneInput{{PrixID: j.yassaID, Quantite: 1}, {PrixID: j.yassaID, Quantite: 2}}

	vue, err := creerCommande(d, in, time.Now(), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(vue.Lignes) != 1 || vue.Lignes[0].Quantite != 3 || vue.SousTotal != 2700 {
		t.Errorf("lignes mal fusionnees : %+v", vue)
	}
}

func TestCreerCommandeRejectsBadRequests(t *testing.T) {
	d := newTestDB(t)
	j := jeuDeTest(t, d)

	// Un restaurant non partenaire (actif = 0) et un plat d'un autre restaurant.
	if err := insertEntry(d, SeedEntry{Resto: "Chez Fatou", Quartier: "Sor", Type: "cantine", Plat: "Yassa poulet", Prix: 1200, Portion: "moyenne"}); err != nil {
		t.Fatal(err)
	}
	var fatouID string
	if err := d.QueryRow(`SELECT id FROM restaurants WHERE nom = 'Chez Fatou'`).Scan(&fatouID); err != nil {
		t.Fatal(err)
	}
	fatouPrix := prixID(t, d, fatouID, "Yassa poulet")

	cases := []struct {
		name string
		mut  func(*CommandeInput)
		want int
	}{
		{"livraison pas encore disponible", func(c *CommandeInput) { c.Mode = "livraison" }, http.StatusBadRequest},
		{"mode inconnu", func(c *CommandeInput) { c.Mode = "teleportation" }, http.StatusBadRequest},
		{"telephone invalide", func(c *CommandeInput) { c.ClientTelephone = "123" }, http.StatusBadRequest},
		{"prenom vide", func(c *CommandeInput) { c.ClientNom = "   " }, http.StatusBadRequest},
		{"prenom trop long", func(c *CommandeInput) { c.ClientNom = strings.Repeat("a", 61) }, http.StatusBadRequest},
		{"panier vide", func(c *CommandeInput) { c.Lignes = nil }, http.StatusBadRequest},
		{"quantite zero", func(c *CommandeInput) { c.Lignes[0].Quantite = 0 }, http.StatusBadRequest},
		{"quantite trop grande", func(c *CommandeInput) { c.Lignes[0].Quantite = 21 }, http.StatusBadRequest},
		{"plat inconnu", func(c *CommandeInput) { c.Lignes[0].PrixID = "nope" }, http.StatusBadRequest},
		{"plat d'un autre restaurant", func(c *CommandeInput) { c.Lignes[0].PrixID = fatouPrix }, http.StatusBadRequest},
		{"restaurant inconnu", func(c *CommandeInput) { c.RestaurantID = "00000000-0000-4000-8000-000000000000" }, http.StatusNotFound},
		{"restaurant non partenaire", func(c *CommandeInput) {
			c.RestaurantID = fatouID
			c.Lignes = []LigneInput{{PrixID: fatouPrix, Quantite: 1}}
		}, http.StatusConflict},
	}
	for _, c := range cases {
		in := commandeValide(j)
		c.mut(&in)
		_, err := creerCommande(d, in, time.Now(), time.Time{})
		es, ok := err.(*erreurSaisie)
		if !ok || es.code != c.want {
			t.Errorf("%s : attendu %d, obtenu %v", c.name, c.want, err)
		}
	}
	if n := countRows(t, d, "commandes"); n != 0 {
		t.Errorf("aucune commande ne doit etre enregistree apres un refus, obtenu %d", n)
	}

	// Plat epuise : refuse tant qu'il est indisponible.
	if _, err := d.Exec(`UPDATE prix SET disponible = 0 WHERE id = ?`, j.yassaID); err != nil {
		t.Fatal(err)
	}
	_, err := creerCommande(d, commandeValide(j), time.Now(), time.Time{})
	if es, ok := err.(*erreurSaisie); !ok || es.code != http.StatusConflict {
		t.Errorf("plat epuise : attendu 409, obtenu %v", err)
	}
}

func TestCodesRetraitAreUniqueAmongOpenOrders(t *testing.T) {
	d := newTestDB(t)
	j := jeuDeTest(t, d)
	seen := map[string]bool{}
	for i := 0; i < 40; i++ {
		vue, err := creerCommande(d, commandeValide(j), time.Now(), time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		var code string
		if err := d.QueryRow(`SELECT code_retrait FROM commandes WHERE token_suivi = ?`, vue.TokenSuivi).Scan(&code); err != nil {
			t.Fatal(err)
		}
		if seen[code] {
			t.Fatalf("code %s utilise deux fois", code)
		}
		seen[code] = true
	}
}

func post(h http.HandlerFunc, target, body string, pv map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range pv {
		req.SetPathValue(k, v)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestHandlerCreerCommandeRefusesUntrustedInput(t *testing.T) {
	useTestDB(t)
	j := jeuDeTest(t, db)

	valid := `{"restaurant_id":"` + j.restoID + `","mode":"retrait","client_nom":"Awa","client_telephone":"771234567",` +
		`"lignes":[{"prix_id":"` + j.yassaID + `","quantite":1}]`

	if rec := post(handleCreerCommande, "/commandes", valid+`}`, nil); rec.Code != http.StatusCreated {
		t.Fatalf("commande valide : %d %s", rec.Code, rec.Body.String())
	}
	// Le client ne peut pas imposer un prix ni un champ inconnu.
	if rec := post(handleCreerCommande, "/commandes", valid+`,"total_a_payer":1}`, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("champ inconnu : attendu 400, obtenu %d", rec.Code)
	}
	if rec := post(handleCreerCommande, "/commandes", `{"client_nom": 12}`, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("mauvais type : attendu 400, obtenu %d", rec.Code)
	}
	if rec := post(handleCreerCommande, "/commandes", `{"client_nom":"`+strings.Repeat("a", 20000)+`"}`, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("corps trop gros : attendu 400, obtenu %d", rec.Code)
	}
	if n := countRows(t, db, "commandes"); n != 1 {
		t.Errorf("attendu 1 commande, obtenu %d", n)
	}
}

func TestSuiviAndManualPaymentFlow(t *testing.T) {
	useTestDB(t)
	j := jeuDeTest(t, db)

	vue, err := creerCommande(db, commandeValide(j), time.Now(), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	suivi := func() CommandeVue {
		rec := call(handleSuivi, "/suivi/"+vue.TokenSuivi, map[string]string{"token": vue.TokenSuivi})
		if rec.Code != http.StatusOK {
			t.Fatalf("suivi : %d %s", rec.Code, rec.Body.String())
		}
		var s CommandeVue
		if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
			t.Fatal(err)
		}
		return s
	}

	// Avant paiement : pas de code de retrait.
	if s := suivi(); s.Statut != "attente_paiement" || s.CodeRetrait != "" || s.PayeLe != nil {
		t.Errorf("avant paiement : %+v", s)
	}

	var id string
	if err := db.QueryRow(`SELECT id FROM commandes WHERE token_suivi = ?`, vue.TokenSuivi).Scan(&id); err != nil {
		t.Fatal(err)
	}
	payer := func(id, ref string) int {
		return post(handleAdminPayee, "/admin/commandes/"+id+"/payee", `{"reference_paiement":"`+ref+`"}`, map[string]string{"id": id}).Code
	}

	if code := payer(id, "WAVE-123"); code != http.StatusOK {
		t.Fatalf("paiement : %d", code)
	}
	if s := suivi(); s.Statut != "payee" || !code4.MatchString(s.CodeRetrait) || s.PayeLe == nil {
		t.Errorf("apres paiement : %+v", s)
	}
	if code := payer(id, "WAVE-124"); code != http.StatusConflict {
		t.Errorf("seconde confirmation : attendu 409, obtenu %d", code)
	}

	// Une meme reference de paiement ne peut pas servir deux fois.
	autre, err := creerCommande(db, commandeValide(j), time.Now(), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	var autreID string
	if err := db.QueryRow(`SELECT id FROM commandes WHERE token_suivi = ?`, autre.TokenSuivi).Scan(&autreID); err != nil {
		t.Fatal(err)
	}
	if code := payer(autreID, "WAVE-123"); code != http.StatusConflict {
		t.Errorf("reference deja utilisee : attendu 409, obtenu %d", code)
	}
	if code := payer("00000000-0000-4000-8000-000000000000", "X"); code != http.StatusNotFound {
		t.Errorf("commande inconnue : attendu 404, obtenu %d", code)
	}
	if rec := call(handleSuivi, "/suivi/inconnu", map[string]string{"token": "inconnu"}); rec.Code != http.StatusNotFound {
		t.Errorf("jeton inconnu : attendu 404, obtenu %d", rec.Code)
	}
}

func TestAdminCommandesList(t *testing.T) {
	useTestDB(t)
	j := jeuDeTest(t, db)
	if _, err := creerCommande(db, commandeValide(j), time.Now(), time.Time{}); err != nil {
		t.Fatal(err)
	}

	rec := call(handleAdminCommandes, "/admin/commandes?statut=attente_paiement", nil)
	var list []CommandeAdmin
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ClientTelephone != "771234567" || list[0].TotalAPayer != 2500 || list[0].FraisResto != 100 {
		t.Errorf("liste inattendue : %+v", list)
	}
	if rec := call(handleAdminCommandes, "/admin/commandes?statut=payee", nil); strings.Contains(rec.Body.String(), "771234567") {
		t.Error("le filtre par statut ne fonctionne pas")
	}
	if rec := call(handleAdminCommandes, "/admin/commandes?statut=nimporte", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("statut inconnu : attendu 400, obtenu %d", rec.Code)
	}
}
