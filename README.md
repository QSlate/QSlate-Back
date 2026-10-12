# QSlate-Back
Backend de recherche quantitative QSlate. Le socle Go contient un gateway et
un premier microservice d'authentification. Le moteur de backtest existant est
en Python/FastAPI ; l'orchestration isolée, TimescaleDB et le Builder sont les
prochaines étapes décrites dans [l'analyse d'architecture](docs/architecture.md).

## Socle microservices Go

```text
gateway/                  routage configurable et vérification JWT
services/auth/            compte, login, profil et migrations PostgreSQL
internal/platform/        configuration, HTTP, JWT et connexion DB techniques
templates/microservice/   service neutre, exécutable et générable
cmd/                      initialisation locale et générateur
deploy/Dockerfile         image indépendante par binaire
docs/                     analyse QSlate/AREA et contrat OpenAPI
```

Un module Go commun simplifie le travail dans ce monorepo ; chaque service
possède son binaire, son `internal`, sa base et son image indépendants.

### Démarrer

Go 1.26+, Docker Compose et Make :

```bash
make up
curl http://localhost:8080/readyz
```

`make up` crée une fois `.env` (mot de passe PostgreSQL aléatoire) et
`.secrets/` (clés Ed25519), puis démarre les services. Les fichiers existants
sont conservés. Seul le gateway est publié sur localhost ; auth et PostgreSQL
restent sur des réseaux internes. Les credentials sont ignorés par Git/Docker.

```bash
curl -X POST http://localhost:8080/api/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"email":"trader@example.com","username":"trader","password":"correct-horse-battery-staple"}'

curl -X POST http://localhost:8080/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"identifier":"trader","password":"correct-horse-battery-staple"}'

curl http://localhost:8080/api/auth/me \
  -H 'Authorization: Bearer <access_token>'
```

Les réponses login/register contiennent `user`, `access_token`,
`token_type` et `expires_in` (900 secondes par défaut). Les mots de passe
sont hachés avec bcrypt ; le hash n'est jamais sérialisé. Email et username
sont normalisés en minuscules. Les JWT imposent signature Ed25519, expiration,
issuer et audience. Le gateway et auth vérifient le Bearer du profil.
Les erreurs applicatives renvoient `{"error":{"code":"...","message":"..."}}`.

Les migrations SQL sont embarquées dans auth et appliquées au démarrage sous
verrou transactionnel : une migration déjà appliquée ne doit pas être modifiée.
Ajouter un nouveau fichier numéroté pour faire évoluer une base existante.

Le contrat API est dans [auth.openapi.yaml](docs/auth.openapi.yaml).
Le frontend n'est pas encore branché et le backtest Python n'est pas routé
par ce nouveau gateway. Le service initial n'inclut pas refresh/logout,
révocation immédiate, MFA ou OAuth.

### Développer et créer un service

```bash
make test                       # tests avec race detector
make vet
make build                      # bin/gateway et bin/auth
API_BASE_URL=http://localhost:8080 make test-e2e  # stack démarrée
make new-service NAME=workspace
go build ./services/workspace/...
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

Chaque test crée et supprime uniquement son schéma `auth_test_*`. La CI fournit
sa propre base. Pour lancer les binaires hors Docker, injecter `DATABASE_URL`
pour auth et `AUTH_SERVICE_URL=http://127.0.0.1:8081` pour le gateway ; `.env`
n'est pas chargé automatiquement par les binaires Go.

### Configuration

| Variable | Composant | Défaut / contrainte |
| --- | --- | --- |
| `HTTP_ADDR` | Gateway / auth | `:8080` / `:8081` hors Docker |
| `AUTH_SERVICE_URL` | Gateway, substitution des routes | Obligatoire pour la configuration livrée |
| `ROUTES_FILE` | Gateway | `gateway/config/routes.json` |
| `ALLOWED_ORIGINS` | Gateway | `http://localhost:3000`, liste séparée par virgules |
| `DATABASE_URL` | Auth | Obligatoire ; choisir les paramètres TLS pour l'environnement |
| `JWT_PRIVATE_KEY_FILE` | Auth uniquement | `.secrets/auth-private.pem` |
| `JWT_PUBLIC_KEY_FILE` | Gateway | `.secrets/auth-public.pem` |
| `JWT_ISSUER` / `JWT_AUDIENCE` | Les deux | `qslate-auth` / `qslate-api` |
| `ACCESS_TOKEN_TTL` | Auth | `15m`, entre `1m` et `1h` |
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
