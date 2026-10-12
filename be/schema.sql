-- ============================================================
-- EXPRESSFOOD (ex CHEAP DISH MAP) - Schema SQLite principal
-- ============================================================
-- Version du schema : 3 (voir schemaVersion dans db.go)
--
-- Identifiants : tous les id sont des UUID v4 stockes en TEXT.
--   Les tables restaurants, plats et prix les generent elles-memes
--   (DEFAULT). Les commandes et les livreurs sont generes par le
--   serveur Go, jamais par le client.
-- Montants : FCFA, entiers.
-- Ce fichier peut etre rejoue a chaque demarrage (IF NOT EXISTS).
-- Une base creee avec une ancienne version doit etre recreee
-- (le serveur refuse de la reutiliser, voir openDB).
-- ============================================================

PRAGMA foreign_keys = ON;

-- ============================================================
-- 1. RESTAURANTS
-- ============================================================

CREATE TABLE IF NOT EXISTS restaurants (
    id TEXT NOT NULL PRIMARY KEY DEFAULT (lower(
        hex(randomblob(4)) || '-' || hex(randomblob(2)) || '-4' ||
        substr(hex(randomblob(2)), 2) || '-' ||
        substr('89ab', 1 + (abs(random()) % 4), 1) ||
        substr(hex(randomblob(2)), 2) || '-' || hex(randomblob(6)))),
    nom TEXT NOT NULL,
    -- Adresse courte du restaurant (QR code) : /r/cantine-sor. Choisie par le
    -- serveur a la creation, puis jamais modifiee.
    slug TEXT NOT NULL UNIQUE,
    quartier TEXT NOT NULL DEFAULT '',
    type TEXT NOT NULL DEFAULT '',
    contact TEXT NOT NULL DEFAULT '',
    actif INTEGER NOT NULL DEFAULT 0
        CHECK (actif IN (0, 1)),
    code_acces_hash TEXT,
    latitude REAL,
    longitude REAL,
    temps_preparation_min INTEGER NOT NULL DEFAULT 15
        CHECK (temps_preparation_min > 0),

    -- Un restaurant est reconnu par son nom et son quartier.
    UNIQUE (nom, quartier)
);

CREATE INDEX IF NOT EXISTS idx_restaurants_nom
    ON restaurants (nom);

CREATE INDEX IF NOT EXISTS idx_restaurants_quartier
    ON restaurants (quartier);

-- ============================================================
-- 2. PLATS
-- ============================================================

CREATE TABLE IF NOT EXISTS plats (
    id TEXT NOT NULL PRIMARY KEY DEFAULT (lower(
        hex(randomblob(4)) || '-' || hex(randomblob(2)) || '-4' ||
        substr(hex(randomblob(2)), 2) || '-' ||
        substr('89ab', 1 + (abs(random()) % 4), 1) ||
        substr(hex(randomblob(2)), 2) || '-' || hex(randomblob(6)))),
    nom TEXT NOT NULL UNIQUE
);

-- ============================================================
-- 3. PRIX (un plat chez un restaurant)
-- ============================================================

CREATE TABLE IF NOT EXISTS prix (
    id TEXT NOT NULL PRIMARY KEY DEFAULT (lower(
        hex(randomblob(4)) || '-' || hex(randomblob(2)) || '-4' ||
        substr(hex(randomblob(2)), 2) || '-' ||
        substr('89ab', 1 + (abs(random()) % 4), 1) ||
        substr(hex(randomblob(2)), 2) || '-' || hex(randomblob(6)))),

    restaurant_id TEXT NOT NULL,
    plat_id TEXT NOT NULL,

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

    -- Une portion donnee n'a qu'un seul prix dans un restaurant.
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

    restaurant_id TEXT NOT NULL,

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

    -- Une livraison a toujours une adresse.
    CHECK (mode = 'retrait' OR adresse_livraison IS NOT NULL),

    FOREIGN KEY (restaurant_id)
        REFERENCES restaurants(id)
        ON DELETE RESTRICT,

    FOREIGN KEY (livreur_id)
        REFERENCES livreurs(id)
        ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS idx_commandes_resto_statut
    ON commandes (restaurant_id, statut);

CREATE INDEX IF NOT EXISTS idx_commandes_livreur
    ON commandes (livreur_id);

-- ============================================================
-- 6. LIGNES DE COMMANDE
-- ============================================================

CREATE TABLE IF NOT EXISTS lignes_commande (
    id TEXT PRIMARY KEY,

    commande_id TEXT NOT NULL,
    prix_id TEXT NOT NULL,

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
