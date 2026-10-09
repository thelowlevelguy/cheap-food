-- NdarExpressFood : extension du schema.sql existant (restaurants, plats, prix)
-- A adapter aux vrais noms de colonnes de votre schema.sql.
-- Tous les montants sont en FCFA, en entiers.
-- Tous les identifiants (id) sont des UUID stockes en TEXT, generes par le serveur Go (jamais par le client).
-- Au demarrage de chaque connexion : PRAGMA foreign_keys = ON;
-- Conseille aussi : PRAGMA journal_mode = WAL; (plusieurs commandes en meme temps)

-- 1. Enrichir l'existant -------------------------------------------------
-- "contact" existe deja dans restaurants : reutilisez-le pour WhatsApp/SMS.
ALTER TABLE restaurants ADD COLUMN actif INTEGER NOT NULL DEFAULT 0;
ALTER TABLE restaurants ADD COLUMN code_acces_hash TEXT;  -- acces du resto a son tableau de bord (stocker un hash, jamais le code en clair)
ALTER TABLE prix ADD COLUMN disponible INTEGER NOT NULL DEFAULT 1;  -- plat epuise = 0
ALTER TABLE prix ADD COLUMN photo TEXT;

-- 2. Livreurs ------------------------------------------------------------
-- Pas de compte client : nom et telephone sont saisis a chaque commande.
CREATE TABLE IF NOT EXISTS livreurs (
  id        TEXT PRIMARY KEY,
  nom       TEXT NOT NULL,
  telephone TEXT NOT NULL UNIQUE,
  actif     INTEGER NOT NULL DEFAULT 1
);

-- 3. Commandes -----------------------------------------------------------
CREATE TABLE IF NOT EXISTS commandes (
  id                 TEXT PRIMARY KEY,
  restaurant_id      TEXT NOT NULL REFERENCES restaurants(id),  -- UUID (restaurants.id doit etre TEXT PRIMARY KEY)
  client_nom         TEXT NOT NULL,
  client_telephone   TEXT NOT NULL,
  token_suivi        TEXT NOT NULL UNIQUE,         -- long code aleatoire, mis dans le lien de suivi de la commande
  livreur_id         TEXT    REFERENCES livreurs(id),
  mode               TEXT NOT NULL CHECK (mode IN ('retrait', 'livraison')),
  adresse_livraison  TEXT,
  statut             TEXT NOT NULL DEFAULT 'attente_paiement'
                     CHECK (statut IN ('attente_paiement', 'payee', 'acceptee',
                                       'prete', 'en_livraison', 'terminee', 'annulee')),
  sous_total         INTEGER NOT NULL,             -- somme des plats, calculee par le serveur
  frais_resto        INTEGER NOT NULL,             -- 50 / 100 / 200 selon le sous_total
  frais_livraison    INTEGER NOT NULL DEFAULT 0,   -- paye par le client si livraison
  part_livreur       INTEGER NOT NULL DEFAULT 0,   -- part du livreur dans frais_livraison
  code_retrait       TEXT NOT NULL,                -- code montre au resto / au livreur
  reference_paiement TEXT UNIQUE,                  -- identifiant de la transaction Wave
  cree_le            TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  paye_le            TEXT
);

CREATE INDEX IF NOT EXISTS idx_commandes_resto_statut
  ON commandes (restaurant_id, statut);

-- 4. Lignes de commande --------------------------------------------------
-- Nom et prix sont copies au moment de la commande : si le resto change
-- ses prix plus tard, l'historique reste correct.
CREATE TABLE IF NOT EXISTS lignes_commande (
  id            TEXT PRIMARY KEY,
  commande_id   TEXT    NOT NULL REFERENCES commandes(id),
  prix_id       TEXT    NOT NULL REFERENCES prix(id),
  nom_plat      TEXT NOT NULL,
  prix_unitaire INTEGER NOT NULL,
  quantite      INTEGER NOT NULL CHECK (quantite > 0)
);