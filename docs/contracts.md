# Contrats v1 et conventions proposées

`qslate.openapi.json` est la source canonique REST. `auth.openapi.yaml` est une
vue des routes Auth/OAuth qui référence ce contrat (JSON est aussi du YAML valide).
`streams.asyncapi.json` décrit les deux flux WebSocket ; ils ne sont pas encore
implémentés. Les contrats distinguent explicitement `x-implementation: implemented`
et `planned` ; seuls les endpoints Auth/OAuth sont enregistrés au gateway.

Le PDF source contient 95 opérations REST et 2 flux WS. Nous ajoutons 3 opérations
OAuth2, soit 98 opérations REST dans le contrat. Les chemins tronqués ont été
reconstruits comme des **propositions**, indiquées par `x-path-origin`.
Les payloads hors Auth sont également des propositions à valider avec le frontend.
Ils ne doivent pas être présentés comme des détails retrouvés dans le PDF.

## Répartition des domaines

`x-owner` nomme le domaine responsable du contrat, pas forcément un conteneur
à créer immédiatement. Un domaine reste isolé dans son module métier même
lorsqu'il partage provisoirement un binaire. Découpage proposé pour les 13 domaines :

| Domaine du contrat | Responsabilité / frontière de déploiement cible |
| --- | --- |
| `auth` | Auth : identité, sessions, préférences de compte, clés API |
| `workspace` | Workspace : projets et layouts privés |
| `widgets` | Catalogue et versions des widgets ; peut débuter dans Workspace |
| `strategy` | Strategy : code et versions, sans exécution utilisateur |
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
`services/auth/config/providers.json`. Modifier ce fichier puis redémarrer Auth.
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
