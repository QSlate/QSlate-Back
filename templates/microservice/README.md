# Template microservice QSlate

Créer un service depuis la racine : `make new-service NAME=workspace`.
Le générateur copie ce dossier, adapte les imports et refuse un nom invalide
ou un dossier existant. Le résultat compile dans le module Go de ce dépôt.

Le template est exécutable : `go run ./templates/microservice/cmd/service`.
Il expose `/healthz` et `/readyz` sur le port 8082, sans base obligatoire.

Ajouter les entités/interfaces dans `internal/domain`, les cas d'utilisation
dans `internal/service`, les adaptateurs dans `internal/repository` et les
handlers dans `internal/http`. `cmd/service` assemble les dépendances.
Chaque service conserve ses règles métier dans son propre `internal`.

Pour le déployer, créer son contrat OpenAPI, ajouter son image/binaire au
Makefile, un service Compose, puis les routes explicites dans
`gateway/config/routes.json`. Pour une route privée, déclarer
`auth_required: true` et vérifier aussi le Bearer JWT dans le service avec
`internal/platform/token.Protect`. Le gateway possède seulement la clé publique.

Build indépendant :

```bash
docker build -f deploy/Dockerfile --build-arg SERVICE_PATH=templates/microservice/cmd/service -t qslate-template .
```
