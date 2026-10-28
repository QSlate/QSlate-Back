# Template microservice QSlate

Créer un service depuis la racine : `make new-service NAME=market-data`.
Le générateur copie ce dossier, adapte le module/imports et refuse d'écraser un
service existant. Les `.env` privés, clés, instructions AGENTS et binaires locaux
ne sont jamais copiés. Les exemples de configuration restent copiables.

Le résultat possède son propre `go.mod`, `go.sum`, `Dockerfile`, `Makefile`,
`.dockerignore`, configuration et helpers techniques. Il ne dépend d'aucun
source racine/voisin et se construit même hors du monorepo :

```bash
cd services/market-data
make test build
make docker-build
make run
```

Les cibles imposent `GOWORK=off` et `-mod=readonly`. Le workspace du dépôt est
facultatif : `go work use ./services/market-data` permet de l'ajouter à l'IDE.
Le Makefile racine détecte les modules `services/*/go.mod` pour les tests/builds.
Le générateur ne modifie ni Compose ni les routes publiques implicitement.

Le template seul se lance avec `make -C templates/microservice run` depuis la
racine. Il expose `/healthz` et `/readyz` sur 8082 localement, 8080 dans l'image.
Aucune base ou identité d'utilisateur fictive n'est requise pour ces probes.

Ajouter les entités/interfaces dans `internal/domain`, les cas d'utilisation
dans `internal/service`, SQL dans `internal/repository` et les handlers dans
`internal/http`. Le point d'entrée assemble les dépendances. `internal/platform`
contient des copies locales testées pour HTTP, configuration, JWT, validation
des sessions, connexion PostgreSQL et migrations ; les utiliser selon le domaine.
Ils ne sont pas des règles métier et ne doivent jamais importer un autre service.
Modifier le template n'entraîne aucune mise à jour automatique des copies existantes.

Pour chaque route privée, vérifier le JWT **et** la session vivante via Auth,
puis le propriétaire dans le service. Le gateway ne signe aucun token. Pour une
base, embarquer les migrations propres au service et attribuer un namespace de
verrou distinct. Copier uniquement la clé publique aux consommateurs d'Auth.

Pour le déploiement, ajouter le contrat OpenAPI et un service dans le Compose
racine avec `build.context: ./services/market-data`, ses réseaux/credentials,
puis les routes explicites dans `gateway/config/routes.json`. Aucun endpoint
ne doit apparaître avant son implémentation et ses tests d'autorisation.

Chaque module doit conserver des tests autonomes et des tests d'intégration
dans `internal/repository` lorsqu'il utilise SQL. Les tests transverses restent
à la racine. `make test-standalone` vérifie les builds hors dépôt ; `make test`
parcourt tous les modules avec leurs propres dépendances.
