Ajouter les migrations SQL numérotées et un runner embarqué si le service utilise
une base. Le service `services/auth/migrations` fournit l'exemple transactionnel
(verrou PostgreSQL, version et checksum). Les tables appartiennent au service :
aucun accès direct aux tables d'un autre service.
