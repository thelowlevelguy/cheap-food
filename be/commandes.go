package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const formatHeure = "2006-01-02 15:04:05" // meme format que CURRENT_TIMESTAMP de SQLite (UTC)

// ---------------------------------------------------------------
// Frais restaurant
// ---------------------------------------------------------------

// fraisGratuitsJusquau : tant que cette date (incluse) n'est pas passee, les
// frais restaurant sont calcules et enregistres, mais factures 0 F.
// La valeur zero signifie qu'il n'y a pas de periode gratuite.
var fraisGratuitsJusquau time.Time

func loadFraisConfig() {
	v := strings.TrimSpace(os.Getenv("FRAIS_RESTO_GRATUITS_JUSQUAU"))
	if v == "" {
		return
	}
	t, err := time.Parse("2006-01-02", v)
	if err != nil {
		log.Fatalf("FRAIS_RESTO_GRATUITS_JUSQUAU doit etre une date au format AAAA-MM-JJ : %v", err)
	}
	fraisGratuitsJusquau = t
}

// fraisResto donne les frais payes par le restaurant selon le total des plats
// (sans la livraison) : 50 F jusqu'a 2 000 F, 100 F jusqu'a 5 000 F, 200 F au-dessus.
func fraisResto(sousTotal int) int {
	switch {
	case sousTotal <= 2000:
		return 50
	case sousTotal <= 5000:
		return 100
	default:
		return 200
	}
}

// fraisRestoFactures applique la periode gratuite : 0 F tant qu'elle dure.
func fraisRestoFactures(frais int, now, gratuitJusquau time.Time) int {
	if !gratuitJusquau.IsZero() && now.Before(gratuitJusquau.AddDate(0, 0, 1)) {
		return 0
	}
	return frais
}

// ---------------------------------------------------------------
// Outils : erreurs, telephone, identifiants
// ---------------------------------------------------------------

// erreurSaisie est une erreur que l'on peut montrer au client.
type erreurSaisie struct {
	msg  string
	code int
}

func (e *erreurSaisie) Error() string { return e.msg }

func saisie(msg string) error  { return &erreurSaisie{msg, http.StatusBadRequest} }
func conflit(msg string) error { return &erreurSaisie{msg, http.StatusConflict} }

func repondreErreur(w http.ResponseWriter, err error) {
	var es *erreurSaisie
	if errors.As(err, &es) {
		writeError(w, es.msg, es.code)
		return
	}
	serverError(w, err)
}

func writeJSONStatus(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

var telephoneMobile = regexp.MustCompile(`^(70|75|76|77|78)[0-9]{7}$`)

// normaliserTelephone accepte "77 000 00 00", "+221 77 000 00 00", "00221770000000"
// et renvoie les 9 chiffres du numero mobile. Les lettres sont refusees.
func normaliserTelephone(s string) (string, bool) {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '+' || r == ' ' || r == '.' || r == '-':
		default:
			return "", false
		}
	}
	d := b.String()
	switch {
	case strings.HasPrefix(d, "00221") && len(d) == 14:
		d = d[5:]
	case strings.HasPrefix(d, "221") && len(d) == 12:
		d = d[3:]
	}
	if !telephoneMobile.MatchString(d) {
		return "", false
	}
	return d, true
}

// newUUID genere un UUID v4 avec le generateur aleatoire du systeme.
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// codeRetraitLibre tire un code a 4 chiffres (5 ou 6 si besoin) qui n'est
// utilise par aucune commande en cours du meme restaurant : au comptoir, le
// code suffit alors a retrouver la commande.
func codeRetraitLibre(q rowQuerier, restaurantID string) (string, error) {
	for digits := 4; digits <= 6; digits++ {
		limite := big.NewInt(1)
		for i := 0; i < digits; i++ {
			limite.Mul(limite, big.NewInt(10))
		}
		for essai := 0; essai < 20; essai++ {
			n, err := rand.Int(rand.Reader, limite)
			if err != nil {
				return "", err
			}
			code := fmt.Sprintf("%0*d", digits, n.Int64())
			var utilise int
			err = q.QueryRow(`
				SELECT COUNT(*) FROM commandes
				WHERE restaurant_id = ? AND code_retrait = ?
				  AND statut IN ('attente_paiement', 'payee', 'acceptee', 'prete')`,
				restaurantID, code).Scan(&utilise)
			if err != nil {
				return "", err
			}
			if utilise == 0 {
				return code, nil
			}
		}
	}
	return "", errors.New("impossible de trouver un code de retrait libre")
}

func libelle(plat, portion string) string {
	if portion == "" {
		return plat
	}
	return plat + " (" + portion + ")"
}

// ---------------------------------------------------------------
// Creation d'une commande
// ---------------------------------------------------------------

type LigneInput struct {
	PrixID   string `json:"prix_id"`
	Quantite int    `json:"quantite"`
}

type CommandeInput struct {
	RestaurantID    string       `json:"restaurant_id"`
	Mode            string       `json:"mode"`
	ClientNom       string       `json:"client_nom"`
	ClientTelephone string       `json:"client_telephone"`
	Lignes          []LigneInput `json:"lignes"`
}

type LigneVue struct {
	Plat         string `json:"plat"`
	Quantite     int    `json:"quantite"`
	PrixUnitaire int    `json:"prix_unitaire"`
}

// CommandeVue est ce que voit le client. Aucun identifiant interne ni
// numero de telephone n'y figure.
type CommandeVue struct {
	TokenSuivi     string     `json:"token_suivi,omitempty"`
	Statut         string     `json:"statut"`
	Restaurant     string     `json:"restaurant"`
	RestaurantSlug string     `json:"restaurant_slug"`
	Mode           string     `json:"mode"`
	Lignes         []LigneVue `json:"lignes"`
	SousTotal      int        `json:"sous_total"`
	FraisLivraison int        `json:"frais_livraison"`
	TotalAPayer    int        `json:"total_a_payer"`
	CodeRetrait    string     `json:"code_retrait,omitempty"`
	CreeLe         string     `json:"cree_le"`
	PayeLe         *string    `json:"paye_le"`
}

const maxLignes, maxQuantite = 20, 20

func fusionnerLignes(in []LigneInput) ([]LigneInput, error) {
	if len(in) == 0 || len(in) > maxLignes {
		return nil, saisie("le panier doit contenir entre 1 et 20 plats")
	}
	index := map[string]int{}
	var out []LigneInput
	for _, l := range in {
		id := strings.TrimSpace(l.PrixID)
		if id == "" {
			return nil, saisie("plat manquant")
		}
		if l.Quantite < 1 || l.Quantite > maxQuantite {
			return nil, saisie("la quantité doit être entre 1 et 20")
		}
		if i, ok := index[id]; ok {
			out[i].Quantite += l.Quantite
			if out[i].Quantite > maxQuantite {
				return nil, saisie("la quantité doit être entre 1 et 20")
			}
			continue
		}
		index[id] = len(out)
		out = append(out, LigneInput{PrixID: id, Quantite: l.Quantite})
	}
	return out, nil
}

type ligneDB struct {
	prixID, label string
	prix, qte     int
}

// creerCommande verifie la demande puis enregistre la commande. Les prix et
// les totaux viennent toujours de la base, jamais de l'application.
func creerCommande(db *sql.DB, in CommandeInput, now, gratuitJusquau time.Time) (*CommandeVue, error) {
	switch in.Mode {
	case "retrait":
	case "livraison":
		return nil, saisie("la livraison n'est pas encore disponible : choisissez le retrait")
	default:
		return nil, saisie("mode de remise inconnu")
	}
	nom := strings.TrimSpace(in.ClientNom)
	if n := utf8.RuneCountInString(nom); n < 1 || n > 60 {
		return nil, saisie("le prénom doit faire entre 1 et 60 caractères")
	}
	tel, ok := normaliserTelephone(in.ClientTelephone)
	if !ok {
		return nil, saisie("numéro de téléphone invalide")
	}
	if strings.TrimSpace(in.RestaurantID) == "" {
		return nil, saisie("restaurant manquant")
	}
	lignes, err := fusionnerLignes(in.Lignes)
	if err != nil {
		return nil, err
	}

	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var restoNom, restoSlug string
	var actif int
	err = tx.QueryRow(`SELECT nom, slug, actif FROM restaurants WHERE id = ?`, in.RestaurantID).
		Scan(&restoNom, &restoSlug, &actif)
	if err == sql.ErrNoRows {
		return nil, &erreurSaisie{"restaurant introuvable", http.StatusNotFound}
	}
	if err != nil {
		return nil, err
	}
	if actif != 1 {
		return nil, conflit("ce restaurant ne prend pas encore de commandes")
	}

	sousTotal := 0
	var detail []ligneDB
	for _, l := range lignes {
		var plat, portion string
		var prix, dispo int
		err := tx.QueryRow(`
			SELECT p.nom, pr.portion, pr.prix_fcfa, pr.disponible
			FROM prix pr JOIN plats p ON p.id = pr.plat_id
			WHERE pr.id = ? AND pr.restaurant_id = ?`, l.PrixID, in.RestaurantID).
			Scan(&plat, &portion, &prix, &dispo)
		if err == sql.ErrNoRows {
			return nil, saisie("un des plats n'existe pas dans ce restaurant")
		}
		if err != nil {
			return nil, err
		}
		if dispo != 1 {
			return nil, conflit(fmt.Sprintf("« %s » n'est plus disponible", libelle(plat, portion)))
		}
		sousTotal += prix * l.Quantite
		detail = append(detail, ligneDB{prixID: l.PrixID, label: libelle(plat, portion), prix: prix, qte: l.Quantite})
	}

	frais := fraisResto(sousTotal)
	factures := fraisRestoFactures(frais, now, gratuitJusquau)

	commandeID, err := newUUID()
	if err != nil {
		return nil, err
	}
	token, err := newUUID()
	if err != nil {
		return nil, err
	}
	code, err := codeRetraitLibre(tx, in.RestaurantID)
	if err != nil {
		return nil, err
	}
	creeLe := now.UTC().Format(formatHeure)

	_, err = tx.Exec(`
		INSERT INTO commandes (id, restaurant_id, client_nom, client_telephone, token_suivi,
			mode, statut, sous_total, frais_resto, frais_resto_facture,
			frais_livraison, part_livreur, total_a_payer, code_retrait, cree_le)
		VALUES (?, ?, ?, ?, ?, 'retrait', 'attente_paiement', ?, ?, ?, 0, 0, ?, ?, ?)`,
		commandeID, in.RestaurantID, nom, tel, token, sousTotal, frais, factures, sousTotal, code, creeLe)
	if err != nil {
		return nil, err
	}

	vue := &CommandeVue{
		TokenSuivi: token, Statut: "attente_paiement",
		Restaurant: restoNom, RestaurantSlug: restoSlug, Mode: "retrait",
		SousTotal: sousTotal, TotalAPayer: sousTotal, CreeLe: creeLe,
		Lignes: []LigneVue{},
	}
	for _, d := range detail {
		ligneID, err := newUUID()
		if err != nil {
			return nil, err
		}
		_, err = tx.Exec(`
			INSERT INTO lignes_commande (id, commande_id, prix_id, nom_plat, prix_unitaire, quantite)
			VALUES (?, ?, ?, ?, ?, ?)`, ligneID, commandeID, d.prixID, d.label, d.prix, d.qte)
		if err != nil {
			return nil, err
		}
		vue.Lignes = append(vue.Lignes, LigneVue{Plat: d.label, Quantite: d.qte, PrixUnitaire: d.prix})
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return vue, nil
}

// ---------------------------------------------------------------
// Suivi par le client
// ---------------------------------------------------------------

// chargerSuivi retrouve une commande grace a son lien secret. Le code de
// retrait n'est montre qu'une fois la commande payee et tant qu'elle est en cours.
func chargerSuivi(db *sql.DB, token string) (*CommandeVue, error) {
	if token == "" || len(token) > 64 {
		return nil, &erreurSaisie{"commande introuvable", http.StatusNotFound}
	}

	var v CommandeVue
	var id, code string
	var paye sql.NullString
	err := db.QueryRow(`
		SELECT c.id, c.statut, r.nom, r.slug, c.mode, c.sous_total, c.frais_livraison,
		       c.total_a_payer, c.code_retrait, c.cree_le, c.paye_le
		FROM commandes c JOIN restaurants r ON r.id = c.restaurant_id
		WHERE c.token_suivi = ?`, token).
		Scan(&id, &v.Statut, &v.Restaurant, &v.RestaurantSlug, &v.Mode, &v.SousTotal,
			&v.FraisLivraison, &v.TotalAPayer, &code, &v.CreeLe, &paye)
	if err == sql.ErrNoRows {
		return nil, &erreurSaisie{"commande introuvable", http.StatusNotFound}
	}
	if err != nil {
		return nil, err
	}
	if paye.Valid {
		v.PayeLe = &paye.String
	}
	switch v.Statut {
	case "payee", "acceptee", "prete":
		v.CodeRetrait = code
	}

	rows, err := db.Query(`SELECT nom_plat, quantite, prix_unitaire FROM lignes_commande WHERE commande_id = ? ORDER BY nom_plat`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	v.Lignes = []LigneVue{}
	for rows.Next() {
		var l LigneVue
		if err := rows.Scan(&l.Plat, &l.Quantite, &l.PrixUnitaire); err != nil {
			return nil, err
		}
		v.Lignes = append(v.Lignes, l)
	}
	return &v, rows.Err()
}

// ---------------------------------------------------------------
// Confirmation de paiement manuelle (administration)
// ---------------------------------------------------------------

// marquerPayee passe une commande de "attente_paiement" a "payee".
func marquerPayee(db *sql.DB, id, reference string, now time.Time) error {
	var ref interface{}
	if reference = strings.TrimSpace(reference); reference != "" {
		ref = reference
	}
	res, err := db.Exec(`
		UPDATE commandes SET statut = 'payee', paye_le = ?, reference_paiement = ?
		WHERE id = ? AND statut = 'attente_paiement'`, now.UTC().Format(formatHeure), ref, id)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return conflit("cette référence de paiement est déjà utilisée")
		}
		return err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return nil
	}
	var statut string
	err = db.QueryRow(`SELECT statut FROM commandes WHERE id = ?`, id).Scan(&statut)
	if err == sql.ErrNoRows {
		return &erreurSaisie{"commande introuvable", http.StatusNotFound}
	}
	if err != nil {
		return err
	}
	return conflit("commande déjà traitée (statut : " + statut + ")")
}

type CommandeAdmin struct {
	ID                string  `json:"id"`
	Statut            string  `json:"statut"`
	Restaurant        string  `json:"restaurant"`
	ClientNom         string  `json:"client_nom"`
	ClientTelephone   string  `json:"client_telephone"`
	Mode              string  `json:"mode"`
	SousTotal         int     `json:"sous_total"`
	TotalAPayer       int     `json:"total_a_payer"`
	FraisResto        int     `json:"frais_resto"`
	FraisRestoFacture int     `json:"frais_resto_facture"`
	CreeLe            string  `json:"cree_le"`
	PayeLe            *string `json:"paye_le"`
	ReferencePaiement *string `json:"reference_paiement"`
}

var statutsCommande = map[string]bool{
	"attente_paiement": true, "payee": true, "acceptee": true,
	"prete": true, "en_livraison": true, "terminee": true, "annulee": true,
}

// ---------------------------------------------------------------
// Handlers HTTP
// ---------------------------------------------------------------

// POST /commandes -> cree une commande (paiement a faire ensuite)
func handleCreerCommande(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var in CommandeInput
	if err := dec.Decode(&in); err != nil {
		writeError(w, "requête invalide", http.StatusBadRequest)
		return
	}
	vue, err := creerCommande(db, in, time.Now(), fraisGratuitsJusquau)
	if err != nil {
		repondreErreur(w, err)
		return
	}
	writeJSONStatus(w, http.StatusCreated, vue)
}

// GET /suivi/{token} -> etat d'une commande, pour son lien de suivi
func handleSuivi(w http.ResponseWriter, r *http.Request) {
	vue, err := chargerSuivi(db, strings.TrimSpace(r.PathValue("token")))
	if err != nil {
		repondreErreur(w, err)
		return
	}
	writeJSON(w, vue)
}

// GET /admin/commandes?statut=... -> dernieres commandes (protege par requireAdmin)
func handleAdminCommandes(w http.ResponseWriter, r *http.Request) {
	statut := r.URL.Query().Get("statut")
	if statut != "" && !statutsCommande[statut] {
		writeError(w, "statut inconnu", http.StatusBadRequest)
		return
	}
	rows, err := db.Query(`
		SELECT c.id, c.statut, r.nom, c.client_nom, c.client_telephone, c.mode, c.sous_total,
		       c.total_a_payer, c.frais_resto, c.frais_resto_facture, c.cree_le, c.paye_le,
		       c.reference_paiement
		FROM commandes c JOIN restaurants r ON r.id = c.restaurant_id
		WHERE (? = '' OR c.statut = ?)
		ORDER BY c.cree_le DESC LIMIT 100`, statut, statut)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()

	out := []CommandeAdmin{}
	for rows.Next() {
		var c CommandeAdmin
		var paye, ref sql.NullString
		if err := rows.Scan(&c.ID, &c.Statut, &c.Restaurant, &c.ClientNom, &c.ClientTelephone, &c.Mode,
			&c.SousTotal, &c.TotalAPayer, &c.FraisResto, &c.FraisRestoFacture, &c.CreeLe, &paye, &ref); err != nil {
			serverError(w, err)
			return
		}
		if paye.Valid {
			c.PayeLe = &paye.String
		}
		if ref.Valid {
			c.ReferencePaiement = &ref.String
		}
		out = append(out, c)
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, out)
}

// POST /admin/commandes/{id}/payee -> confirme a la main un paiement recu
// (corps facultatif : {"reference_paiement": "..."}).
func handleAdminPayee(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var body struct {
		ReferencePaiement string `json:"reference_paiement"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil && err != io.EOF {
		writeError(w, "requête invalide", http.StatusBadRequest)
		return
	}
	if err := marquerPayee(db, r.PathValue("id"), body.ReferencePaiement, time.Now()); err != nil {
		repondreErreur(w, err)
		return
	}
	writeJSON(w, map[string]string{"statut": "payee"})
}
