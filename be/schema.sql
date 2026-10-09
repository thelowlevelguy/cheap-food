-- ============================================================
-- CHEAP DISH MAP / NDAR EXPRESS FOOD
-- Schema SQLite principal
-- ============================================================
--
-- Restaurants :
--   Chaque établissement possède son propre identifiant.
--   Plusieurs restaurants peuvent partager le même nom
--   et/ou le même quartier.
--
-- Plats :
--   Un plat est partage entre plusieurs restaurants.
--
-- Prix :
--   Relie un restaurant a un plat et enregistre son prix.
--
-- Commandes :
--   Permet de gerer les commandes et leurs lignes.
--
-- Identifiants :
--   INTEGER AUTOINCREMENT pour restaurants, plats et prix.
--   TEXT pour les identifiants des commandes et des livreurs.
--
-- Montants :
--   FCFA, stockes en entiers.
-- ============================================================

PRAGMA foreign_keys = ON;

-- ============================================================
-- 1. RESTAURANTS
-- ============================================================

CREATE TABLE IF NOT EXISTS restaurants (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    nom TEXT NOT NULL,
    quartier TEXT NOT NULL DEFAULT '',
    type TEXT NOT NULL DEFAULT '',
    contact TEXT NOT NULL DEFAULT '',
    actif INTEGER NOT NULL DEFAULT 0
        CHECK (actif IN (0, 1)),
    code_acces_hash TEXT
);

CREATE INDEX IF NOT EXISTS idx_restaurants_nom
    ON restaurants (nom);

CREATE INDEX IF NOT EXISTS idx_restaurants_quartier
    ON restaurants (quartier);

-- ============================================================
-- 2. PLATS
-- ============================================================

CREATE TABLE IF NOT EXISTS plats (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    nom TEXT NOT NULL UNIQUE
);

-- ============================================================
-- 3. PRIX
-- ============================================================

CREATE TABLE IF NOT EXISTS prix (
    id INTEGER PRIMARY KEY AUTOINCREMENT,

    restaurant_id INTEGER NOT NULL,
    plat_id INTEGER NOT NULL,

    prix_fcfa INTEGER NOT NULL
        CHECK (prix_fcfa > 0),

    portion TEXT NOT NULL DEFAULT '',
    date_releve TEXT NOT NULL DEFAULT (date('now')),

    disponible INTEGER NOT NULL DEFAULT 1
        CHECK (disponible IN (0, 1)),

    photo TEXT,

    FOREIGN KEY (restaurant_id)
        REFERENCES restaurants(id)
        ON DELETE CASCADE,

    FOREIGN KEY (plat_id)
        REFERENCES plats(id)
        ON DELETE RESTRICT,

    -- Un restaurant peut proposer plusieurs plats.
    -- Un plat peut etre vendu dans plusieurs restaurants.
    -- Une portion donnee ne doit pas avoir plusieurs
    -- enregistrements de prix dans le meme restaurant.
    UNIQUE (restaurant_id, plat_id, portion)
);

CREATE INDEX IF NOT EXISTS idx_prix_restaurant
    ON prix (restaurant_id);

CREATE INDEX IF NOT EXISTS idx_prix_plat
    ON prix (plat_id);

CREATE INDEX IF NOT EXISTS idx_prix_montant
    ON prix (prix_fcfa);

CREATE INDEX IF NOT EXISTS idx_prix_disponible
    ON prix (disponible);

-- ============================================================
-- 4. LIVREURS
-- ============================================================

CREATE TABLE IF NOT EXISTS livreurs (
    id TEXT PRIMARY KEY,
    nom TEXT NOT NULL,
    telephone TEXT NOT NULL UNIQUE,
    actif INTEGER NOT NULL DEFAULT 1
        CHECK (actif IN (0, 1))
);

-- ============================================================
-- 5. COMMANDES
-- ============================================================

CREATE TABLE IF NOT EXISTS commandes (
    id TEXT PRIMARY KEY,

    restaurant_id INTEGER NOT NULL,

    client_nom TEXT NOT NULL,
    client_telephone TEXT NOT NULL,

    token_suivi TEXT NOT NULL UNIQUE,

    livreur_id TEXT,

    mode TEXT NOT NULL
        CHECK (mode IN ('retrait', 'livraison')),

    adresse_livraison TEXT,

    statut TEXT NOT NULL DEFAULT 'attente_paiement'
        CHECK (
            statut IN (
                'attente_paiement',
                'payee',
                'acceptee',
                'prete',
                'en_livraison',
                'terminee',
                'annulee'
            )
        ),

    sous_total INTEGER NOT NULL
        CHECK (sous_total >= 0),

    frais_resto INTEGER NOT NULL
        CHECK (frais_resto >= 0),

    frais_livraison INTEGER NOT NULL DEFAULT 0
        CHECK (frais_livraison >= 0),

    part_livreur INTEGER NOT NULL DEFAULT 0
        CHECK (part_livreur >= 0),

    code_retrait TEXT NOT NULL,

    reference_paiement TEXT UNIQUE,

    cree_le TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    paye_le TEXT,

    FOREIGN KEY (restaurant_id)
        REFERENCES restaurants(id)
        ON DELETE RESTRICT,

    FOREIGN KEY (livreur_id)
        REFERENCES livreurs(id)
        ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS idx_commandes_resto_statut
    ON commandes (restaurant_id, statut);

CREATE INDEX IF NOT EXISTS idx_commandes_token_suivi
    ON commandes (token_suivi);

CREATE INDEX IF NOT EXISTS idx_commandes_livreur
    ON commandes (livreur_id);

-- ============================================================
-- 6. LIGNES DE COMMANDE
-- ============================================================

CREATE TABLE IF NOT EXISTS lignes_commande (
    id TEXT PRIMARY KEY,

    commande_id TEXT NOT NULL,
    prix_id INTEGER NOT NULL,

    -- Informations conservees telles qu'elles etaient
    -- au moment de la commande.
    nom_plat TEXT NOT NULL,

    prix_unitaire INTEGER NOT NULL
        CHECK (prix_unitaire >= 0),

    quantite INTEGER NOT NULL
        CHECK (quantite > 0),

    FOREIGN KEY (commande_id)
        REFERENCES commandes(id)
        ON DELETE CASCADE,

    FOREIGN KEY (prix_id)
        REFERENCES prix(id)
        ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_lignes_commande_commande
    ON lignes_commande (commande_id);

CREATE INDEX IF NOT EXISTS idx_lignes_commande_prix
    ON lignes_commande (prix_id);

-- ============================================================
-- FIN DU SCHEMA
-- ============================================================