package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------
// Code d'acces des restaurants
// ---------------------------------------------------------------
//
// Chaque restaurant recoit un code de 12 caracteres (par exemple K7M4-9QXT-2HWP).
// Il le saisit une fois sur sa tablette et recoit en echange un jeton de
// session. Seul le hash SHA-256 du code est conserve : le code est aleatoire
// (60 bits), un hash lent n'est donc pas necessaire.

const (
	alphabetCode = "ABCDEFGHJKMNPQRSTUVWXYZ23456789" // sans I, L, O, 0, 1 : pas de confusion
	longueurCode = 12
	dureeSession = 30 * 24 * time.Hour
)

func nouveauCodeAcces() (string, error) {
	limite := big.NewInt(int64(len(alphabetCode)))
	var b strings.Builder
	for i := 0; i < longueurCode; i++ {
		if i > 0 && i%4 == 0 {
			b.WriteByte('-')
		}
		n, err := rand.Int(rand.Reader, limite)
		if err != nil {
			return "", err
		}
		b.WriteByte(alphabetCode[n.Int64()])
	}
	return b.String(), nil
}

// normaliserCode accepte des minuscules, des espaces et des tirets.
func normaliserCode(s string) (string, bool) {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if r == ' ' || r == '-' {
			continue
		}
		if !strings.ContainsRune(alphabetCode, r) {
			return "", false
		}
		b.WriteRune(r)
	}
	if b.Len() != longueurCode {
		return "", false
	}
	return b.String(), true
}

func hacher(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// definirCodeAcces cree un nouveau code pour un restaurant, invalide l'ancien
// et ferme toutes ses sessions. Le code n'est renvoye qu'ici, une seule fois.
func definirCodeAcces(db *sql.DB, restaurantID string) (string, error) {
	code, err := nouveauCodeAcces()
	if err != nil {
		return "", err
	}
	norm, _ := normaliserCode(code)

	tx, err := db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	res, err := tx.Exec(`UPDATE restaurants SET code_acces_hash = ? WHERE id = ?`, hacher(norm), restaurantID)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", &erreurSaisie{"restaurant introuvable", http.StatusNotFound}
	}
	if _, err := tx.Exec(`DELETE FROM sessions_restaurant WHERE restaurant_id = ?`, restaurantID); err != nil {
		return "", err
	}
	return code, tx.Commit()
}

var errIdentifiants = &erreurSaisie{"code d'accès invalide", http.StatusUnauthorized}

// connexion echange un code d'acces contre un jeton de session.
func connexion(db *sql.DB, code string, now time.Time) (token, restaurantID string, err error) {
	norm, ok := normaliserCode(code)
	if !ok {
		return "", "", errIdentifiants
	}
	err = db.QueryRow(`SELECT id FROM restaurants WHERE code_acces_hash = ? AND actif = 1`, hacher(norm)).Scan(&restaurantID)
	if err == sql.ErrNoRows {
		return "", "", errIdentifiants
	}
	if err != nil {
		return "", "", err
	}

	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", "", err
	}
	token = base64.RawURLEncoding.EncodeToString(b[:])

	_, _ = db.Exec(`DELETE FROM sessions_restaurant WHERE expire_le < ?`, now.UTC().Format(formatHeure))
	_, err = db.Exec(`INSERT INTO sessions_restaurant (token_hash, restaurant_id, cree_le, expire_le) VALUES (?, ?, ?, ?)`,
		hacher(token), restaurantID, now.UTC().Format(formatHeure), now.Add(dureeSession).UTC().Format(formatHeure))
	if err != nil {
		return "", "", err
	}
	return token, restaurantID, nil
}

func tokenDepuisRequete(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// restaurantDepuisToken renvoie le restaurant d'une session valide, ou "".
func restaurantDepuisToken(db *sql.DB, token string, now time.Time) (string, error) {
	if token == "" || len(token) > 100 {
		return "", nil
	}
	var id string
	err := db.QueryRow(`
		SELECT s.restaurant_id
		FROM sessions_restaurant s JOIN restaurants r ON r.id = s.restaurant_id
		WHERE s.token_hash = ? AND s.expire_le > ? AND r.actif = 1`,
		hacher(token), now.UTC().Format(formatHeure)).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return id, err
}

// requireResto protege une route reservee aux restaurants connectes.
func requireResto(h func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := restaurantDepuisToken(db, tokenDepuisRequete(r), time.Now())
		if err != nil {
			serverError(w, err)
			return
		}
		if id == "" {
			writeError(w, "connexion requise", http.StatusUnauthorized)
			return
		}
		h(w, r, id)
	}
}

// ---------------------------------------------------------------
// Limitation des tentatives de connexion
// ---------------------------------------------------------------

type limiteur struct {
	mu      sync.Mutex
	max     int
	fenetre time.Duration
	echecs  map[string][]time.Time
}

func newLimiteur(max int, fenetre time.Duration) *limiteur {
	return &limiteur{max: max, fenetre: fenetre, echecs: map[string][]time.Time{}}
}

func (l *limiteur) recents(cle string, now time.Time) []time.Time {
	var garde []time.Time
	for _, t := range l.echecs[cle] {
		if now.Sub(t) < l.fenetre {
			garde = append(garde, t)
		}
	}
	return garde
}

// bloque indique si la cle a trop d'echecs recents.
func (l *limiteur) bloque(cle string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.recents(cle, now)) >= l.max
}

func (l *limiteur) echec(cle string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.echecs) > 10000 {
		for k := range l.echecs {
			if len(l.recents(k, now)) == 0 {
				delete(l.echecs, k)
			}
		}
	}
	l.echecs[cle] = append(l.recents(cle, now), now)
}

var limiteConnexion = newLimiteur(10, 15*time.Minute)

// clientIP : derriere un proxy de confiance (Caddy), mettez TRUST_PROXY=1 pour
// lire l'adresse du client dans X-Forwarded-For (derniere valeur ajoutee par le proxy).
func clientIP(r *http.Request) string {
	if os.Getenv("TRUST_PROXY") == "1" {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ---------------------------------------------------------------
// Commandes vues par le restaurant
// ---------------------------------------------------------------

// CommandePro est ce que voit la tablette. Ni le code de retrait (secret du
// client) ni son telephone n'y figurent.
type CommandePro struct {
	ID              string     `json:"id"`
	Reference       string     `json:"reference"`
	Statut          string     `json:"statut"`
	Mode            string     `json:"mode"`
	ClientNom       string     `json:"client_nom"`
	Lignes          []LigneVue `json:"lignes"`
	SousTotal       int        `json:"sous_total"`
	PayeLe          *string    `json:"paye_le"`
	AccepteeLe      *string    `json:"acceptee_le"`
	PretEstime      *string    `json:"pret_estime"`
	PreteLe         *string    `json:"prete_le"`
	RetireeLe       *string    `json:"retiree_le"`
	MotifAnnulation *string    `json:"motif_annulation"`
}

func strPtr(n sql.NullString) *string {
	if n.Valid {
		return &n.String
	}
	return nil
}

func commandesPro(db *sql.DB, restaurantID string, statuts []string) ([]CommandePro, error) {
	marques := strings.TrimSuffix(strings.Repeat("?,", len(statuts)), ",")
	args := []interface{}{restaurantID}
	for _, s := range statuts {
		args = append(args, s)
	}
	rows, err := db.Query(`
		SELECT c.id, c.statut, c.mode, c.client_nom, c.sous_total, c.paye_le, c.acceptee_le,
		       c.prete_le, c.retiree_le, c.motif_annulation, r.temps_preparation_min
		FROM commandes c JOIN restaurants r ON r.id = c.restaurant_id
		WHERE c.restaurant_id = ? AND c.statut IN (`+marques+`)
		ORDER BY c.paye_le ASC, c.cree_le ASC LIMIT 200`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []CommandePro{}
	for rows.Next() {
		var c CommandePro
		var paye, acc, prete, ret, motif sql.NullString
		var tprep int
		if err := rows.Scan(&c.ID, &c.Statut, &c.Mode, &c.ClientNom, &c.SousTotal, &paye, &acc,
			&prete, &ret, &motif, &tprep); err != nil {
			return nil, err
		}
		c.Reference = strings.ToUpper(c.ID[len(c.ID)-4:])
		c.PayeLe, c.AccepteeLe, c.PreteLe, c.RetireeLe, c.MotifAnnulation =
			strPtr(paye), strPtr(acc), strPtr(prete), strPtr(ret), strPtr(motif)
		if acc.Valid {
			if t, err := time.Parse(formatHeure, acc.String); err == nil {
				estime := t.Add(time.Duration(tprep) * time.Minute).Format(formatHeure)
				c.PretEstime = &estime
			}
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range out {
		lr, err := db.Query(`SELECT nom_plat, quantite, prix_unitaire FROM lignes_commande WHERE commande_id = ? ORDER BY nom_plat`, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Lignes = []LigneVue{}
		for lr.Next() {
			var l LigneVue
			if err := lr.Scan(&l.Plat, &l.Quantite, &l.PrixUnitaire); err != nil {
				lr.Close()
				return nil, err
			}
			out[i].Lignes = append(out[i].Lignes, l)
		}
		lr.Close()
	}
	return out, nil
}

// changerStatut fait avancer une commande de CE restaurant. Une commande d'un
// autre restaurant est traitee comme introuvable.
func changerStatut(db *sql.DB, restaurantID, id string, depuis []string, vers, colonneHeure string, now time.Time) error {
	marques := strings.TrimSuffix(strings.Repeat("?,", len(depuis)), ",")
	requete := `UPDATE commandes SET statut = ?`
	args := []interface{}{vers}
	if colonneHeure != "" { // valeurs fixes definies dans ce fichier, jamais saisies par un utilisateur
		requete += `, ` + colonneHeure + ` = ?`
		args = append(args, now.UTC().Format(formatHeure))
	}
	requete += ` WHERE id = ? AND restaurant_id = ? AND statut IN (` + marques + `)`
	args = append(args, id, restaurantID)
	for _, s := range depuis {
		args = append(args, s)
	}

	res, err := db.Exec(requete, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return nil
	}
	return erreurTransition(db, restaurantID, id)
}

func erreurTransition(db *sql.DB, restaurantID, id string) error {
	var statut string
	err := db.QueryRow(`SELECT statut FROM commandes WHERE id = ? AND restaurant_id = ?`, id, restaurantID).Scan(&statut)
	if err == sql.ErrNoRows {
		return &erreurSaisie{"commande introuvable", http.StatusNotFound}
	}
	if err != nil {
		return err
	}
	return conflit("action impossible pour une commande au statut « " + statut + " »")
}

func refuserCommande(db *sql.DB, restaurantID, id, motif string, now time.Time) error {
	motif = strings.TrimSpace(motif)
	if motif == "" {
		motif = "refusée par le restaurant"
	}
	if len([]rune(motif)) > 200 {
		return saisie("le motif ne doit pas dépasser 200 caractères")
	}
	res, err := db.Exec(`
		UPDATE commandes SET statut = 'annulee', motif_annulation = ?
		WHERE id = ? AND restaurant_id = ? AND statut IN ('payee', 'acceptee')`, motif, id, restaurantID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return nil
	}
	return erreurTransition(db, restaurantID, id)
}

// validerRetrait : le restaurant saisit le code montre par le client.
func validerRetrait(db *sql.DB, restaurantID, id, code string, now time.Time) error {
	var statut, attendu string
	err := db.QueryRow(`SELECT statut, code_retrait FROM commandes WHERE id = ? AND restaurant_id = ?`, id, restaurantID).
		Scan(&statut, &attendu)
	if err == sql.ErrNoRows {
		return &erreurSaisie{"commande introuvable", http.StatusNotFound}
	}
	if err != nil {
		return err
	}
	if statut != "prete" {
		return conflit("la commande n'est pas prête (statut : " + statut + ")")
	}
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(code)), []byte(attendu)) != 1 {
		return saisie("code de retrait incorrect")
	}
	return changerStatut(db, restaurantID, id, []string{"prete"}, "terminee", "retiree_le", now)
}

// ---------------------------------------------------------------
// Handlers HTTP : espace restaurant
// ---------------------------------------------------------------

func lireJSON(w http.ResponseWriter, r *http.Request, v interface{}) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, "requête invalide", http.StatusBadRequest)
		return false
	}
	return true
}

func chargerResume(db *sql.DB, id string) (*RestoResume, error) {
	var rr RestoResume
	var actif, ouvert int
	err := db.QueryRow(`
		SELECT id, slug, nom, quartier, type, actif, ouvert, temps_preparation_min
		FROM restaurants WHERE id = ?`, id).
		Scan(&rr.ID, &rr.Slug, &rr.Nom, &rr.Quartier, &rr.Type, &actif, &ouvert, &rr.TempsPreparationMin)
	rr.Ouvert = ouvert == 1
	rr.Commandable = actif == 1 && ouvert == 1
	return &rr, err
}

// POST /pro/connexion {"code": "K7M4-9QXT-2HWP"}
func handleProConnexion(w http.ResponseWriter, r *http.Request) {
	ip, now := clientIP(r), time.Now()
	if limiteConnexion.bloque(ip, now) {
		writeError(w, "trop de tentatives, réessayez dans quelques minutes", http.StatusTooManyRequests)
		return
	}
	var in struct {
		Code string `json:"code"`
	}
	if !lireJSON(w, r, &in) {
		return
	}
	token, id, err := connexion(db, in.Code, now)
	if err != nil {
		if errors.Is(err, errIdentifiants) {
			limiteConnexion.echec(ip, now)
		}
		repondreErreur(w, err)
		return
	}
	resto, err := chargerResume(db, id)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, map[string]interface{}{
		"token":      token,
		"expire_le":  now.Add(dureeSession).UTC().Format(formatHeure),
		"restaurant": resto,
	})
}

// POST /pro/deconnexion
func handleProDeconnexion(w http.ResponseWriter, r *http.Request, _ string) {
	if _, err := db.Exec(`DELETE FROM sessions_restaurant WHERE token_hash = ?`, hacher(tokenDepuisRequete(r))); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, map[string]string{"statut": "deconnecte"})
}

// GET /pro/moi
func handleProMoi(w http.ResponseWriter, r *http.Request, id string) {
	resto, err := chargerResume(db, id)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, resto)
}

// POST /pro/ouverture {"ouvert": false} -> pause : plus de nouvelles commandes
func handleProOuverture(w http.ResponseWriter, r *http.Request, id string) {
	var in struct {
		Ouvert *bool `json:"ouvert"`
	}
	if !lireJSON(w, r, &in) {
		return
	}
	if in.Ouvert == nil {
		writeError(w, "champ « ouvert » manquant", http.StatusBadRequest)
		return
	}
	v := 0
	if *in.Ouvert {
		v = 1
	}
	if _, err := db.Exec(`UPDATE restaurants SET ouvert = ? WHERE id = ?`, v, id); err != nil {
		serverError(w, err)
		return
	}
	handleProMoi(w, r, id)
}

// GET /pro/commandes?statut=payee,acceptee  (par defaut : payee, acceptee, prete)
func handleProCommandes(w http.ResponseWriter, r *http.Request, id string) {
	statuts := []string{"payee", "acceptee", "prete"}
	if q := strings.TrimSpace(r.URL.Query().Get("statut")); q != "" {
		statuts = nil
		for _, s := range strings.Split(q, ",") {
			s = strings.TrimSpace(s)
			if !statutsCommande[s] || s == "attente_paiement" {
				writeError(w, "statut inconnu", http.StatusBadRequest)
				return
			}
			statuts = append(statuts, s)
		}
	}
	list, err := commandesPro(db, id, statuts)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, list)
}

func actionStatut(depuis []string, vers, colonne string) func(http.ResponseWriter, *http.Request, string) {
	return func(w http.ResponseWriter, r *http.Request, restoID string) {
		if err := changerStatut(db, restoID, r.PathValue("id"), depuis, vers, colonne, time.Now()); err != nil {
			repondreErreur(w, err)
			return
		}
		writeJSON(w, map[string]string{"statut": vers})
	}
}

var (
	handleProAccepter = actionStatut([]string{"payee"}, "acceptee", "acceptee_le")
	handleProPrete    = actionStatut([]string{"acceptee"}, "prete", "prete_le")
)

// POST /pro/commandes/{id}/retiree {"code": "4821"}
func handleProRetiree(w http.ResponseWriter, r *http.Request, restoID string) {
	var in struct {
		Code string `json:"code"`
	}
	if !lireJSON(w, r, &in) {
		return
	}
	if err := validerRetrait(db, restoID, r.PathValue("id"), in.Code, time.Now()); err != nil {
		repondreErreur(w, err)
		return
	}
	writeJSON(w, map[string]string{"statut": "terminee"})
}

// POST /pro/commandes/{id}/refuser {"motif": "plat épuisé"}
func handleProRefuser(w http.ResponseWriter, r *http.Request, restoID string) {
	var in struct {
		Motif string `json:"motif"`
	}
	if !lireJSON(w, r, &in) {
		return
	}
	if err := refuserCommande(db, restoID, r.PathValue("id"), in.Motif, time.Now()); err != nil {
		repondreErreur(w, err)
		return
	}
	writeJSON(w, map[string]string{"statut": "annulee"})
}

// POST /pro/plats/{prix_id}/disponible {"disponible": false} -> plat epuise
func handleProDisponible(w http.ResponseWriter, r *http.Request, restoID string) {
	var in struct {
		Disponible *bool `json:"disponible"`
	}
	if !lireJSON(w, r, &in) {
		return
	}
	if in.Disponible == nil {
		writeError(w, "champ « disponible » manquant", http.StatusBadRequest)
		return
	}
	v := 0
	if *in.Disponible {
		v = 1
	}
	res, err := db.Exec(`UPDATE prix SET disponible = ? WHERE id = ? AND restaurant_id = ?`, v, r.PathValue("prix_id"), restoID)
	if err != nil {
		serverError(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, "plat introuvable", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]bool{"disponible": v == 1})
}

// ---------------------------------------------------------------
// Handlers HTTP : administration
// ---------------------------------------------------------------

// POST /admin/restaurants/{id}/code -> nouveau code d'acces (affiche une seule fois)
func handleAdminCodeResto(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	code, err := definirCodeAcces(db, id)
	if err != nil {
		repondreErreur(w, err)
		return
	}
	resto, err := chargerResume(db, id)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, map[string]string{"restaurant": resto.Nom, "code": code})
}

// POST /admin/commandes/{id}/remboursee -> note qu'une commande annulee a ete remboursee
func handleAdminRemboursee(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	res, err := db.Exec(`
		UPDATE commandes SET rembourse_le = ?
		WHERE id = ? AND statut = 'annulee' AND paye_le IS NOT NULL AND rembourse_le IS NULL`,
		time.Now().UTC().Format(formatHeure), id)
	if err != nil {
		serverError(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 1 {
		writeJSON(w, map[string]string{"statut": "remboursee"})
		return
	}
	var statut string
	err = db.QueryRow(`SELECT statut FROM commandes WHERE id = ?`, id).Scan(&statut)
	if err == sql.ErrNoRows {
		writeError(w, "commande introuvable", http.StatusNotFound)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	writeError(w, "seule une commande annulée et payée, pas encore remboursée, peut l'être", http.StatusConflict)
}
