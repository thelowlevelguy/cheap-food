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

var codeAccesFormat = regexp.MustCompile(`^[ABCDEFGHJKMNPQRSTUVWXYZ2-9]{4}-[ABCDEFGHJKMNPQRSTUVWXYZ2-9]{4}-[ABCDEFGHJKMNPQRSTUVWXYZ2-9]{4}$`)

func TestCodeAccesFormatAndNormalisation(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		c, err := nouveauCodeAcces()
		if err != nil {
			t.Fatal(err)
		}
		if !codeAccesFormat.MatchString(c) || seen[c] {
			t.Fatalf("code invalide ou repete : %q", c)
		}
		seen[c] = true
	}
	if got, ok := normaliserCode("k7m4 9qxt-2hwp"); !ok || got != "K7M49QXT2HWP" {
		t.Errorf("normalisation : %q %v", got, ok)
	}
	for _, bad := range []string{"", "K7M4-9QXT", "K7M4-9QXT-2HWPX", "K7M4-9QXT-2HW0", "K7M4-9QXT-2HW!"} {
		if _, ok := normaliserCode(bad); ok {
			t.Errorf("%q aurait du etre refuse", bad)
		}
	}
}

func TestLoginSessionsAndCodeReset(t *testing.T) {
	d := newTestDB(t)
	j := jeuDeTest(t, d)
	now := time.Now()

	code, err := definirCodeAcces(d, j.restoID)
	if err != nil {
		t.Fatal(err)
	}
	// Seul le hash est conserve.
	var hash string
	if err := d.QueryRow(`SELECT code_acces_hash FROM restaurants WHERE id = ?`, j.restoID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if hash == "" || strings.Contains(hash, strings.ReplaceAll(code, "-", "")) {
		t.Fatal("le code ne doit pas etre stocke en clair")
	}

	token, id, err := connexion(d, strings.ToLower(code), now)
	if err != nil || id != j.restoID || token == "" {
		t.Fatalf("connexion : %v %q", err, id)
	}
	if got, _ := restaurantDepuisToken(d, token, now.Add(time.Hour)); got != j.restoID {
		t.Errorf("session valide non reconnue : %q", got)
	}
	if got, _ := restaurantDepuisToken(d, token, now.Add(31*24*time.Hour)); got != "" {
		t.Error("une session de plus de 30 jours doit expirer")
	}
	if got, _ := restaurantDepuisToken(d, "n-importe-quoi", now); got != "" {
		t.Error("un jeton inconnu doit etre refuse")
	}

	if _, _, err := connexion(d, "AAAA-BBBB-CCCC", now); err != errIdentifiants {
		t.Errorf("mauvais code : %v", err)
	}
	if _, _, err := connexion(d, "pas un code", now); err != errIdentifiants {
		t.Errorf("code mal forme : %v", err)
	}

	// Un restaurant non partenaire ne peut pas se connecter.
	if _, err := d.Exec(`UPDATE restaurants SET actif = 0`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := connexion(d, code, now); err != errIdentifiants {
		t.Errorf("restaurant inactif : %v", err)
	}
	if _, err := d.Exec(`UPDATE restaurants SET actif = 1`); err != nil {
		t.Fatal(err)
	}

	// Un nouveau code invalide l'ancien code ET les sessions ouvertes.
	nouveau, err := definirCodeAcces(d, j.restoID)
	if err != nil || nouveau == code {
		t.Fatalf("nouveau code : %v", err)
	}
	if got, _ := restaurantDepuisToken(d, token, now); got != "" {
		t.Error("l'ancienne session aurait du etre fermee")
	}
	if _, _, err := connexion(d, code, now); err != errIdentifiants {
		t.Errorf("l'ancien code ne doit plus marcher : %v", err)
	}
	if _, _, err := connexion(d, nouveau, now); err != nil {
		t.Errorf("le nouveau code doit marcher : %v", err)
	}
	if _, err := definirCodeAcces(d, "00000000-0000-4000-8000-000000000000"); err == nil {
		t.Error("un restaurant inconnu doit etre refuse")
	}
}

func TestLoginRateLimiter(t *testing.T) {
	l := newLimiteur(3, time.Minute)
	t0 := time.Now()
	for i := 0; i < 3; i++ {
		if l.bloque("1.2.3.4", t0) {
			t.Fatal("bloque trop tot")
		}
		l.echec("1.2.3.4", t0)
	}
	if !l.bloque("1.2.3.4", t0.Add(10*time.Second)) {
		t.Error("apres 3 echecs, l'adresse doit etre bloquee")
	}
	if l.bloque("5.6.7.8", t0) {
		t.Error("une autre adresse ne doit pas etre bloquee")
	}
	if l.bloque("1.2.3.4", t0.Add(2*time.Minute)) {
		t.Error("le blocage doit se terminer apres la fenetre")
	}
}

func proReq(h http.HandlerFunc, method, target, body, token string, pv map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range pv {
		req.SetPathValue(k, v)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// tablette cree un restaurant partenaire connecte.
func tablette(t *testing.T, d *sql.DB, j jeu) string {
	t.Helper()
	code, err := definirCodeAcces(d, j.restoID)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := connexion(d, code, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func commandePayee(t *testing.T, d *sql.DB, in CommandeInput) (id, code string) {
	t.Helper()
	vue, err := creerCommande(d, in, time.Now(), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT id, code_retrait FROM commandes WHERE token_suivi = ?`, vue.TokenSuivi).Scan(&id, &code); err != nil {
		t.Fatal(err)
	}
	if err := marquerPayee(d, id, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	return id, code
}

func TestProRoutesRequireSession(t *testing.T) {
	useTestDB(t)
	j := jeuDeTest(t, db)
	token := tablette(t, db, j)
	h := requireResto(handleProCommandes)

	if rec := proReq(h, "GET", "/pro/commandes", "", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("sans jeton : %d", rec.Code)
	}
	if rec := proReq(h, "GET", "/pro/commandes", "", "faux", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("faux jeton : %d", rec.Code)
	}
	if rec := proReq(h, "GET", "/pro/commandes", "", token, nil); rec.Code != http.StatusOK {
		t.Errorf("bon jeton : %d", rec.Code)
	}
	// Apres deconnexion, le jeton ne marche plus.
	proReq(requireResto(handleProDeconnexion), "POST", "/pro/deconnexion", "", token, nil)
	if rec := proReq(h, "GET", "/pro/commandes", "", token, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("apres deconnexion : %d", rec.Code)
	}
}

func TestProLoginHandlerAndRateLimit(t *testing.T) {
	useTestDB(t)
	j := jeuDeTest(t, db)
	code, _ := definirCodeAcces(db, j.restoID)
	limiteConnexion = newLimiteur(3, time.Minute)

	rec := proReq(handleProConnexion, "POST", "/pro/connexion", `{"code":"`+code+`"}`, "", nil)
	var out struct {
		Token      string      `json:"token"`
		Restaurant RestoResume `json:"restaurant"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Token == "" || out.Restaurant.Slug != "cantine-test" {
		t.Fatalf("connexion : %d %s", rec.Code, rec.Body.String())
	}

	for i := 0; i < 3; i++ {
		if rec := proReq(handleProConnexion, "POST", "/pro/connexion", `{"code":"AAAA-BBBB-CCCC"}`, "", nil); rec.Code != http.StatusUnauthorized {
			t.Fatalf("essai %d : attendu 401, obtenu %d", i+1, rec.Code)
		}
	}
	if rec := proReq(handleProConnexion, "POST", "/pro/connexion", `{"code":"`+code+`"}`, "", nil); rec.Code != http.StatusTooManyRequests {
		t.Errorf("apres trop d'echecs, meme le bon code est bloque : %d", rec.Code)
	}
}

func TestProOrderLifecycle(t *testing.T) {
	useTestDB(t)
	j := jeuDeTest(t, db)
	token := tablette(t, db, j)
	id, codeClient := commandePayee(t, db, commandeValide(j))

	list := func(statut string) []CommandePro {
		rec := proReq(requireResto(handleProCommandes), "GET", "/pro/commandes?statut="+statut, "", token, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("liste : %d %s", rec.Code, rec.Body.String())
		}
		var l []CommandePro
		if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
			t.Fatal(err)
		}
		return l
	}
	agir := func(action func(http.ResponseWriter, *http.Request, string), nom, body string) int {
		return proReq(requireResto(action), "POST", "/pro/commandes/"+id+"/"+nom, body, token, map[string]string{"id": id}).Code
	}

	l := list("payee")
	if len(l) != 1 || l[0].ID != id || len(l[0].Reference) != 4 || len(l[0].Lignes) != 2 {
		t.Fatalf("commande payee non visible : %+v", l)
	}
	brut := proReq(requireResto(handleProCommandes), "GET", "/pro/commandes", "", token, nil).Body.String()
	if strings.Contains(brut, codeClient) && len(codeClient) == 4 && strings.Contains(brut, `"`+codeClient+`"`) || strings.Contains(brut, "771234567") {
		t.Error("la tablette ne doit voir ni le code de retrait ni le telephone du client")
	}

	if c := agir(handleProPrete, "prete", ""); c != http.StatusConflict {
		t.Errorf("prete avant acceptation : attendu 409, obtenu %d", c)
	}
	if c := agir(handleProAccepter, "accepter", ""); c != http.StatusOK {
		t.Fatalf("accepter : %d", c)
	}
	if c := agir(handleProAccepter, "accepter", ""); c != http.StatusConflict {
		t.Errorf("accepter deux fois : attendu 409, obtenu %d", c)
	}
	if a := list("acceptee"); len(a) != 1 || a[0].PretEstime == nil || a[0].AccepteeLe == nil {
		t.Errorf("heure estimee manquante : %+v", a)
	}
	if c := agir(handleProPrete, "prete", ""); c != http.StatusOK {
		t.Fatalf("prete : %d", c)
	}
	if c := agir(handleProRetiree, "retiree", `{"code":"0000x"}`); c != http.StatusBadRequest {
		t.Errorf("mauvais code : attendu 400, obtenu %d", c)
	}
	if c := agir(handleProRetiree, "retiree", `{"code":"`+codeClient+`"}`); c != http.StatusOK {
		t.Fatalf("bon code : %d", c)
	}
	if c := agir(handleProRetiree, "retiree", `{"code":"`+codeClient+`"}`); c != http.StatusConflict {
		t.Errorf("retrait deux fois : attendu 409, obtenu %d", c)
	}
	if n := len(list("payee,acceptee,prete")); n != 0 {
		t.Errorf("une commande retiree ne doit plus apparaitre dans les commandes en cours (%d)", n)
	}
	if n := len(list("terminee")); n != 1 {
		t.Errorf("la commande doit etre dans les terminees (%d)", n)
	}
	if rec := proReq(requireResto(handleProCommandes), "GET", "/pro/commandes?statut=nimporte", "", token, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("statut inconnu : %d", rec.Code)
	}
}

func TestProCannotTouchAnotherRestaurantsData(t *testing.T) {
	useTestDB(t)
	j := jeuDeTest(t, db)
	id, _ := commandePayee(t, db, commandeValide(j))

	autreID, err := insertRestaurantOnly(db, NewRestaurantInput{Nom: "Chez Autre", Quartier: "Sud", Type: "resto"})
	if err != nil {
		t.Fatal(err)
	}
	autre := jeu{restoID: autreID}
	tokenAutre := tablette(t, db, autre)

	bodies := map[string]string{"refuser": `{"motif":"x"}`, "retiree": `{"code":"1234"}`}
	for _, nom := range []string{"accepter", "prete", "refuser", "retiree"} {
		h := map[string]func(http.ResponseWriter, *http.Request, string){"accepter": handleProAccepter, "prete": handleProPrete, "refuser": handleProRefuser, "retiree": handleProRetiree}[nom]
		rec := proReq(requireResto(h), "POST", "/pro/commandes/"+id+"/"+nom, bodies[nom], tokenAutre, map[string]string{"id": id})
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s sur la commande d'un autre restaurant : attendu 404, obtenu %d", nom, rec.Code)
		}
	}
	rec := proReq(requireResto(handleProCommandes), "GET", "/pro/commandes", "", tokenAutre, nil)
	if strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Errorf("l'autre restaurant ne doit voir aucune commande : %s", rec.Body.String())
	}
	// Ni changer la disponibilite d'un plat qui n'est pas a lui.
	rec = proReq(requireResto(handleProDisponible), "POST", "/pro/plats/"+j.yassaID+"/disponible", `{"disponible":false}`, tokenAutre, map[string]string{"prix_id": j.yassaID})
	if rec.Code != http.StatusNotFound {
		t.Errorf("plat d'un autre restaurant : attendu 404, obtenu %d", rec.Code)
	}
}

func TestRefusalAndRefundTracking(t *testing.T) {
	useTestDB(t)
	j := jeuDeTest(t, db)
	token := tablette(t, db, j)
	id, _ := commandePayee(t, db, commandeValide(j))

	rec := proReq(requireResto(handleProRefuser), "POST", "/pro/commandes/"+id+"/refuser", `{"motif":"plat épuisé"}`, token, map[string]string{"id": id})
	if rec.Code != http.StatusOK {
		t.Fatalf("refus : %d %s", rec.Code, rec.Body.String())
	}
	rec = proReq(requireResto(handleProRefuser), "POST", "/pro/commandes/"+id+"/refuser", `{"motif":"x"}`, token, map[string]string{"id": id})
	if rec.Code != http.StatusConflict {
		t.Errorf("refuser deux fois : %d", rec.Code)
	}
	if rec := proReq(requireResto(handleProRefuser), "POST", "/pro/commandes/"+id+"/refuser", `{"motif":"`+strings.Repeat("a", 201)+`"}`, token, map[string]string{"id": id}); rec.Code == http.StatusOK {
		t.Error("un motif trop long doit etre refuse")
	}

	rec = call(handleAdminCommandes, "/admin/commandes?statut=annulee", nil)
	var list []CommandeAdmin
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list) != 1 ||
		list[0].MotifAnnulation == nil || *list[0].MotifAnnulation != "plat épuisé" || list[0].RembourseLe != nil {
		t.Fatalf("liste admin des annulations : %s", rec.Body.String())
	}

	rembourser := func(id string) int {
		return proReq(handleAdminRemboursee, "POST", "/admin/commandes/"+id+"/remboursee", "", "", map[string]string{"id": id}).Code
	}
	if c := rembourser(id); c != http.StatusOK {
		t.Errorf("remboursement : %d", c)
	}
	if c := rembourser(id); c != http.StatusConflict {
		t.Errorf("second remboursement : attendu 409, obtenu %d", c)
	}
	autre, _ := commandePayee(t, db, commandeValide(j))
	if c := rembourser(autre); c != http.StatusConflict {
		t.Errorf("rembourser une commande non annulee : attendu 409, obtenu %d", c)
	}
	if c := rembourser("00000000-0000-4000-8000-000000000000"); c != http.StatusNotFound {
		t.Errorf("commande inconnue : attendu 404, obtenu %d", c)
	}
}

func TestAvailabilityAndPauseBlockOrders(t *testing.T) {
	useTestDB(t)
	j := jeuDeTest(t, db)
	token := tablette(t, db, j)

	// Plat epuise : il disparait de la recherche et ne peut plus etre commande.
	rec := proReq(requireResto(handleProDisponible), "POST", "/pro/plats/"+j.yassaID+"/disponible", `{"disponible":false}`, token, map[string]string{"prix_id": j.yassaID})
	if rec.Code != http.StatusOK {
		t.Fatalf("epuiser : %d", rec.Code)
	}
	if rec := call(handleMoinsCher, "/plats/Yassa%20poulet/moins-cher", map[string]string{"nom": "Yassa poulet"}); rec.Code != http.StatusNotFound {
		t.Errorf("plat epuise encore visible : %d", rec.Code)
	}
	if _, err := creerCommande(db, commandeValide(j), time.Now(), time.Time{}); err == nil {
		t.Error("un plat epuise ne doit pas etre commandable")
	}
	proReq(requireResto(handleProDisponible), "POST", "/pro/plats/"+j.yassaID+"/disponible", `{"disponible":true}`, token, map[string]string{"prix_id": j.yassaID})

	// Pause : plus de nouvelles commandes, et le restaurant n'est plus "commandable".
	if rec := proReq(requireResto(handleProOuverture), "POST", "/pro/ouverture", `{}`, token, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("champ manquant : %d", rec.Code)
	}
	if rec := proReq(requireResto(handleProOuverture), "POST", "/pro/ouverture", `{"ouvert":false}`, token, nil); rec.Code != http.StatusOK {
		t.Fatalf("pause : %d", rec.Code)
	}
	if _, err := creerCommande(db, commandeValide(j), time.Now(), time.Time{}); err == nil || !strings.Contains(err.Error(), "fermé") {
		t.Errorf("restaurant en pause : %v", err)
	}
	var menu MenuResto
	rec = call(handleMenuResto, "/restos/cantine-test/menu", map[string]string{"slug": "cantine-test"})
	if err := json.Unmarshal(rec.Body.Bytes(), &menu); err != nil || menu.Ouvert || menu.Commandable {
		t.Errorf("menu en pause : ouvert=%v commandable=%v", menu.Ouvert, menu.Commandable)
	}
	proReq(requireResto(handleProOuverture), "POST", "/pro/ouverture", `{"ouvert":true}`, token, nil)
	if _, err := creerCommande(db, commandeValide(j), time.Now(), time.Time{}); err != nil {
		t.Errorf("apres reouverture : %v", err)
	}
}

func TestAdminCodeHandler(t *testing.T) {
	useTestDB(t)
	j := jeuDeTest(t, db)
	rec := proReq(handleAdminCodeResto, "POST", "/admin/restaurants/"+j.restoID+"/code", "", "", map[string]string{"id": j.restoID})
	var out map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || !codeAccesFormat.MatchString(out["code"]) || out["restaurant"] != "Cantine Test" {
		t.Fatalf("code admin : %d %s", rec.Code, rec.Body.String())
	}
	if rec := proReq(handleAdminCodeResto, "POST", "/admin/restaurants/inconnu/code", "", "", map[string]string{"id": "inconnu"}); rec.Code != http.StatusNotFound {
		t.Errorf("restaurant inconnu : %d", rec.Code)
	}
}
