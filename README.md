# QSlate-Back
Backend de recherche quantitative QSlate. Le socle Go contient un gateway et
les microservices Auth, Workspace, Widgets et Strategy. Le moteur de backtest existant est
en Python/FastAPI ; l'orchestration isolée, TimescaleDB et le Builder sont les
prochaines étapes décrites dans [l'analyse d'architecture](docs/architecture.md).

## Socle microservices Go

```text
gateway/                  routage configurable et vérification JWT
services/auth/            comptes, sessions, OAuth2, emails et clés API
services/workspace/       dashboards privés, grille et instances de widgets
services/widgets/         catalogue, versions immuables et admission de preview
services/strategy/        code Python, historique, restauration et diagnostics IDE
go.work                   workspace de développement, pas une dépendance de build
templates/microservice/   service neutre, exécutable et générable
cmd/                      initialisation locale et générateur
*/Dockerfile              image construite depuis le seul dossier du composant
docs/                     architecture, contrats OpenAPI et AsyncAPI
```

Comme dans AREA, chaque service et le gateway possèdent leur `go.mod`, `go.sum`,
`Dockerfile`, `Makefile` et configuration. Leur petit socle `internal/platform`
est une copie locale, pas une bibliothèque racine partagée. Aucun import Go entre
services ni `replace` vers un dossier voisin : chaque dossier peut être copié,
testé et construit sans le monorepo, avec `GOWORK=off`.

Le `go.work` facilite uniquement l'édition multi-module. Le module racine contient
les outils et tests transverses. `make test` parcourt tous les modules : un simple
`go test ./...` à la racine ne teste pas les modules enfants. Le Compose racine
orchestre le développement ; chaque image n'embarque que son binaire et sa propre
configuration. Les contrats documentaires restent regroupés dans `docs/`.

Cette autonomie de construction ne supprime pas les dépendances fonctionnelles :
Auth reste l'autorité des sessions, Workspace appelle Widgets par HTTP et Strategy
appelle son analyseur. Aucune de ces relations ne partage de tables ni de code métier.

### Démarrer

Go 1.26+, Docker Compose et Make :

```bash
make up
curl http://localhost:8080/readyz
```

`make up` crée une fois `.env` (mots de passe PostgreSQL indépendants) et
`.secrets/` (clés Ed25519), puis démarre les services. Les fichiers existants
sont conservés ; `make init` ajoute les mots de passe Workspace/Widgets/Strategy manquants sans
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

La section 5 ajoute neuf opérations Strategy : CRUD privé, import `.py`, historique,
restauration et diagnostics Python pour Monaco. Les sauvegardes/restaurations
exigent la version courante (`base_version_id`, sinon 409). Les révisions sont
immuables ; le lint compile le texte sans l'exécuter dans un analyseur Python
3.13 séparé, limité en ressources et sans secrets ni accès DB. Ce n'est pas une
autorisation de sécurité ou un moteur de backtest. Voir [Strategy](services/strategy/README.md).

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
make build                      # binaires dans le bin/ de chaque composant
make test-standalone            # builds isolés hors du dépôt, sans go.work
python3 -B -m unittest discover -s services/strategy/analyzer -p 'test_*.py'
API_BASE_URL=http://localhost:8080 MAILPIT_URL=http://localhost:8025 make test-e2e
make new-service NAME=market-data
make -C services/market-data test build
make logs
make down                       # arrêt, données PostgreSQL conservées
```

Suivre [le template](templates/microservice/README.md) pour ajouter un domaine,
ses cas d'utilisation, ses adaptateurs, son contrat et ses routes publiques.
Les handlers n'accèdent pas directement au SQL ; les services ne partagent
ni repositories ni entités métier.

Exemple depuis le seul dossier d'un service :

```bash
cd services/strategy
make test build                 # GOWORK=off et dépendances propres au service
make docker-build               # contexte Docker limité à ce dossier
```

Les helpers techniques copiés restent sous la responsabilité du service. Une
correction de sécurité doit être reportée explicitement dans les copies concernées
et validée par les tests de chaque module ; modifier le template ne met pas à jour
silencieusement les services existants.

Tests PostgreSQL, sur une base de développement accessible :

```bash
TEST_DATABASE_URL='postgres://user:password@localhost:5432/database?sslmode=disable' make test-integration
```

Chaque test crée et supprime uniquement son schéma `auth_test_*`, `workspace_test_*`
ou `widgets_test_*`, `strategy_test_*`. La CI fournit
sa propre base. Pour lancer les binaires hors Docker, injecter `DATABASE_URL`
propre à chaque service, `AUTH_SERVICE_URL=http://127.0.0.1:8081` pour Workspace et
le gateway, `WORKSPACE_SERVICE_URL=http://127.0.0.1:8082` pour le gateway et
`WIDGETS_SERVICE_URL=http://127.0.0.1:8083` pour Workspace/gateway et
`STRATEGY_SERVICE_URL=http://127.0.0.1:8084` pour le gateway. Strategy vérifie aussi
Auth et utilise `STRATEGY_ANALYZER_URL` pour le lint ; `.env`
n'est pas chargé automatiquement par les binaires Go. Exécuter `make run` depuis
le dossier du service, avec les variables exportées et les chemins des clés injectés.
Les fichiers de configuration par défaut sont relatifs à ce dossier, pas à la racine.

### Configuration

| Variable | Composant | Défaut / contrainte |
| --- | --- | --- |
| `HTTP_ADDR` | Gateway / auth / workspace / widgets / strategy | `:8080` / `:8081` / `:8082` / `:8083` / `:8084` hors Docker |
| `AUTH_SERVICE_URL` | Gateway, Workspace, Widgets et Strategy | Obligatoire pour les routes et la validation des sessions |
| `STRATEGY_SERVICE_URL` | Gateway | Obligatoire pour les neuf routes Strategy |
| `STRATEGY_CONFIG_FILE` | Strategy | `config/strategy.json` depuis le service ; `/app/strategy.json` dans l'image |
| `STRATEGY_ANALYZER_URL` | Strategy | Origine de l'analyseur ; HTTP explicitement autorisé pour le réseau interne local |
| `WIDGETS_SERVICE_URL` | Gateway / Workspace | Obligatoire pour le catalogue |
| `WIDGETS_CONFIG_FILE` | Widgets | `config/catalogue.json` depuis le service ; `/app/catalogue.json` dans l'image |
| `WORKSPACE_SERVICE_URL` | Gateway | Obligatoire pour la configuration livrée |
| `WORKSPACE_LAYOUT_FILE` | Workspace | `config/layout.json` depuis le service ; `/app/layout.json` dans l'image |
| `ROUTES_FILE` | Gateway | `config/routes.json` depuis le gateway |
| `ALLOWED_ORIGINS` | Gateway / auth | `http://localhost:3000`, liste séparée par virgules |
| `DATABASE_URL` | Auth / Workspace / Widgets / Strategy | Une base propre à chaque service ; paramètres TLS adaptés à l'environnement |
| `JWT_PRIVATE_KEY_FILE` | Auth uniquement | `.secrets/auth-private.pem` |
| `JWT_PUBLIC_KEY_FILE` | Gateway / Workspace / Widgets / Strategy | `.secrets/auth-public.pem` |
| `JWT_ISSUER` / `JWT_AUDIENCE` | Tous | `qslate-auth` / `qslate-api` |
| `ACCESS_TOKEN_TTL` | Auth | `15m`, entre `1m` et `1h` |
| `REFRESH_TOKEN_TTL` | Auth | `720h`, supérieur à l'access token, maximum `2160h` |
| `COOKIE_SECURE` | Auth | `true` ; `false` uniquement pour HTTP local dans Compose |
| `OAUTH_PROVIDERS_FILE` | Auth | `config/providers.json` depuis Auth ; `/app/providers.json` dans l'image |
| `PUBLIC_APP_URL` | Auth | `http://localhost:3000`, base des liens email et callback frontend |
| `SMTP_ADDR` / `SMTP_FROM` | Auth | `localhost:1025` / `noreply@qslate.local` ; Mailpit dans Compose |
| `SMTP_TLS_MODE` | Auth | `starttls` ; `tls` ou `plain` (Mailpit local) |
| `SMTP_USERNAME` / `SMTP_PASSWORD` | Auth | Optionnels ensemble, TLS obligatoire si renseignés |
| `BCRYPT_COST` | Auth | `12`, entre `10` et `14` |

Les clés de développement sont montées avec l'UID/GID local généré dans `.env`.
En déploiement, injecter les fichiers de clés avec les permissions de
l'utilisateur du conteneur et gérer leur cycle de vie via le secret manager.

## Backend de backtest Python existant

The backtest flow is split into two services:

- `main.py`: public API layer
- `runner_service.py`: dedicated backtest runner

When the frontend calls `POST /api/backtest`, the API forwards the request to the runner through `RUNNER_SERVICE_URL`. The runner executes the backtest job and returns the final `{ report, trades }` payload.

## Endpoints

API service (`main.py`):

- `GET /api/options`
- `GET /api/data/{ticker}?limit=100`
- `GET /api/assets`
- `POST /api/assets/download/{ticker}`
- `POST /api/backtest`

Runner service (`runner_service.py`):

- `GET /healthz`
- `POST /backtest/run`

## Requirements

Install the direct project dependencies with:

```bash
pip install -r requirements.txt
```

## Environment variables

API:

- `RUNNER_SERVICE_URL` default: `http://localhost:8090/backtest/run`
- `RUNNER_TIMEOUT_SECONDS` default: `600`
- `API_HOST` default: `127.0.0.1`
- `API_PORT` default: `8000`

Runner:

- `RUNNER_HOST` default: `127.0.0.1`
- `RUNNER_PORT` default: `8090`

## Local run

From the project root:

```bash
cd QSlate-Back
source venv/bin/activate
```

Start the runner in terminal A:

```bash
python runner_service.py
```

Start the API in terminal B:

```bash
cd QSlate-Back
source venv/bin/activate
RUNNER_SERVICE_URL=http://127.0.0.1:8090/backtest/run python main.py
```

## Manual test

Check that the API is up:

```bash
curl http://127.0.0.1:8000/api/options
```

If you do not already have local data for a ticker, download some:

```bash
curl -X POST http://127.0.0.1:8000/api/assets/download/AAPL
```

Check the local candles:

```bash
curl "http://127.0.0.1:8000/api/data/AAPL?limit=3"
```

Run a minimal backtest:

```bash
curl -X POST http://127.0.0.1:8000/api/backtest \
  -H "Content-Type: application/json" \
  -d '{"ticker":"AAPL","initial_capital":10000,"window":5,"indicators":[],"strategy_code":"def custom_strategy(history, open_trades, remaining_capital):\n    return []","strategy_function_name":"custom_strategy","requested_stats":null,"sort_trades_by":"date","top_trades":null}'
```

Expected result for this empty strategy:

```json
{"report":{"Message":"No trades to analyze."},"trades":[]}
```
