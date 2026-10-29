# QSlate-Back
Backend de recherche quantitative QSlate. Le socle Go contient un gateway et
les microservices Auth, Workspace et Widgets. Le moteur de backtest existant est
en Python/FastAPI ; l'orchestration isolée, TimescaleDB et le Builder sont les
prochaines étapes décrites dans [l'analyse d'architecture](docs/architecture.md).

## Socle microservices Go

```text
gateway/                  routage configurable et vérification JWT
services/auth/            comptes, sessions, OAuth2, emails et clés API
services/workspace/       dashboards privés, grille et instances de widgets
services/widgets/         catalogue, versions immuables et admission de preview
internal/platform/        configuration, HTTP, JWT et connexion DB techniques
templates/microservice/   service neutre, exécutable et générable
cmd/                      initialisation locale et générateur
deploy/Dockerfile         image indépendante par binaire
docs/                     architecture, contrats OpenAPI et AsyncAPI
```

Un module Go commun simplifie le travail dans ce monorepo ; chaque service
possède son binaire, son `internal`, sa base et son image indépendants.

### Démarrer

Go 1.26+, Docker Compose et Make :

```bash
make up
curl http://localhost:8080/readyz
```

`make up` crée une fois `.env` (mots de passe PostgreSQL indépendants) et
`.secrets/` (clés Ed25519), puis démarre les services. Les fichiers existants
sont conservés ; `make init` ajoute les mots de passe Workspace/Widgets manquants sans
modifier les valeurs existantes. Le gateway est publié sur localhost:8080 et la
boîte email de développement Mailpit sur localhost:8025 ; les services et leurs
bases PostgreSQL n'ont pas de port
hôte. Auth dispose d'une sortie réseau pour OAuth2/SMTP. Les credentials sont
ignorés par Git/Docker.

```bash
curl -c /tmp/qslate-cookies -X POST http://localhost:8080/qslate/v1/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"email":"trader@example.com","username":"trader","password":"correct-horse-battery-staple"}'

curl -c /tmp/qslate-cookies -X POST http://localhost:8080/qslate/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"identifier":"trader","password":"correct-horse-battery-staple"}'

curl http://localhost:8080/qslate/v1/users/me \
  -H 'Authorization: Bearer <access_token>'

curl -b /tmp/qslate-cookies -c /tmp/qslate-cookies \
  -X POST http://localhost:8080/qslate/v1/auth/refresh \
  -H 'Origin: http://localhost:3000'
```

Les réponses login/register contiennent `user`, `access_token`,
`token_type` et `expires_in` (900 secondes par défaut). Les mots de passe
sont hachés avec bcrypt ; le hash n'est jamais sérialisé. Email et username
sont normalisés en minuscules. Les JWT imposent signature Ed25519, expiration,
issuer et audience. Le gateway et auth vérifient le Bearer ; auth vérifie aussi
que la session n'est ni révoquée ni expirée. Le refresh token reste dans un
cookie HttpOnly, est haché en base, tournant et lié à une session de 30 jours
maximum par défaut. Un rejeu révoque la session. Le frontend doit sérialiser
ses refresh et envoyer `credentials: include`.
Les erreurs applicatives renvoient `{"error":{"code":"...","message":"..."}}`.

Les migrations SQL sont embarquées dans chaque service et appliquées au démarrage sous
verrou transactionnel : une migration déjà appliquée ne doit pas être modifiée.
Ajouter un nouveau fichier numéroté pour faire évoluer une base existante.

Les 14 opérations de la section Auth du PDF sont implémentées : compte,
login/logout/refresh, récupération de mot de passe, vérification d'email,
profil, préférences et gestion des clés API Runner. Trois routes ajoutent
OAuth2 configurable (fournisseurs, authorize, callback). Les emails sont mis
en file chiffrée transactionnelle puis envoyés par SMTP.
Les 11 opérations de la section 3 sont également implémentées : dashboards,
duplication, sauvegarde de grille avec révision, instances de widgets et reset
de layout configurable. Workspace vérifie la session active auprès d'Auth sur
chaque requête et applique la propriété des ressources dans sa propre base.

La section 4 expose les listes de widgets installés/personnalisés, le CRUD
versionné et l'admission de prévisualisation. Workspace vérifie les références
de widget/version par l'API privée du catalogue. La prévisualisation est désactivée
par défaut (503) : un contrôleur sandbox externe gVisor reste à fournir ; aucun
code utilisateur n'est exécuté dans l'API, ni présenté comme exécuté par les tests.
Voir [Widgets](services/widgets/README.md) pour le contrat et les limites.

Voir [le contrat Auth](docs/auth.openapi.yaml), [le contrat REST complet](docs/qslate.openapi.json)
et [le service Workspace](services/workspace/README.md), ainsi que
[les conventions et contrats proposés](docs/contracts.md). Les autres domaines
et les deux flux [WebSocket](docs/streams.asyncapi.json) sont **planifiés**, pas
implémentés. Le frontend et le backtest Python ne sont pas encore intégrés.
MFA, liaison explicite de comptes et validation des clés côté Runner restent à faire.

### Configurer OAuth2 sans modifier le code

Éditer [providers.json](services/auth/config/providers.json) : URLs, scopes,
mapping des claims, callback et noms des variables contenant les identifiants.
Copier `.env.oauth.example` vers `.env.oauth` et renseigner les secrets, puis
activer le fournisseur et redémarrer Auth (`docker compose up -d --force-recreate auth`).
Le fichier est monté en lecture seule dans Compose ; hors Docker, utiliser
`OAUTH_PROVIDERS_FILE`. Aucun fournisseur n'est activé par défaut.

Le flux Authorization Code + PKCE S256 utilise un état à usage unique, puis
une identité vérifiée via userinfo. Aucun token n'est exposé dans l'URL de
redirection ; le frontend appelle refresh après le callback. Une adresse email
identique n'entraîne jamais de fusion automatique de comptes. Les fournisseurs
nécessitant un protocole différent devront disposer d'un adaptateur dédié.

### Développer et créer un service

```bash
make test                       # tests avec race detector
make vet
make build                      # bin/gateway, bin/auth, bin/workspace et bin/widgets
API_BASE_URL=http://localhost:8080 MAILPIT_URL=http://localhost:8025 make test-e2e
make new-service NAME=strategy
go build ./services/strategy/...
make logs
make down                       # arrêt, données PostgreSQL conservées
```

Suivre [le template](templates/microservice/README.md) pour ajouter un domaine,
ses cas d'utilisation, ses adaptateurs, son contrat et ses routes publiques.
Les handlers n'accèdent pas directement au SQL ; les services ne partagent
ni repositories ni entités métier.

Tests PostgreSQL, sur une base de développement accessible :

```bash
TEST_DATABASE_URL='postgres://user:password@localhost:5432/database?sslmode=disable' make test-integration
```

Chaque test crée et supprime uniquement son schéma `auth_test_*`, `workspace_test_*`
ou `widgets_test_*`. La CI fournit
sa propre base. Pour lancer les binaires hors Docker, injecter `DATABASE_URL`
propre à chaque service, `AUTH_SERVICE_URL=http://127.0.0.1:8081` pour Workspace et
le gateway, `WORKSPACE_SERVICE_URL=http://127.0.0.1:8082` pour le gateway et
`WIDGETS_SERVICE_URL=http://127.0.0.1:8083` pour Workspace/gateway ; `.env`
n'est pas chargé automatiquement par les binaires Go.

### Configuration

| Variable | Composant | Défaut / contrainte |
| --- | --- | --- |
| `HTTP_ADDR` | Gateway / auth / workspace / widgets | `:8080` / `:8081` / `:8082` / `:8083` hors Docker |
| `AUTH_SERVICE_URL` | Gateway, Workspace et Widgets | Obligatoire pour les routes et la validation des sessions |
| `WIDGETS_SERVICE_URL` | Gateway / Workspace | Obligatoire pour le catalogue |
| `WIDGETS_CONFIG_FILE` | Widgets | `services/widgets/config/catalogue.json` ; `/app/catalogue.json` dans l'image |
| `WORKSPACE_SERVICE_URL` | Gateway | Obligatoire pour la configuration livrée |
| `WORKSPACE_LAYOUT_FILE` | Workspace | `services/workspace/config/layout.json` ; `/app/layout.json` dans l'image |
| `ROUTES_FILE` | Gateway | `gateway/config/routes.json` |
| `ALLOWED_ORIGINS` | Gateway / auth | `http://localhost:3000`, liste séparée par virgules |
| `DATABASE_URL` | Auth / Workspace / Widgets | Une base propre à chaque service ; paramètres TLS adaptés à l'environnement |
| `JWT_PRIVATE_KEY_FILE` | Auth uniquement | `.secrets/auth-private.pem` |
| `JWT_PUBLIC_KEY_FILE` | Gateway / Workspace / Widgets | `.secrets/auth-public.pem` |
| `JWT_ISSUER` / `JWT_AUDIENCE` | Tous | `qslate-auth` / `qslate-api` |
| `ACCESS_TOKEN_TTL` | Auth | `15m`, entre `1m` et `1h` |
| `REFRESH_TOKEN_TTL` | Auth | `720h`, supérieur à l'access token, maximum `2160h` |
| `COOKIE_SECURE` | Auth | `true` ; `false` uniquement pour HTTP local dans Compose |
| `OAUTH_PROVIDERS_FILE` | Auth | `services/auth/config/providers.json` ; `/app/providers.json` dans l'image |
| `PUBLIC_APP_URL` | Auth | `http://localhost:3000`, base des liens email et callback frontend |
| `SMTP_ADDR` / `SMTP_FROM` | Auth | `localhost:1025` / `noreply@qslate.local` ; Mailpit dans Compose |
| `SMTP_TLS_MODE` | Auth | `starttls` ; `tls` ou `plain` (Mailpit local) |
| `SMTP_USERNAME` / `SMTP_PASSWORD` | Auth | Optionnels ensemble, TLS obligatoire si renseignés |
| `BCRYPT_COST` | Auth | `12`, entre `10` et `14` |

Les clés de développement sont montées avec l'UID/GID local généré dans `.env`.
En déploiement, injecter les fichiers de clés avec les permissions de
l'utilisateur du conteneur et gérer leur cycle de vie via le secret manager.

## Python Backtesting Engine & Job Runner

The Python simulation engine is completely modularized in [`simulator/`](simulator/) and designed as an **ephemeral job runner** for cloud microservices (Docker, Kubernetes Jobs, AWS ECS, Google Cloud Run).

See the comprehensive [Simulator Documentation](simulator/README.md) for full architecture, API guide, and configuration contracts.

### Quick Start

```bash
# Activate virtual environment
source .venv/bin/activate

# 1. Run quick local test suite with default example strategy
python simulator/tests/run_test.py

# 2. Run with your own .py strategy file
python simulator/tests/run_test.py --strategy path/to/my_strategy.py --ticker AAPL --capital 15000

# 3. Run as an ephemeral containerized job with JSON configuration
python -m simulator.job.cli --config simulator/examples/config_local.json --output-dir ./results

# 4. Run automated test suite
python -m unittest discover simulator/tests
```

### Architecture Highlights

- **Candle-by-Candle Engine**: High-fidelity simulation with commission rates, slippage in basis points, and automatic Stop Loss, Take Profit, and bar timeouts.
- **Modern Strategy API**: Object-oriented `Strategy` class with `on_init`, `on_bar`, `on_stop`, and `custom_metrics` lifecycle methods.
- **Marketplace Extensions**: Pluggable indicators and post-processing analytics loaded dynamically from independent modules.
- **Real-Time Telemetry**: In-strategy streaming via `ctx.record(...)` generating time-series for frontend chart overlays.
- **Standardized Artifacts**: Produces `summary.json`, `trades.json`, `equity_curve.json`, `telemetry.json`, and `logs.txt`.

