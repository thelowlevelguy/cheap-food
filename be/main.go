// Cheap Dish Map - Saint-Louis
package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
)

var db *sql.DB

// writeJSON écrit une réponse JSON formatée. Les en-têtes CORS globaux sont gérés par le middleware.
func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// writeError renvoie une erreur standardisée au format JSON.
func writeError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"erreur": msg})
}

// serverError journalise l'erreur technique et renvoie un message neutre :
// le detail (requetes SQL, noms de colonnes) ne doit pas sortir du serveur.
func serverError(w http.ResponseWriter, err error) {
	log.Printf("erreur interne : %v", err)
	writeError(w, "erreur interne du serveur", http.StatusInternalServerError)
}

// GET /health -> Vérification de l'état de la base de données
func handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := db.Ping(); err != nil {
		writeError(w, "base de données inaccessible", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

// GET /plats -> Liste de tous les plats distincts triés par ordre alphabétique
func handlePlats(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query(`SELECT nom FROM plats ORDER BY nom`)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()

	var plats []string
	for rows.Next() {
		var nom string
		if err := rows.Scan(&nom); err != nil {
			serverError(w, err)
			return
		}
		plats = append(plats, nom)
	}

	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}

	writeJSON(w, plats)
}

// PrixResult definit la structure de retour pour le comparateur de prix.
// resto_id, resto_slug, prix_id et photo permettent a l'interface d'ouvrir
// la page du restaurant et de commander.
type PrixResult struct {
	RestoID             string  `json:"resto_id"`
	RestoSlug           string  `json:"resto_slug"`
	Resto               string  `json:"resto"`
	Quartier            string  `json:"quartier"`
	Type                string  `json:"type"`
	PrixID              string  `json:"prix_id"`
	Plat                string  `json:"plat"`
	PrixFCFA            int     `json:"prix_fcfa"`
	Portion             string  `json:"portion"`
	DateReleve          string  `json:"date_releve"`
	Photo               *string `json:"photo"`
	Commandable         bool    `json:"commandable"`
	TempsPreparationMin int     `json:"temps_preparation_min"`
}

// photoPtr renvoie nil quand il n'y a pas de photo.
func photoPtr(p sql.NullString) *string {
	if p.Valid && p.String != "" {
		return &p.String
	}
	return nil
}

// GET /plats/{nom}/moins-cher -> Restaurants vendant un plat, du moins cher au plus cher.
// Les plats marques indisponibles (epuises) n'apparaissent pas.
func handleMoinsCher(w http.ResponseWriter, r *http.Request) {
	nom := strings.TrimSpace(r.PathValue("nom"))
	if nom == "" {
		writeError(w, "nom de plat manquant", http.StatusBadRequest)
		return
	}

	rows, err := db.Query(`
		SELECT r.id, r.slug, r.nom, r.quartier, r.type,
		       pr.id, p.nom, pr.prix_fcfa, pr.portion, pr.date_releve, pr.photo,
		       r.actif, r.temps_preparation_min
		FROM prix pr
		JOIN restaurants r ON r.id = pr.restaurant_id
		JOIN plats p ON p.id = pr.plat_id
		WHERE LOWER(p.nom) = LOWER(?) AND pr.disponible = 1
		ORDER BY pr.prix_fcfa ASC, r.nom ASC
	`, nom)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()

	results := []PrixResult{}
	for rows.Next() {
		var res PrixResult
		var photo sql.NullString
		var actif int
		if err := rows.Scan(&res.RestoID, &res.RestoSlug, &res.Resto, &res.Quartier, &res.Type,
			&res.PrixID, &res.Plat, &res.PrixFCFA, &res.Portion, &res.DateReleve, &photo,
			&actif, &res.TempsPreparationMin); err != nil {
			serverError(w, err)
			return
		}
		res.Photo = photoPtr(photo)
		res.Commandable = actif == 1
		results = append(results, res)
	}

	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}

	if len(results) == 0 {
		writeError(w, "aucun resto trouvé pour ce plat actuellement à Saint-Louis", http.StatusNotFound)
		return
	}
	writeJSON(w, results)
}

// RestoResume decrit un restaurant. commandable vaut true pour les
// restaurants partenaires qui peuvent recevoir des commandes.
type RestoResume struct {
	ID                  string `json:"id"`
	Slug                string `json:"slug"`
	Nom                 string `json:"nom"`
	Quartier            string `json:"quartier"`
	Type                string `json:"type"`
	Commandable         bool   `json:"commandable"`
	TempsPreparationMin int    `json:"temps_preparation_min"`
}

// GET /restos -> Liste complete des etablissements
func handleRestos(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query(`
		SELECT id, slug, nom, quartier, type, actif, temps_preparation_min
		FROM restaurants
		ORDER BY nom`)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()

	restos := []RestoResume{}
	for rows.Next() {
		var rr RestoResume
		var actif int
		if err := rows.Scan(&rr.ID, &rr.Slug, &rr.Nom, &rr.Quartier, &rr.Type, &actif, &rr.TempsPreparationMin); err != nil {
			serverError(w, err)
			return
		}
		rr.Commandable = actif == 1
		restos = append(restos, rr)
	}

	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}

	writeJSON(w, restos)
}

// PlatMenu est un plat propose par un restaurant.
type PlatMenu struct {
	PrixID     string  `json:"prix_id"`
	Plat       string  `json:"plat"`
	Portion    string  `json:"portion"`
	PrixFCFA   int     `json:"prix_fcfa"`
	Photo      *string `json:"photo"`
	Disponible bool    `json:"disponible"`
	DateReleve string  `json:"date_releve"`
}

// MenuResto est la page d'un restaurant : ses informations et ses plats.
type MenuResto struct {
	RestoResume
	Plats []PlatMenu `json:"plats"`
}

// GET /restos/{slug}/menu -> Page du restaurant ouverte par le QR code.
// Les plats epuises restent dans la liste (disponible = false) pour que
// l'interface puisse les griser.
func handleMenuResto(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSpace(r.PathValue("slug"))

	var m MenuResto
	var actif int
	err := db.QueryRow(`
		SELECT id, slug, nom, quartier, type, actif, temps_preparation_min
		FROM restaurants
		WHERE slug = ?`, slug).
		Scan(&m.ID, &m.Slug, &m.Nom, &m.Quartier, &m.Type, &actif, &m.TempsPreparationMin)
	if err == sql.ErrNoRows {
		writeError(w, "restaurant introuvable", http.StatusNotFound)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	m.Commandable = actif == 1

	rows, err := db.Query(`
		SELECT pr.id, p.nom, pr.portion, pr.prix_fcfa, pr.photo, pr.disponible, pr.date_releve
		FROM prix pr
		JOIN plats p ON p.id = pr.plat_id
		WHERE pr.restaurant_id = ?
		ORDER BY p.nom, pr.prix_fcfa`, m.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()

	m.Plats = []PlatMenu{}
	for rows.Next() {
		var pm PlatMenu
		var photo sql.NullString
		var dispo int
		if err := rows.Scan(&pm.PrixID, &pm.Plat, &pm.Portion, &pm.PrixFCFA, &photo, &dispo, &pm.DateReleve); err != nil {
			serverError(w, err)
			return
		}
		pm.Photo = photoPtr(photo)
		pm.Disponible = dispo == 1
		m.Plats = append(m.Plats, pm)
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}

	writeJSON(w, m)
}

// NewPrixInput définit le schéma JSON attendu pour l'insertion d'un prix
type NewPrixInput struct {
	Resto    string `json:"resto"`
	Quartier string `json:"quartier"`
	Type     string `json:"type"`
	Plat     string `json:"plat"`
	Prix     int    `json:"prix"`
	Portion  string `json:"portion"`
}

// POST /prix -> Ajoute ou met à jour le prix d'un plat (sécurisé par clé d'administration)
func handleAjouterPrix(w http.ResponseWriter, r *http.Request) {
	var in NewPrixInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, "corps JSON invalide", http.StatusBadRequest)
		return
	}
	if in.Resto == "" || in.Plat == "" || in.Prix <= 0 {
		writeError(w, "les champs resto, plat et prix (> 0) sont obligatoires", http.StatusBadRequest)
		return
	}

	if err := insertEntry(db, SeedEntry{
		Resto: in.Resto, Quartier: in.Quartier, Type: in.Type,
		Plat: in.Plat, Prix: in.Prix, Portion: in.Portion,
	}); err != nil {
		serverError(w, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]string{"status": "enregistré"})
}

func main() {
	// Chargement de la clé d'administration depuis l'environnement ou .env
	loadAdminKey()

	var err error
	// Connexion et migration intelligente via votre logique db.go mise à jour
	db, err = openDB("cheapdish.db", "schema.sql")
	if err != nil {
		log.Fatalf("ouverture de la base: %v", err)
	}
	defer db.Close()

	// Chargement du fichier de collecte si la base de données est vide
	if err := seedIfEmpty(db, "data/restos.json"); err != nil {
		log.Fatalf("initialisation des données: %v", err)
	}

	// Configuration du routeur natif (Go 1.22+)
	mux := http.NewServeMux()
	
	// Routes Publiques
	mux.HandleFunc("GET /health", handleHealth)
	mux.HandleFunc("GET /plats", handlePlats)
	mux.HandleFunc("GET /plats/{nom}/moins-cher", handleMoinsCher)
	mux.HandleFunc("GET /restos", handleRestos)
	mux.HandleFunc("GET /restos/{slug}/menu", handleMenuResto)
	
	// Écran d'administration Web (Page publique, soumissions sécurisées)
	mux.HandleFunc("GET /admin", handleAdminPage)

	// Routes Sécurisées (Emballées par le middleware de vérification de clé requireAdmin)
	mux.HandleFunc("POST /prix", requireAdmin(handleAjouterPrix))
	mux.HandleFunc("POST /admin/restaurants", requireAdmin(handleAdminAjouterResto))

	port := os.Getenv("PORT")

	if port == "" {
		port = "8080"
	}

	addr := ":" + port

	log.Printf("Serveur Cheap Dish Map démarré sur le port %s", port)

	log.Fatal(http.ListenAndServe(addr, MiddlewareCORS(mux)))
}
