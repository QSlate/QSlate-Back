# Contrats v1 et conventions proposées

`qslate.openapi.json` est la source canonique REST. `auth.openapi.yaml` est une
vue des routes Auth/OAuth qui référence ce contrat (JSON est aussi du YAML valide).
`streams.asyncapi.json` décrit les deux flux WebSocket ; ils ne sont pas encore
implémentés. Les contrats distinguent explicitement `x-implementation: implemented`
et `planned` ; les 17 endpoints Auth/OAuth, 11 endpoints Workspace, 7 endpoints
Widgets et 9 endpoints Strategy sont enregistrés au gateway (44 sur 98 opérations REST). Preview est
une admission vers un contrôleur externe, indisponible par défaut, pas un runtime
d'exécution livré dans l'API.

Le PDF source contient 95 opérations REST et 2 flux WS. Nous ajoutons 3 opérations
OAuth2, soit 98 opérations REST dans le contrat. Les chemins tronqués ont été
reconstruits comme des **propositions**, indiquées par `x-path-origin`.
Les payloads hors Auth sont également des conventions à valider avec le frontend.
Ils ne doivent pas être présentés comme des détails retrouvés dans le PDF.

## Répartition des domaines

`x-owner` nomme le domaine responsable du contrat, pas forcément un conteneur
à créer immédiatement. Un domaine reste isolé dans son module métier même
lorsqu'il partage provisoirement un binaire. Découpage proposé pour les 13 domaines :

| Domaine du contrat | Responsabilité / frontière de déploiement cible |
| --- | --- |
| `auth` | Auth : identité, sessions, préférences de compte, clés API |
| `workspace` | Workspace : projets et layouts privés |
| `widgets` | Catalogue et versions des widgets ; service et base indépendants |
| `strategy` | Strategy : code/versions, import et diagnostics via analyseur isolé, sans exécution utilisateur |
| `market-data` | Market data : catalogue, imports, datasets et ticks |
| `lab` | Lab : runs, orchestration asynchrone et résultats |
| `ai` | Builder : génération et explication, dépendances IA isolées |
| `marketplace` | Marketplace : publications, catalogue partagé et droits |
| `security` | Contrôle d'exécution : AST, état sandbox et audit ; jamais Auth |
| `usage` | Usage : agrégation consommation/coûts/carbone via événements |
| `runner` | Runner : agents et configurations, admission dédiée des clés API |
| `exports` | Workers d'export : lecture via contrats propriétaires, artefacts et jobs |
| `system` | Composition légère au gateway : santé/version, aucune donnée métier |

Les flux backtest et ticks appartiennent respectivement à Lab et Market data.
Les services Go livrés possèdent chacun leur module, helpers et image autonomes.
Le workspace Go et les contrats regroupés ici sont des outils de développement,
pas des bibliothèques métier importées par les services. Les appels HTTP à Auth,
au catalogue Widgets ou à l'analyseur restent des dépendances fonctionnelles
explicites, distinctes de l'autonomie de compilation.
Les futures routes restent hors de `gateway/config/routes.json` jusqu'à leur
implémentation et aux tests d'autorisation. Aucun accès SQL inter-service :
références d'IDs, appels contractuels et événements remplacent les jointures.

## Conventions transverses

- Préfixe public `/qslate/v1`. Pas d'alias implicite `/api/auth`.
- JWT Ed25519 avec issuer, audience, expiration et `sid` lié à une session vivante.
- La propriété des ressources est vérifiée par leur service propriétaire ; un JWT
  valide ne donne pas accès à tous les IDs.
- IDs métier opaques ; l'identifiant de compte existant reste un entier.
- Dates RFC3339, UTC. Montants financiers décimaux en chaînes pour les futurs
  contrats de calcul. Les tailles de mémoire sont en octets, le CPU en secondes.
- Erreurs JSON `{ "error": { "code": "...", "message": "..." } }`.
- Collections métier paginées par `limit` et `cursor`. La petite liste des clés
  du compte est actuellement renvoyée sous `items`, sans curseur.
- Les jobs longs renvoient `202` et un ID ; les futures commandes de job utilisent
  `Idempotency-Key`. `queued/running/completed/failed/cancelled` forment le cycle proposé.
- Un run fige ses versions de stratégie et dataset ; restaurer une stratégie
  crée une nouvelle version. Sauver une grille utilise une révision optimiste.
- Les téléchargements sont binaires ; les imports sont multipart, pas du JSON.

## Section 2 : choix livrés

Les 14 opérations sont :

| Méthode | Suffixe après `/qslate/v1` |
| --- | --- |
| POST | `/auth/register` |
| POST | `/auth/login` |
| POST | `/auth/logout` |
| POST | `/auth/refresh` |
| POST | `/auth/forgot-password` |
| POST | `/auth/reset-password` |
| POST | `/auth/verify-email` |
| GET / PATCH | `/users/me` |
| GET / PATCH | `/users/me/preferences` |
| GET / POST | `/users/me/api-keys` |
| DELETE | `/users/me/api-keys/{keyId}` |

Le profil modifiable contient `username` et `display_name` ; l'email n'est pas
modifiable par ce PATCH. Les préférences sont `theme` (`dark/light/system`) et
`colorblind_mode`. Les comptes non vérifiés peuvent se connecter ; l'état de
vérification est renvoyé dans le profil pour les futures politiques produit.

Le refresh token est opaque, haché en base et porté uniquement par un cookie
HttpOnly, SameSite=Lax, host-only, Path=/qslate/v1/auth. Secure est activé par
défaut ; Compose le désactive pour localhost HTTP. Le frontend doit utiliser
`credentials: include`. Refresh/logout exigent un `Origin` explicitement autorisé.
Ce mode suppose un frontend et une API sur le même site en production.

L'access token dure 15 minutes, la session a une échéance absolue de 30 jours
(paramétrables). Chaque refresh consomme son token ; un rejeu révoque toute
la famille. Le frontend doit sérialiser les refresh concurrents. Logout et reset
invalident les sessions concernées immédiatement pour les endpoints Auth.
Les futurs services devront aussi vérifier la session active pour appliquer une
révocation immédiate : vérifier seulement la signature au gateway ne suffit pas.

Forgot-password répond identiquement pour un compte existant ou absent.
Reset et vérification consomment un token à usage unique, respectivement valable
1 h et 24 h. `verify-email` accepte soit `{token}` (204), soit `{email}` pour
redemander un envoi (202), jamais les deux. Un nouvel envoi remplace le précédent.

Les emails sont mis en file transactionnellement avec le token. La file contient
des payloads chiffrés AES-GCM ; la clé est dérivée de la clé privée Auth avec
séparation de domaine. Les messages sont purgés après envoi/expiration et retentés
jusqu'à 10 fois. La livraison est au moins une fois, donc un email peut être reçu
en double. Une rotation de clé Auth doit traiter les messages et états OAuth
encore en attente avant de retirer l'ancienne clé.

Les clés API Runner ont un nom, des scopes bornés et une durée de 1 à 365 jours
(90 par défaut). Le secret n'est renvoyé qu'à la création ; seul son digest est
conservé. Une clé Runner n'authentifie pas les routes de gestion du compte.
L'admission des clés sur les futurs endpoints Runner reste à implémenter.

## OAuth2 configurable

Le service charge `OAUTH_PROVIDERS_FILE` au démarrage, par défaut
`config/providers.json` depuis le dossier Auth. Modifier ce fichier puis redémarrer Auth.
Le fournisseur d'exemple est désactivé : aucun identifiant factice n'est utilisé
pour prétendre disposer d'une connexion fournisseur opérationnelle.

Chaque fournisseur définit ses URLs authorize/token/userinfo, scopes, mapping
de claims, méthode d'authentification du token endpoint, paramètres authorize et
noms des variables d'environnement du client. Les mappings acceptent des chemins
JSON pointés. Aucun branchement de code ne dépend d'un nom de fournisseur.

Pour ajouter un fournisseur :

1. Ajouter son entrée JSON et des noms `client_id_env/client_secret_env`.
2. Déposer les valeurs dans `.env.oauth` (Compose) ou le secret manager du déploiement.
3. Enregistrer l'URL de callback exacte chez le fournisseur, puis activer l'entrée.
4. Redémarrer Auth. Il n'est pas nécessaire de modifier l'image ou le gateway.

Les 3 routes supplémentaires sont `GET /auth/providers`,
`GET /auth/oauth/{provider}/authorize` et `GET /auth/oauth/{provider}/callback`.
Le flow utilise Authorization Code + PKCE S256 et un état à usage unique, lié à
un cookie navigateur et au fournisseur. Le callback utilise l'endpoint userinfo
authentifié ; ce n'est pas une implémentation de validation d'ID tokens OIDC.
Le fournisseur doit exposer une identité stable, un email et, par défaut, un
booléen d'email vérifié. Les providers avec un protocole userinfo différent
nécessiteront un adaptateur explicite plutôt qu'un contournement des vérifications.

Une identité externe possède son association `(provider, subject)`. Un email
identique à un compte existant ne déclenche **aucune fusion automatique** :
le callback renvoie 409 et un futur parcours explicite de liaison sera nécessaire.
Les comptes OAuth-only n'ont pas de mot de passe local utilisable ; le parcours
forgot/reset peut en définir un après validation du lien reçu par email.

Après succès, le callback pose le cookie de session et redirige vers l'URL fixe
`PUBLIC_APP_URL/auth/callback?status=success`. Le frontend appelle ensuite refresh
pour obtenir l'access token ; aucun token n'est ajouté à une URL de redirection.
Le login/register par mot de passe et OAuth utilisent la même émission de session.

## Section 3 : Workspace livré

Les 11 opérations dashboards/layouts/instances sont implémentées dans un binaire
Workspace et une base PostgreSQL indépendants. Le
[contrat Workspace](workspace.openapi.yaml) référence le contrat canonique ; le
[README du service](../services/workspace/README.md) détaille les règles.

Chaque requête valide le JWT puis la session auprès d'Auth par `GET /users/me`.
Logout et reset invalident donc aussi Workspace ; un Auth indisponible entraîne
503, jamais une autorisation sur la seule signature. Les ressources privées sont
filtrées par propriétaire en SQL, sans lecture de la base Auth.

Les modifications sont sérialisées par verrou de ligne et incrémentent la
révision. La sauvegarde de grille exige la révision courante (sinon 409) et
remplace atomiquement toutes les positions : une par instance, sans chevauchement
ni dépassement des bornes configurées. Les réponses dashboard exposent `layout`,
`widgets`, `revision` et dates ; les listes sont des résumés paginés par ID.

La duplication régénère tous les IDs et conserve un instantané cohérent. Ajout et
suppression de widget mettent à jour ses positions dans la même transaction.
PATCH remplace entièrement l'objet configuration, sans changer la version.
Reset conserve les instances et utilise les dimensions du fichier
`services/workspace/config/layout.json`, chargé via `WORKSPACE_LAYOUT_FILE`.

Depuis la section 4, Workspace résout les références de widget/version via l'API
privée du catalogue, sous la session active du propriétaire. Les versions
archivées restent accessibles pour des instances déjà créées, pas de nouvelles
instanciations. Les schémas de paramètres propres à chaque widget restent à faire.
Les limites de grille et la sémantique détaillée des payloads sont des choix
d'implémentation explicites là où le PDF ne les définit pas.

## Section 4 : catalogue et admission de preview

Les sept routes sont décrites dans [Widgets OpenAPI](widgets.openapi.yaml).
Le service possède sa base, ses versions immuables et un catalogue installé
paramétrable par fichier. `installed` inclut les définitions globalement
disponibles et les définitions privées de l'utilisateur ; le catalogue global
est vide par défaut. Il n'implémente pas les droits d'achat de Marketplace.

PUT crée une nouvelle version sans casser les instances déjà épinglées.
DELETE archive la définition, sans détruire les anciennes versions. Chaque
requête valide le JWT, la session et le propriétaire ; les listes ne renvoient
pas le code source. La configuration est un objet UI opaque, pas un JSON Schema.
Les sources sont limitées à 100000 octets UTF-8 et les objets à 4096, avec un
corps HTTP Widgets maximal de 1 MiB (les limites Auth/Workspace restent 16 KiB).

Preview transmet l'instantané de la version actuelle à un contrôleur sandbox
de confiance configuré par fichier et variables de secrets. Il exige un accusé
202 `{job_id,status:"queued"}` cohérent et renvoie 503 sinon. Il ne compile ni
n'exécute le code et ne garantit pas l'existence du dataset. Le contrôleur doit
contrôler les droits sur le dataset, la durabilité, la déduplication, l'isolation
gVisor, les quotas et l'accès aux résultats. **Ce contrôleur/runtime reste à
déployer et valider** ; le flow est désactivé par défaut. Les tests de dispatch
utilisent un serveur de test, pas une sandbox présentée comme opérationnelle.

Voir [les limites et le protocole](../services/widgets/README.md).

## Section 5 : stratégies et Mini IDE

Les [neuf routes Strategy](strategy.openapi.yaml) ont un binaire et une base privés.
POST crée un brouillon Python avec paramètres JSON opaques. PUT exige
`base_version_id` et crée un instantané immuable ; 409 signale une modification
concurrente. Restore exige la version actuelle et celle à restaurer, puis copie
nom, source et paramètres dans une nouvelle révision sans modifier l'historique.
Les listes sont sans source ; l'historique retourne les instantanés complets
avec pagination par révision (10 par défaut, 20 maximum). DELETE archive.

L'import multipart exige un seul nom et un seul fichier `.py` UTF-8, sans chemins
ni fichiers temporaires. Le code peut être incomplet pendant l'édition : le lint
est séparé. Sources de 100000 octets, paramètres de 4096, corps JSON/import de
1 MiB ; les limites et le transport d'analyse sont configurés en fichiers.

Le lint appelle un analyseur Python 3.13 fourni dans un conteneur distinct,
sans secrets/DB, sur un réseau interne et avec limites CPU/mémoire/PID/temps.
Chaque requête compile le texte dans un processus jetable sans exécuter l'objet
code. Les erreurs de syntaxe/portée portent des positions Monaco 1-based/UTF-16.
Un analyseur indisponible renvoie 503, jamais un faux résultat valide. Ce n'est
ni du lint de style/type ni la politique AST d'exécution de la section 10.
Un script dangereux peut être syntaxiquement valide ; `valid:true` n'autorise
aucune exécution. Les tests E2E utilisent le vrai conteneur Python.

JWT, session vivante et propriétaire sont contrôlés sur les neuf opérations.
Voir [le service Strategy](../services/strategy/README.md) pour les contrats,
choix de rétention et limites opérationnelles. Les suffixes/payloads reconstruits
restent des propositions explicites ; le frontend Monaco n'est pas intégré ici.

## WebSocket

Le contrat proposé authentifie la connexion dans le premier message dans les
5 secondes, sans token en query string. Après validation de la session et des
ressources, le client reçoit les événements versionnés. Expiration/révocation,
reconnexion, curseurs, heartbeat et backpressure sont documentés dans AsyncAPI.
L'upgrade reste un GET HTTP ; `WS` n'est pas une nouvelle méthode REST.

## Configuration d'exploitation

`SMTP_ADDR`, `SMTP_FROM`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_TLS_MODE`
(`starttls` par défaut, `tls` ou `plain`) et `PUBLIC_APP_URL` pilotent les emails.
Les credentials SMTP ne sont acceptés qu'avec TLS. Le mode plain de Compose est
réservé à Mailpit local, visible sur `http://localhost:8025`.
`COOKIE_SECURE`, `ALLOWED_ORIGINS`, `REFRESH_TOKEN_TTL` et `ACCESS_TOKEN_TTL`
pilotent les sessions ; `JWT_ISSUER/JWT_AUDIENCE` restent communs au gateway/Auth.
